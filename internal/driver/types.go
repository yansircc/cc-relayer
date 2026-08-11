package driver

import (
	"encoding/json"
	"net/http"
	"time"
)

// ErrorPauses holds configurable error pause durations.
// Shared between pool and driver implementations.
type ErrorPauses struct {
	Pause401        time.Duration
	Pause401Refresh time.Duration // short cooldown for background token refresh on 401
	Pause403        time.Duration
	Pause429        time.Duration
	Pause529        time.Duration
}

// EffectKind classifies the outcome of an upstream request.
type EffectKind int

const (
	EffectSuccess     EffectKind = iota
	EffectCooldown               // rate-limit style rejection with cooldown
	EffectReject                 // non-retriable reject without cooldown
	EffectOverload               // 529
	EffectBlock                  // provider block / disabled signal
	EffectAuthFail               // 401
	EffectServerError            // 500
)

// EffectScope determines whether an effect applies to one credential or an entire bucket.
type EffectScope int

const (
	EffectScopeAccount EffectScope = iota
	EffectScopeBucket
)

// Effect is the provider-agnostic outcome of an upstream request.
// Pool.Observe applies it without knowing any provider-specific details.
type Effect struct {
	Kind                 EffectKind
	Scope                EffectScope
	CooldownUntil        time.Time
	ErrorMessage         string
	UpstreamStatus       int
	UpstreamErrorType    string
	UpstreamErrorMessage string
	UpdatedState         json.RawMessage // opaque provider state blob
}

// RelayInput carries the parsed client request.
type RelayInput struct {
	Body          map[string]interface{}
	RawBody       []byte
	Headers       http.Header
	Path          string
	RawQuery      string
	Model         string
	UserID        string // broker user ID, used to synthesize metadata.user_id when absent
	IsStream      bool
	IsCountTokens bool
}

// RelayPlan captures provider-owned request execution decisions.
type RelayPlan struct {
	IsStream      bool
	IsCountTokens bool
	Affinity      RouteAffinity
}

// AffinityContinuity declares whether a request can move between upstream
// identities when its preferred target is unavailable.
type AffinityContinuity string

const (
	AffinityPrefer  AffinityContinuity = "prefer"
	AffinityRequire AffinityContinuity = "require"
)

// RouteAffinity is the provider-owned description of one continuity scope.
// RawKey is normalized and hashed by relay before it reaches persistence or
// logs; provider-specific extraction stays in the driver.
type RouteAffinity struct {
	RawKey     string
	Kind       string
	Continuity AffinityContinuity
}

// CapacityAssessment is the provider-neutral scheduler projection derived from
// opaque provider state for one requested model.
type CapacityAssessment struct {
	Eligible bool
	Priority int
	Class    string
}

// StreamPreflight describes a provider-classified failure found before any
// downstream bytes were committed. A nil Effect means the driver accepted the
// stream and restored resp.Body so normal streaming can replay buffered bytes.
type StreamPreflight struct {
	Effect    *Effect
	ErrorBody []byte
}

// Usage holds token counts from a completed request.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
}

// TokenResponse is the OAuth refresh/exchange response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ExchangeResult holds the tokens and identity from an authorization code exchange.
type ExchangeResult struct {
	AccessToken   string
	RefreshToken  string
	ExpiresIn     int
	Subject       string // REQUIRED: provider-stable subject (orgUUID, chatgptAccountId, Google sub)
	Email         string
	Identity      map[string]string
	ProviderState json.RawMessage
}

// OAuthSession holds PKCE parameters for a pending OAuth flow.
type OAuthSession struct {
	CodeVerifier string `json:"code_verifier"`
	State        string `json:"state"`
}

// UtilWindow represents a rate-limit utilization window.
type UtilWindow struct {
	Label    string // provider-defined display label
	Pct      int    // 0-100
	Reset    int64  // unix seconds
	SubLabel string // optional: second family name (e.g. "spark")
	SubPct   int    // optional: second family pct (-1 = absent)
	SubReset int64  // optional: second family reset
}

type AccountField struct {
	Label string
	Value string
}

type ProbeResult struct {
	Effect        Effect
	Observe       bool
	ClearCooldown bool
}

type ProviderInfo struct {
	Label               string
	RelayPaths          []string
	OAuthStateRequired  bool
	CallbackPlaceholder string
	CallbackHint        string
	ProbeLabel          string
}

type Model struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	ContextWindow int    `json:"context_window"`
}

// RequestValidationError indicates that the client request is invalid at the
// driver boundary and should be rejected locally without forwarding upstream.
type RequestValidationError struct {
	StatusCode int
	Message    string
}

func (e *RequestValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewRequestValidationError(statusCode int, message string) error {
	return &RequestValidationError{
		StatusCode: statusCode,
		Message:    message,
	}
}

func mustMarshalJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
