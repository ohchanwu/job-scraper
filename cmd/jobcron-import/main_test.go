package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestImportDefaultsToDryRunAndWritesNothing(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	targetURL, owners := prepareImportTarget(t, postgresURL, "dry-run-owner@example.invalid")

	var out bytes.Buffer
	err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  "dry-run-owner@example.invalid",
		out:         &out,
	})
	if err != nil {
		t.Fatalf("runImport dry-run: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"dry run: true",
		"postings: 2",
		"profile: 1",
		"scores: 1",
		"bookmarks: 1",
		"not_interested: 1",
		"ai_extractions: 1",
		"ai_scores: 1",
		"ai_usage: 1",
		"ai_providers: 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, got)
		}
	}
	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertPostgresScalar(t, db, `SELECT count(*) FROM users`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM postings`, 0)
	assertPostgresScalar(t, db, `SELECT count(*) FROM profiles`, 0)
	assertPostgresScalar(t, db, `SELECT count(*) FROM local_data_imports`, 0)
	if owners["dry-run-owner@example.invalid"] <= 0 {
		t.Fatal("prepared owner has no positive ID")
	}
}

func TestImportDryRunReportsAllCategoriesFingerprintAndCollisions(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	targetURL, owners := prepareImportTarget(t, postgresURL, "collision-owner@example.invalid")
	target, err := storage.OpenPostgres(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := target.UpsertPosting(context.Background(), scraper.Posting{
		Source:          "jumpit",
		SourcePostingID: "import-1",
		URL:             "https://example.invalid/existing",
		Title:           "existing",
		Company:         "existing",
		Description:     "existing",
		RawJSON:         `{}`,
		FirstSeenAt:     time.Now().UTC(),
		LastSeenAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := target.SaveProfileForUser(context.Background(), owners["collision-owner@example.invalid"], `{"stacks":["Rust"]}`); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  "collision-owner@example.invalid",
		out:         &out,
	}); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, category := range []string{
		"profile", "postings", "scores", "bookmarks", "not_interested",
		"ai_extractions", "ai_scores", "ai_usage", "ai_providers",
	} {
		if !strings.Contains(report, category+":") {
			t.Fatalf("report missing category %q:\n%s", category, report)
		}
	}
	if !regexp.MustCompile(`source_sha256: [0-9a-f]{64}`).MatchString(report) {
		t.Fatalf("report missing lowercase SHA-256 fingerprint:\n%s", report)
	}
	for _, want := range []string{"collisions:", "profile: 1", "postings: 1"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestImportRequiresExistingSoleOwner(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	for _, tc := range []struct {
		name   string
		emails []string
	}{
		{name: "missing", emails: nil},
		{name: "multiple", emails: []string{"first@example.invalid", "second@example.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetURL, _ := prepareImportTarget(t, postgresURL, tc.emails...)
			err := runImport(context.Background(), importOptions{
				sqlitePath:  sqlitePath,
				postgresURL: targetURL,
				ownerEmail:  "first@example.invalid",
				out:         io.Discard,
			})
			if err == nil || !strings.Contains(err.Error(), "exactly one owner") {
				t.Fatalf("runImport error = %v, want exactly-one-owner refusal", err)
			}
		})
	}
}

func TestImportRefusesOwnerEmailMismatch(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	targetURL, _ := prepareImportTarget(t, postgresURL, "actual-owner@example.invalid")
	err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  "different-owner@example.invalid",
		out:         io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "owner email mismatch") {
		t.Fatalf("runImport error = %v, want owner email mismatch", err)
	}
}

func TestImportReportDoesNotContainSecretsOrPrivateInputs(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "private-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	keysPath := filepath.Join(t.TempDir(), "private-legacy-keys.json")
	const secret = "synthetic-secret-that-must-not-appear"
	if err := os.WriteFile(keysPath, []byte(`{"Anthropic":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  ownerEmail,
		aiKeysPath:  keysPath,
		out:         &out,
	}); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, private := range []string{sqlitePath, targetURL, ownerEmail, keysPath, secret} {
		if strings.Contains(report, private) {
			t.Fatalf("report contains private input %q:\n%s", private, report)
		}
	}
	for _, want := range []string{"ai_providers: 1", "anthropic"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestImportDryRunRejectsDuplicateNormalizedProviders(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	targetURL, _ := prepareImportTarget(t, postgresURL, "provider-owner@example.invalid")
	keysPath := filepath.Join(t.TempDir(), "duplicate-provider-keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"Anthropic":"first"," anthropic ":"second"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  "provider-owner@example.invalid",
		aiKeysPath:  keysPath,
		out:         io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate provider") {
		t.Fatalf("runImport error = %v, want duplicate provider refusal", err)
	}
}

func TestImportErrorsDoNotContainPrivateInputs(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	ownerEmail := "private-error-owner@example.invalid"

	t.Run("source path", func(t *testing.T) {
		privatePath := filepath.Join(t.TempDir(), "private-missing-source.db")
		err := runImport(context.Background(), importOptions{
			sqlitePath:  privatePath,
			postgresURL: "postgres://private-target.invalid/jobs",
			ownerEmail:  ownerEmail,
			out:         io.Discard,
		})
		assertImportErrorRedacts(t, err, privatePath, "private-target", ownerEmail)
	})

	t.Run("legacy key path", func(t *testing.T) {
		sqlitePath := seedSQLiteImportFixture(t)
		targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
		keysPath := filepath.Join(t.TempDir(), "private-malformed-keys.json")
		if err := os.WriteFile(keysPath, []byte(`{"anthropic":`), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runImport(context.Background(), importOptions{
			sqlitePath:  sqlitePath,
			postgresURL: targetURL,
			ownerEmail:  ownerEmail,
			aiKeysPath:  keysPath,
			out:         io.Discard,
		})
		assertImportErrorRedacts(t, err, sqlitePath, targetURL, ownerEmail, keysPath)
	})

	t.Run("postgres URL", func(t *testing.T) {
		sqlitePath := seedSQLiteImportFixture(t)
		privateURL := "postgres://private-user:private-password@127.0.0.1:1/private-db?sslmode=disable&connect_timeout=1"
		err := runImport(context.Background(), importOptions{
			sqlitePath:  sqlitePath,
			postgresURL: privateURL,
			ownerEmail:  ownerEmail,
			out:         io.Discard,
		})
		assertImportErrorRedacts(t, err, sqlitePath, privateURL, "private-user", "private-password", "private-db", ownerEmail)
	})
}

func assertImportErrorRedacts(t *testing.T, err error, privateValues ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("runImport succeeded, want failure")
	}
	message := err.Error()
	for _, private := range privateValues {
		if strings.Contains(message, private) {
			t.Fatalf("error contains private input %q: %s", private, message)
		}
	}
}

func TestImportApplyCopiesRepresentativeData(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	sqlitePath := seedSQLiteImportFixture(t)
	schema := createPostgresImportSchema(t, postgresURL)
	targetURL := databaseURLWithSearchPath(postgresURL, schema)
	ownerEmail := "intended-owner@example.com"

	preexisting, err := storage.OpenPostgresMigrating(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("OpenPostgres preexisting target: %v", err)
	}
	if _, err := preexisting.SQLDB().Exec(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, 'preexisting', now(), now())`, ownerEmail); err != nil {
		t.Fatalf("seed preexisting user: %v", err)
	}
	if err := preexisting.Close(); err != nil {
		t.Fatalf("close preexisting target: %v", err)
	}

	var out bytes.Buffer
	if err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  ownerEmail,
		apply:       true,
		out:         &out,
	}); err != nil {
		t.Fatalf("runImport: %v\n%s", err, out.String())
	}

	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatalf("open postgres verification db: %v", err)
	}
	defer db.Close()

	assertPostgresScalar(t, db, `SELECT count(*) FROM users`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM postings`, 2)
	assertPostgresScalar(t, db, `SELECT count(*) FROM profiles`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM scores`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM bookmarks`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM not_interested`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM ai_extractions`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM ai_scores`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM ai_usage`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM local_data_imports`, 1)
	ownerID := lookupUserIDByEmail(t, db, ownerEmail)
	assertAIUsage(t, db, ownerID, "2026-07-09", 123, 45)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM profiles WHERE user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM scores WHERE posting_id = 1 AND user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM bookmarks WHERE posting_id = 1 AND user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM not_interested WHERE posting_id = 1 AND user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM ai_scores WHERE user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM ai_scores WHERE user_id <> %d`, ownerID), 0)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM ai_usage WHERE user_id = %d`, ownerID), 1)
	assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM ai_usage WHERE user_id <> %d`, ownerID), 0)
	var careerEvidence, educationEvidence string
	if err := db.QueryRow(`
SELECT career_evidence, education_evidence
  FROM ai_extractions
 WHERE posting_id = 1`).Scan(&careerEvidence, &educationEvidence); err != nil {
		t.Fatalf("query imported extraction evidence: %v", err)
	}
	if careerEvidence != "신입" || educationEvidence != "" {
		t.Fatalf("imported extraction evidence = career:%q education:%q", careerEvidence, educationEvidence)
	}

	var title, company, profileJSON, importedOwnerEmail string
	if err := db.QueryRow(`
SELECT p.title, p.company, pr.profile_json, u.email
FROM postings p
JOIN scores s ON s.posting_id = p.id
JOIN bookmarks b ON b.posting_id = p.id
JOIN not_interested n ON n.posting_id = p.id
JOIN profiles pr ON pr.profile_json LIKE '%"stacks":["Go"]%'
JOIN users u ON u.id = pr.user_id
WHERE p.source = 'jumpit' AND p.source_posting_id = 'import-1' AND s.total = 87`,
	).Scan(&title, &company, &profileJSON, &importedOwnerEmail); err != nil {
		t.Fatalf("query representative imported data: %v", err)
	}
	if title != "신입 백엔드 개발자" || company != "테스트컴퍼니" {
		t.Fatalf("posting title/company = %q/%q", title, company)
	}
	if !strings.Contains(profileJSON, `"stacks":["Go"]`) {
		t.Fatalf("profile_json = %s", profileJSON)
	}
	if importedOwnerEmail != ownerEmail {
		t.Fatalf("profile owner email = %q, want %q", importedOwnerEmail, ownerEmail)
	}

	var duplicateOf sql.NullInt64
	if err := db.QueryRow(`SELECT duplicate_of FROM postings WHERE source_posting_id = 'import-duplicate'`).Scan(&duplicateOf); err != nil {
		t.Fatalf("query duplicate_of: %v", err)
	}
	if !duplicateOf.Valid || duplicateOf.Int64 != 1 {
		t.Fatalf("duplicate_of = %+v, want posting 1", duplicateOf)
	}
}

func TestImportApplyPreservesForwardDuplicateLink(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ctx := context.Background()

	source, err := storage.OpenSQLiteAt(sqlitePath)
	if err != nil {
		t.Fatalf("open SQLite source: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })
	seenAt := time.Date(2026, 7, 9, 1, 4, 3, 0, time.UTC)
	forwardSourceID, _, err := source.UpsertPosting(ctx, scraper.Posting{
		Source:          "demoday",
		SourcePostingID: "import-forward-source",
		URL:             "https://demoday.example/jobs/import-forward-source",
		Title:           "forward duplicate",
		Company:         "test company",
		Description:     "points to a later posting ID",
		RawJSON:         `{}`,
		FirstSeenAt:     seenAt,
		LastSeenAt:      seenAt,
	})
	if err != nil {
		t.Fatalf("insert forward duplicate: %v", err)
	}
	forwardTargetID, _, err := source.UpsertPosting(ctx, scraper.Posting{
		Source:          "greeting",
		SourcePostingID: "import-forward-target",
		URL:             "https://greeting.example/jobs/import-forward-target",
		Title:           "forward duplicate target",
		Company:         "test company",
		Description:     "has a later posting ID",
		RawJSON:         `{}`,
		FirstSeenAt:     seenAt.Add(time.Minute),
		LastSeenAt:      seenAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("insert forward duplicate target: %v", err)
	}
	if forwardSourceID >= forwardTargetID {
		t.Fatalf("source ID %d must precede target ID %d", forwardSourceID, forwardTargetID)
	}
	if _, err := source.SQLDB().ExecContext(ctx,
		`UPDATE postings SET duplicate_of = ? WHERE id = ?`, forwardTargetID, forwardSourceID); err != nil {
		t.Fatalf("set forward duplicate link: %v", err)
	}

	ownerEmail := "forward-duplicate-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	if err := runImport(ctx, importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
	}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	target, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatalf("open PostgreSQL target: %v", err)
	}
	defer target.Close()
	var duplicateOf int64
	if err := target.QueryRow(
		`SELECT duplicate_of FROM postings WHERE id = $1`, forwardSourceID).Scan(&duplicateOf); err != nil {
		t.Fatalf("read imported forward duplicate link: %v", err)
	}
	if duplicateOf != forwardTargetID {
		t.Fatalf("duplicate_of = %d, want later posting %d", duplicateOf, forwardTargetID)
	}
}

func TestImportCredentialIsEncryptedWithConfiguredMasterKey(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "credential-owner@example.invalid"
	targetURL, owners := prepareImportTarget(t, postgresURL, ownerEmail)
	keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
	const plaintext = "synthetic-anthropic-key"
	if err := os.WriteFile(keysPath, []byte(`{"Anthropic":"`+plaintext+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{0x42}, credential.MasterKeyBytes)
	t.Setenv("JOBCRON_ENV", "production")
	t.Setenv("JOBCRON_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(masterKey))

	if err := runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		aiKeysPath: keysPath, apply: true, out: io.Discard,
	}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ciphertext, nonce []byte
	var version int16
	if err := db.QueryRow(`
SELECT ciphertext, nonce, encryption_version
FROM user_ai_credentials WHERE user_id = $1 AND provider = 'anthropic'`,
		owners[ownerEmail]).Scan(&ciphertext, &nonce, &version); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(plaintext)) {
		t.Fatal("credential ciphertext contains plaintext")
	}
	cipher, err := credential.NewAESGCMCipher(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cipher.Open(owners[ownerEmail], "anthropic", ciphertext, nonce, version)
	if err != nil {
		t.Fatal(err)
	}
	if got != plaintext {
		t.Fatalf("decrypted credential mismatch")
	}
}

func TestImportCredentialMasterKeyRules(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	for _, tc := range []struct {
		name       string
		production bool
		envKey     string
		localKey   []byte
		wantError  bool
	}{
		{name: "production missing", production: true, wantError: true},
		{name: "production malformed", production: true, envKey: "not-base64", wantError: true},
		{name: "local fallback", localKey: bytes.Repeat([]byte{0x24}, credential.MasterKeyBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlitePath := seedSQLiteImportFixture(t)
			ownerEmail := "master-key-owner@example.invalid"
			targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
			keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
			if err := os.WriteFile(keysPath, []byte(`{"anthropic":"secret"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.production {
				t.Setenv("JOBCRON_ENV", "production")
			} else {
				t.Setenv("JOBCRON_ENV", "")
			}
			t.Setenv("JOBCRON_CREDENTIAL_ENCRYPTION_KEY", tc.envKey)
			localCalls := 0
			err := runImport(context.Background(), importOptions{
				sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
				aiKeysPath: keysPath, apply: true, out: io.Discard,
				loadLocalMasterKey: func() ([]byte, error) {
					localCalls++
					return tc.localKey, nil
				},
			})
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "credential encryption key") {
					t.Fatalf("runImport error = %v, want credential encryption key refusal", err)
				}
			} else if err != nil {
				t.Fatalf("runImport: %v", err)
			}
			want := 0
			if tc.name == "local fallback" {
				want = 1
			}
			if localCalls != want {
				t.Fatalf("local key loader calls = %d, want %d", localCalls, want)
			}
			db, err := sql.Open("pgx", targetURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if tc.wantError {
				assertPostgresScalar(t, db, `SELECT count(*) FROM postings`, 0)
				assertPostgresScalar(t, db, `SELECT count(*) FROM user_ai_credentials`, 0)
				assertPostgresScalar(t, db, `SELECT count(*) FROM local_data_imports`, 0)
			}
		})
	}
}

func TestImportRollbackAtEveryApplyBoundary(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	stages := []string{
		"after_postings", "after_profile", "after_scores", "after_bookmarks",
		"after_not_interested", "after_ai_extractions", "after_ai_scores",
		"after_ai_usage", "after_credential", "during_count_comparison",
		"before_ledger_insert",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			sqlitePath := seedSQLiteImportFixture(t)
			ownerEmail := "rollback-owner@example.invalid"
			targetURL, owners := prepareImportTarget(t, postgresURL, ownerEmail)
			keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
			if err := os.WriteFile(keysPath, []byte(`{"anthropic":"rollback-secret"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			const passwordHash = "synthetic-owner-password-hash"
			err := runImport(context.Background(), importOptions{
				sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
				aiKeysPath: keysPath, apply: true, out: io.Discard,
				loadLocalMasterKey: func() ([]byte, error) {
					return bytes.Repeat([]byte{0x61}, credential.MasterKeyBytes), nil
				},
				failAt: func(got string) error {
					if got == stage {
						return errors.New("injected apply failure")
					}
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "injected apply failure") {
				t.Fatalf("runImport error = %v, want injected failure", err)
			}
			db, err := sql.Open("pgx", targetURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range []string{
				"postings", "profiles", "scores", "bookmarks", "not_interested",
				"ai_extractions", "ai_scores", "ai_usage", "user_ai_credentials",
				"local_data_imports",
			} {
				assertPostgresScalar(t, db, `SELECT count(*) FROM `+table, 0)
			}
			var gotPassword string
			if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, owners[ownerEmail]).Scan(&gotPassword); err != nil {
				t.Fatal(err)
			}
			if gotPassword != passwordHash {
				t.Fatalf("password hash = %q, want unchanged", gotPassword)
			}
		})
	}
}

func TestImportRollbackRestoresPostingsSequence(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "sequence-rollback-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	err := runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
		failAt: func(stage string) error {
			if stage == "before_ledger_insert" {
				return errors.New("rollback after sequence reset")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rollback after sequence reset") {
		t.Fatalf("runImport error = %v, want injected rollback", err)
	}
	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var nextID int64
	if err := db.QueryRow(`SELECT nextval('postings_id_seq')`).Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	if nextID != 1 {
		t.Fatalf("postings sequence next ID after rollback = %d, want 1", nextID)
	}
}

func TestImportApplyEmptyPostingsRestartsSequenceAtOne(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"scores", "bookmarks", "not_interested", "ai_extractions", "ai_scores", "postings",
	} {
		if _, err := source.Exec(`DELETE FROM ` + table); err != nil {
			_ = source.Close()
			t.Fatalf("empty %s: %v", table, err)
		}
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	ownerEmail := "empty-postings-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	if err := runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	target, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var nextID int64
	if err := target.QueryRow(`SELECT nextval('postings_id_seq')`).Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	if nextID != 1 {
		t.Fatalf("empty import postings sequence next ID = %d, want 1", nextID)
	}
}

func TestImportRefusalDoesNotLoadOrCreateLocalMasterKey(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "refused-key-owner@example.invalid"
	targetURL, owners := prepareImportTarget(t, postgresURL, ownerEmail)
	target, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(`
INSERT INTO profiles (user_id, profile_json, profile_hash, updated_at)
VALUES ($1, '{}', 'existing', now())`, owners[ownerEmail]); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"anthropic":"must-not-load-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaderCalls := 0
	err = runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		aiKeysPath: keysPath, apply: true, out: io.Discard,
		loadLocalMasterKey: func() ([]byte, error) {
			loaderCalls++
			return bytes.Repeat([]byte{0x71}, credential.MasterKeyBytes), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "clean target") {
		t.Fatalf("runImport error = %v, want clean-target refusal", err)
	}
	if loaderCalls != 0 {
		t.Fatalf("local key loader calls on refused apply = %d, want 0", loaderCalls)
	}
}

func TestImportSameFingerprintVerifiesWithoutWrites(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "same-fingerprint-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"anthropic":"same-fingerprint-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{0x35}, credential.MasterKeyBytes)
	opts := importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		aiKeysPath: keysPath, apply: true, out: io.Discard,
		loadLocalMasterKey: func() ([]byte, error) { return masterKey, nil },
	}
	if err := runImport(context.Background(), opts); err != nil {
		t.Fatalf("first import: %v", err)
	}
	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := captureImportedTargetState(t, db)

	var out bytes.Buffer
	opts.out = &out
	opts.failAt = func(stage string) error {
		return fmt.Errorf("copy stage unexpectedly reached: %s", stage)
	}
	if err := runImport(context.Background(), opts); err != nil {
		t.Fatalf("same-fingerprint import: %v", err)
	}
	if !strings.Contains(out.String(), "already imported") {
		t.Fatalf("output missing already imported: %s", out.String())
	}
	after := captureImportedTargetState(t, db)
	if after != before {
		t.Fatalf("same-fingerprint verification mutated target\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestImportDifferentFingerprintForImportedOwnerIsRefused(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	ownerEmail := "different-fingerprint-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	firstSource := seedSQLiteImportFixture(t)
	if err := runImport(context.Background(), importOptions{
		sqlitePath: firstSource, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}

	secondSource := seedSQLiteImportFixture(t)
	sourceDB, err := sql.Open("sqlite", secondSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.Exec(`UPDATE postings SET title = 'different source' WHERE id = 1`); err != nil {
		_ = sourceDB.Close()
		t.Fatal(err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}
	err = runImport(context.Background(), importOptions{
		sqlitePath: secondSource, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
		failAt: func(stage string) error {
			return fmt.Errorf("copy stage unexpectedly reached: %s", stage)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "different source fingerprint") {
		t.Fatalf("runImport error = %v, want different-fingerprint refusal", err)
	}
}

func TestImportPostCommitMismatchFailsVerification(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "post-commit-mismatch-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	err := runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		apply: true, out: io.Discard,
		failAt: func(stage string) error {
			if stage != "after_commit_before_verification" {
				return nil
			}
			db, err := sql.Open("pgx", targetURL)
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(`UPDATE postings SET title = 'post-commit corruption' WHERE id = 1`)
			return err
		},
	})
	if err == nil || !strings.Contains(err.Error(), "post-commit verification failed") ||
		!strings.Contains(err.Error(), "preserve both systems") {
		t.Fatalf("runImport error = %v, want distinct post-commit verification failure", err)
	}
	db, openErr := sql.Open("pgx", targetURL)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	assertPostgresScalar(t, db, `SELECT count(*) FROM local_data_imports`, 1)
	assertPostgresScalar(t, db, `SELECT count(*) FROM postings WHERE title = 'post-commit corruption'`, 1)
}

func TestImportDoesNotModifySourceOrLegacyKeyFile(t *testing.T) {
	postgresURL := requireImportPostgres(t)
	sqlitePath := seedSQLiteImportFixture(t)
	ownerEmail := "source-safety-owner@example.invalid"
	targetURL, _ := prepareImportTarget(t, postgresURL, ownerEmail)
	keysPath := filepath.Join(t.TempDir(), "ai_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"anthropic":"source-safety-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeFiles := map[string]string{
		"database": hashOptionalFile(t, sqlitePath),
		"wal":      hashOptionalFile(t, sqlitePath+"-wal"),
		"key":      hashOptionalFile(t, keysPath),
	}
	if beforeFiles["wal"] == "missing" {
		t.Fatal("source-safety fixture must exercise a durable WAL file")
	}
	beforeLogical := captureSQLiteLogicalState(t, sqlitePath)

	if err := runImport(context.Background(), importOptions{
		sqlitePath: sqlitePath, postgresURL: targetURL, ownerEmail: ownerEmail,
		aiKeysPath: keysPath, apply: true, out: io.Discard,
		loadLocalMasterKey: func() ([]byte, error) {
			return bytes.Repeat([]byte{0x44}, credential.MasterKeyBytes), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	afterFiles := map[string]string{
		"database": hashOptionalFile(t, sqlitePath),
		"wal":      hashOptionalFile(t, sqlitePath+"-wal"),
		"key":      hashOptionalFile(t, keysPath),
	}
	afterLogical := captureSQLiteLogicalState(t, sqlitePath)
	if !reflect.DeepEqual(afterFiles, beforeFiles) {
		t.Fatalf("durable source hashes changed\nbefore: %v\nafter:  %v", beforeFiles, afterFiles)
	}
	if afterLogical != beforeLogical {
		t.Fatalf("source schema or rows changed\nbefore: %s\nafter:  %s", beforeLogical, afterLogical)
	}
	// Deliberately no -shm byte comparison: it is SQLite's rebuildable WAL index.
}

func TestImportSQLiteToPostgresUsesExistingOwnerWithoutChangingPassword(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	sqlitePath := seedSQLiteImportFixture(t)
	schema := createPostgresImportSchema(t, postgresURL)
	targetURL := databaseURLWithSearchPath(postgresURL, schema)
	ownerEmail := "existing-owner@example.com"
	passwordHash := "real-owner-password-hash"

	preexisting, err := storage.OpenPostgresMigrating(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("OpenPostgres preexisting target: %v", err)
	}
	var ownerID int64
	if err := preexisting.SQLDB().QueryRow(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, $2, now(), now())
RETURNING id`, ownerEmail, passwordHash).Scan(&ownerID); err != nil {
		_ = preexisting.Close()
		t.Fatalf("seed existing owner: %v", err)
	}
	if err := preexisting.Close(); err != nil {
		t.Fatalf("close preexisting target: %v", err)
	}

	var out bytes.Buffer
	if err := runImport(context.Background(), importOptions{
		sqlitePath:  sqlitePath,
		postgresURL: targetURL,
		ownerEmail:  ownerEmail,
		apply:       true,
		out:         &out,
	}); err != nil {
		t.Fatalf("runImport: %v\n%s", err, out.String())
	}

	db, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatalf("open postgres verification db: %v", err)
	}
	defer db.Close()

	if gotOwnerID := lookupUserIDByEmail(t, db, ownerEmail); gotOwnerID != ownerID {
		t.Fatalf("imported owner ID = %d, want existing owner ID %d", gotOwnerID, ownerID)
	}
	var gotPasswordHash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, ownerID).Scan(&gotPasswordHash); err != nil {
		t.Fatalf("query existing owner password hash: %v", err)
	}
	if gotPasswordHash != passwordHash {
		t.Fatalf("existing owner password hash = %q, want %q", gotPasswordHash, passwordHash)
	}

	assertPostgresScalar(t, db, `SELECT count(*) FROM users`, 1)
	for _, table := range []string{"profiles", "scores", "bookmarks", "not_interested", "ai_scores", "ai_usage"} {
		assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = %d`, table, ownerID), 1)
		assertPostgresScalar(t, db, fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id <> %d`, table, ownerID), 0)
	}
}

func seedSQLiteImportFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	st, err := storage.OpenSQLiteAt(path)
	if err != nil {
		t.Fatalf("OpenSQLiteAt: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profileJSON := `{"stacks":["Go"],"locations":["서울"],"min_score":40}`
	profileHash, _, err := st.SaveProfile(ctx, profileJSON)
	if err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	id, _, err := st.UpsertPosting(ctx, scraper.Posting{
		Source:          "jumpit",
		SourcePostingID: "import-1",
		URL:             "https://jumpit.example/jobs/import-1",
		Title:           "신입 백엔드 개발자",
		Company:         "테스트컴퍼니",
		Location:        "서울",
		Newcomer:        true,
		MinCareer:       0,
		MaxCareer:       0,
		CareerLevel:     "신입",
		StackTags:       []string{"Go", "PostgreSQL"},
		Tags:            []scraper.Tag{{ID: "tag-1", Name: "신입", Category: "career"}},
		Description:     "Go와 PostgreSQL을 사용하는 신입 백엔드 포지션",
		RawJSON:         `{"id":"import-1"}`,
		AlwaysOpen:      true,
		FirstSeenAt:     time.Date(2026, 7, 9, 1, 2, 3, 0, time.UTC),
		LastSeenAt:      time.Date(2026, 7, 9, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	duplicateID, _, err := st.UpsertPosting(ctx, scraper.Posting{
		Source:          "rallit",
		SourcePostingID: "import-duplicate",
		URL:             "https://rallit.example/jobs/import-duplicate",
		Title:           "신입 백엔드 개발자",
		Company:         "테스트컴퍼니",
		Location:        "서울",
		Newcomer:        true,
		MinCareer:       0,
		MaxCareer:       0,
		CareerLevel:     "신입",
		StackTags:       []string{"Go"},
		Description:     "중복 공고",
		RawJSON:         `{"id":"import-duplicate"}`,
		AlwaysOpen:      true,
		FirstSeenAt:     time.Date(2026, 7, 9, 1, 3, 3, 0, time.UTC),
		LastSeenAt:      time.Date(2026, 7, 9, 1, 3, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpsertPosting duplicate: %v", err)
	}
	if _, err := st.SQLDB().ExecContext(ctx, `UPDATE postings SET duplicate_of = ? WHERE id = ?`, id, duplicateID); err != nil {
		t.Fatalf("set duplicate_of: %v", err)
	}
	when := time.Date(2026, 7, 9, 2, 3, 4, 0, time.UTC)
	if err := st.UpsertScore(ctx, storage.Score{PostingID: id, ProfileHash: profileHash, Total: 87, BreakdownJSON: `[{"label":"stack"}]`, ComputedAt: when}); err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}
	if err := st.SetBookmark(ctx, id, when); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	if err := st.SetNotInterested(ctx, id, when.Add(time.Minute)); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}
	maxCareer := 2
	if err := st.UpsertAIExtraction(ctx, id, "content-hash", "ai-v1", ai.Extraction{
		MinCareer:      0,
		MaxCareer:      &maxCareer,
		Newcomer:       true,
		EducationEnum:  "bachelor",
		CareerEvidence: "신입",
	}, when); err != nil {
		t.Fatalf("UpsertAIExtraction: %v", err)
	}
	if err := st.UpsertAIScore(ctx, 1, id, "input-hash", "ai-v1", ai.Delta{
		Items:    []ai.DeltaItem{{Signal: "Go", Kind: "positive", Delta: 3, Evidence: "Go", MatchedGoal: "Go"}},
		NetDelta: 3,
	}, when); err != nil {
		t.Fatalf("UpsertAIScore: %v", err)
	}
	if err := st.AddAIUsage(ctx, 1, "2026-07-09", 123, 45); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	return path
}

func createPostgresImportSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	schema := postgresImportSchemaName(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	return schema
}

func requireImportPostgres(t *testing.T) string {
	t.Helper()
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	return postgresURL
}

func prepareImportTarget(t *testing.T, postgresURL string, emails ...string) (string, map[string]int64) {
	t.Helper()
	schema := createPostgresImportSchema(t, postgresURL)
	targetURL := databaseURLWithSearchPath(postgresURL, schema)
	target, err := storage.OpenPostgresMigrating(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("open prepared import target: %v", err)
	}
	owners := make(map[string]int64, len(emails))
	for _, email := range emails {
		var ownerID int64
		if err := target.SQLDB().QueryRow(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, 'synthetic-owner-password-hash', now(), now())
RETURNING id`, email).Scan(&ownerID); err != nil {
			_ = target.Close()
			t.Fatalf("insert prepared owner: %v", err)
		}
		owners[email] = ownerID
	}
	if err := target.Close(); err != nil {
		t.Fatalf("close prepared import target: %v", err)
	}
	return targetURL, owners
}

func assertPostgresScalar(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}

func assertAIUsage(t *testing.T, db *sql.DB, userID int64, day string, wantInput, wantOutput int) {
	t.Helper()
	var gotInput, gotOutput int
	if err := db.QueryRow(`SELECT input_tokens, output_tokens FROM ai_usage WHERE user_id = $1 AND day = $2`, userID, day).
		Scan(&gotInput, &gotOutput); err != nil {
		t.Fatalf("query ai_usage for user %d on %s: %v", userID, day, err)
	}
	if gotInput != wantInput || gotOutput != wantOutput {
		t.Fatalf("ai_usage[%s] = input %d output %d, want input %d output %d", day, gotInput, gotOutput, wantInput, wantOutput)
	}
}

func lookupUserIDByEmail(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("query user id for %s: %v", email, err)
	}
	return id
}

func captureImportedTargetState(t *testing.T, db *sql.DB) string {
	t.Helper()
	queries := []string{
		`SELECT * FROM users ORDER BY id`,
		`SELECT * FROM profiles ORDER BY user_id`,
		`SELECT * FROM postings ORDER BY id`,
		`SELECT * FROM scores ORDER BY user_id, posting_id`,
		`SELECT * FROM bookmarks ORDER BY user_id, posting_id`,
		`SELECT * FROM not_interested ORDER BY user_id, posting_id`,
		`SELECT * FROM ai_extractions ORDER BY posting_id, content_hash, ai_version`,
		`SELECT * FROM ai_scores ORDER BY user_id, posting_id, ai_input_hash, ai_version`,
		`SELECT * FROM ai_usage ORDER BY user_id, day`,
		`SELECT * FROM user_ai_credentials ORDER BY user_id, provider`,
		`SELECT * FROM local_data_imports ORDER BY user_id, source_sha256`,
	}
	return dumpSQLQueries(t, db, queries)
}

func captureSQLiteLogicalState(t *testing.T, path string) string {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := u.Query()
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queries := []string{
		`SELECT type, name, tbl_name, sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`,
		`SELECT * FROM profile ORDER BY id`,
		`SELECT * FROM postings ORDER BY id`,
		`SELECT * FROM scores ORDER BY posting_id`,
		`SELECT * FROM bookmarks ORDER BY posting_id`,
		`SELECT * FROM not_interested ORDER BY posting_id`,
		`SELECT * FROM ai_extractions ORDER BY posting_id, content_hash, ai_version`,
		`SELECT * FROM ai_scores ORDER BY posting_id, ai_input_hash, ai_version`,
		`SELECT * FROM ai_usage ORDER BY day`,
	}
	return dumpSQLQueries(t, db, queries)
}

func dumpSQLQueries(t *testing.T, db *sql.DB, queries []string) string {
	t.Helper()
	var out strings.Builder
	for _, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("query state %q: %v", query, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s|%v\n", query, columns)
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for _, value := range values {
				switch value := value.(type) {
				case []byte:
					fmt.Fprintf(&out, "bytes:%x|", value)
				default:
					fmt.Fprintf(&out, "%T:%v|", value, value)
				}
			}
			out.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

func hashOptionalFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var nonSchemaChars = regexp.MustCompile(`[^a-z0-9_]`)

func postgresImportSchemaName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "_")
	name = nonSchemaChars.ReplaceAllString(name, "_")
	return fmt.Sprintf("test_import_%s_%d_%d", name, time.Now().UnixNano(), rand.Uint64())
}

func databaseURLWithSearchPath(databaseURL, schema string) string {
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	return databaseURL + separator + "search_path=" + schema
}
