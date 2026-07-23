package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func saveProfileJSON(t *testing.T, srv *Server, p profile.Profile) {
	t.Helper()
	j, err := profile.Marshal(p)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if _, _, err := srv.store.SaveProfile(context.Background(), j); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

func TestAIRuntimeForUserIsolatesEncryptedCredentials(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userA := insertAIRuntimeTestUser(t, st, "runtime-a@example.invalid")
	userB := insertAIRuntimeTestUser(t, st, "runtime-b@example.invalid")
	masterKey := bytes.Repeat([]byte{0x71}, credential.MasterKeyBytes)
	cipher, err := credential.NewAESGCMCipher(masterKey)
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	srv.SetCredentialCipher(cipher)
	srv.newAIProvider = func(provider, key, model string, _ time.Duration) (ai.Provider, error) {
		return &fingerprintProvider{name: provider, keyFingerprint: keyFingerprint(key)}, nil
	}

	keys := map[int64]string{
		userA: "synthetic-user-a-provider-key",
		userB: "synthetic-user-b-provider-key",
	}
	profiles := map[int64]profile.Profile{
		userA: {
			AIProvider:           "anthropic",
			AIModel:              "model-a",
			AIDailyTokenCap:      12345,
			AIPerCallCap:         7,
			AIMonthlyUSDCapCents: 9,
			AIDailyUSDCapCents:   8,
			AIRunUSDCapCents:     7,
		},
		userB: {
			AIProvider:           "anthropic",
			AIModel:              "model-b",
			AIDailyTokenCap:      54321,
			AIPerCallCap:         11,
			AIMonthlyUSDCapCents: 6,
			AIDailyUSDCapCents:   5,
			AIRunUSDCapCents:     4,
		},
	}
	for _, userID := range []int64{userA, userB} {
		saveAIRuntimeProfile(t, st, userID, profiles[userID])
		saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", keys[userID])
	}

	runtimeA, err := srv.aiRuntimeForUser(ctx, userA)
	if err != nil {
		t.Fatalf("aiRuntimeForUser user A: %v", err)
	}
	runtimeB, err := srv.aiRuntimeForUser(ctx, userB)
	if err != nil {
		t.Fatalf("aiRuntimeForUser user B: %v", err)
	}
	if runtimeA == nil || runtimeB == nil {
		t.Fatalf("resolved runtimes = A:%v B:%v, want both non-nil", runtimeA != nil, runtimeB != nil)
	}
	providerA := runtimeA.Provider.(*fingerprintProvider)
	providerB := runtimeB.Provider.(*fingerprintProvider)
	if providerA.keyFingerprint != keyFingerprint(keys[userA]) || providerB.keyFingerprint != keyFingerprint(keys[userB]) {
		t.Fatal("provider factory received the wrong user's credential fingerprint")
	}
	if providerA.keyFingerprint == providerB.keyFingerprint {
		t.Fatal("distinct user credentials produced the same test fingerprint")
	}
	if runtimeA.UserID != userA ||
		runtimeA.DealbreakerVersion != ai.DealbreakerVersion("anthropic", "model-a") ||
		runtimeA.ScoreVersion != ai.ScoreVersion("anthropic", "model-a") ||
		runtimeA.RunTokenCap != aiRunTokenCapForUSDCents(7) ||
		runtimeA.DailyTokenCap != minPositive(12345, aiDailyTokenCapForUSDCents(8)) ||
		runtimeA.MonthlyTokenCap != aiMonthlyTokenCapForUSDCents(9) || runtimeA.PerCallCap != 7 {
		t.Fatalf("user A runtime metadata = %+v", runtimeA)
	}
	if runtimeB.UserID != userB ||
		runtimeB.DealbreakerVersion != ai.DealbreakerVersion("anthropic", "model-b") ||
		runtimeB.ScoreVersion != ai.ScoreVersion("anthropic", "model-b") ||
		runtimeB.PerCallCap != 11 {
		t.Fatalf("user B runtime metadata = %+v", runtimeB)
	}
}

func TestAIRuntimeForUserFallbacksAndSafeFailures(t *testing.T) {
	t.Run("AI disabled", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-disabled@example.invalid")
		saveAIRuntimeProfile(t, st, userID, profile.Profile{})
		runtime, err := srv.aiRuntimeForUser(context.Background(), userID)
		if err != nil || runtime != nil {
			t.Fatalf("aiRuntimeForUser = runtime %v err=%v, want nil nil", runtime, err)
		}
	})

	t.Run("missing credential", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-missing@example.invalid")
		saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic"})
		runtime, err := srv.aiRuntimeForUser(context.Background(), userID)
		if err != nil || runtime != nil {
			t.Fatalf("aiRuntimeForUser = runtime %v err=%v, want nil nil", runtime, err)
		}
	})

	t.Run("cipher not configured", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-no-cipher@example.invalid")
		cipher := newAIRuntimeTestCipher(t, 0x41)
		saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic"})
		saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "synthetic-no-cipher-key")
		if _, err := srv.aiRuntimeForUser(context.Background(), userID); err == nil || !strings.Contains(err.Error(), "credential cipher") {
			t.Fatalf("aiRuntimeForUser error = %v, want stable missing-cipher error", err)
		}
	})

	t.Run("wrong master key", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-wrong-key@example.invalid")
		encryptingCipher := newAIRuntimeTestCipher(t, 0x42)
		srv.SetCredentialCipher(newAIRuntimeTestCipher(t, 0x43))
		saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic"})
		saveAIRuntimeCredential(t, st, encryptingCipher, userID, "anthropic", "synthetic-wrong-master-key")
		if _, err := srv.aiRuntimeForUser(context.Background(), userID); err == nil || !strings.Contains(err.Error(), "decrypt") {
			t.Fatalf("aiRuntimeForUser error = %v, want non-secret decrypt failure", err)
		}
	})

	t.Run("moved ciphertext", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userA := insertAIRuntimeTestUser(t, st, "runtime-moved-a@example.invalid")
		userB := insertAIRuntimeTestUser(t, st, "runtime-moved-b@example.invalid")
		cipher := newAIRuntimeTestCipher(t, 0x44)
		srv.SetCredentialCipher(cipher)
		saveAIRuntimeProfile(t, st, userB, profile.Profile{AIProvider: "anthropic"})
		ciphertext, nonce, version, err := cipher.Seal(userA, "anthropic", "synthetic-moved-key")
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if err := st.UpsertUserAICredential(context.Background(), storage.EncryptedAICredential{
			UserID: userB, Provider: "anthropic", Ciphertext: ciphertext, Nonce: nonce, EncryptionVersion: version,
		}); err != nil {
			t.Fatalf("store moved credential: %v", err)
		}
		if _, err := srv.aiRuntimeForUser(context.Background(), userB); err == nil || !strings.Contains(err.Error(), "decrypt") {
			t.Fatalf("aiRuntimeForUser error = %v, want moved-ciphertext failure", err)
		}
	})

	t.Run("unknown encryption version", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-version@example.invalid")
		cipher := newAIRuntimeTestCipher(t, 0x45)
		srv.SetCredentialCipher(cipher)
		saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic"})
		ciphertext, nonce, _, err := cipher.Seal(userID, "anthropic", "synthetic-version-key")
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if err := st.UpsertUserAICredential(context.Background(), storage.EncryptedAICredential{
			UserID: userID, Provider: "anthropic", Ciphertext: ciphertext, Nonce: nonce, EncryptionVersion: 2,
		}); err != nil {
			t.Fatalf("store unknown-version credential: %v", err)
		}
		if _, err := srv.aiRuntimeForUser(context.Background(), userID); err == nil || !strings.Contains(err.Error(), "unsupported encryption version") {
			t.Fatalf("aiRuntimeForUser error = %v, want unknown-version failure", err)
		}
	})

	t.Run("provider construction failure", func(t *testing.T) {
		srv, st := newPostgresTestServer(t, &fakeScraper{})
		userID := insertAIRuntimeTestUser(t, st, "runtime-provider@example.invalid")
		cipher := newAIRuntimeTestCipher(t, 0x46)
		srv.SetCredentialCipher(cipher)
		saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "openai", AIModel: ai.DefaultModel("openai")})
		saveAIRuntimeCredential(t, st, cipher, userID, "openai", "synthetic-provider-key")
		srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) {
			return nil, errors.New("synthetic provider construction failure")
		}
		if _, err := srv.aiRuntimeForUser(context.Background(), userID); err == nil || !strings.Contains(err.Error(), "construct AI provider") {
			t.Fatalf("aiRuntimeForUser error = %v, want provider construction failure", err)
		}
	})
}

type fingerprintProvider struct {
	name           string
	keyFingerprint string
}

func (p *fingerprintProvider) Name() string { return p.name }
func (p *fingerprintProvider) Extract(context.Context, string) (ai.Extraction, ai.Usage, error) {
	return ai.Extraction{}, ai.Usage{}, ai.ErrNotImplemented
}
func (p *fingerprintProvider) ValidateDealbreakers(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
	return nil, ai.Usage{}, ai.ErrNotImplemented
}
func (p *fingerprintProvider) ScoreDelta(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
	return nil, ai.Usage{}, ai.ErrNotImplemented
}

func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

func insertAIRuntimeTestUser(t *testing.T, st *storage.Store, email string) int64 {
	t.Helper()
	var userID int64
	if err := st.SQLDB().QueryRow(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, 'synthetic-password-hash', now(), now())
RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("insert AI runtime user: %v", err)
	}
	return userID
}

func saveAIRuntimeProfile(t *testing.T, st *storage.Store, userID int64, p profile.Profile) {
	t.Helper()
	profileJSON, err := profile.Marshal(p)
	if err != nil {
		t.Fatalf("profile.Marshal: %v", err)
	}
	if _, _, err := st.SaveProfileForUser(context.Background(), userID, profileJSON); err != nil {
		t.Fatalf("SaveProfileForUser: %v", err)
	}
}

func saveAIRuntimeCredential(
	t *testing.T,
	st *storage.Store,
	cipher credential.Cipher,
	userID int64,
	provider, plaintext string,
) {
	t.Helper()
	ciphertext, nonce, version, err := cipher.Seal(userID, provider, plaintext)
	if err != nil {
		t.Fatalf("Seal credential: %v", err)
	}
	if err := st.UpsertUserAICredential(context.Background(), storage.EncryptedAICredential{
		UserID:            userID,
		Provider:          provider,
		Ciphertext:        ciphertext,
		Nonce:             nonce,
		EncryptionVersion: version,
	}); err != nil {
		t.Fatalf("UpsertUserAICredential: %v", err)
	}
}

func newAIRuntimeTestCipher(t *testing.T, fill byte) credential.Cipher {
	t.Helper()
	cipher, err := credential.NewAESGCMCipher(bytes.Repeat([]byte{fill}, credential.MasterKeyBytes))
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	return cipher
}

// TestDailyTokenCapHaltsAIRegexContinues covers: the daily cap halts AI while
// regex scoring still runs.
func TestDailyTokenCapHaltsAIRegexContinues(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "백엔드 신입")},
	}
	srv, st := newTestServer(t, f)
	stub := newcomerStub()
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.DailyTokenCap = 50 // tiny daily cap
	saveSinipProfile(t, srv)
	ctx := context.Background()

	// Pre-spend today's budget so the run starts already over the daily cap.
	day := time.Now().UTC().Format("2006-01-02")
	if err := st.AddAIUsage(ctx, 1, day, 100, 0); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	res, err := srv.runScrape(ctx, noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if stub.Calls != 0 {
		t.Errorf("Extract calls = %d, want 0 (daily cap already exhausted)", stub.Calls)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Errorf("ai_extractions rows = %d, want 0 (AI halted)", n)
	}
	// Regex scoring still ran: the posting is present and scored.
	if res.New != 1 || res.Scored != 1 {
		t.Errorf("ScrapeResult = %+v, want the posting inserted + scored by regex", res)
	}
}

func TestDailyUSDCapHaltsManualRerate(t *testing.T) {
	srv, _, _ := seedRerate(t)
	ctx := context.Background()
	saveProfileJSON(t, srv, profile.Profile{
		CareerYears:        0,
		JobLikes:           "백엔드 서버 개발",
		AIDailyUSDCapCents: 1,
	})
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.DailyTokenCap = aiDailyTokenCapForUSDCents(1)
	day := time.Now().UTC().Format("2006-01-02")
	if err := srv.store.AddAIUsage(ctx, 1, day, aiDailyTokenCapForUSDCents(1), 0); err != nil {
		t.Fatalf("seed daily usage: %v", err)
	}

	if _, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime); err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if stub.ScoreDeltaCalls != 0 {
		t.Fatalf("ScoreDelta calls = %d, want 0 when daily USD cap is exhausted", stub.ScoreDeltaCalls)
	}
}

func TestMonthlyUSDCapHaltsManualRerate(t *testing.T) {
	srv, _, _ := seedRerate(t)
	ctx := context.Background()
	saveProfileJSON(t, srv, profile.Profile{
		CareerYears:          0,
		JobLikes:             "백엔드 서버 개발",
		AIMonthlyUSDCapCents: 1,
	})
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.MonthlyTokenCap = aiMonthlyTokenCapForUSDCents(1)
	now := time.Now().UTC()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	if err := srv.store.AddAIUsage(ctx, 1, firstOfMonth, aiMonthlyTokenCapForUSDCents(1), 0); err != nil {
		t.Fatalf("seed monthly usage: %v", err)
	}

	if _, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime); err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if stub.ScoreDeltaCalls != 0 {
		t.Fatalf("ScoreDelta calls = %d, want 0 when monthly USD cap is exhausted", stub.ScoreDeltaCalls)
	}
}

// TestBudgetLedgerPersistsAcrossRestart covers: the ledger accumulates across a
// simulated restart (a fresh Server on the same store sees the prior spend).
func TestBudgetLedgerPersistsAcrossRestart(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "백엔드 신입")},
	}
	srv, st := newTestServer(t, f)
	runtime := testAIRuntime(1, newcomerStub(), "test-model") // each Extract spends 120 tokens
	ctx := context.Background()
	p := listingPosting("1", "백엔드 신입")
	id := mustUpsert(t, st, p)
	srv.extractStage1(
		ctx,
		id,
		p,
		time.Now().UTC(),
		testStage1Resolver(testStage1Funding(srv, ctx, 1, runtime)),
	)
	day := time.Now().UTC().Format("2006-01-02")
	in, out, _ := st.AIUsageForDay(ctx, 1, day)
	if in != 100 || out != 20 {
		t.Fatalf("ledger after scrape = (%d,%d), want (100,20) — the debit must persist", in, out)
	}

	// "Restart": a brand-new Server over the same store. Its budget must start
	// from the persisted daily total, not zero.
	srv2 := New(st, f)
	runtime2 := testAIRuntime(1, newcomerStub(), "test-model")
	b := srv2.newAIBudget(ctx, 1, runtime2)
	if b == nil || b.dailyAtStart != 120 {
		t.Fatalf("new run's dailyAtStart = %v, want 120 (read from the persisted ledger)", b)
	}
}
