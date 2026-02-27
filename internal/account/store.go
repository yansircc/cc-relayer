package account

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/cc-relayer/internal/store"
)

const claudeSalt = "salt"

// Account represents an OAuth account (Claude or Codex).
type Account struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Provider      string     `json:"provider"` // "claude" or "codex"
	Status        string     `json:"status"`   // active, created, error, disabled
	ErrorMessage  string     `json:"errorMessage,omitempty"`
	Schedulable   bool       `json:"schedulable"`
	Priority      int        `json:"priority"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	LastRefreshAt *time.Time `json:"lastRefreshAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ExpiresAt     int64      `json:"expiresAt"` // milliseconds

	// Proxy config (JSON stored)
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// Rate limit state (Claude)
	FiveHourStatus     string     `json:"fiveHourStatus,omitempty"`
	FiveHourUtil       float64    `json:"fiveHourUtil,omitempty"`
	FiveHourReset      int64      `json:"fiveHourReset,omitempty"`
	SevenDayUtil       float64    `json:"sevenDayUtil,omitempty"`
	SevenDayReset      int64      `json:"sevenDayReset,omitempty"`
	OpusRateLimitEndAt *time.Time `json:"opusRateLimitEndAt,omitempty"`
	OverloadedUntil    *time.Time `json:"overloadedUntil,omitempty"`

	// Rate limit state (Codex)
	CodexPrimaryUtil    float64 `json:"codexPrimaryUtil,omitempty"`
	CodexPrimaryReset   int64   `json:"codexPrimaryReset,omitempty"`
	CodexSecondaryUtil  float64 `json:"codexSecondaryUtil,omitempty"`
	CodexSecondaryReset int64   `json:"codexSecondaryReset,omitempty"`

	// Priority mode
	PriorityMode string `json:"priorityMode,omitempty"` // "auto" or "manual"

	// Extra info (account_uuid used for identity transform)
	ExtInfo map[string]interface{} `json:"extInfo,omitempty"`
}

type ProxyConfig struct {
	Type     string `json:"type"` // socks5, http, https
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// AccountStore manages Claude Official accounts.
type AccountStore struct {
	store  store.Store
	crypto *Crypto
}

func NewAccountStore(s store.Store, c *Crypto) *AccountStore {
	return &AccountStore{store: s, crypto: c}
}

// Create adds a new account. The refreshToken is encrypted before storage.
func (as *AccountStore) Create(ctx context.Context, email, refreshToken string, proxy *ProxyConfig, priority int, provider string) (*Account, error) {
	id := uuid.New().String()

	encRefresh, err := as.crypto.Encrypt(refreshToken, claudeSalt)
	if err != nil {
		return nil, err
	}

	if provider == "" {
		provider = "claude"
	}

	now := time.Now().UTC()
	fields := map[string]string{
		"id":             id,
		"email":          email,
		"provider":       provider,
		"refreshToken":   encRefresh,
		"status":         "created",
		"schedulable":    "true",
		"priority":       strconv.Itoa(priority),
		"createdAt":      now.Format(time.RFC3339),
		"lastUsedAt":     "",
		"lastRefreshAt":  "",
		"expiresAt":      "0",
		"errorMessage":   "",
		"fiveHourStatus": "",
	}

	if proxy != nil {
		proxyJSON, _ := json.Marshal(proxy)
		fields["proxy"] = string(proxyJSON)
	}

	if err := as.store.SetAccount(ctx, id, fields); err != nil {
		return nil, err
	}

	return &Account{
		ID:          id,
		Email:       email,
		Provider:    provider,
		Status:      "created",
		Schedulable: true,
		Priority:    priority,
		CreatedAt:   now,
		Proxy:       proxy,
	}, nil
}

// Get returns an account by ID with decrypted tokens.
func (as *AccountStore) Get(ctx context.Context, id string) (*Account, error) {
	data, err := as.store.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return as.fromMap(data), nil
}

// List returns all accounts.
func (as *AccountStore) List(ctx context.Context) ([]*Account, error) {
	ids, err := as.store.ListAccountIDs(ctx)
	if err != nil {
		return nil, err
	}

	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		data, err := as.store.GetAccount(ctx, id)
		if err != nil {
			continue
		}
		if len(data) == 0 {
			continue
		}
		accounts = append(accounts, as.fromMap(data))
	}
	return accounts, nil
}

// Delete removes an account.
func (as *AccountStore) Delete(ctx context.Context, id string) error {
	return as.store.DeleteAccount(ctx, id)
}

// Update modifies account fields.
func (as *AccountStore) Update(ctx context.Context, id string, fields map[string]string) error {
	return as.store.SetAccountFields(ctx, id, fields)
}

// GetDecryptedRefreshToken returns the decrypted refresh token.
func (as *AccountStore) GetDecryptedRefreshToken(ctx context.Context, id string) (string, error) {
	data, err := as.store.GetAccount(ctx, id)
	if err != nil {
		return "", err
	}
	enc, ok := data["refreshToken"]
	if !ok || enc == "" {
		return "", nil
	}
	return as.crypto.Decrypt(enc, claudeSalt)
}

// GetDecryptedAccessToken returns the decrypted access token.
func (as *AccountStore) GetDecryptedAccessToken(ctx context.Context, id string) (string, error) {
	data, err := as.store.GetAccount(ctx, id)
	if err != nil {
		return "", err
	}
	enc, ok := data["accessToken"]
	if !ok || enc == "" {
		return "", nil
	}
	return as.crypto.Decrypt(enc, claudeSalt)
}

// StoreTokens encrypts and stores new tokens after a refresh.
func (as *AccountStore) StoreTokens(ctx context.Context, id, accessToken, refreshToken string, expiresIn int) error {
	encAccess, err := as.crypto.Encrypt(accessToken, claudeSalt)
	if err != nil {
		return err
	}
	encRefresh, err := as.crypto.Encrypt(refreshToken, claudeSalt)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second).UnixMilli()

	return as.store.SetAccountFields(ctx, id, map[string]string{
		"accessToken":   encAccess,
		"refreshToken":  encRefresh,
		"expiresAt":     strconv.FormatInt(expiresAt, 10),
		"lastRefreshAt": now.Format(time.RFC3339),
		"status":        "active",
		"errorMessage":  "",
		// Clear temporary cooldown markers after a successful refresh.
		"overloadedAt":    "",
		"overloadedUntil": "",
	})
}

// fromMap converts a Redis hash map to an Account struct.
func (as *AccountStore) fromMap(m map[string]string) *Account {
	priorityMode := m["priorityMode"]
	if priorityMode == "" {
		priorityMode = "auto"
	}
	provider := m["provider"]
	if provider == "" {
		provider = "claude"
	}
	a := &Account{
		ID:                  m["id"],
		Email:               m["email"],
		Provider:            provider,
		Status:              m["status"],
		ErrorMessage:        m["errorMessage"],
		Schedulable:         m["schedulable"] == "true",
		Priority:            atoi(m["priority"], 50),
		PriorityMode:        priorityMode,
		ExpiresAt:           atoi64(m["expiresAt"], 0),
		FiveHourStatus:      m["fiveHourStatus"],
		FiveHourUtil:        atof(m["fiveHourUtil"]),
		FiveHourReset:       atoi64(m["fiveHourReset"], 0),
		SevenDayUtil:        atof(m["sevenDayUtil"]),
		SevenDayReset:       atoi64(m["sevenDayReset"], 0),
		CodexPrimaryUtil:    atof(m["codexPrimaryUtil"]),
		CodexPrimaryReset:   atoi64(m["codexPrimaryReset"], 0),
		CodexSecondaryUtil:  atof(m["codexSecondaryUtil"]),
		CodexSecondaryReset: atoi64(m["codexSecondaryReset"], 0),
	}

	if t, err := time.Parse(time.RFC3339, m["createdAt"]); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, m["lastUsedAt"]); err == nil {
		a.LastUsedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, m["lastRefreshAt"]); err == nil {
		a.LastRefreshAt = &t
	}
	if t, err := time.Parse(time.RFC3339, m["opusRateLimitEndAt"]); err == nil {
		a.OpusRateLimitEndAt = &t
	}
	if t, err := time.Parse(time.RFC3339, m["overloadedUntil"]); err == nil {
		a.OverloadedUntil = &t
	}

	if proxyStr := m["proxy"]; proxyStr != "" {
		var p ProxyConfig
		if json.Unmarshal([]byte(proxyStr), &p) == nil && p.Host != "" {
			a.Proxy = &p
		}
	}

	if extStr := m["extInfo"]; extStr != "" {
		var ext map[string]interface{}
		if json.Unmarshal([]byte(extStr), &ext) == nil {
			a.ExtInfo = ext
		}
	}

	return a
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func atoi64(s string, def int64) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return def
}

func atof(s string) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return 0
}
