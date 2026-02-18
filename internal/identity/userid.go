package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// userIDPattern matches Claude Code user_id format:
// user_{64hex}_account__session_{uuid}
var userIDPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account__session_([\w-]+)$`)

// sessionUUIDPattern extracts the session UUID from user_id.
var sessionUUIDPattern = regexp.MustCompile(`session_([a-f0-9-]{36})$`)

// RewriteUserID replaces the user_id to match the account's real identity
// while maintaining session consistency.
//
// Input:  user_{original_hash}_account__session_{session_uuid}
// Output: user_{account_hash}_account__session_{stable_uuid}
//
// Where:
//   - account_hash: derived from account's real UUID (extInfo.account_uuid)
//   - stable_uuid: SHA-256(accountID + session_uuid) formatted as UUID
func RewriteUserID(originalUserID, accountID, accountUUID string) string {
	matches := userIDPattern.FindStringSubmatch(originalUserID)
	if len(matches) < 3 {
		// Not a Claude Code format, return a synthetic one
		return buildUserID(accountID, accountUUID, "default")
	}

	sessionPart := matches[2]
	return buildUserID(accountID, accountUUID, sessionPart)
}

func buildUserID(accountID, accountUUID, sessionTail string) string {
	// Account hash: use real account UUID if available, otherwise derive from accountID
	accountHash := deriveAccountHash(accountUUID, accountID)

	// Stable session UUID: deterministic from accountID + sessionTail
	stableSession := deriveSessionUUID(accountID, sessionTail)

	return fmt.Sprintf("user_%s_account__session_%s", accountHash, stableSession)
}

// deriveAccountHash produces 64 hex chars for the account portion of user_id.
func deriveAccountHash(accountUUID, accountID string) string {
	source := accountUUID
	if source == "" {
		source = accountID
	}
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])
}

// deriveSessionUUID produces a UUID-formatted string from accountID + sessionTail.
func deriveSessionUUID(accountID, sessionTail string) string {
	h := sha256.Sum256([]byte(accountID + ":" + sessionTail))
	hex := hex.EncodeToString(h[:16])
	// Format as UUID: 8-4-4-4-12
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])
}

// ExtractSessionUUID extracts the session UUID from a user_id string.
// Returns empty string if not found.
func ExtractSessionUUID(userID string) string {
	matches := sessionUUIDPattern.FindStringSubmatch(userID)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// GetAccountUUID extracts account_uuid from the account's extInfo.
func GetAccountUUID(extInfo map[string]interface{}) string {
	if extInfo == nil {
		return ""
	}
	if v, ok := extInfo["account_uuid"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// HasValidUserIDFormat checks if a user_id matches the expected Claude Code format.
func HasValidUserIDFormat(userID string) bool {
	return userIDPattern.MatchString(userID)
}

// ExtractSessionFromUserID gets the session tail from a user_id.
func ExtractSessionFromUserID(userID string) string {
	if idx := strings.LastIndex(userID, "session_"); idx >= 0 {
		return userID[idx+len("session_"):]
	}
	return ""
}
