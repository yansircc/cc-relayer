package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis key patterns — must match Node.js project for migration compatibility.
const (
	KeyAccountPrefix    = "claude:account:"
	KeyAccountIndex     = "claude:account:index"
	KeyStickySession    = "sticky_session:"
	KeySessionBinding   = "original_session_binding:"
	KeyStainless        = "fmt_claude_req:stainless_headers:"
	KeyTokenRefreshLock = "token_refresh_lock:claude:"
)

type Store struct {
	rdb *redis.Client
}

func New(addr, password string, db int) (*Store, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	return &Store{rdb: rdb}, nil
}

func (s *Store) Close() error {
	return s.rdb.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.rdb.Ping(ctx).Err()
}

func (s *Store) Client() *redis.Client {
	return s.rdb
}

// --- Account operations ---

func (s *Store) GetAccount(ctx context.Context, id string) (map[string]string, error) {
	return s.rdb.HGetAll(ctx, KeyAccountPrefix+id).Result()
}

func (s *Store) SetAccount(ctx context.Context, id string, fields map[string]string) error {
	pipe := s.rdb.Pipeline()
	vals := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		vals = append(vals, k, v)
	}
	pipe.HSet(ctx, KeyAccountPrefix+id, vals...)
	pipe.SAdd(ctx, KeyAccountIndex, id)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) SetAccountField(ctx context.Context, id, field, value string) error {
	return s.rdb.HSet(ctx, KeyAccountPrefix+id, field, value).Err()
}

func (s *Store) SetAccountFields(ctx context.Context, id string, fields map[string]string) error {
	vals := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		vals = append(vals, k, v)
	}
	return s.rdb.HSet(ctx, KeyAccountPrefix+id, vals...).Err()
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, KeyAccountPrefix+id)
	pipe.SRem(ctx, KeyAccountIndex, id)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) ListAccountIDs(ctx context.Context) ([]string, error) {
	return s.rdb.SMembers(ctx, KeyAccountIndex).Result()
}

// --- Sticky session ---

func (s *Store) GetStickySession(ctx context.Context, hash string) (string, error) {
	val, err := s.rdb.Get(ctx, KeyStickySession+hash).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (s *Store) SetStickySession(ctx context.Context, hash, accountID string, ttl time.Duration) error {
	return s.rdb.Set(ctx, KeyStickySession+hash, accountID, ttl).Err()
}

// --- Session binding ---

func (s *Store) GetSessionBinding(ctx context.Context, sessionUUID string) (map[string]string, error) {
	val, err := s.rdb.HGetAll(ctx, KeySessionBinding+sessionUUID).Result()
	if err != nil {
		return nil, err
	}
	if len(val) == 0 {
		return nil, nil
	}
	return val, nil
}

func (s *Store) SetSessionBinding(ctx context.Context, sessionUUID, accountID string, ttl time.Duration) error {
	key := KeySessionBinding + sessionUUID
	now := time.Now().UTC().Format(time.RFC3339)
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, "accountId", accountID, "createdAt", now, "lastUsedAt", now)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) RenewSessionBinding(ctx context.Context, sessionUUID string, ttl time.Duration) error {
	key := KeySessionBinding + sessionUUID
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, "lastUsedAt", time.Now().UTC().Format(time.RFC3339))
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// --- Stainless fingerprint ---

func (s *Store) GetStainlessHeaders(ctx context.Context, accountID string) (string, error) {
	val, err := s.rdb.Get(ctx, KeyStainless+accountID).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (s *Store) SetStainlessHeadersNX(ctx context.Context, accountID, headersJSON string) (bool, error) {
	return s.rdb.SetNX(ctx, KeyStainless+accountID, headersJSON, 0).Result()
}

// --- Token refresh lock ---

func (s *Store) AcquireRefreshLock(ctx context.Context, accountID, lockID string) (bool, error) {
	return s.rdb.SetNX(ctx, KeyTokenRefreshLock+accountID, lockID, 60*time.Second).Result()
}

// ReleaseRefreshLock releases only if the lock is owned by the given lockID.
var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end
`)

func (s *Store) ReleaseRefreshLock(ctx context.Context, accountID, lockID string) error {
	_, err := releaseScript.Run(ctx, s.rdb, []string{KeyTokenRefreshLock + accountID}, lockID).Result()
	return err
}

