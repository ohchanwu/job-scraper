package credential

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trims and lowercases", input: " Anthropic ", want: "anthropic"},
		{name: "accepts openai", input: " OpenAI ", want: "openai"},
		{name: "accepts gemini", input: " GEMINI ", want: "gemini"},
		{name: "rejects unknown provider", input: "provider_2-beta", wantErr: true},
		{name: "rejects empty", input: "  ", wantErr: true},
		{name: "rejects punctuation", input: "anthropic.com", wantErr: true},
		{name: "rejects spaces", input: "anthropic api", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeProvider(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeProvider(%q) succeeded, want error", tt.input)
				}
				if strings.Contains(err.Error(), tt.input) {
					t.Fatalf("NormalizeProvider error %q contains rejected input", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeProvider(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeProvider(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewAESGCMCipherValidatesKeyLength(t *testing.T) {
	validKey := bytes.Repeat([]byte{0x42}, 32)
	if _, err := NewAESGCMCipher(validKey); err != nil {
		t.Fatalf("NewAESGCMCipher(valid key): %v", err)
	}

	for _, size := range []int{0, 16, 31, 33} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			key := bytes.Repeat([]byte{0x41}, size)
			_, err := NewAESGCMCipher(key)
			if err == nil {
				t.Fatalf("NewAESGCMCipher(%d-byte key) succeeded", size)
			}
			assertErrorOmits(t, err, string(key), base64.StdEncoding.EncodeToString(key))
		})
	}
}

func TestAESGCMCipherRoundTrip(t *testing.T) {
	c := newTestCipher(t, 0x42)
	plaintext := "synthetic-credential-marker"

	ciphertext, nonce, version, err := c.Seal(101, " Anthropic ", plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}
	if version != EncryptionVersionAES256GCM {
		t.Fatalf("version = %d, want %d", version, EncryptionVersionAES256GCM)
	}
	if bytes.Contains(ciphertext, []byte(plaintext)) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := c.Open(101, "anthropic", ciphertext, nonce, version)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != plaintext {
		t.Fatalf("Open plaintext = %q, want %q", got, plaintext)
	}
}

func TestAESGCMCipherUsesFreshNonce(t *testing.T) {
	c := newTestCipher(t, 0x42)
	firstCiphertext, firstNonce, _, err := c.Seal(101, "anthropic", "same-plaintext")
	if err != nil {
		t.Fatalf("first Seal: %v", err)
	}
	secondCiphertext, secondNonce, _, err := c.Seal(101, "anthropic", "same-plaintext")
	if err != nil {
		t.Fatalf("second Seal: %v", err)
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("two seals reused the same nonce")
	}
	if bytes.Equal(firstCiphertext, secondCiphertext) {
		t.Fatal("two seals produced identical ciphertext")
	}
}

func TestAESGCMCipherRejectsWrongBindingAndVersion(t *testing.T) {
	c := newTestCipher(t, 0x42)
	plaintext := "synthetic-credential-marker"
	ciphertext, nonce, version, err := c.Seal(101, "anthropic", plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tests := []struct {
		name       string
		cipher     *AESGCMCipher
		userID     int64
		provider   string
		version    int16
		ciphertext []byte
		nonce      []byte
	}{
		{name: "wrong key", cipher: newTestCipher(t, 0x24), userID: 101, provider: "anthropic", version: version, ciphertext: ciphertext, nonce: nonce},
		{name: "wrong user", cipher: c, userID: 202, provider: "anthropic", version: version, ciphertext: ciphertext, nonce: nonce},
		{name: "wrong provider", cipher: c, userID: 101, provider: "openai", version: version, ciphertext: ciphertext, nonce: nonce},
		{name: "wrong version", cipher: c, userID: 101, provider: "anthropic", version: version + 1, ciphertext: ciphertext, nonce: nonce},
		{name: "truncated ciphertext", cipher: c, userID: 101, provider: "anthropic", version: version, ciphertext: ciphertext[:len(ciphertext)-1], nonce: nonce},
		{name: "short nonce", cipher: c, userID: 101, provider: "anthropic", version: version, ciphertext: ciphertext, nonce: nonce[:len(nonce)-1]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cipher.Open(tt.userID, tt.provider, tt.ciphertext, tt.nonce, tt.version)
			if err == nil {
				t.Fatal("Open succeeded, want authentication error")
			}
			assertErrorOmits(
				t,
				err,
				plaintext,
				string(tt.ciphertext),
				base64.StdEncoding.EncodeToString(tt.ciphertext),
				hex.EncodeToString(tt.ciphertext),
				string(tt.nonce),
				base64.StdEncoding.EncodeToString(tt.nonce),
				hex.EncodeToString(tt.nonce),
			)
		})
	}
}

func TestAESGCMCipherRejectsInvalidSealInput(t *testing.T) {
	c := newTestCipher(t, 0x42)
	tests := []struct {
		name      string
		userID    int64
		provider  string
		plaintext string
	}{
		{name: "zero user", userID: 0, provider: "anthropic", plaintext: "synthetic-marker"},
		{name: "invalid provider", userID: 101, provider: "anthropic.com", plaintext: "synthetic-marker"},
		{name: "empty plaintext", userID: 101, provider: "anthropic", plaintext: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := c.Seal(tt.userID, tt.provider, tt.plaintext)
			if err == nil {
				t.Fatal("Seal succeeded, want validation error")
			}
			assertErrorOmits(t, err, tt.provider, tt.plaintext)
		})
	}
}

func newTestCipher(t *testing.T, fill byte) *AESGCMCipher {
	t.Helper()
	c, err := NewAESGCMCipher(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	return c
}

func assertErrorOmits(t *testing.T, err error, values ...string) {
	t.Helper()
	for _, value := range values {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error %q contains sensitive value %q", err, value)
		}
	}
}
