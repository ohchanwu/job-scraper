package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
)

const goTrimSpaceCharacters = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

func TestPostgresMigrationsCreateCoreTables(t *testing.T) {
	st, schema := newPostgresTestStoreWithSchema(t)

	for _, table := range []string{
		"ai_dealbreaker_validations",
		"local_data_imports",
		"users",
		"user_ai_credentials",
		"sessions",
		"postings",
		"profiles",
		"bookmarks",
		"not_interested",
		"scores",
		"scrape_runs",
	} {
		var exists bool
		err := st.db.QueryRowContext(context.Background(), `SELECT EXISTS (
			SELECT 1
			  FROM information_schema.tables
			 WHERE table_schema = $1
			   AND table_name = $2
		)`, schema, table).Scan(&exists)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
}

func TestCanonicalEmailMigrationNormalizesExistingRowsAndEnforcesConstraint(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 17)
	ctx := context.Background()
	users := []struct {
		id   int64
		want string
	}{
		{insertMigrationTestUser(t, st, " Student@Example.COM "), "student@example.com"},
		{insertMigrationTestUser(t, st, "\tTabbed@Example.COM\n"), "tabbed@example.com"},
		{insertMigrationTestUser(t, st, goTrimSpaceCharacters+"Unicode@Example.COM"+goTrimSpaceCharacters), "unicode@example.com"},
	}

	if err := applyPostgresMigrationVersion(st.db, 18); err != nil {
		t.Fatalf("apply migration 0018: %v", err)
	}
	for _, user := range users {
		var email string
		if err := st.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, user.id).Scan(&email); err != nil {
			t.Fatalf("read normalized email: %v", err)
		}
		if email != user.want {
			t.Fatalf("migrated email = %q, want %q", email, user.want)
		}
	}
	var constraintExists bool
	if err := st.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM pg_constraint
     WHERE conrelid = 'users'::regclass
       AND conname = 'users_email_canonical'
)`).Scan(&constraintExists); err != nil {
		t.Fatalf("inspect canonical email constraint: %v", err)
	}
	if !constraintExists {
		t.Fatal("users_email_canonical constraint does not exist")
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO users (email, password_hash) VALUES ('Other@Example.COM', 'hash')`); err == nil {
		t.Fatal("non-canonical email insert succeeded")
	}
	for _, email := range []string{
		"\tother@example.com\n",
		"\u00a0other@example.com\u3000",
	} {
		if _, err := st.db.ExecContext(ctx, `
INSERT INTO users (email, password_hash) VALUES ($1, 'hash')`, email); err == nil {
			t.Fatalf("whitespace-wrapped email %q insert succeeded", email)
		}
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO users (email, password_hash) VALUES ('', 'hash')`); err == nil {
		t.Fatal("empty email insert succeeded")
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO users (email, password_hash) VALUES ('student@example.com', 'hash')`); err == nil {
		t.Fatal("duplicate canonical email insert succeeded")
	}
}

func TestCanonicalEmailMigrationRejectsHistoricalCollision(t *testing.T) {
	for _, tc := range []struct {
		name, wrapped string
	}{
		{name: "ASCII space", wrapped: " Student@Example.COM "},
		{name: "tab", wrapped: "\tStudent@Example.COM\n"},
		{name: "Unicode whitespace", wrapped: goTrimSpaceCharacters + "Student@Example.COM" + goTrimSpaceCharacters},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newPostgresTestStoreThroughMigration(t, 17)
			insertMigrationTestUser(t, st, tc.wrapped)
			insertMigrationTestUser(t, st, "student@example.com")

			if err := applyPostgresMigrationVersion(st.db, 18); err == nil {
				t.Fatal("migration 0018 succeeded with colliding historical emails")
			}
			assertMigrationVersionRecorded(t, st, 18, false)
		})
	}
}

func TestContextualDealbreakerMigrationSplitsExtractionEvidence(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 16)
	ctx := context.Background()
	postingID := insertMigrationTestPosting(t, st, "contextual-evidence")
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO ai_extractions (
    posting_id, content_hash, ai_version, min_career, newcomer,
    education_enum, evidence, computed_at
) VALUES ($1, 'content', 'ai-v1', 0, true, 'none', '신입 가능', now())`, postingID); err != nil {
		t.Fatalf("seed extraction: %v", err)
	}

	if err := applyPostgresMigrationVersion(st.db, 17); err != nil {
		t.Fatalf("apply migration 0017: %v", err)
	}
	var careerEvidence, educationEvidence string
	if err := st.db.QueryRowContext(ctx, `
SELECT career_evidence, education_evidence
  FROM ai_extractions
 WHERE posting_id = $1`, postingID).Scan(&careerEvidence, &educationEvidence); err != nil {
		t.Fatalf("read migrated extraction: %v", err)
	}
	if careerEvidence != "신입 가능" || educationEvidence != "" {
		t.Fatalf("migrated evidence = career:%q education:%q", careerEvidence, educationEvidence)
	}
}

func TestDealbreakerMatchProvenanceMigration(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 18)
	ctx := context.Background()
	userID := insertMigrationTestUser(t, st, "provenance-migration@example.invalid")
	postingID := insertMigrationTestPosting(t, st, "provenance-migration")
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO ai_dealbreaker_validations (
    user_id, posting_id, content_hash, ai_version, keyword_hash,
    verdict, evidence, computed_at
) VALUES ($1, $2, 'content', 'anthropic|model|1|prof', 'keyword', 'applies', '야근이 잦습니다', now())`,
		userID, postingID); err != nil {
		t.Fatalf("seed version-1 validation: %v", err)
	}

	if err := applyPostgresMigrationVersion(st.db, 19); err != nil {
		t.Fatalf("apply migration 0019: %v", err)
	}
	assertMigrationVersionRecorded(t, st, 19, true)

	var matchJSON, reasonCode, reasonEvidence string
	if err := st.db.QueryRowContext(ctx, `
SELECT match_json, reason_code, reason_evidence
  FROM ai_dealbreaker_validations
 WHERE user_id = $1 AND posting_id = $2`, userID, postingID).Scan(&matchJSON, &reasonCode, &reasonEvidence); err != nil {
		t.Fatalf("read migrated validation: %v", err)
	}
	if matchJSON != "{}" || reasonCode != "" || reasonEvidence != "" {
		t.Fatalf("migrated row = match:%q reason_code:%q reason_evidence:%q", matchJSON, reasonCode, reasonEvidence)
	}

	var evidenceExists bool
	if err := st.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM information_schema.columns
     WHERE table_schema = current_schema()
       AND table_name = 'ai_dealbreaker_validations'
       AND column_name = 'evidence'
)`).Scan(&evidenceExists); err != nil {
		t.Fatalf("inspect evidence column: %v", err)
	}
	if evidenceExists {
		t.Fatal("legacy evidence column still exists after migration 0019")
	}

	// A version-2 row differs only by ai_version, so it coexists under the
	// unchanged primary key and the version-1 row misses a version-2 lookup.
	match := ai.DealbreakerMatch{Evidence: "야근이 잦습니다", Source: ai.DealbreakerMatchDescription}
	validation := ai.DealbreakerValidation{
		CandidateID: "keyword",
		Verdict:     ai.DealbreakerApplies,
		ReasonCode:  ai.DealbreakerReasonExpectedCondition,
	}
	const version2 = "anthropic|model|2|prof"
	if err := st.UpsertAIDealbreakerValidation(ctx, userID, postingID, "content", version2, "keyword", match, validation, time.Now()); err != nil {
		t.Fatalf("insert version-2 validation: %v", err)
	}
	var rows int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM ai_dealbreaker_validations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows after version-2 insert = %d, want 2", rows)
	}
	got, err := st.AIDealbreakerValidationsByPostingID(ctx, userID, version2)
	if err != nil {
		t.Fatalf("AIDealbreakerValidationsByPostingID: %v", err)
	}
	if len(got[postingID]) != 1 {
		t.Fatalf("version-2 lookup returned %d rows, want only the version-2 row", len(got[postingID]))
	}
	if row := got[postingID]["content\x00keyword"]; row.Match != match || row.Validation != validation {
		t.Fatalf("version-2 lookup row = %+v", row)
	}
}

func TestLocalDataImportsLedgerConstraints(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	ctx := context.Background()
	firstOwnerID := insertMigrationTestUser(t, st, "first-import-owner@example.invalid")
	secondOwnerID := insertMigrationTestUser(t, st, "second-import-owner@example.invalid")
	const fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const counts = `{"postings":1}`

	insert := func(userID int64, sourceSHA256 string) error {
		_, err := st.db.ExecContext(ctx, `
INSERT INTO local_data_imports (user_id, source_sha256, source_counts, imported_counts)
VALUES ($1, $2, $3::jsonb, $3::jsonb)`, userID, sourceSHA256, counts)
		return err
	}

	if err := insert(firstOwnerID, fingerprint); err != nil {
		t.Fatalf("insert first owner ledger: %v", err)
	}
	if err := insert(secondOwnerID, fingerprint); err != nil {
		t.Fatalf("insert same fingerprint for second owner: %v", err)
	}
	if err := insert(firstOwnerID, fingerprint); err == nil {
		t.Fatal("duplicate owner/fingerprint ledger insert succeeded")
	}
	if err := insert(firstOwnerID, "too-short"); err == nil {
		t.Fatal("invalid fingerprint ledger insert succeeded")
	}

	if _, err := st.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, firstOwnerID); err != nil {
		t.Fatalf("delete first owner: %v", err)
	}
	var firstRows, secondRows int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM local_data_imports WHERE user_id = $1`, firstOwnerID).Scan(&firstRows); err != nil {
		t.Fatalf("count cascaded ledger rows: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM local_data_imports WHERE user_id = $1`, secondOwnerID).Scan(&secondRows); err != nil {
		t.Fatalf("count retained ledger rows: %v", err)
	}
	if firstRows != 0 || secondRows != 1 {
		t.Fatalf("ledger rows after owner delete = first:%d second:%d, want first:0 second:1", firstRows, secondRows)
	}
}

func TestUserScopedAIMigrationAssignsGlobalRowsToSoleOwner(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)
	ctx := context.Background()
	userID := insertMigrationTestUser(t, st, "sole-owner@example.invalid")
	postingID := insertMigrationTestPosting(t, st, "sole-owner")
	computedAt := time.Date(2026, 7, 15, 1, 2, 3, 456000000, time.UTC)
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO ai_scores (posting_id, ai_input_hash, ai_version, items_json, net_delta, computed_at)
VALUES ($1, $2, $3, $4, $5, $6)`, postingID, "input-hash", "model-v1", `[{"signal":"stack","delta":7}]`, 7, computedAt); err != nil {
		t.Fatalf("seed ai score: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO ai_usage (day, input_tokens, output_tokens)
VALUES ('2026-07-15', 1234, 321)`); err != nil {
		t.Fatalf("seed ai usage: %v", err)
	}

	if err := applyPostgresMigrationVersion(st.db, 15); err != nil {
		t.Fatalf("apply migration 0015: %v", err)
	}

	var gotUserID, gotPostingID int64
	var gotHash, gotVersion, gotItems string
	var gotDelta int
	var gotComputedAt time.Time
	if err := st.db.QueryRowContext(ctx, `
SELECT user_id, posting_id, ai_input_hash, ai_version, items_json, net_delta, computed_at
  FROM ai_scores`).Scan(&gotUserID, &gotPostingID, &gotHash, &gotVersion, &gotItems, &gotDelta, &gotComputedAt); err != nil {
		t.Fatalf("read migrated ai score: %v", err)
	}
	if gotUserID != userID || gotPostingID != postingID || gotHash != "input-hash" ||
		gotVersion != "model-v1" || gotItems != `[{"signal":"stack","delta":7}]` ||
		gotDelta != 7 || !gotComputedAt.Equal(computedAt) {
		t.Fatalf("migrated ai score = user=%d posting=%d hash=%q version=%q items=%q delta=%d computed=%s",
			gotUserID, gotPostingID, gotHash, gotVersion, gotItems, gotDelta, gotComputedAt)
	}

	var gotUsageUserID int64
	var gotDay string
	var gotInput, gotOutput int64
	if err := st.db.QueryRowContext(ctx, `
SELECT user_id, day::text, input_tokens, output_tokens
  FROM ai_usage`).Scan(&gotUsageUserID, &gotDay, &gotInput, &gotOutput); err != nil {
		t.Fatalf("read migrated ai usage: %v", err)
	}
	if gotUsageUserID != userID || gotDay != "2026-07-15" || gotInput != 1234 || gotOutput != 321 {
		t.Fatalf("migrated ai usage = user=%d day=%q input=%d output=%d",
			gotUsageUserID, gotDay, gotInput, gotOutput)
	}
	assertMigrationVersionRecorded(t, st, 15, true)
}

func TestUserScopedAIMigrationRejectsRowsWithNoOwner(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)
	postingID := insertMigrationTestPosting(t, st, "no-owner")
	seedLegacyMigrationScore(t, st, postingID)

	err := applyPostgresMigrationVersion(st.db, 15)
	if err == nil {
		t.Fatal("migration 0015 succeeded with legacy AI rows and no owner")
	}
	assertLegacyMigrationStateIntact(t, st, postingID)
	assertMigrationVersionRecorded(t, st, 15, false)
}

func TestUserScopedAIMigrationRejectsRowsWithMultipleOwners(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)
	insertMigrationTestUser(t, st, "first-owner@example.invalid")
	insertMigrationTestUser(t, st, "second-owner@example.invalid")
	postingID := insertMigrationTestPosting(t, st, "multiple-owners")
	seedLegacyMigrationScore(t, st, postingID)

	err := applyPostgresMigrationVersion(st.db, 15)
	if err == nil {
		t.Fatal("migration 0015 succeeded with legacy AI rows and multiple owners")
	}
	assertLegacyMigrationStateIntact(t, st, postingID)
	assertMigrationVersionRecorded(t, st, 15, false)
}

func TestUserScopedAIMigrationAllowsEmptyTablesWithoutOwner(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)

	if err := applyPostgresMigrationVersion(st.db, 15); err != nil {
		t.Fatalf("apply migration 0015 to empty database: %v", err)
	}

	for _, table := range []string{"ai_scores", "ai_usage"} {
		var count int
		if err := st.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s row count = %d, want 0", table, count)
		}
		var hasUserID bool
		if err := st.db.QueryRow(`
SELECT EXISTS (
    SELECT 1
      FROM information_schema.columns
     WHERE table_schema = current_schema()
       AND table_name = $1
       AND column_name = 'user_id'
)`, table).Scan(&hasUserID); err != nil {
			t.Fatalf("inspect %s.user_id: %v", table, err)
		}
		if !hasUserID {
			t.Fatalf("%s.user_id is missing", table)
		}
	}
	var hasPostingIndex bool
	if err := st.db.QueryRow(`
SELECT EXISTS (
    SELECT 1
      FROM pg_indexes
     WHERE schemaname = current_schema()
       AND tablename = 'ai_scores'
       AND indexname = 'idx_ai_scores_posting_id'
)`).Scan(&hasPostingIndex); err != nil {
		t.Fatalf("inspect ai_scores posting index: %v", err)
	}
	if !hasPostingIndex {
		t.Fatal("idx_ai_scores_posting_id is missing")
	}
	assertMigrationVersionRecorded(t, st, 15, true)
}

func TestUserScopedAIMigrationLetsEarlierPostingWriterFinishBeforeCopy(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)
	userID := insertMigrationTestUser(t, st, "locked-owner@example.invalid")
	postingID := insertMigrationTestPosting(t, st, "locks")
	seedLegacyMigrationScore(t, st, postingID)

	writer, err := st.db.Begin()
	if err != nil {
		t.Fatalf("begin legacy writer: %v", err)
	}
	defer writer.Rollback()
	if _, err := writer.Exec(`UPDATE postings SET title = 'writer committed first' WHERE id = $1`, postingID); err != nil {
		t.Fatalf("legacy writer update posting: %v", err)
	}
	if _, err := writer.Exec(`UPDATE ai_scores SET net_delta = 42 WHERE posting_id = $1`, postingID); err != nil {
		t.Fatalf("legacy writer update AI score: %v", err)
	}

	migrationDone := make(chan error, 1)
	go func() { migrationDone <- applyPostgresMigrationVersion(st.db, 15) }()
	waitForMigrationTableLock(t, st.db, "postings", "ShareRowExclusiveLock", false)
	assertNoGrantedTableLock(t, st.db, "ai_scores", "AccessExclusiveLock")

	if err := writer.Commit(); err != nil {
		t.Fatalf("commit legacy writer: %v", err)
	}
	select {
	case err := <-migrationDone:
		if err != nil {
			t.Fatalf("apply migration 0015: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not finish after releasing blocker")
	}

	assertMigrationVersionRecorded(t, st, 15, true)
	var gotUserID int64
	var gotDelta int
	if err := st.db.QueryRow(`SELECT user_id, net_delta FROM ai_scores`).Scan(&gotUserID, &gotDelta); err != nil {
		t.Fatalf("read migrated score owner: %v", err)
	}
	if gotUserID != userID || gotDelta != 42 {
		t.Fatalf("migrated score = owner %d delta %d, want owner %d delta 42", gotUserID, gotDelta, userID)
	}
}

func TestUserScopedAIMigrationBlocksLaterWriterAtPostingsFirst(t *testing.T) {
	st := newPostgresTestStoreThroughMigration(t, 14)
	insertMigrationTestUser(t, st, "blocked-owner@example.invalid")
	postingID := insertMigrationTestPosting(t, st, "blocked-writer")
	seedLegacyMigrationScore(t, st, postingID)

	blocker, err := st.db.Begin()
	if err != nil {
		t.Fatalf("begin schema migration blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.Exec(`LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock schema_migrations: %v", err)
	}
	migrationDone := make(chan error, 1)
	go func() { migrationDone <- applyPostgresMigrationVersion(st.db, 15) }()
	waitForMigrationTableLock(t, st.db, "postings", "ShareRowExclusiveLock", true)

	assertMigrationBlocksWrite(t, st.db, `UPDATE postings SET title = 'must not persist' WHERE id = `+strconv.FormatInt(postingID, 10))

	if err := blocker.Commit(); err != nil {
		t.Fatalf("release schema migration blocker: %v", err)
	}
	select {
	case err := <-migrationDone:
		if err != nil {
			t.Fatalf("apply migration 0015: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not finish after releasing blocker")
	}
	var title string
	if err := st.db.QueryRow(`SELECT title FROM postings WHERE id = $1`, postingID).Scan(&title); err != nil {
		t.Fatalf("read posting after blocked writer: %v", err)
	}
	if title == "must not persist" {
		t.Fatal("writer touched posting while migration held ordered locks")
	}
}

func waitForMigrationTableLock(t *testing.T, db *sql.DB, table, mode string, granted bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var locked bool
		err := db.QueryRow(`
SELECT EXISTS (
    SELECT 1
      FROM pg_locks
     WHERE relation = to_regclass($1)
       AND mode = $2
       AND granted = $3
)`, table, mode, granted).Scan(&locked)
		if err != nil {
			t.Fatalf("inspect migration lock on %s: %v", table, err)
		}
		if locked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("migration did not acquire %s on %s", mode, table)
}

func assertNoGrantedTableLock(t *testing.T, db *sql.DB, table, mode string) {
	t.Helper()
	var locked bool
	if err := db.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM pg_locks
     WHERE relation = to_regclass($1) AND mode = $2 AND granted
)`, table, mode).Scan(&locked); err != nil {
		t.Fatalf("inspect granted lock on %s: %v", table, err)
	}
	if locked {
		t.Fatalf("migration acquired %s on %s before earlier postings writer finished", mode, table)
	}
}

func assertMigrationBlocksWrite(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := db.ExecContext(ctx, query)
	if err == nil {
		t.Fatalf("write completed while migration locks were held: %s", query)
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("blocked write error = %v, want deadline exceeded", err)
	}
}

func newPostgresTestStoreThroughMigration(t *testing.T, maxVersion int) *Store {
	t.Helper()
	databaseURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := postgresTestSchemaName(t)
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create postgres test schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})

	db, err := sql.Open("pgx", databaseURLWithSearchPath(databaseURL, schema))
	if err != nil {
		t.Fatalf("open postgres test store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE schema_migrations (
    version    integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := postgresMigrationsFS.ReadDir("postgres_migrations")
	if err != nil {
		t.Fatalf("read postgres migrations: %v", err)
	}
	for _, entry := range entries {
		version, err := strconv.Atoi(entry.Name()[:4])
		if err != nil {
			t.Fatalf("parse migration version %q: %v", entry.Name(), err)
		}
		if version > maxVersion {
			continue
		}
		if err := applyPostgresMigrationVersion(db, version); err != nil {
			t.Fatalf("apply migration %04d: %v", version, err)
		}
	}
	return &Store{db: db, dialect: DialectPostgres}
}

func applyPostgresMigrationVersion(db *sql.DB, version int) error {
	entries, err := postgresMigrationsFS.ReadDir("postgres_migrations")
	if err != nil {
		return err
	}
	var name string
	for _, entry := range entries {
		got, err := strconv.Atoi(entry.Name()[:4])
		if err != nil {
			return err
		}
		if got == version {
			name = entry.Name()
			break
		}
	}
	if name == "" {
		return fmt.Errorf("postgres migration %04d does not exist", version)
	}
	sqlBytes, err := postgresMigrationsFS.ReadFile("postgres_migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(string(sqlBytes)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())`, version); err != nil {
		return err
	}
	return tx.Commit()
}

func insertMigrationTestUser(t *testing.T, st *Store, email string) int64 {
	t.Helper()
	var userID int64
	if err := st.db.QueryRow(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, 'synthetic-password-hash', now(), now())
RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("insert migration test user: %v", err)
	}
	return userID
}

func insertMigrationTestPosting(t *testing.T, st *Store, suffix string) int64 {
	t.Helper()
	posting := samplePosting()
	posting.SourcePostingID = "migration-0015-" + suffix
	posting.ID = 0
	id, _, err := st.UpsertPosting(context.Background(), posting)
	if err != nil {
		t.Fatalf("insert migration test posting: %v", err)
	}
	return id
}

func seedLegacyMigrationScore(t *testing.T, st *Store, postingID int64) {
	t.Helper()
	if _, err := st.db.Exec(`
INSERT INTO ai_scores (posting_id, ai_input_hash, ai_version, items_json, net_delta, computed_at)
VALUES ($1, 'legacy-hash', 'legacy-version', '[{"signal":"legacy","delta":-3}]', -3,
        '2026-07-15T04:05:06Z')`, postingID); err != nil {
		t.Fatalf("seed legacy ai score: %v", err)
	}
	if _, err := st.db.Exec(`
INSERT INTO ai_usage (day, input_tokens, output_tokens)
VALUES ('2026-07-15', 99, 11)`); err != nil {
		t.Fatalf("seed legacy ai usage: %v", err)
	}
}

func assertLegacyMigrationStateIntact(t *testing.T, st *Store, postingID int64) {
	t.Helper()
	var gotPostingID int64
	var gotHash, gotVersion, gotItems string
	var gotDelta int
	var gotComputedAt time.Time
	if err := st.db.QueryRow(`
SELECT posting_id, ai_input_hash, ai_version, items_json, net_delta, computed_at
  FROM ai_scores`).Scan(&gotPostingID, &gotHash, &gotVersion, &gotItems, &gotDelta, &gotComputedAt); err != nil {
		t.Fatalf("read legacy ai score after rejected migration: %v", err)
	}
	wantComputedAt := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	if gotPostingID != postingID || gotHash != "legacy-hash" || gotVersion != "legacy-version" ||
		gotItems != `[{"signal":"legacy","delta":-3}]` || gotDelta != -3 || !gotComputedAt.Equal(wantComputedAt) {
		t.Fatalf("legacy ai score changed after rejected migration: posting=%d hash=%q version=%q items=%q delta=%d computed=%s",
			gotPostingID, gotHash, gotVersion, gotItems, gotDelta, gotComputedAt)
	}
	var day string
	var inputTokens, outputTokens int
	if err := st.db.QueryRow(`SELECT day, input_tokens, output_tokens FROM ai_usage`).Scan(&day, &inputTokens, &outputTokens); err != nil {
		t.Fatalf("read legacy ai usage after rejected migration: %v", err)
	}
	if day != "2026-07-15" || inputTokens != 99 || outputTokens != 11 {
		t.Fatalf("legacy ai usage changed after rejected migration: day=%q input=%d output=%d", day, inputTokens, outputTokens)
	}
}

func assertMigrationVersionRecorded(t *testing.T, st *Store, version int, want bool) {
	t.Helper()
	var exists bool
	if err := st.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
		t.Fatalf("check migration version %d: %v", version, err)
	}
	if exists != want {
		t.Fatalf("migration version %d recorded = %v, want %v", version, exists, want)
	}
}

func TestPostgresCredentialMigrationConstraintsAndCascade(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()

	var userID int64
	if err := st.db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('credential-schema@example.invalid', 'synthetic-password-hash', now(), now())
RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert credential owner: %v", err)
	}

	validCiphertext := []byte("synthetic-ciphertext")
	validNonce := []byte("nonce-12byte")
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO user_ai_credentials (user_id, provider, ciphertext, nonce, encryption_version)
VALUES ($1, $2, $3, $4, $5)`, userID, "anthropic", validCiphertext, validNonce, 1); err != nil {
		t.Fatalf("insert valid credential: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
INSERT INTO user_ai_credentials (user_id, provider, ciphertext, nonce, encryption_version)
VALUES ($1, $2, $3, $4, $5)`, userID, "anthropic", validCiphertext, validNonce, 1); err == nil {
		t.Fatal("duplicate user/provider insert succeeded, want primary-key violation")
	}

	tests := []struct {
		name       string
		provider   string
		ciphertext []byte
		nonce      []byte
		version    int16
	}{
		{name: "empty provider", provider: "", ciphertext: validCiphertext, nonce: validNonce, version: 1},
		{name: "ciphertext is only the GCM tag", provider: "short-ciphertext", ciphertext: []byte("0123456789abcdef"), nonce: validNonce, version: 1},
		{name: "nonce is not twelve bytes", provider: "short-nonce", ciphertext: validCiphertext, nonce: []byte("eleven-byte"), version: 1},
		{name: "version is zero", provider: "zero-version", ciphertext: validCiphertext, nonce: validNonce, version: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := st.db.ExecContext(ctx, `
INSERT INTO user_ai_credentials (user_id, provider, ciphertext, nonce, encryption_version)
VALUES ($1, $2, $3, $4, $5)`, userID, tt.provider, tt.ciphertext, tt.nonce, tt.version); err == nil {
				t.Fatal("invalid credential insert succeeded")
			}
		})
	}

	if _, err := st.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete credential owner: %v", err)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `
SELECT count(*) FROM user_ai_credentials WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count cascaded credentials: %v", err)
	}
	if count != 0 {
		t.Fatalf("credential rows after user delete = %d, want 0", count)
	}
}

func TestRenameImportOwnerMigrationRenamesLegacyFallbackAndPreservesID(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	if _, err := st.SQLDB().ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	var legacyID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('sqlite-import-owner@job-scraper.local', 'imported-sqlite-no-login', now(), now())
RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}

	executeRenameImportOwnerMigration(t, st)

	var gotID int64
	var gotEmail, gotPasswordHash string
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT id, email, password_hash
  FROM users
 WHERE email = 'sqlite-import-owner@jobcron.local'`).Scan(&gotID, &gotEmail, &gotPasswordHash); err != nil {
		t.Fatal(err)
	}
	if gotID != legacyID {
		t.Fatalf("renamed owner ID = %d, want %d", gotID, legacyID)
	}
	if gotEmail != "sqlite-import-owner@jobcron.local" || gotPasswordHash != "imported-sqlite-no-login" {
		t.Fatalf("renamed owner = email %q password_hash %q", gotEmail, gotPasswordHash)
	}
}

func TestRenameImportOwnerMigrationAlreadyRenamedIsIdempotent(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	if _, err := st.SQLDB().ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	var canonicalID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('sqlite-import-owner@jobcron.local', 'imported-sqlite-no-login', now(), now())
RETURNING id`).Scan(&canonicalID); err != nil {
		t.Fatal(err)
	}

	executeRenameImportOwnerMigration(t, st)
	executeRenameImportOwnerMigration(t, st)

	var gotID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT id
  FROM users
 WHERE email = 'sqlite-import-owner@jobcron.local'
   AND password_hash = 'imported-sqlite-no-login'`).Scan(&gotID); err != nil {
		t.Fatal(err)
	}
	if gotID != canonicalID {
		t.Fatalf("already-renamed owner ID = %d, want %d", gotID, canonicalID)
	}
	var legacyCount int
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT count(*)
  FROM users
 WHERE email = 'sqlite-import-owner@job-scraper.local'`).Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy owner count = %d, want 0", legacyCount)
	}
}

func TestRenameImportOwnerMigrationPreservesRealOwner(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	if _, err := st.SQLDB().ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SQLDB().ExecContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('owner@example.com', 'real-hash', now(), now())`); err != nil {
		t.Fatal(err)
	}

	executeRenameImportOwnerMigration(t, st)

	var count int
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT count(*)
  FROM users
 WHERE email = 'owner@example.com'
   AND password_hash = 'real-hash'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("real owner count = %d, want 1", count)
	}
}

func TestRenameImportOwnerMigrationPreservesLegacyRowWhenCanonicalAddressExists(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	if _, err := st.SQLDB().ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	var legacyID, canonicalID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('sqlite-import-owner@job-scraper.local', 'imported-sqlite-no-login', now(), now())
RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if err := st.SQLDB().QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ('sqlite-import-owner@jobcron.local', 'canonical-owner-hash', now(), now())
RETURNING id`).Scan(&canonicalID); err != nil {
		t.Fatal(err)
	}

	executeRenameImportOwnerMigration(t, st)

	var gotLegacyID, gotCanonicalID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT id
  FROM users
 WHERE email = 'sqlite-import-owner@job-scraper.local'
   AND password_hash = 'imported-sqlite-no-login'`).Scan(&gotLegacyID); err != nil {
		t.Fatal(err)
	}
	if err := st.SQLDB().QueryRowContext(ctx, `
SELECT id
  FROM users
 WHERE email = 'sqlite-import-owner@jobcron.local'
   AND password_hash = 'canonical-owner-hash'`).Scan(&gotCanonicalID); err != nil {
		t.Fatal(err)
	}
	if gotLegacyID != legacyID || gotCanonicalID != canonicalID {
		t.Fatalf("user IDs after collision = legacy %d canonical %d, want legacy %d canonical %d", gotLegacyID, gotCanonicalID, legacyID, canonicalID)
	}
}

func executeRenameImportOwnerMigration(t *testing.T, st *Store) {
	t.Helper()
	sqlBytes, err := postgresMigrationsFS.ReadFile("postgres_migrations/0013_rename_import_owner_email.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SQLDB().ExecContext(context.Background(), string(sqlBytes)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresScrapeRunsStartFinishAndLatestRuntime(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	ctx := context.Background()

	first, err := st.StartScrapeRun(ctx, ScrapeTriggerManual)
	if err != nil {
		t.Fatalf("StartScrapeRun first: %v", err)
	}
	if first.Trigger != ScrapeTriggerManual {
		t.Fatalf("first Trigger = %q, want %q", first.Trigger, ScrapeTriggerManual)
	}
	if first.Status != ScrapeRunStatusRunning {
		t.Fatalf("first Status = %q, want %q", first.Status, ScrapeRunStatusRunning)
	}
	if first.FinishedAt != nil {
		t.Fatalf("first FinishedAt = %v, want nil while running", first.FinishedAt)
	}

	firstResult := ScrapeResult{
		Listed:     5,
		New:        2,
		Refreshed:  1,
		Scored:     4,
		Removed:    1,
		Duplicates: 1,
		Failed:     0,
	}
	if err := st.FinishScrapeRun(ctx, first.ID, firstResult, ScrapeRunStatusSuccess, ""); err != nil {
		t.Fatalf("FinishScrapeRun first: %v", err)
	}
	latest, ok, err := st.LatestScrapeRun(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun after first ok=%v err=%v", ok, err)
	}
	if latest.ID != first.ID {
		t.Fatalf("latest ID = %d, want first ID %d", latest.ID, first.ID)
	}
	if latest.Status != ScrapeRunStatusSuccess {
		t.Fatalf("latest Status = %q, want %q", latest.Status, ScrapeRunStatusSuccess)
	}
	if latest.Result != firstResult {
		t.Fatalf("latest Result = %+v, want %+v", latest.Result, firstResult)
	}
	if latest.ErrorSummary != "" {
		t.Fatalf("latest ErrorSummary = %q, want empty", latest.ErrorSummary)
	}
	if latest.FinishedAt == nil {
		t.Fatal("latest FinishedAt = nil, want success finish time")
	}

	second, err := st.StartScrapeRun(ctx, ScrapeTriggerScheduled)
	if err != nil {
		t.Fatalf("StartScrapeRun second: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("second ID = %d, want greater than first ID %d", second.ID, first.ID)
	}
	secondResult := ScrapeResult{
		Listed:     7,
		New:        3,
		Refreshed:  2,
		Scored:     6,
		Removed:    2,
		Duplicates: 0,
		Failed:     1,
	}
	if err := st.FinishScrapeRun(ctx, second.ID, secondResult, ScrapeRunStatusFailure, "jumpit timeout"); err != nil {
		t.Fatalf("FinishScrapeRun second: %v", err)
	}
	latest, ok, err = st.LatestScrapeRun(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun after second ok=%v err=%v", ok, err)
	}
	if latest.ID != second.ID {
		t.Fatalf("latest ID = %d, want second ID %d", latest.ID, second.ID)
	}
	if latest.Trigger != ScrapeTriggerScheduled {
		t.Fatalf("latest Trigger = %q, want %q", latest.Trigger, ScrapeTriggerScheduled)
	}
	if latest.Status != ScrapeRunStatusFailure {
		t.Fatalf("latest Status = %q, want %q", latest.Status, ScrapeRunStatusFailure)
	}
	if latest.Result != secondResult {
		t.Fatalf("latest Result = %+v, want %+v", latest.Result, secondResult)
	}
	if latest.ErrorSummary != "jumpit timeout" {
		t.Fatalf("latest ErrorSummary = %q, want jumpit timeout", latest.ErrorSummary)
	}
	if latest.FinishedAt == nil {
		t.Fatal("latest FinishedAt = nil, want failure finish time")
	}
}
