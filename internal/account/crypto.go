package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/scrypt"
)

// Crypto handles AES-256-CBC encryption compatible with the Node.js project.
// The encryption format is "{iv_hex}:{ciphertext_hex}".
type Crypto struct {
	encryptionKey string
	mu            sync.RWMutex
	derivedKeys   map[string][]byte // salt → derived key cache
}

func NewCrypto(encryptionKey string) *Crypto {
	return &Crypto{
		encryptionKey: encryptionKey,
		derivedKeys:   make(map[string][]byte),
	}
}

// DeriveKey derives an AES-256 key using scrypt. Result is cached.
// Salt values must match the Node.js project:
//   - Claude Official: "salt"
func (c *Crypto) DeriveKey(salt string) ([]byte, error) {
	c.mu.RLock()
	if key, ok := c.derivedKeys[salt]; ok {
		c.mu.RUnlock()
		return key, nil
	}
	c.mu.RUnlock()

	key, err := scrypt.Key([]byte(c.encryptionKey), []byte(salt), 32768, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("scrypt derive: %w", err)
	}

	c.mu.Lock()
	c.derivedKeys[salt] = key
	c.mu.Unlock()

	return key, nil
}

// Encrypt encrypts plaintext using AES-256-CBC with a random IV.
// Returns format: "{iv_hex}:{ciphertext_hex}"
func (c *Crypto) Encrypt(plaintext string, salt string) (string, error) {
	key, err := c.DeriveKey(salt)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("rand iv: %w", err)
	}

	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return hex.EncodeToString(iv) + ":" + hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts data in format "{iv_hex}:{ciphertext_hex}".
func (c *Crypto) Decrypt(encrypted string, salt string) (string, error) {
	key, err := c.DeriveKey(salt)
	if err != nil {
		return "", err
	}

	parts := strings.SplitN(encrypted, ":", 2)
	if len(parts) != 2 {
		return "", errors.New("invalid encrypted format: missing ':'")
	}

	iv, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode iv: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("invalid iv length: %d", len(iv))
	}

	ciphertext, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext not block-aligned: %d", len(ciphertext))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", fmt.Errorf("unpad: %w", err)
	}

	return string(unpadded), nil
}

// HashAPIKey computes SHA-256(apiKey + encryptionKey) matching the Node.js project.
func (c *Crypto) HashAPIKey(apiKey string) string {
	h := sha256.Sum256([]byte(apiKey + c.encryptionKey))
	return hex.EncodeToString(h[:])
}

// --- PKCS7 padding ---

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	pad := make([]byte, padding)
	for i := range pad {
		pad[i] = byte(padding)
	}
	return append(data, pad...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding: %d", padding)
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, errors.New("invalid padding bytes")
		}
	}
	return data[:len(data)-padding], nil
}
