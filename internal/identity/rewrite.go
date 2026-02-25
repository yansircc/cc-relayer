package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

var userIDPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account__session_([\w-]+)$`)
var sessionUUIDPattern = regexp.MustCompile(`session_([a-f0-9-]{36})$`)

// RewriteUserID replaces the user_id to match the account's real identity
// while maintaining session consistency.
func RewriteUserID(originalUserID, accountID, accountUUID string) string {
	matches := userIDPattern.FindStringSubmatch(originalUserID)
	if len(matches) < 3 {
		return buildUserID(accountID, accountUUID, "default")
	}
	return buildUserID(accountID, accountUUID, matches[2])
}

// ExtractSessionUUID extracts the session UUID from a user_id string.
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

func buildUserID(accountID, accountUUID, sessionTail string) string {
	accountHash := deriveAccountHash(accountUUID, accountID)
	stableSession := deriveSessionUUID(accountID, sessionTail)
	return fmt.Sprintf("user_%s_account__session_%s", accountHash, stableSession)
}

func deriveAccountHash(accountUUID, accountID string) string {
	source := accountUUID
	if source == "" {
		source = accountID
	}
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])
}

func deriveSessionUUID(accountID, sessionTail string) string {
	h := sha256.Sum256([]byte(accountID + ":" + sessionTail))
	hx := hex.EncodeToString(h[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hx[0:8], hx[8:12], hx[12:16], hx[16:20], hx[20:32])
}
