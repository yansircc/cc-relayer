package ratelimit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yansir/cc-relayer/internal/store"
)

func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedAccount(t *testing.T, s *store.SQLiteStore, id string, fields map[string]string) {
	t.Helper()
	base := map[string]string{
		"email":       "test@example.com",
		"status":      "active",
		"schedulable": "true",
		"createdAt":   time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range fields {
		base[k] = v
	}
	if err := s.SetAccount(context.Background(), id, base); err != nil {
		t.Fatalf("seed account: %v", err)
	}
}

func TestAllowedWarningDoesNotAutoStop(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-warning"

	seedAccount(t, s, accountID, map[string]string{
		"schedulable": "true",
	})

	mgr.updateFiveHourStatus(context.Background(), accountID, "allowed_warning", "")

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "true" {
		t.Fatalf("schedulable should stay true on warning, got %q", got)
	}
}

func TestCleanupSelfHealsStaleSchedulableFalse(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-stale"

	seedAccount(t, s, accountID, map[string]string{
		"schedulable": "false",
		"status":      "active",
	})

	mgr.cleanup(context.Background())

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "true" {
		t.Fatalf("schedulable should self-heal to true, got %q", got)
	}
}

func TestCleanupKeepsDisabledAccountUnschedulable(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-disabled"

	seedAccount(t, s, accountID, map[string]string{
		"schedulable": "false",
		"status":      "disabled",
	})

	mgr.cleanup(context.Background())

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "false" {
		t.Fatalf("disabled account should stay unschedulable, got %q", got)
	}
}

func TestCleanupKeepsOverloadedAccountUnschedulable(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-overloaded"

	seedAccount(t, s, accountID, map[string]string{
		"schedulable":     "false",
		"status":          "active",
		"overloadedUntil": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
	})

	mgr.cleanup(context.Background())

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "false" {
		t.Fatalf("overloaded account should stay unschedulable, got %q", got)
	}
}

func TestRejectedSetsOverloadedUntil(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-rejected"

	seedAccount(t, s, accountID, map[string]string{
		"schedulable": "true",
	})

	resetTime := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	mgr.updateFiveHourStatus(context.Background(), accountID, "rejected", resetTime)

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "false" {
		t.Fatalf("schedulable should be false after rejected, got %q", got)
	}
	if got := data["overloadedUntil"]; got == "" {
		t.Fatal("overloadedUntil should be set after rejected")
	}
	if got := data["fiveHourStatus"]; got != "rejected" {
		t.Fatalf("fiveHourStatus should be rejected, got %q", got)
	}
}

func TestOverloadRecoveryRestoresSchedulable(t *testing.T) {
	s := newTestStore(t)
	mgr := NewManager(s)
	accountID := "acct-recover"

	// Simulate an account whose overloadedUntil has expired.
	seedAccount(t, s, accountID, map[string]string{
		"schedulable":     "false",
		"status":          "active",
		"overloadedAt":    time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		"overloadedUntil": time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339),
		"fiveHourStatus":  "rejected",
	})

	mgr.cleanup(context.Background())

	data, err := s.GetAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got := data["schedulable"]; got != "true" {
		t.Fatalf("schedulable should be restored after overload recovery, got %q", got)
	}
	if got := data["overloadedUntil"]; got != "" {
		t.Fatalf("overloadedUntil should be cleared, got %q", got)
	}
	if got := data["fiveHourStatus"]; got != "" {
		t.Fatalf("fiveHourStatus should be cleared, got %q", got)
	}
}
