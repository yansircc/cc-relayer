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
	KeyAPIKeyPrefix     = "apikey:"
	KeyAPIKeyHashMap    = "apikey:hash_map"
	KeyStickySession    = "sticky_session:"
	KeySessionBinding   = "original_session_binding:"
	KeyStainless        = "fmt_claude_req:stainless_headers:"
	KeyConcurrency      = "concurrency:"
	KeyWeeklyOpus       = "usage:opus:weekly:"
	KeyTokenRefreshLock = "token_refresh_lock:claude:"
	KeyUserAgentDaily   = "claude_code_user_agent:daily"
	KeyAdminCreds       = "admin:credentials"
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

// --- API Key operations ---

func (s *Store) GetAPIKey(ctx context.Context, id string) (map[string]string, error) {
	return s.rdb.HGetAll(ctx, KeyAPIKeyPrefix+id).Result()
}

func (s *Store) SetAPIKey(ctx context.Context, id string, fields map[string]string) error {
	vals := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		vals = append(vals, k, v)
	}
	return s.rdb.HSet(ctx, KeyAPIKeyPrefix+id, vals...).Err()
}

func (s *Store) DeleteAPIKey(ctx context.Context, id string) error {
	return s.rdb.Del(ctx, KeyAPIKeyPrefix+id).Err()
}

func (s *Store) SetAPIKeyHash(ctx context.Context, hash, keyID string) error {
	return s.rdb.HSet(ctx, KeyAPIKeyHashMap, hash, keyID).Err()
}

func (s *Store) GetAPIKeyIDByHash(ctx context.Context, hash string) (string, error) {
	return s.rdb.HGet(ctx, KeyAPIKeyHashMap, hash).Result()
}

func (s *Store) DeleteAPIKeyHash(ctx context.Context, hash string) error {
	return s.rdb.HDel(ctx, KeyAPIKeyHashMap, hash).Err()
}

func (s *Store) ListAPIKeyHashes(ctx context.Context) (map[string]string, error) {
	return s.rdb.HGetAll(ctx, KeyAPIKeyHashMap).Result()
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

// --- Concurrency ---

// acquireConcurrencyScript atomically cleans expired slots, checks limit, and adds a new slot.
var acquireConcurrencyScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local count = redis.call('ZCARD', KEYS[1])
if count >= tonumber(ARGV[2]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[3], ARGV[4])
redis.call('EXPIRE', KEYS[1], 330)
return 1
`)

// TryAcquireConcurrencySlot atomically cleans expired slots, checks the limit,
// and acquires a new slot in a single Lua script (no race conditions).
// Returns true if the slot was acquired, false if the limit is reached.
func (s *Store) TryAcquireConcurrencySlot(ctx context.Context, keyID, requestID string, limit int, ttl time.Duration) (bool, error) {
	key := KeyConcurrency + keyID
	now := float64(time.Now().Unix())
	expiresAt := float64(time.Now().Add(ttl).Unix())
	result, err := acquireConcurrencyScript.Run(ctx, s.rdb, []string{key},
		fmt.Sprintf("%f", now), limit, fmt.Sprintf("%f", expiresAt), requestID,
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (s *Store) ReleaseConcurrencySlot(ctx context.Context, keyID, requestID string) error {
	return s.rdb.ZRem(ctx, KeyConcurrency+keyID, requestID).Err()
}

// --- Weekly Opus cost ---

func (s *Store) GetWeeklyOpusCost(ctx context.Context, keyID, weekStr string) (float64, error) {
	val, err := s.rdb.Get(ctx, KeyWeeklyOpus+keyID+":"+weekStr).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f float64
	fmt.Sscanf(val, "%f", &f)
	return f, nil
}

func (s *Store) IncrWeeklyOpusCost(ctx context.Context, keyID, weekStr string, amount float64) error {
	key := KeyWeeklyOpus + keyID + ":" + weekStr
	pipe := s.rdb.Pipeline()
	pipe.IncrByFloat(ctx, key, amount)
	pipe.Expire(ctx, key, 14*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
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

// --- User-Agent cache ---

func (s *Store) GetCachedUserAgent(ctx context.Context) (string, error) {
	val, err := s.rdb.Get(ctx, KeyUserAgentDaily).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (s *Store) SetCachedUserAgent(ctx context.Context, ua string) error {
	return s.rdb.Set(ctx, KeyUserAgentDaily, ua, 25*time.Hour).Err()
}
