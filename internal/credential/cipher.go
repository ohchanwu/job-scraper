// Package credential encrypts per-user AI provider credentials before storage.
package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// EncryptionVersionAES256GCM identifies the first persisted credential
	// envelope format.
	EncryptionVersionAES256GCM int16 = 1
)

// Cipher seals and opens provider credentials bound to one user and provider.
type Cipher interface {
	Seal(userID int64, provider, plaintext string) (ciphertext, nonce []byte, version int16, err error)
	Open(userID int64, provider string, ciphertext, nonce []byte, version int16) (string, error)
}

// AESGCMCipher implements Cipher with AES-256-GCM.
type AESGCMCipher struct {
	aead cipher.AEAD
}

// NormalizeProvider returns the canonical provider identifier used in storage
// keys and authenticated encryption metadata.
func NormalizeProvider(provider string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "anthropic", "openai", "gemini":
		return normalized, nil
	default:
		return "", errors.New("credential: invalid provider")
	}
}

// NewAESGCMCipher constructs an AES-256-GCM credential cipher.
func NewAESGCMCipher(masterKey []byte) (*AESGCMCipher, error) {
	if len(masterKey) != MasterKeyBytes {
		return nil, fmt.Errorf("credential: master key must be exactly %d bytes", MasterKeyBytes)
	}
	key := append([]byte(nil), masterKey...)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("credential: initialize cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("credential: initialize authenticated cipher")
	}
	return &AESGCMCipher{aead: aead}, nil
}

// Seal encrypts plaintext with a fresh nonce and binds it to the canonical
// user/provider identity through additional authenticated data.
func (c *AESGCMCipher) Seal(userID int64, provider, plaintext string) ([]byte, []byte, int16, error) {
	if userID <= 0 {
		return nil, nil, 0, errors.New("credential: invalid user ID")
	}
	normalizedProvider, err := NormalizeProvider(provider)
	if err != nil {
		return nil, nil, 0, err
	}
	if plaintext == "" {
		return nil, nil, 0, errors.New("credential: plaintext is required")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, 0, errors.New("credential: generate nonce")
	}
	version := EncryptionVersionAES256GCM
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), credentialAAD(userID, normalizedProvider, version))
	return ciphertext, nonce, version, nil
}

// Open authenticates and decrypts a credential for the requested user and
// provider. Authentication errors are deliberately generic so secret material
// never enters logs through a wrapped error.
func (c *AESGCMCipher) Open(userID int64, provider string, ciphertext, nonce []byte, version int16) (string, error) {
	if userID <= 0 {
		return "", errors.New("credential: invalid user ID")
	}
	normalizedProvider, err := NormalizeProvider(provider)
	if err != nil {
		return "", err
	}
	if version != EncryptionVersionAES256GCM {
		return "", errors.New("credential: unsupported encryption version")
	}
	if len(nonce) != c.aead.NonceSize() {
		return "", errors.New("credential: invalid nonce")
	}
	if len(ciphertext) <= c.aead.Overhead() {
		return "", errors.New("credential: decrypt failed")
	}

	plaintext, err := c.aead.Open(nil, nonce, ciphertext, credentialAAD(userID, normalizedProvider, version))
	if err != nil {
		return "", errors.New("credential: decrypt failed")
	}
	return string(plaintext), nil
}

func credentialAAD(userID int64, provider string, version int16) []byte {
	return []byte(fmt.Sprintf(
		"jobcron:user-ai-credential:v%d:%d:%s",
		version,
		userID,
		provider,
	))
}
