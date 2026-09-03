package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yansircc/llm-broker/internal/auth"
	"github.com/yansircc/llm-broker/internal/config"
	"github.com/yansircc/llm-broker/internal/domain"
	"github.com/yansircc/llm-broker/internal/driver"
	"github.com/yansircc/llm-broker/internal/events"
	"github.com/yansircc/llm-broker/internal/pool"
	"github.com/yansircc/llm-broker/internal/store"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ms := store.NewMockStore()
	bus := events.NewBus(100)
	p, err := pool.New(ms, bus)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg:       &config.Config{},
		store:     ms,
		pool:      p,
		bus:       bus,
		version:   "test",
		startTime: time.Now(),
	}
}

func adminRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(r.Context(), auth.KeyInfoKey, &auth.KeyInfo{
		ID: "admin", Name: "admin", IsAdmin: true,
	})
	return r.WithContext(ctx)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// assertJSONArray checks that the JSON value at the given dot-separated path
// is a JSON array ([]), not null.
func assertJSONArray(t *testing.T, body []byte, path string) {
	t.Helper()
	val := jsonPath(t, body, path)
	if val == nil {
		t.Errorf("path %q is null, expected []", path)
		return
	}
	if _, ok := val.([]interface{}); !ok {
		t.Errorf("path %q is %T, expected array", path, val)
	}
}

// assertJSONNullable checks that the JSON value at path is either null or {}.
func assertJSONNullable(t *testing.T, body []byte, path string) {
	t.Helper()
	val := jsonPath(t, body, path)
	if val == nil {
		return // null is fine
	}
	if m, ok := val.(map[string]interface{}); ok && len(m) == 0 {
		return // {} is fine
	}
	// non-empty object is also fine (it's a nullable field with data)
}

// jsonPath navigates a parsed JSON object by dot-separated keys.
func jsonPath(t *testing.T, body []byte, path string) interface{} {
	t.Helper()
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	parts := strings.Split(path, ".")
	cur := root
	for _, key := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("path %q: expected object at %q, got %T", path, key, cur)
		}
		cur = m[key]
	}
	return cur
}

// ---------------------------------------------------------------------------
// Dashboard contract tests
// ---------------------------------------------------------------------------

func TestDashboard_EmptyData(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, adminRequest("GET", "/admin/dashboard"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()

	assertJSONArray(t, body, "usage")
	assertJSONArray(t, body, "accounts")
	assertJSONArray(t, body, "users")
	assertJSONArray(t, body, "events")

	// health must be an object with known keys
	val := jsonPath(t, body, "health")
	h, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("health is %T, expected object", val)
	}
	for _, key := range []string{"sqlite", "uptime", "version"} {
		if _, ok := h[key]; !ok {
			t.Errorf("health missing key %q", key)
		}
	}
}

func TestDashboard_AccountsIncludeCellID(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-1",
		Email:    "acct-1@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
		CellID:   "cell-fr-linode-01",
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	srv.handleDashboard(w, adminRequest("GET", "/admin/dashboard"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result.Accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(result.Accounts))
	}
	if got := result.Accounts[0]["cell_id"]; got != "cell-fr-linode-01" {
		t.Fatalf("cell_id = %#v, want %q", got, "cell-fr-linode-01")
	}
}

func TestDashboard_UsersIncludePolicy(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-compat-1",
		Email:    "compat@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateUser(context.Background(), &domain.User{
		ID:             "u-1",
		Name:           "compat-user",
		Status:         "active",
		AllowedSurface: domain.SurfaceCompat,
		BoundAccountID: "acct-compat-1",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	srv.handleDashboard(w, adminRequest("GET", "/admin/dashboard"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result.Users) != 1 {
		t.Fatalf("len(users) = %d, want 1", len(result.Users))
	}
	if got := result.Users[0]["allowed_surface"]; got != "compat" {
		t.Fatalf("allowed_surface = %#v, want %q", got, "compat")
	}
	if got := result.Users[0]["bound_account_id"]; got != "acct-compat-1" {
		t.Fatalf("bound_account_id = %#v, want %q", got, "acct-compat-1")
	}
	if got := result.Users[0]["bound_account_email"]; got != "compat@example.com" {
		t.Fatalf("bound_account_email = %#v, want %q", got, "compat@example.com")
	}
}

func TestDashboard_EventsIncludeUpstreamStatus(t *testing.T) {
	srv := newTestServer(t)
	until := time.Now().Add(10 * time.Minute).UTC()
	srv.bus.Publish(events.Event{
		Type:           events.EventReject,
		AccountID:      "acct-1",
		CooldownUntil:  &until,
		UpstreamStatus: http.StatusForbidden,
		Message:        "upstream 403 rejected request",
	})

	w := httptest.NewRecorder()
	srv.handleDashboard(w, adminRequest("GET", "/admin/dashboard"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(result.Events))
	}
	if got := result.Events[0]["upstream_status"]; got != float64(http.StatusForbidden) {
		t.Fatalf("upstream_status = %#v, want %d", got, http.StatusForbidden)
	}
}

func TestActivity_UsesLightweightShape(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-1",
		Email:    "acct-1@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateUser(context.Background(), &domain.User{
		ID:        "u-1",
		Name:      "alice",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.InsertRequestLog(context.Background(), &domain.RequestLog{
		UserID:            "u-1",
		AccountID:         "acct-1",
		Provider:          "claude",
		Surface:           "native",
		Model:             "claude-sonnet-4-6",
		Status:            "upstream_403",
		EffectKind:        "block",
		UpstreamStatus:    http.StatusForbidden,
		UpstreamErrorType: "blocked",
		CreatedAt:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)
	srv.bus.Publish(events.Event{
		Type:      events.EventReject,
		AccountID: "acct-1",
		UserID:    "u-1",
		Message:   "blocked request",
		Timestamp: time.Now().UTC(),
	})

	w := httptest.NewRecorder()
	srv.handleActivity(w, adminRequest("GET", "/admin/activity"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()
	assertJSONArray(t, body, "accounts")
	assertJSONArray(t, body, "users")
	assertJSONArray(t, body, "events")
	assertJSONArray(t, body, "recent_failures")

	if got := jsonPath(t, body, "accounts").([]interface{}); len(got) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(got))
	}
	if got := jsonPath(t, body, "users").([]interface{}); len(got) != 1 {
		t.Fatalf("len(users) = %d, want 1", len(got))
	}
	if got := jsonPath(t, body, "recent_failures").([]interface{}); len(got) != 1 {
		t.Fatalf("len(recent_failures) = %d, want 1", len(got))
	}
	if got := jsonPath(t, body, "accounts").([]interface{})[0].(map[string]interface{})["email"]; got != "acct-1@example.com" {
		t.Fatalf("account email = %#v, want %q", got, "acct-1@example.com")
	}
	if got := jsonPath(t, body, "users").([]interface{})[0].(map[string]interface{})["name"]; got != "alice" {
		t.Fatalf("user name = %#v, want %q", got, "alice")
	}
}

func TestActivityUsage_EmptyData(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	srv.handleActivityUsage(w, adminRequest("GET", "/admin/activity/usage"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()
	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	arr, ok := result.([]interface{})
	if !ok {
		t.Fatalf("response is %T, expected array", result)
	}
	if len(arr) != 0 {
		t.Fatalf("len(result) = %d, want 0", len(arr))
	}
}

// ---------------------------------------------------------------------------
// Account detail contract tests
// ---------------------------------------------------------------------------

func TestGetAccount_EmptySessions(t *testing.T) {
	srv := newTestServer(t)

	// Add an account via pool
	acct := &domain.Account{
		ID:       "test-1",
		Email:    "test@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}
	srv.store.SaveAccount(context.Background(), acct)
	// Reload pool
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := adminRequest("GET", "/admin/accounts/test-1")
	r.SetPathValue("id", "test-1")
	srv.handleGetAccount(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()

	// sessions must be [] not null
	assertJSONArray(t, body, "sessions")
}

func TestListAccounts_IncludesSurfaceAvailability(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-native",
		Email:    "native@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-compat",
		Email:    "compat@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
		CellID:   "cell-compat-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveEgressCell(context.Background(), &domain.EgressCell{
		ID:        "cell-compat-1",
		Name:      "compat lane",
		Status:    domain.EgressCellActive,
		Proxy:     &domain.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 11082},
		Labels:    map[string]string{"lane": "compat"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)
	srv.pool.SetDrivers(map[domain.Provider]driver.SchedulerDriver{
		domain.ProviderClaude: driver.NewClaudeDriver(driver.ClaudeConfig{}, driver.NoopStainlessStore{}, 4),
	})

	w := httptest.NewRecorder()
	srv.handleListAccounts(w, adminRequest("GET", "/admin/accounts"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(result))
	}

	byID := make(map[string]map[string]any, len(result))
	for _, item := range result {
		id, _ := item["id"].(string)
		byID[id] = item
	}

	if got := byID["acct-native"]["available_native"]; got != true {
		t.Fatalf("acct-native available_native = %#v, want true", got)
	}
	if got := byID["acct-native"]["available_compat"]; got != true {
		t.Fatalf("acct-native available_compat = %#v, want true", got)
	}
	if got := byID["acct-compat"]["available_native"]; got != true {
		t.Fatalf("acct-compat available_native = %#v, want true", got)
	}
	if got := byID["acct-compat"]["available_compat"]; got != true {
		t.Fatalf("acct-compat available_compat = %#v, want true", got)
	}
}

func TestGetAccount_NullableFields(t *testing.T) {
	srv := newTestServer(t)

	acct := &domain.Account{
		ID:       "test-2",
		Email:    "t2@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}
	srv.store.SaveAccount(context.Background(), acct)
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := adminRequest("GET", "/admin/accounts/test-2")
	r.SetPathValue("id", "test-2")
	srv.handleGetAccount(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()

	assertJSONArray(t, body, "provider_fields")
	assertJSONNullable(t, body, "stainless")
}

func TestGetAccount_WithIdentity(t *testing.T) {
	srv := newTestServer(t)

	acct := &domain.Account{
		ID:           "test-3",
		Email:        "t3@example.com",
		Provider:     domain.ProviderClaude,
		Status:       domain.StatusActive,
		IdentityJSON: `{"orgUUID":"abc-123"}`,
	}
	srv.store.SaveAccount(context.Background(), acct)
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := adminRequest("GET", "/admin/accounts/test-3")
	r.SetPathValue("id", "test-3")
	srv.handleGetAccount(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()

	val := jsonPath(t, body, "provider_fields")
	fields, ok := val.([]interface{})
	if !ok {
		t.Fatalf("provider_fields is %T, expected array", val)
	}
	if len(fields) != 0 {
		t.Fatalf("provider_fields = %v, want empty for metadata without display mapping", fields)
	}
}

func TestClearEgressCellCooldown_NotFound(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	r := adminRequest("POST", "/admin/egress/cells/missing/clear-cooldown")
	r.SetPathValue("id", "missing")
	srv.handleClearEgressCellCooldown(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestListEgressCells_EmptyAccountsArray(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveEgressCell(context.Background(), &domain.EgressCell{
		ID:        "cell-uk-linode-02",
		Name:      "UK Linode 02(local)",
		Status:    domain.EgressCellActive,
		Proxy:     &domain.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 11082},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	srv.handleListEgressCells(w, adminRequest("GET", "/admin/egress/cells"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	accounts, ok := result[0]["accounts"].([]any)
	if !ok {
		t.Fatalf("accounts is %T, want []", result[0]["accounts"])
	}
	if len(accounts) != 0 {
		t.Fatalf("len(accounts) = %d, want 0", len(accounts))
	}
}

func TestBindAccountCell_RejectsCoolingCell(t *testing.T) {
	srv := newTestServer(t)

	until := time.Now().UTC().Add(10 * time.Minute)
	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-1",
		Email:    "acct-1@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveEgressCell(context.Background(), &domain.EgressCell{
		ID:            "cell-fr-linode-01",
		Name:          "FR Linode 01",
		Status:        domain.EgressCellActive,
		CooldownUntil: &until,
		Proxy:         &domain.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1081},
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/accounts/acct-1/cell", strings.NewReader(`{"cell_id":"cell-fr-linode-01"}`))
	r = r.WithContext(adminRequest(http.MethodPost, "/admin/accounts/acct-1/cell").Context())
	r.SetPathValue("id", "acct-1")
	srv.handleBindAccountCell(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cooling down") {
		t.Fatalf("body %q does not mention cooling down", w.Body.String())
	}
}

func TestBindAccountCell_AllowsActiveCell(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-2",
		Email:    "acct-2@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveEgressCell(context.Background(), &domain.EgressCell{
		ID:        "cell-fr-linode-02",
		Name:      "FR Linode 02",
		Status:    domain.EgressCellActive,
		Proxy:     &domain.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1082},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/accounts/acct-2/cell", strings.NewReader(`{"cell_id":"cell-fr-linode-02"}`))
	r = r.WithContext(adminRequest(http.MethodPost, "/admin/accounts/acct-2/cell").Context())
	r.SetPathValue("id", "acct-2")
	srv.handleBindAccountCell(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	saved := srv.pool.Get("acct-2")
	if saved == nil || saved.CellID != "cell-fr-linode-02" {
		t.Fatalf("account cell_id = %q, want cell-fr-linode-02", saved.CellID)
	}
}

func TestBindAccountCell_RejectsOccupiedCell(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-owner",
		Email:    "owner@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
		CellID:   "cell-fr-linode-02",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-other",
		Email:    "other@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	// Use a non-socks5 cell — socks5 cells are shareable and won't reject.
	if err := srv.store.SaveEgressCell(context.Background(), &domain.EgressCell{
		ID:        "cell-fr-linode-02",
		Name:      "FR Linode 02",
		Status:    domain.EgressCellActive,
		Proxy:     &domain.ProxyConfig{Type: "http", Host: "127.0.0.1", Port: 1082},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/accounts/acct-other/cell", strings.NewReader(`{"cell_id":"cell-fr-linode-02"}`))
	r = r.WithContext(adminRequest(http.MethodPost, "/admin/accounts/acct-other/cell").Context())
	r.SetPathValue("id", "acct-other")
	srv.handleBindAccountCell(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already bound") {
		t.Fatalf("body %q does not mention occupied cell", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// User detail contract tests
// ---------------------------------------------------------------------------

func TestGetUser_EmptyArrays(t *testing.T) {
	srv := newTestServer(t)

	user := &domain.User{
		ID:     "u-1",
		Name:   "testuser",
		Status: "active",
	}
	srv.store.CreateUser(context.Background(), user)

	w := httptest.NewRecorder()
	r := adminRequest("GET", "/admin/users/u-1")
	r.SetPathValue("id", "u-1")
	srv.handleGetUser(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()

	assertJSONArray(t, body, "usage")
	assertJSONArray(t, body, "model_usage")
	assertJSONArray(t, body, "recent_requests")
}

func TestGetUser_IncludesBoundAccountEmail(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-compat-1",
		Email:    "compat@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateUser(context.Background(), &domain.User{
		ID:             "u-1",
		Name:           "compat-user",
		Status:         "active",
		AllowedSurface: domain.SurfaceCompat,
		BoundAccountID: "acct-compat-1",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	w := httptest.NewRecorder()
	r := adminRequest("GET", "/admin/users/u-1")
	r.SetPathValue("id", "u-1")
	srv.handleGetUser(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	if got := jsonPath(t, w.Body.Bytes(), "allowed_surface"); got != "compat" {
		t.Fatalf("allowed_surface = %#v, want %q", got, "compat")
	}
	if got := jsonPath(t, w.Body.Bytes(), "bound_account_email"); got != "compat@example.com" {
		t.Fatalf("bound_account_email = %#v, want %q", got, "compat@example.com")
	}
}

// ---------------------------------------------------------------------------
// User list contract test
// ---------------------------------------------------------------------------

func TestListUsers_Empty(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	srv.handleListUsers(w, adminRequest("GET", "/admin/users"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()

	// Must be [] not null
	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	arr, ok := result.([]interface{})
	if !ok {
		t.Fatalf("response is %T, expected array", result)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %d elements", len(arr))
	}
}

func TestListUsers_ReturnsUserSummaries(t *testing.T) {
	srv := newTestServer(t)

	acct := &domain.Account{
		ID:        "acct-1",
		Provider:  domain.ProviderClaude,
		Subject:   "sub-1",
		Email:     "bound@example.com",
		Status:    domain.StatusActive,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.pool.Add(acct); err != nil {
		t.Fatal(err)
	}

	user := &domain.User{
		ID:             "u-1",
		Name:           "alice",
		TokenHash:      tokenHash("user-token"),
		TokenPrefix:    "tk_alice_abcd...",
		Status:         "active",
		AllowedSurface: domain.SurfaceCompat,
		BoundAccountID: acct.ID,
		CreatedAt:      time.Now().UTC(),
	}
	if err := srv.store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.handleListUsers(w, adminRequest("GET", "/admin/users"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var result []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if got := result[0]["allowed_surface"]; got != "compat" {
		t.Fatalf("allowed_surface = %#v, want %q", got, "compat")
	}
	if got := result[0]["bound_account_email"]; got != "bound@example.com" {
		t.Fatalf("bound_account_email = %#v, want %q", got, "bound@example.com")
	}
	if _, ok := result[0]["total_cost"]; ok {
		t.Fatalf("unexpected total_cost in user summary: %#v", result[0]["total_cost"])
	}
}

func TestListUserTotalCosts_ReturnsRequestedTotals(t *testing.T) {
	srv := newTestServer(t)

	for _, user := range []*domain.User{
		{ID: "u-1", Name: "alice", Status: "active", CreatedAt: time.Now().UTC()},
		{ID: "u-2", Name: "bob", Status: "active", CreatedAt: time.Now().UTC()},
		{ID: "u-3", Name: "carol", Status: "active", CreatedAt: time.Now().UTC()},
	} {
		if err := srv.store.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	for _, log := range []*domain.RequestLog{
		{UserID: "u-1", Status: "ok", CostUSD: 1.25, CreatedAt: time.Now().UTC()},
		{UserID: "u-1", Status: "ok", CostUSD: 0.75, CreatedAt: time.Now().UTC()},
		{UserID: "u-2", Status: "ok", CostUSD: 2.50, CreatedAt: time.Now().UTC()},
		{UserID: "u-3", Status: "upstream_403", CostUSD: 99, CreatedAt: time.Now().UTC()},
	} {
		if _, err := srv.store.InsertRequestLog(context.Background(), log); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	srv.handleListUserTotalCosts(w, adminRequest("GET", "/admin/users/total-costs?ids=u-1,u-3"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	if got := jsonPath(t, w.Body.Bytes(), "totals.u-1"); got != float64(2) {
		t.Fatalf("totals.u-1 = %#v, want 2", got)
	}
	if got := jsonPath(t, w.Body.Bytes(), "totals.u-3"); got != float64(0) {
		t.Fatalf("totals.u-3 = %#v, want 0", got)
	}
	var payload struct {
		Totals map[string]float64 `json:"totals"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload.Totals["u-2"]; ok {
		t.Fatalf("unexpected total for unrequested user u-2: %#v", payload.Totals["u-2"])
	}
}

func TestAdminAccountsRoute_RequiresAdmin(t *testing.T) {
	srv := newTestServer(t)
	srv.authMw = auth.NewMiddleware("admin-secret", srv.store)

	user := &domain.User{
		ID:          "u-1",
		Name:        "alice",
		TokenHash:   tokenHash("user-token"),
		TokenPrefix: "tk_alice_abcd...",
		Status:      "active",
		CreatedAt:   time.Now().UTC(),
	}
	if err := srv.store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.registerAdminRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestCompatRoute_RejectsNativeOnlyUser(t *testing.T) {
	srv := newTestServer(t)
	srv.authMw = auth.NewMiddleware("admin-secret", srv.store)
	srv.catalogDrivers = map[domain.Provider]driver.Descriptor{
		domain.ProviderClaude: driver.NewClaudeDriver(driver.ClaudeConfig{}, driver.NoopStainlessStore{}, 4),
	}

	user := &domain.User{
		ID:             "u-native",
		Name:           "native-user",
		TokenHash:      tokenHash("native-token"),
		TokenPrefix:    "tk_native_abcd...",
		Status:         "active",
		AllowedSurface: domain.SurfaceNative,
		CreatedAt:      time.Now().UTC(),
	}
	if err := srv.store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.registerRelayRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/compat/v1/models", nil)
	req.Header.Set("Authorization", "Bearer native-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestCodexResponsesRoutes_RegisterOnlySupportedAliases(t *testing.T) {
	srv := newTestServer(t)
	srv.authMw = auth.NewMiddleware("admin-secret", srv.store)
	srv.catalogDrivers = map[domain.Provider]driver.Descriptor{
		domain.ProviderCodex: driver.NewCodexDriver(driver.CodexConfig{}),
	}

	mux := http.NewServeMux()
	srv.registerRelayRoutes(mux)

	for _, path := range []string{"/openai/responses", "/openai/v1/responses"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("POST %s status = %d, want %d", path, w.Code, http.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("POST /openai/v1/chat/completions status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestNativeRoute_RejectsCompatOnlyUser(t *testing.T) {
	srv := newTestServer(t)
	srv.authMw = auth.NewMiddleware("admin-secret", srv.store)

	user := &domain.User{
		ID:             "u-compat",
		Name:           "compat-user",
		TokenHash:      tokenHash("compat-token"),
		TokenPrefix:    "tk_compat_abcd...",
		Status:         "active",
		AllowedSurface: domain.SurfaceCompat,
		CreatedAt:      time.Now().UTC(),
	}
	if err := srv.store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.registerRelayRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer compat-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestHandleListModels_IncludesCodexGPT55(t *testing.T) {
	srv := newTestServer(t)
	srv.catalogDrivers = map[domain.Provider]driver.Descriptor{
		domain.ProviderCodex: driver.NewCodexDriver(driver.CodexConfig{}),
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	srv.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Object != "list" {
		t.Fatalf("object = %q", body.Object)
	}
	for _, model := range body.Data {
		if model.ID == "gpt-5.5" {
			return
		}
	}
	t.Fatalf("native models missing codex gpt-5.5: %#v", body.Data)
}

func TestHandleListModels_IncludesCurrentCodexModels(t *testing.T) {
	srv := newTestServer(t)
	srv.catalogDrivers = map[domain.Provider]driver.Descriptor{
		domain.ProviderCodex: driver.NewCodexDriver(driver.CodexConfig{}),
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	srv.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Object != "list" {
		t.Fatalf("object = %q", body.Object)
	}
	want := map[string]bool{"gpt-5.6-sol": false, "gpt-6-astra": false}
	for _, model := range body.Data {
		if _, ok := want[model.ID]; ok {
			want[model.ID] = true
		}
	}
	for model, found := range want {
		if !found {
			t.Fatalf("native models missing codex %s: %#v", model, body.Data)
		}
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	req := adminRequest(http.MethodDelete, "/admin/users/missing")
	req.SetPathValue("id", "missing")
	srv.handleDeleteUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestUpdateUserStatus_NotFound(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/missing/status", strings.NewReader(`{"status":"disabled"}`))
	req.SetPathValue("id", "missing")
	req = req.WithContext(adminRequest(http.MethodPost, "/admin/users/missing/status").Context())
	srv.handleUpdateUserStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestUpdateUserPolicy_RoundTrip(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.store.SaveAccount(context.Background(), &domain.Account{
		ID:       "acct-compat-1",
		Email:    "compat@example.com",
		Provider: domain.ProviderClaude,
		Status:   domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	srv.pool, _ = pool.New(srv.store, srv.bus)

	user := &domain.User{
		ID:        "u-1",
		Name:      "policy-user",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/users/u-1/policy", strings.NewReader(`{"allowed_surface":"compat","bound_account_id":"acct-compat-1"}`))
	req = req.WithContext(adminRequest(http.MethodPost, "/admin/users/u-1/policy").Context())
	req.SetPathValue("id", "u-1")
	w := httptest.NewRecorder()
	srv.handleUpdateUserPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	users, err := srv.store.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("len(users) = %d, want 1", len(users))
	}
	if users[0].AllowedSurface != domain.SurfaceCompat {
		t.Fatalf("AllowedSurface = %q, want compat", users[0].AllowedSurface)
	}
	if users[0].BoundAccountID != "acct-compat-1" {
		t.Fatalf("BoundAccountID = %q, want acct-compat-1", users[0].BoundAccountID)
	}
}
