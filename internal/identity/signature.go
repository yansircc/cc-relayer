package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// SignatureCache caches thinking block signatures in memory.
// Claude Code strips thoughtSignature from thinking blocks, but the API
// needs them for conversation continuity.
type SignatureCache struct {
	mu       sync.RWMutex
	items    map[string]sigEntry
	sessions map[string]int // sessionID → count of entries
}

type sigEntry struct {
	Signature string
	SessionID string
	ExpiresAt time.Time
}

const (
	signatureTTL      = 1 * time.Hour
	maxPerSession     = 100
)

func NewSignatureCache() *SignatureCache {
	sc := &SignatureCache{
		items:    make(map[string]sigEntry),
		sessions: make(map[string]int),
	}
	go sc.cleanupLoop()
	return sc
}

// Store caches a thinking signature. Enforces max 100 entries per session.
func (sc *SignatureCache) Store(sessionID, thinkingText, signature string) {
	if signature == "" {
		return
	}
	key := signatureKey(sessionID, thinkingText)

	sc.mu.Lock()
	// Check if this is a new entry (not an update)
	if _, exists := sc.items[key]; !exists {
		if sc.sessions[sessionID] >= maxPerSession {
			sc.mu.Unlock()
			return // at capacity for this session
		}
		sc.sessions[sessionID]++
	}
	sc.items[key] = sigEntry{
		Signature: signature,
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(signatureTTL),
	}
	sc.mu.Unlock()
}

// Lookup retrieves a cached signature for a thinking block.
func (sc *SignatureCache) Lookup(sessionID, thinkingText string) string {
	key := signatureKey(sessionID, thinkingText)

	sc.mu.RLock()
	entry, ok := sc.items[key]
	sc.mu.RUnlock()

	if !ok || time.Now().After(entry.ExpiresAt) {
		return ""
	}
	return entry.Signature
}

func signatureKey(sessionID, thinkingText string) string {
	h := sha256.Sum256([]byte(sessionID + ":" + thinkingText))
	return hex.EncodeToString(h[:])
}

func (sc *SignatureCache) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	for range ticker.C {
		sc.cleanup()
	}
}

func (sc *SignatureCache) cleanup() {
	now := time.Now()
	sc.mu.Lock()
	defer sc.mu.Unlock()

	for key, entry := range sc.items {
		if now.After(entry.ExpiresAt) {
			delete(sc.items, key)
			if entry.SessionID != "" {
				sc.sessions[entry.SessionID]--
				if sc.sessions[entry.SessionID] <= 0 {
					delete(sc.sessions, entry.SessionID)
				}
			}
		}
	}
}
