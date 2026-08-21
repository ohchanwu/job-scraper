package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/scraper"
)

// requiredObjects are the tables / virtual tables the migrations must create.
var requiredObjects = []string{
	"postings", "postings_fts", "profile", "scores", "bookmarks",
	"ai_extractions", "ai_scores", "ai_usage", // migration 0008 (BYOK AI v2.0)
	"users", "sessions", // migration 0011 (production auth)
	"scrape_runs", // migration 0012 (scrape run history)
}

// newTestStore opens a fresh, migrated database in a temp directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenSQLiteAt(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("OpenSQLiteAt: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// samplePosting is a fully-populated posting used across storage tests.
func samplePosting() scraper.Posting {
	pub := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	closed := time.Date(2026, 5, 30, 23, 59, 59, 0, time.UTC)
	seen := time.Date(2026, 5, 21, 8, 0, 0, 0, time.UTC)
	edu := 4
	return scraper.Posting{
		Source:          "jumpit",
		SourcePostingID: "53688979",
		URL:             "https://jumpit.saramin.co.kr/position/53688979",
		Title:           "B2B 프로젝트 개발팀 신입",
		Company:         "에스피에이치",
		Location:        "서울 마포구",
		Newcomer:        true,
		MinCareer:       0,
		MaxCareer:       0,
		CareerLevel:     "신입",
		Education:       &edu,
		EducationName:   "대학교졸업(4년) 이상",
		StackTags:       []string{"Git", "Java", "React", "AI/인공지능"},
		Tags:            []scraper.Tag{{ID: "com_143", Name: "평균연봉 6,000 이상", Category: "salary"}},
		Description:     "서버 개발자를 찾습니다\n\n복지: 재택 가능",
		RawJSON:         `{"id":53688979}`,
		PublishedAt:     &pub,
		ClosedAt:        &closed,
		AlwaysOpen:      false,
		FirstSeenAt:     seen,
		LastSeenAt:      seen,
	}
}

// assertPosting compares two postings, using time.Equal for time fields
// (reflect.DeepEqual is fragile on time.Time across a DB round-trip).
func assertPosting(t *testing.T, got, want scraper.Posting) {
	t.Helper()
	eqPtr := func(name string, a, b *time.Time) {
		switch {
		case a == nil && b == nil:
		case a == nil || b == nil, !a.Equal(*b):
			t.Errorf("%s: got %v, want %v", name, a, b)
		}
	}
	eqPtr("PublishedAt", got.PublishedAt, want.PublishedAt)
	eqPtr("ClosedAt", got.ClosedAt, want.ClosedAt)
	if !got.FirstSeenAt.Equal(want.FirstSeenAt) {
		t.Errorf("FirstSeenAt: got %v, want %v", got.FirstSeenAt, want.FirstSeenAt)
	}
	if !got.LastSeenAt.Equal(want.LastSeenAt) {
		t.Errorf("LastSeenAt: got %v, want %v", got.LastSeenAt, want.LastSeenAt)
	}
	got.PublishedAt, want.PublishedAt = nil, nil
	got.ClosedAt, want.ClosedAt = nil, nil
	got.FirstSeenAt, want.FirstSeenAt = time.Time{}, time.Time{}
	got.LastSeenAt, want.LastSeenAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("non-time fields mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestOpenSQLiteAtAppliesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")

	st, err := OpenSQLiteAt(path)
	if err != nil {
		t.Fatalf("OpenSQLiteAt: %v", err)
	}
	for _, name := range requiredObjects {
		var got string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE name = ?`, name).Scan(&got)
		if err != nil {
			t.Errorf("object %q not found after OpenAt: %v", name, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenSQLiteAtIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")

	st1, err := OpenSQLiteAt(path)
	if err != nil {
		t.Fatalf("first OpenAt: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Re-opening an already-migrated database must succeed without
	// re-applying migration 0001.
	st2, err := OpenSQLiteAt(path)
	if err != nil {
		t.Fatalf("second OpenAt: %v", err)
	}
	defer st2.Close()
}

func TestOpenSQLiteAtUsesSQLiteDialect(t *testing.T) {
	st := newTestStore(t)
	if st.Dialect() != DialectSQLite {
		t.Fatalf("Dialect = %v, want sqlite", st.Dialect())
	}
}

func TestDialectPlaceholder(t *testing.T) {
	if got := DialectSQLite.Placeholder(3); got != "?" {
		t.Fatalf("sqlite placeholder = %q, want ?", got)
	}
	if got := DialectPostgres.Placeholder(3); got != "$3" {
		t.Fatalf("postgres placeholder = %q, want $3", got)
	}
}

func TestOpenPostgresInvalidURLReturnsOpenError(t *testing.T) {
	_, err := OpenPostgres("://not-a-valid-postgres-url")
	if err == nil {
		t.Fatal("OpenPostgres succeeded with invalid URL")
	}
}

func TestOpenPostgresMigratingAppliesSchema(t *testing.T) {
	st, schema := newPostgresTestStoreWithSchema(t)
	if st.Dialect() != DialectPostgres {
		t.Fatalf("Dialect = %v, want postgres", st.Dialect())
	}
	for _, name := range []string{"postings", "profile", "scores", "bookmarks", "not_interested", "ai_extractions", "ai_scores", "ai_usage", "scrape_runs"} {
		var got string
		err := st.db.QueryRow(
			`SELECT table_name FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`, schema, name).Scan(&got)
		if err != nil {
			t.Errorf("table %q not found after OpenPostgres: %v", name, err)
		}
	}
	var version int
	if err := st.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil && err != sql.ErrNoRows {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if version != 19 {
		t.Fatalf("schema version = %d, want 19", version)
	}
}

func TestOpenPostgresVerifiesMigratedSchemaWithoutCreatePrivilege(t *testing.T) {
	databaseURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := fmt.Sprintf("test_runtime_%d", rand.Uint64())
	role := fmt.Sprintf("runtime_%d", rand.Uint64())
	const password = "runtime-test-password"
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create postgres test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_, _ = admin.Exec(`DROP ROLE IF EXISTS ` + role)
		_ = admin.Close()
	})
	schemaURL := databaseURLWithSearchPath(databaseURL, schema)
	if _, err := OpenPostgres(schemaURL); err == nil {
		t.Fatal("OpenPostgres accepted a schema with pending migrations")
	}
	var runtimeCreatedMigrationTable bool
	if err := admin.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, schema+`.schema_migrations`).Scan(&runtimeCreatedMigrationTable); err != nil {
		t.Fatalf("query unexpected runtime DDL: %v", err)
	}
	if runtimeCreatedMigrationTable {
		t.Fatal("OpenPostgres created schema_migrations")
	}
	migrating, err := OpenPostgresMigrating(context.Background(), schemaURL)
	if err != nil {
		t.Fatalf("OpenPostgresMigrating: %v", err)
	}
	if err := migrating.Close(); err != nil {
		t.Fatalf("close migrating store: %v", err)
	}
	if _, err := admin.Exec(`CREATE ROLE ` + role + ` LOGIN PASSWORD '` + password + `'`); err != nil {
		t.Fatalf("create runtime role: %v", err)
	}
	if _, err := admin.Exec(`GRANT USAGE ON SCHEMA ` + schema + ` TO ` + role + `;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` + schema + ` TO ` + role + `;
REVOKE INSERT, UPDATE, DELETE ON TABLE ` + schema + `.schema_migrations FROM ` + role + `;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ` + schema + ` TO ` + role); err != nil {
		t.Fatalf("grant runtime privileges: %v", err)
	}
	parsed, err := url.Parse(databaseURLWithSearchPath(databaseURL, schema))
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	parsed.User = url.UserPassword(role, password)
	runtime, err := OpenPostgres(parsed.String())
	if err != nil {
		t.Fatalf("OpenPostgres with DML-only role: %v", err)
	}
	if _, err := runtime.SQLDB().Exec(`INSERT INTO schema_migrations (version) VALUES (9999)`); err == nil {
		t.Fatal("runtime role inserted a forged migration version")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime store: %v", err)
	}
	var canCreate bool
	if err := admin.QueryRow(`SELECT has_schema_privilege($1, $2, 'CREATE')`, role, schema).Scan(&canCreate); err != nil {
		t.Fatalf("query runtime CREATE privilege: %v", err)
	}
	if canCreate {
		t.Fatal("runtime role unexpectedly has schema CREATE")
	}
}

func TestOpenPostgresRejectsUnknownAppliedMigration(t *testing.T) {
	databaseURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := fmt.Sprintf("test_ahead_%d", rand.Uint64())
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create postgres test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	schemaURL := databaseURLWithSearchPath(databaseURL, schema)
	migrating, err := OpenPostgresMigrating(context.Background(), schemaURL)
	if err != nil {
		t.Fatalf("OpenPostgresMigrating: %v", err)
	}
	if err := migrating.Close(); err != nil {
		t.Fatalf("close migrating store: %v", err)
	}
	if _, err := admin.Exec(`INSERT INTO ` + schema + `.schema_migrations (version) VALUES (9999)`); err != nil {
		t.Fatalf("insert future migration marker: %v", err)
	}
	for name, open := range map[string]func() (*Store, error){
		"runtime":  func() (*Store, error) { return OpenPostgres(schemaURL) },
		"operator": func() (*Store, error) { return OpenPostgresMigrating(context.Background(), schemaURL) },
	} {
		t.Run(name, func(t *testing.T) {
			st, err := open()
			if st != nil {
				_ = st.Close()
			}
			var migrationErr *PostgresMigrationError
			if !errors.As(err, &migrationErr) || migrationErr.Stage != "unknown-version" {
				t.Fatalf("open error = %v, want unknown-version migration error", err)
			}
		})
	}
}

func TestPostgresMigrationManifestRejectsInvalidEntries(t *testing.T) {
	for name, source := range map[string]fstest.MapFS{
		"short name": {
			"x": &fstest.MapFile{Data: []byte("SELECT 1")},
		},
		"non-file": {
			"0001_directory/child.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		},
		"duplicate version": {
			"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1")},
			"0001_second.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := postgresMigrationManifest(source); err == nil {
				t.Fatal("postgresMigrationManifest accepted invalid entries")
			}
		})
	}
}

func TestOpenPostgresMigratingHonorsContextWhileWaitingForAdvisoryLock(t *testing.T) {
	databaseURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := fmt.Sprintf("test_lock_%d", rand.Uint64())
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create postgres test schema: %v", err)
	}
	blocker, err := admin.Begin()
	if err != nil {
		admin.Close()
		t.Fatalf("begin advisory-lock blocker: %v", err)
	}
	if _, err := blocker.Exec(`SELECT pg_advisory_xact_lock($1)`, postgresMigrationAdvisoryLock); err != nil {
		_ = blocker.Rollback()
		admin.Close()
		t.Fatalf("hold migration advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_ = blocker.Rollback()
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = OpenPostgresMigrating(ctx, databaseURLWithSearchPath(databaseURL, schema))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("OpenPostgresMigrating error = %v, want context deadline", err)
	}
	var migrationErr *PostgresMigrationError
	if !errors.As(err, &migrationErr) {
		t.Fatalf("OpenPostgresMigrating error type = %T, want PostgresMigrationError", err)
	}
	if migrationErr.Migration != "schema_migrations" || migrationErr.Stage != "lock" {
		t.Fatalf("migration failure = %q/%q, want schema_migrations/lock", migrationErr.Migration, migrationErr.Stage)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("migration cancellation took %s", elapsed)
	}
}

func TestPostgresRuntimeStorageMethods(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	user, err := st.CreateOwnerUser(ctx, "runtime-owner@example.test", "hash")
	if err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}

	p := samplePosting()
	p.SourcePostingID = fmt.Sprintf("pg-runtime-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	id, isNew, err := st.UpsertPosting(ctx, p)
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	if !isNew {
		t.Fatal("isNew = false, want true")
	}
	p.ID = id
	got, ok, err := st.PostingByID(ctx, id)
	if err != nil || !ok {
		t.Fatalf("PostingByID: ok=%v err=%v", ok, err)
	}
	assertPosting(t, got, p)
	if postings, err := st.AllPostings(ctx); err != nil || len(postings) == 0 {
		t.Fatalf("AllPostings len=%d err=%v", len(postings), err)
	}
	if postings, err := st.CanonicalPostings(ctx); err != nil || len(postings) == 0 {
		t.Fatalf("CanonicalPostings len=%d err=%v", len(postings), err)
	}
	if ids, err := st.KnownSourceIDs(ctx, p.Source); err != nil || !ids[p.SourcePostingID] {
		t.Fatalf("KnownSourceIDs has source posting = %v err=%v", ids[p.SourcePostingID], err)
	}
	if seen, err := st.SeenDetail(ctx, p.Source); err != nil || seen[p.SourcePostingID].ID != id {
		t.Fatalf("SeenDetail id=%d err=%v", seen[p.SourcePostingID].ID, err)
	}

	profileInput := fmt.Sprintf(`{"stacks":["Go"],"run":%d}`, time.Now().UnixNano())
	hash, changed, err := st.SaveProfile(ctx, profileInput)
	if err != nil || !changed {
		t.Fatalf("SaveProfile hash=%q changed=%v err=%v", hash, changed, err)
	}
	profileJSON, profileHash, ok, err := st.Profile(ctx)
	if err != nil || !ok || profileJSON != profileInput || profileHash != hash {
		t.Fatalf("Profile json=%q hash=%q ok=%v err=%v", profileJSON, profileHash, ok, err)
	}

	when := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	if err := st.SetBookmark(ctx, id, when); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	if bookmarked, err := st.IsBookmarked(ctx, id); err != nil || !bookmarked {
		t.Fatalf("IsBookmarked=%v err=%v", bookmarked, err)
	}
	if ids, err := st.BookmarkedIDs(ctx); err != nil || !ids[id] {
		t.Fatalf("BookmarkedIDs has id=%v err=%v", ids[id], err)
	}
	if postings, err := st.BookmarkedPostings(ctx); err != nil || len(postings) == 0 {
		t.Fatalf("BookmarkedPostings len=%d err=%v", len(postings), err)
	}
	if err := st.ClearBookmark(ctx, id); err != nil {
		t.Fatalf("ClearBookmark: %v", err)
	}

	if err := st.SetNotInterested(ctx, id, when); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}
	if muted, err := st.IsNotInterested(ctx, id); err != nil || !muted {
		t.Fatalf("IsNotInterested=%v err=%v", muted, err)
	}
	if ids, err := st.NotInterestedIDs(ctx); err != nil || !ids[id] {
		t.Fatalf("NotInterestedIDs has id=%v err=%v", ids[id], err)
	}
	if postings, err := st.NotInterestedPostings(ctx); err != nil || len(postings) == 0 {
		t.Fatalf("NotInterestedPostings len=%d err=%v", len(postings), err)
	}
	if err := st.ClearNotInterested(ctx, id); err != nil {
		t.Fatalf("ClearNotInterested: %v", err)
	}

	sc := Score{PostingID: id, ProfileHash: hash, Total: 88, BreakdownJSON: `[]`, ComputedAt: when}
	if err := st.UpsertScore(ctx, sc); err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}
	if got, ok, err := st.ScoreByPostingID(ctx, id); err != nil || !ok || got.Total != sc.Total {
		t.Fatalf("ScoreByPostingID total=%d ok=%v err=%v", got.Total, ok, err)
	}
	if scores, err := st.ScoresByPostingID(ctx); err != nil || scores[id].Total != sc.Total {
		t.Fatalf("ScoresByPostingID total=%d err=%v", scores[id].Total, err)
	}

	maxCareer := 3
	ext := ai.Extraction{MinCareer: 1, MaxCareer: &maxCareer, Newcomer: true, EducationEnum: "bachelor", CareerEvidence: "evidence"}
	if err := st.UpsertAIExtraction(ctx, id, "content", "v1", ext, when); err != nil {
		t.Fatalf("UpsertAIExtraction: %v", err)
	}
	if got, ok, err := st.AIExtraction(ctx, id, "content", "v1"); err != nil || !ok || got.MinCareer != ext.MinCareer {
		t.Fatalf("AIExtraction min=%d ok=%v err=%v", got.MinCareer, ok, err)
	}
	if got, err := st.AIExtractionsByPostingID(ctx, map[int64]string{id: "content"}, "v1"); err != nil || got[id].MinCareer != ext.MinCareer {
		t.Fatalf("AIExtractionsByPostingID min=%d err=%v", got[id].MinCareer, err)
	}

	delta := ai.Delta{Items: []ai.DeltaItem{{Signal: "Go", Kind: "positive", Delta: 4, Evidence: "Go", MatchedGoal: "Go"}}, NetDelta: 4}
	if err := st.UpsertAIScore(ctx, user.ID, id, "input", "v1", delta, when); err != nil {
		t.Fatalf("UpsertAIScore: %v", err)
	}
	if got, ok, err := st.AIScore(ctx, user.ID, id, "input", "v1"); err != nil || !ok || got.NetDelta != delta.NetDelta {
		t.Fatalf("AIScore net=%d ok=%v err=%v", got.NetDelta, ok, err)
	}
	if got, err := st.AIScoresByPostingID(ctx, user.ID, "input", "v1"); err != nil || got[id].NetDelta != delta.NetDelta {
		t.Fatalf("AIScoresByPostingID net=%d err=%v", got[id].NetDelta, err)
	}
	if got, err := st.LatestAIScoresByPostingID(ctx, user.ID, "v1"); err != nil || got[id].NetDelta != delta.NetDelta {
		t.Fatalf("LatestAIScoresByPostingID net=%d err=%v", got[id].NetDelta, err)
	}
	if got, err := st.LatestAIScoresAnyVersionByPostingID(ctx, user.ID); err != nil || got[id].NetDelta != delta.NetDelta {
		t.Fatalf("LatestAIScoresAnyVersionByPostingID net=%d err=%v", got[id].NetDelta, err)
	}
	if err := st.AddAIUsage(ctx, user.ID, "2026-07-09", 10, 3); err != nil {
		t.Fatalf("AddAIUsage: %v", err)
	}
	if in, out, err := st.AIUsageForDay(ctx, user.ID, "2026-07-09"); err != nil || in != 10 || out != 3 {
		t.Fatalf("AIUsageForDay in=%d out=%d err=%v", in, out, err)
	}
}

func newPostgresTestStore(t *testing.T) *Store {
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
	st, err := OpenPostgresMigrating(context.Background(), databaseURLWithSearchPath(databaseURL, schema))
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newPostgresTestStoreWithSchema(t *testing.T) (*Store, string) {
	t.Helper()
	st := newPostgresTestStore(t)
	var schema string
	if err := st.db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	return st, schema
}

var nonSchemaChars = regexp.MustCompile(`[^a-z0-9_]`)

func postgresTestSchemaName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "_")
	name = nonSchemaChars.ReplaceAllString(name, "_")
	return fmt.Sprintf("test_%s_%d_%d", name, time.Now().UnixNano(), rand.Uint64())
}

func databaseURLWithSearchPath(databaseURL, schema string) string {
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	return databaseURL + separator + "search_path=" + schema
}

func TestUpsertPostingInsertsNewPosting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	in := samplePosting()

	id, isNew, err := st.UpsertPosting(ctx, in)
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	if !isNew {
		t.Errorf("isNew = false, want true for a brand-new posting")
	}
	if id <= 0 {
		t.Fatalf("id = %d, want a positive row id", id)
	}

	got, ok, err := st.PostingByID(ctx, id)
	if err != nil {
		t.Fatalf("PostingByID: %v", err)
	}
	if !ok {
		t.Fatalf("PostingByID: posting %d not found", id)
	}

	want := in
	want.ID = id
	assertPosting(t, got, want)
}

func TestUpsertPostingUpdatesLastSeenOnConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	first := samplePosting()
	id1, isNew, err := st.UpsertPosting(ctx, first)
	if err != nil || !isNew {
		t.Fatalf("first UpsertPosting: id=%d isNew=%v err=%v", id1, isNew, err)
	}

	// The same posting (same source + source_posting_id) seen again on a
	// later scrape run, carrying a later last_seen_at.
	again := samplePosting()
	later := first.LastSeenAt.Add(48 * time.Hour)
	again.FirstSeenAt = later
	again.LastSeenAt = later

	id2, isNew, err := st.UpsertPosting(ctx, again)
	if err != nil {
		t.Fatalf("second UpsertPosting: %v", err)
	}
	if isNew {
		t.Errorf("isNew = true, want false for an already-seen posting")
	}
	if id2 != id1 {
		t.Errorf("id = %d, want %d (same posting, not a duplicate)", id2, id1)
	}

	got, ok, err := st.PostingByID(ctx, id1)
	if err != nil || !ok {
		t.Fatalf("PostingByID: ok=%v err=%v", ok, err)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("LastSeenAt = %v, want %v (must advance)", got.LastSeenAt, later)
	}
	if !got.FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Errorf("FirstSeenAt = %v, want %v (must not change)", got.FirstSeenAt, first.FirstSeenAt)
	}

	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM postings`).Scan(&count); err != nil {
		t.Fatalf("count postings: %v", err)
	}
	if count != 1 {
		t.Errorf("posting count = %d, want 1 (conflict updated, not duplicated)", count)
	}
}

func TestUpsertPostingRefreshesListingFieldsOnConflict(t *testing.T) {
	// The 당근 URL repair landed as a one-shot migration in 0004 because
	// UpsertPosting used to only advance last_seen_at on the already-seen
	// path. This test guards the structural fix that replaced the migration:
	// the already-seen branch now refreshes url/title/company/location from
	// the fresh listing every scrape, so a source changing any of those
	// fields propagates without needing a migration.
	st := newTestStore(t)
	ctx := context.Background()

	first := samplePosting()
	first.URL = "https://about.daangn.com?gh_jid=123"
	first.Title = "Backend Engineer"
	first.Company = "당근"
	first.Location = "서울 관악구"
	id1, isNew, err := st.UpsertPosting(ctx, first)
	if err != nil || !isNew {
		t.Fatalf("first UpsertPosting: id=%d isNew=%v err=%v", id1, isNew, err)
	}

	// Same posting on a later scrape — listing now reports a corrected URL
	// (scraper bug fix) and a slightly reworded title/company/location.
	again := samplePosting()
	again.URL = "https://careers.daangn.com/jobs/role/123/"
	again.Title = "Backend Engineer (Platform)"
	again.Company = "당근마켓"
	again.Location = "서울 관악구 봉천동"
	later := first.LastSeenAt.Add(24 * time.Hour)
	again.FirstSeenAt = later
	again.LastSeenAt = later
	id2, isNew, err := st.UpsertPosting(ctx, again)
	if err != nil {
		t.Fatalf("second UpsertPosting: %v", err)
	}
	if isNew {
		t.Errorf("isNew = true, want false on the already-seen path")
	}
	if id2 != id1 {
		t.Errorf("id = %d, want %d (same posting)", id2, id1)
	}

	got, ok, err := st.PostingByID(ctx, id1)
	if err != nil || !ok {
		t.Fatalf("PostingByID: ok=%v err=%v", ok, err)
	}
	if got.URL != again.URL {
		t.Errorf("URL = %q, want %q (must refresh)", got.URL, again.URL)
	}
	if got.Title != again.Title {
		t.Errorf("Title = %q, want %q (must refresh)", got.Title, again.Title)
	}
	if got.Company != again.Company {
		t.Errorf("Company = %q, want %q (must refresh)", got.Company, again.Company)
	}
	if got.Location != again.Location {
		t.Errorf("Location = %q, want %q (must refresh)", got.Location, again.Location)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("LastSeenAt = %v, want %v (must advance)", got.LastSeenAt, later)
	}
	if !got.FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Errorf("FirstSeenAt = %v, want %v (must NOT change)", got.FirstSeenAt, first.FirstSeenAt)
	}
}

func TestUpsertScoreInsertsScore(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	postingID, _, err := st.UpsertPosting(ctx, samplePosting())
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}

	in := Score{
		PostingID:     postingID,
		ProfileHash:   "abc123def456",
		Total:         85,
		BreakdownJSON: `[{"label":"React","delta":20}]`,
		ComputedAt:    time.Date(2026, 5, 21, 8, 30, 0, 0, time.UTC),
	}
	if err := st.UpsertScore(ctx, in); err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}

	got, ok, err := st.ScoreByPostingID(ctx, postingID)
	if err != nil {
		t.Fatalf("ScoreByPostingID: %v", err)
	}
	if !ok {
		t.Fatalf("ScoreByPostingID: score for posting %d not found", postingID)
	}
	if got.PostingID != in.PostingID || got.ProfileHash != in.ProfileHash ||
		got.Total != in.Total || got.BreakdownJSON != in.BreakdownJSON {
		t.Errorf("score mismatch:\n got = %+v\nwant = %+v", got, in)
	}
	if !got.ComputedAt.Equal(in.ComputedAt) {
		t.Errorf("ComputedAt = %v, want %v", got.ComputedAt, in.ComputedAt)
	}
}

func TestUpsertScoreOverwritesExisting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	postingID, _, err := st.UpsertPosting(ctx, samplePosting())
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}

	base := Score{
		PostingID: postingID, ProfileHash: "hash-one-000", Total: 40,
		BreakdownJSON: `[]`, ComputedAt: time.Date(2026, 5, 21, 8, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertScore(ctx, base); err != nil {
		t.Fatalf("first UpsertScore: %v", err)
	}

	// The posting is re-scored against a different profile.
	updated := Score{
		PostingID: postingID, ProfileHash: "hash-two-000", Total: 90,
		BreakdownJSON: `[{"label":"TS","delta":10}]`,
		ComputedAt:    time.Date(2026, 5, 22, 9, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertScore(ctx, updated); err != nil {
		t.Fatalf("second UpsertScore: %v", err)
	}

	got, ok, err := st.ScoreByPostingID(ctx, postingID)
	if err != nil || !ok {
		t.Fatalf("ScoreByPostingID: ok=%v err=%v", ok, err)
	}
	if got.Total != 90 || got.ProfileHash != "hash-two-000" {
		t.Errorf("score not overwritten: got total=%d hash=%s, want 90/hash-two-000",
			got.Total, got.ProfileHash)
	}

	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM scores`).Scan(&count); err != nil {
		t.Fatalf("count scores: %v", err)
	}
	if count != 1 {
		t.Errorf("score count = %d, want 1 (one score per posting)", count)
	}
}

func TestSaveProfileStoresProfileWithHash(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const profileJSON = `{"career":"신입","stacks":[{"name":"React","weight":20}]}`

	hash, changed, err := st.SaveProfile(ctx, profileJSON)
	if err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true for the first save")
	}
	sum := sha256.Sum256([]byte(profileJSON))
	wantHash := hex.EncodeToString(sum[:])[:12]
	if hash != wantHash {
		t.Errorf("hash = %q, want %q (sha256(canonical_json)[:12])", hash, wantHash)
	}

	gotJSON, gotHash, ok, err := st.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if !ok {
		t.Fatalf("Profile: ok = false, want a saved profile")
	}
	if gotJSON != profileJSON {
		t.Errorf("profile JSON = %q, want %q", gotJSON, profileJSON)
	}
	if gotHash != hash {
		t.Errorf("profile hash = %q, want %q", gotHash, hash)
	}
}

func TestSaveProfileWritesOnlyWhenContentChanges(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const v1 = `{"career":"신입","stacks":[{"name":"React","weight":20}]}`
	const v2 = `{"career":"신입","stacks":[{"name":"Go","weight":30}]}`

	h1, _, err := st.SaveProfile(ctx, v1)
	if err != nil {
		t.Fatalf("save v1: %v", err)
	}

	// Re-saving identical content is a no-op: changed=false keeps scores valid.
	h1again, changed, err := st.SaveProfile(ctx, v1)
	if err != nil {
		t.Fatalf("re-save v1: %v", err)
	}
	if changed {
		t.Errorf("changed = true on identical re-save, want false")
	}
	if h1again != h1 {
		t.Errorf("hash = %q on identical re-save, want %q", h1again, h1)
	}

	// Saving different content persists the change and reports changed=true.
	h2, changed, err := st.SaveProfile(ctx, v2)
	if err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if !changed {
		t.Errorf("changed = false after a real content change, want true")
	}
	if h2 == h1 {
		t.Errorf("hash unchanged (%q) after content change, want a different hash", h2)
	}

	gotJSON, gotHash, ok, err := st.Profile(ctx)
	if err != nil || !ok {
		t.Fatalf("Profile: ok=%v err=%v", ok, err)
	}
	if gotJSON != v2 || gotHash != h2 {
		t.Errorf("stored profile = (%q,%q), want (%q,%q)", gotJSON, gotHash, v2, h2)
	}
}

func TestKnownSourceIDs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p1 := samplePosting()
	p2 := samplePosting()
	p2.SourcePostingID = "99999"
	other := samplePosting()
	other.Source = "wanted"
	other.SourcePostingID = "wanted-1"
	for _, p := range []scraper.Posting{p1, p2, other} {
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}

	ids, err := st.KnownSourceIDs(ctx, "jumpit")
	if err != nil {
		t.Fatalf("KnownSourceIDs: %v", err)
	}
	if len(ids) != 2 || !ids["53688979"] || !ids["99999"] {
		t.Errorf("KnownSourceIDs = %v, want {53688979, 99999}", ids)
	}
	if ids["wanted-1"] {
		t.Error("KnownSourceIDs returned an id belonging to a different source")
	}
}

func TestAllPostings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p2 := samplePosting()
	p2.SourcePostingID = "222"
	for _, p := range []scraper.Posting{samplePosting(), p2} {
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	all, err := st.AllPostings(ctx)
	if err != nil {
		t.Fatalf("AllPostings: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("AllPostings returned %d, want 2", len(all))
	}
}

func TestScoresByPostingID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, _, err := st.UpsertPosting(ctx, samplePosting())
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	in := Score{
		PostingID: id, ProfileHash: "h", Total: 70, BreakdownJSON: "[]",
		ComputedAt: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertScore(ctx, in); err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}
	scores, err := st.ScoresByPostingID(ctx)
	if err != nil {
		t.Fatalf("ScoresByPostingID: %v", err)
	}
	got, ok := scores[id]
	if !ok || got.Total != 70 {
		t.Errorf("scores[%d] = %+v (ok=%v), want total 70", id, got, ok)
	}
}
