package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/credential"
)

func TestUserAICredentialValidation(t *testing.T) {
	valid := validEncryptedAICredential(101, "anthropic", 0x42)
	postgresWithoutDB := &Store{dialect: DialectPostgres}

	tests := []struct {
		name       string
		credential EncryptedAICredential
	}{
		{name: "invalid user", credential: withCredentialUser(valid, 0)},
		{name: "empty provider", credential: withCredentialProvider(valid, "")},
		{name: "invalid provider", credential: withCredentialProvider(valid, "anthropic.com")},
		{name: "ciphertext is only the GCM tag", credential: withCredentialCiphertext(valid, []byte("0123456789abcdef"))},
		{name: "short nonce", credential: withCredentialNonce(valid, []byte("eleven-byte"))},
		{name: "zero version", credential: withCredentialVersion(valid, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := postgresWithoutDB.UpsertUserAICredential(context.Background(), tt.credential)
			if err == nil {
				t.Fatal("UpsertUserAICredential succeeded, want validation error")
			}
			assertStorageErrorOmits(t, err, tt.credential.Ciphertext, tt.credential.Nonce)
		})
	}

	if _, _, err := postgresWithoutDB.UserAICredential(context.Background(), 0, "anthropic"); err == nil {
		t.Fatal("UserAICredential accepted zero user ID")
	}
	if err := postgresWithoutDB.DeleteUserAICredential(context.Background(), 101, "anthropic.com"); err == nil {
		t.Fatal("DeleteUserAICredential accepted invalid provider")
	}
}

func TestUserAICredentialRequiresPostgres(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	valid := validEncryptedAICredential(101, "anthropic", 0x42)

	if err := st.UpsertUserAICredential(ctx, valid); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("UpsertUserAICredential error = %v, want PostgreSQL requirement", err)
	}
	if _, _, err := st.UserAICredential(ctx, valid.UserID, valid.Provider); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("UserAICredential error = %v, want PostgreSQL requirement", err)
	}
	if err := st.DeleteUserAICredential(ctx, valid.UserID, valid.Provider); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("DeleteUserAICredential error = %v, want PostgreSQL requirement", err)
	}
}

func TestUserAICredentialMissing(t *testing.T) {
	st := newPostgresTestStore(t)
	userID := insertCredentialTestUser(t, st, "missing")

	got, found, err := st.UserAICredential(context.Background(), userID, "anthropic")
	if err != nil {
		t.Fatalf("UserAICredential: %v", err)
	}
	if found {
		t.Fatalf("UserAICredential found unexpected row: %+v", got)
	}
	if got.UserID != 0 || got.Provider != "" || len(got.Ciphertext) != 0 || len(got.Nonce) != 0 || got.EncryptionVersion != 0 || !got.UpdatedAt.IsZero() {
		t.Fatalf("UserAICredential missing result = %+v, want zero value", got)
	}
}

func TestUserAICredentialUpsertAndLookup(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userID := insertCredentialTestUser(t, st, "round-trip")
	want := validEncryptedAICredential(userID, " Anthropic ", 0x42)

	if err := st.UpsertUserAICredential(ctx, want); err != nil {
		t.Fatalf("UpsertUserAICredential: %v", err)
	}
	got, found, err := st.UserAICredential(ctx, userID, "ANTHROPIC")
	if err != nil {
		t.Fatalf("UserAICredential: %v", err)
	}
	if !found {
		t.Fatal("UserAICredential did not find inserted row")
	}
	if got.UserID != userID || got.Provider != "anthropic" || got.EncryptionVersion != want.EncryptionVersion {
		t.Fatalf("UserAICredential metadata = %+v, want user=%d provider=anthropic version=%d", got, userID, want.EncryptionVersion)
	}
	if !bytes.Equal(got.Ciphertext, want.Ciphertext) || !bytes.Equal(got.Nonce, want.Nonce) {
		t.Fatalf("UserAICredential encrypted payload = ciphertext %x nonce %x, want ciphertext %x nonce %x", got.Ciphertext, got.Nonce, want.Ciphertext, want.Nonce)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UserAICredential UpdatedAt is zero")
	}
}

func TestUserAICredentialUpsertReplacesEncryptedPayload(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userID := insertCredentialTestUser(t, st, "replace")
	first := validEncryptedAICredential(userID, "anthropic", 0x42)
	if err := st.UpsertUserAICredential(ctx, first); err != nil {
		t.Fatalf("first UpsertUserAICredential: %v", err)
	}
	before, found, err := st.UserAICredential(ctx, userID, "anthropic")
	if err != nil || !found {
		t.Fatalf("first UserAICredential: found=%v err=%v", found, err)
	}

	time.Sleep(2 * time.Millisecond)
	second := validEncryptedAICredential(userID, "anthropic", 0x24)
	second.EncryptionVersion = 2
	if err := st.UpsertUserAICredential(ctx, second); err != nil {
		t.Fatalf("second UpsertUserAICredential: %v", err)
	}
	after, found, err := st.UserAICredential(ctx, userID, "anthropic")
	if err != nil || !found {
		t.Fatalf("second UserAICredential: found=%v err=%v", found, err)
	}
	if !bytes.Equal(after.Ciphertext, second.Ciphertext) || !bytes.Equal(after.Nonce, second.Nonce) || after.EncryptionVersion != 2 {
		t.Fatalf("updated credential = %+v, want replacement payload", after)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `
SELECT count(*) FROM user_ai_credentials WHERE user_id = $1 AND provider = 'anthropic'`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("credential row count = %d, want 1", count)
	}
}

func TestUserAICredentialIsolatesUsers(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userA := insertCredentialTestUser(t, st, "user-a")
	userB := insertCredentialTestUser(t, st, "user-b")
	credentialA := validEncryptedAICredential(userA, "anthropic", 0x42)
	credentialB := validEncryptedAICredential(userB, "anthropic", 0x24)

	if err := st.UpsertUserAICredential(ctx, credentialA); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUserAICredential(ctx, credentialB); err != nil {
		t.Fatal(err)
	}
	gotA, found, err := st.UserAICredential(ctx, userA, "anthropic")
	if err != nil || !found {
		t.Fatalf("user A lookup: found=%v err=%v", found, err)
	}
	gotB, found, err := st.UserAICredential(ctx, userB, "anthropic")
	if err != nil || !found {
		t.Fatalf("user B lookup: found=%v err=%v", found, err)
	}
	if !bytes.Equal(gotA.Ciphertext, credentialA.Ciphertext) || !bytes.Equal(gotB.Ciphertext, credentialB.Ciphertext) {
		t.Fatal("credential lookup crossed user boundary")
	}
	if bytes.Equal(gotA.Ciphertext, gotB.Ciphertext) {
		t.Fatal("distinct user credentials unexpectedly match")
	}
}

func TestDeleteUserAICredentialIsScopedAndIdempotent(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userA := insertCredentialTestUser(t, st, "delete-a")
	userB := insertCredentialTestUser(t, st, "delete-b")
	if err := st.UpsertUserAICredential(ctx, validEncryptedAICredential(userA, "anthropic", 0x42)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUserAICredential(ctx, validEncryptedAICredential(userB, "anthropic", 0x24)); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteUserAICredential(ctx, userB, "openai"); err != nil {
		t.Fatalf("delete missing user/provider: %v", err)
	}
	if _, found, err := st.UserAICredential(ctx, userA, "anthropic"); err != nil || !found {
		t.Fatalf("user A row after scoped no-op delete: found=%v err=%v", found, err)
	}
	if err := st.DeleteUserAICredential(ctx, userA, " ANTHROPIC "); err != nil {
		t.Fatalf("delete user A: %v", err)
	}
	if err := st.DeleteUserAICredential(ctx, userA, "anthropic"); err != nil {
		t.Fatalf("repeat delete user A: %v", err)
	}
	if _, found, err := st.UserAICredential(ctx, userA, "anthropic"); err != nil || found {
		t.Fatalf("user A row after delete: found=%v err=%v", found, err)
	}
	if _, found, err := st.UserAICredential(ctx, userB, "anthropic"); err != nil || !found {
		t.Fatalf("user B row after user A delete: found=%v err=%v", found, err)
	}
}

func TestUserAICredentialCascadesOnUserDelete(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userA := insertCredentialTestUser(t, st, "cascade-a")
	userB := insertCredentialTestUser(t, st, "cascade-b")
	if err := st.UpsertUserAICredential(ctx, validEncryptedAICredential(userA, "anthropic", 0x42)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUserAICredential(ctx, validEncryptedAICredential(userB, "anthropic", 0x24)); err != nil {
		t.Fatal(err)
	}

	if _, err := st.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userA); err != nil {
		t.Fatalf("delete user A: %v", err)
	}
	if _, found, err := st.UserAICredential(ctx, userA, "anthropic"); err != nil || found {
		t.Fatalf("user A credential after cascade: found=%v err=%v", found, err)
	}
	if _, found, err := st.UserAICredential(ctx, userB, "anthropic"); err != nil || !found {
		t.Fatalf("user B credential after user A cascade: found=%v err=%v", found, err)
	}
}

func TestUserAICredentialEncryptedRoundTrip(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	userA := insertCredentialTestUser(t, st, "encrypted-round-trip-a")
	userB := insertCredentialTestUser(t, st, "encrypted-round-trip-b")
	masterKey := bytes.Repeat([]byte{0x42}, credential.MasterKeyBytes)
	cipher, err := credential.NewAESGCMCipher(masterKey)
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	plaintext := "slice-one-plaintext-marker"

	ciphertext, nonce, version, err := cipher.Seal(userA, "anthropic", plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := st.UpsertUserAICredential(ctx, EncryptedAICredential{
		UserID:            userA,
		Provider:          "anthropic",
		Ciphertext:        ciphertext,
		Nonce:             nonce,
		EncryptionVersion: version,
	}); err != nil {
		t.Fatalf("UpsertUserAICredential: %v", err)
	}

	stored, found, err := st.UserAICredential(ctx, userA, "anthropic")
	if err != nil || !found {
		t.Fatalf("UserAICredential: found=%v err=%v", found, err)
	}
	if bytes.Contains(stored.Ciphertext, []byte(plaintext)) || bytes.Contains(stored.Nonce, []byte(plaintext)) {
		t.Fatal("stored encrypted fields contain plaintext")
	}
	opened, err := cipher.Open(stored.UserID, stored.Provider, stored.Ciphertext, stored.Nonce, stored.EncryptionVersion)
	if err != nil {
		t.Fatalf("Open stored credential: %v", err)
	}
	if opened != plaintext {
		t.Fatalf("opened credential = %q, want plaintext marker", opened)
	}

	_, wrongUserErr := cipher.Open(userB, stored.Provider, stored.Ciphertext, stored.Nonce, stored.EncryptionVersion)
	if wrongUserErr == nil {
		t.Fatal("Open succeeded after credential was reassigned to another user")
	}
	_, wrongProviderErr := cipher.Open(stored.UserID, "openai", stored.Ciphertext, stored.Nonce, stored.EncryptionVersion)
	if wrongProviderErr == nil {
		t.Fatal("Open succeeded after credential was reassigned to another provider")
	}
	for _, openErr := range []error{wrongUserErr, wrongProviderErr} {
		assertErrorExcludesCredentialMaterial(t, openErr, plaintext, masterKey, stored.Ciphertext, stored.Nonce)
	}

	var ciphertextHex, nonceHex string
	if err := st.db.QueryRowContext(ctx, `
SELECT encode(ciphertext, 'hex'), encode(nonce, 'hex')
  FROM user_ai_credentials
 WHERE user_id = $1 AND provider = $2`, userA, "anthropic").Scan(&ciphertextHex, &nonceHex); err != nil {
		t.Fatalf("read encoded credential fields: %v", err)
	}
	plaintextHex := hex.EncodeToString([]byte(plaintext))
	if strings.Contains(ciphertextHex, plaintextHex) || strings.Contains(nonceHex, plaintextHex) {
		t.Fatal("PostgreSQL encoded credential fields contain plaintext bytes")
	}
}

func insertCredentialTestUser(t *testing.T, st *Store, label string) int64 {
	t.Helper()
	var userID int64
	if err := st.db.QueryRowContext(context.Background(), `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, 'synthetic-password-hash', now(), now())
RETURNING id`, fmt.Sprintf("credential-%s@example.invalid", label)).Scan(&userID); err != nil {
		t.Fatalf("insert credential test user: %v", err)
	}
	return userID
}

func validEncryptedAICredential(userID int64, provider string, fill byte) EncryptedAICredential {
	return EncryptedAICredential{
		UserID:            userID,
		Provider:          provider,
		Ciphertext:        bytes.Repeat([]byte{fill}, 24),
		Nonce:             bytes.Repeat([]byte{fill + 1}, 12),
		EncryptionVersion: 1,
	}
}

func withCredentialUser(c EncryptedAICredential, userID int64) EncryptedAICredential {
	c.UserID = userID
	return c
}

func withCredentialProvider(c EncryptedAICredential, provider string) EncryptedAICredential {
	c.Provider = provider
	return c
}

func withCredentialCiphertext(c EncryptedAICredential, ciphertext []byte) EncryptedAICredential {
	c.Ciphertext = ciphertext
	return c
}

func withCredentialNonce(c EncryptedAICredential, nonce []byte) EncryptedAICredential {
	c.Nonce = nonce
	return c
}

func withCredentialVersion(c EncryptedAICredential, version int16) EncryptedAICredential {
	c.EncryptionVersion = version
	return c
}

func assertStorageErrorOmits(t *testing.T, err error, values ...[]byte) {
	t.Helper()
	for _, value := range values {
		if len(value) != 0 && strings.Contains(err.Error(), string(value)) {
			t.Fatalf("error %q contains encrypted value", err)
		}
	}
}

func assertErrorExcludesCredentialMaterial(t *testing.T, err error, plaintext string, values ...[]byte) {
	t.Helper()
	for _, forbidden := range []string{plaintext, hex.EncodeToString([]byte(plaintext)), base64.StdEncoding.EncodeToString([]byte(plaintext))} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q contains plaintext material", err)
		}
	}
	for _, value := range values {
		for _, forbidden := range []string{string(value), hex.EncodeToString(value), base64.StdEncoding.EncodeToString(value)} {
			if forbidden != "" && strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error %q contains credential material", err)
			}
		}
	}
}
