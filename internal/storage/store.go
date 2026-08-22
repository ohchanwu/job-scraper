package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // pure-Go PostgreSQL driver, registered as "pgx"
	_ "modernc.org/sqlite"             // pure-Go SQLite driver, registered as "sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed postgres_migrations/*.sql
var postgresMigrationsFS embed.FS

const (
	postgresMigrationAdvisoryLock int64 = 0x4a4f4243524f4e
	pinnedPostgresMigrationTree         = "4650d3f225f26eb33cb67e02290606983c40c241"
)

// PostgresMigrationError identifies the embedded migration and phase that
// failed without exposing driver diagnostics or connection coordinates.
type PostgresMigrationError struct {
	Migration string
	Stage     string
	err       error
}

func (e *PostgresMigrationError) Error() string {
	return fmt.Sprintf("storage: postgres migration %q failed during %s", e.Migration, e.Stage)
}

func (e *PostgresMigrationError) Unwrap() error { return e.err }

func postgresMigrationError(migration, stage string, err error) error {
	return &PostgresMigrationError{Migration: migration, Stage: stage, err: err}
}

type postgresMigration struct {
	name       string
	version    int
	digest     string
	statements []byte
}

var pinnedPostgresMigrationDigests = map[string]string{
	"0001_initial.sql":                      "ebdc95e67a88348752df41ba4284f76423dc9a0b1a764c45af65b9e7768c5372",
	"0002_user_state.sql":                   "a9bc41f68f6c2fe3acf4738851e690f1e5648622f2ac9a6893c26746a55f839a",
	"0003_boolean_runtime_columns.sql":      "47dbb61b636411533807e3df341026b531e22eaf98473eda3c55a5835674c013",
	"0004_production_app_tables.sql":        "dcda6c41e6df2350d3f9e65d983502a0593c8fe2f2a2a567ee5c77fe2a7e7469",
	"0005_session_last_seen_at.sql":         "0d88f441f3b285a51bf18f42aa81d09da4a805885609b730c5aac266705cda41",
	"0006_user_scoped_state.sql":            "c097c181baa0baca4b5335bfc1fcf5e00382690d70f6ba0c0cabb267a0890aee",
	"0007_scrape_runs.sql":                  "a9adee02b7a5d7edf08efebe6613c8ea9a757e8dbf8216630a538689144b5e13",
	"0013_rename_import_owner_email.sql":    "28abaeb53f79e9d20244c970b30838775a35a5f3d0a37d7d5ec0e10e9c4ab305",
	"0014_user_ai_credentials.sql":          "5c041ca0c73880eb440aae5660ae05342e4e4afa7be4b31eef1b73916fc7a087",
	"0015_user_scoped_ai_state.sql":         "276ea12c7c458c84896853b67f780d6c1cd940dadd478a910eb3ef1b03023160",
	"0016_local_data_imports.sql":           "f9c572c3e876bb76568a86cc5d7f6795482e7d86731b75687cf1006ab736e857",
	"0017_contextual_dealbreakers.sql":      "18a7eba2022a232471e48441e8f08cd9e51dbb415524a50d1ddde91e5e81a40f",
	"0018_multi_user_accounts.sql":          "1fa379ef0b47da66fa2929a256a9b7621167970332beb17fe655040a10d18328",
	"0019_dealbreaker_match_provenance.sql": "014d8266b6dee0fa40b4f331fbbe3edc2362ee05c82dce8f2596c001751c4b72",
}

func postgresMigrationManifest(source fs.FS) ([]postgresMigration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("storage: read postgres migration manifest: %w", err)
	}
	migrations := make([]postgresMigration, 0, len(entries))
	versions := make(map[int]string, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("storage: inspect postgres migration %q: %w", name, err)
		}
		if !info.Mode().IsRegular() || len(name) <= len("0000_.sql") || name[4] != '_' || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("storage: postgres migration manifest entry %q must be a regular NNNN_name.sql file", name)
		}
		version, err := strconv.Atoi(name[:4])
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("storage: postgres migration %q must start with a positive 4-digit version", name)
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("storage: postgres migrations %q and %q share version %04d", previous, name, version)
		}
		statements, err := fs.ReadFile(source, name)
		if err != nil {
			return nil, fmt.Errorf("storage: read postgres migration %q: %w", name, err)
		}
		sum := sha256.Sum256(statements)
		versions[version] = name
		migrations = append(migrations, postgresMigration{
			name:       name,
			version:    version,
			digest:     hex.EncodeToString(sum[:]),
			statements: statements,
		})
	}
	return migrations, nil
}

func embeddedPostgresMigrationManifest() ([]postgresMigration, error) {
	source, err := fs.Sub(postgresMigrationsFS, "postgres_migrations")
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres migration manifest: %w", err)
	}
	migrations, err := postgresMigrationManifest(source)
	if err != nil {
		return nil, err
	}
	if err := validatePinnedPostgresMigrations(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

func validatePinnedPostgresMigrations(migrations []postgresMigration) error {
	if len(migrations) != len(pinnedPostgresMigrationDigests) {
		return fmt.Errorf("storage: postgres migration manifest does not match pinned file set")
	}
	for _, migration := range migrations {
		expected, exists := pinnedPostgresMigrationDigests[migration.name]
		if !exists || migration.digest != expected {
			return fmt.Errorf("storage: postgres migration %q does not match its pinned digest", migration.name)
		}
	}
	return nil
}

// Store is the jobcron persistence layer: a single concrete handle over
// the configured SQL database, with every repository method hanging off it.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// OpenSQLiteAt opens an explicit legacy SQLite source for the one-time importer
// and isolated compatibility fixtures. Normal application startup never calls
// this function.
func OpenSQLiteAt(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("storage: create db directory: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dialect: DialectSQLite}, nil
}

// OpenPostgres opens a PostgreSQL database URL and verifies that its applied
// migrations exactly match the embedded manifest without issuing DDL.
func OpenPostgres(databaseURL string) (*Store, error) {
	return openPostgres(context.Background(), databaseURL, verifyPostgresMigrations)
}

// OpenPostgresMigrating opens a PostgreSQL database URL and applies pending
// migrations. Production callers must reserve it for operator credentials.
func OpenPostgresMigrating(ctx context.Context, databaseURL string) (*Store, error) {
	return openPostgres(ctx, databaseURL, migratePostgres)
}

// OpenPostgresMigratingWithLegacyBackfill performs the one-time conversion of
// a version-only migration ledger after the operator has audited that the
// previously deployed migration tree exactly matches this binary's manifest.
func OpenPostgresMigratingWithLegacyBackfill(ctx context.Context, databaseURL, auditedMigrationTree string) (*Store, error) {
	if auditedMigrationTree != pinnedPostgresMigrationTree {
		return nil, postgresMigrationError("schema_migrations", "legacy-audit", fmt.Errorf("audited migration tree differs"))
	}
	return openPostgres(ctx, databaseURL, func(ctx context.Context, db *sql.DB, schema, ledger string) error {
		return migratePostgresWithLegacyBackfill(ctx, db, schema, ledger, true)
	})
}

func openPostgres(ctx context.Context, databaseURL string, prepare func(context.Context, *sql.DB, string, string) error) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: open postgres: %w", err)
	}
	schema, ledger, err := postgresMigrationTarget(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := prepare(ctx, db, schema, ledger); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dialect: DialectPostgres}, nil
}

func postgresMigrationTarget(ctx context.Context, db *sql.DB) (string, string, error) {
	var schema string
	if err := db.QueryRowContext(ctx, `
SELECT schema_name
  FROM pg_catalog.unnest(pg_catalog.current_schemas(false))
       WITH ORDINALITY AS schemas(schema_name, position)
 WHERE pg_catalog.substr(schema_name, 1, 3) <> 'pg_'
   AND schema_name <> 'information_schema'
 ORDER BY position
 LIMIT 1`).Scan(&schema); err != nil {
		return "", "", postgresMigrationError("schema_migrations", "schema", err)
	}
	if schema == "" {
		return "", "", postgresMigrationError("schema_migrations", "schema", fmt.Errorf("current schema is unset"))
	}
	quotedSchema := quotePostgresIdentifier(schema)
	return quotedSchema, quotedSchema + `.schema_migrations`, nil
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func verifyPostgresMigrations(ctx context.Context, db *sql.DB, _ string, ledger string) error {
	migrations, err := embeddedPostgresMigrationManifest()
	if err != nil {
		return err
	}
	return verifyPostgresMigrationSet(ctx, db, ledger, migrations)
}

func appliedPostgresMigrationVersions(ctx context.Context, db *sql.DB, ledger string, migrations []postgresMigration) (map[int]struct{}, error) {
	known := make(map[int]postgresMigration, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = migration
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT version, name, sha256 FROM %s`, ledger))
	if err != nil {
		return nil, postgresMigrationError("schema_migrations", "verify", err)
	}
	defer rows.Close()
	applied := make(map[int]struct{}, len(migrations))
	for rows.Next() {
		var version int
		var name, digest string
		if err := rows.Scan(&version, &name, &digest); err != nil {
			return nil, postgresMigrationError("schema_migrations", "verify", err)
		}
		migration, exists := known[version]
		if !exists {
			return nil, postgresMigrationError("schema_migrations", "unknown-version", fmt.Errorf("unknown version %d", version))
		}
		if name != migration.name || digest != migration.digest {
			return nil, postgresMigrationError(migration.name, "identity-mismatch", fmt.Errorf("stored migration identity differs"))
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, postgresMigrationError("schema_migrations", "verify", err)
	}
	return applied, nil
}

func verifyPostgresMigrationSet(ctx context.Context, db *sql.DB, ledger string, migrations []postgresMigration) error {
	applied, err := appliedPostgresMigrationVersions(ctx, db, ledger, migrations)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if _, exists := applied[migration.version]; !exists {
			return fmt.Errorf("storage: pending postgres migration %q", migration.name)
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Dialect returns the SQL backend used by this store.
func (s *Store) Dialect() Dialect { return s.dialect }

// SQLDB returns the underlying database handle for command-line maintenance
// tools that need table-level operations outside the app's runtime methods.
func (s *Store) SQLDB() *sql.DB { return s.db }

// migrate applies every embedded migration whose version is newer than the
// database's current PRAGMA user_version, in ascending order. Each migration
// file is named NNNN_description.sql, where NNNN is its version.
func migrate(db *sql.DB) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("storage: read migrations: %w", err)
	}
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("storage: read schema version: %w", err)
	}
	for _, e := range entries {
		version, err := strconv.Atoi(e.Name()[:4])
		if err != nil {
			return fmt.Errorf("storage: migration %q: name must start with a 4-digit version", e.Name())
		}
		if version <= current {
			continue
		}
		stmts, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("storage: read migration %q: %w", e.Name(), err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("storage: begin migration %q: %w", e.Name(), err)
		}
		if _, err := tx.Exec(string(stmts)); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: apply migration %q: %w", e.Name(), err)
		}
		// PRAGMA user_version cannot be parameterized.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: bump schema version for %q: %w", e.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage: commit migration %q: %w", e.Name(), err)
		}
	}
	return nil
}

func migratePostgres(ctx context.Context, db *sql.DB, schema, ledger string) error {
	return migratePostgresWithLegacyBackfill(ctx, db, schema, ledger, false)
}

func migratePostgresWithLegacyBackfill(ctx context.Context, db *sql.DB, schema, ledger string, allowLegacyBackfill bool) error {
	migrations, err := embeddedPostgresMigrationManifest()
	if err != nil {
		return err
	}
	tx, err := beginPostgresMigration(ctx, db, "schema_migrations", schema)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	version    integer PRIMARY KEY,
	name       text NOT NULL,
	sha256     text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`, ledger)); err != nil {
		tx.Rollback()
		return postgresMigrationError("schema_migrations", "prepare", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
ALTER TABLE %s
    ADD COLUMN IF NOT EXISTS name text,
    ADD COLUMN IF NOT EXISTS sha256 text`, ledger)); err != nil {
		tx.Rollback()
		return postgresMigrationError("schema_migrations", "prepare", err)
	}
	if err := backfillPostgresMigrationLedger(ctx, tx, ledger, migrations, allowLegacyBackfill); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
ALTER TABLE %s
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN sha256 SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS schema_migrations_name_idx ON %s(name)`, ledger, ledger)); err != nil {
		tx.Rollback()
		return postgresMigrationError("schema_migrations", "prepare", err)
	}
	if err := tx.Commit(); err != nil {
		return postgresMigrationError("schema_migrations", "commit", err)
	}
	if _, err := appliedPostgresMigrationVersions(ctx, db, ledger, migrations); err != nil {
		return err
	}
	for _, migration := range migrations {
		tx, err := beginPostgresMigration(ctx, db, migration.name, schema)
		if err != nil {
			return err
		}
		var appliedName, appliedDigest string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT name, sha256 FROM %s WHERE version = $1`, ledger), migration.version).Scan(&appliedName, &appliedDigest); err == nil {
			if appliedName != migration.name || appliedDigest != migration.digest {
				tx.Rollback()
				return postgresMigrationError(migration.name, "identity-mismatch", fmt.Errorf("stored migration identity differs"))
			}
			tx.Rollback()
			continue
		} else if err != sql.ErrNoRows {
			tx.Rollback()
			return postgresMigrationError(migration.name, "check", err)
		}
		if _, err := tx.ExecContext(ctx, string(migration.statements)); err != nil {
			tx.Rollback()
			return postgresMigrationError(migration.name, "apply", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (version, name, sha256, applied_at) VALUES ($1, $2, $3, now())`, ledger), migration.version, migration.name, migration.digest); err != nil {
			tx.Rollback()
			return postgresMigrationError(migration.name, "record", err)
		}
		if err := tx.Commit(); err != nil {
			return postgresMigrationError(migration.name, "commit", err)
		}
	}
	return verifyPostgresMigrationSet(ctx, db, ledger, migrations)
}

func backfillPostgresMigrationLedger(ctx context.Context, tx *sql.Tx, ledger string, migrations []postgresMigration, allow bool) error {
	known := make(map[int]postgresMigration, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = migration
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT version, name, sha256 FROM %s ORDER BY version`, ledger))
	if err != nil {
		return postgresMigrationError("schema_migrations", "verify", err)
	}
	type legacyRow struct {
		version int
		name    sql.NullString
		digest  sql.NullString
	}
	var legacy []legacyRow
	for rows.Next() {
		var row legacyRow
		if err := rows.Scan(&row.version, &row.name, &row.digest); err != nil {
			rows.Close()
			return postgresMigrationError("schema_migrations", "verify", err)
		}
		migration, exists := known[row.version]
		if !exists {
			rows.Close()
			return postgresMigrationError("schema_migrations", "unknown-version", fmt.Errorf("unknown version %d", row.version))
		}
		if row.name.Valid != row.digest.Valid || (row.name.Valid && (row.name.String != migration.name || row.digest.String != migration.digest)) {
			rows.Close()
			return postgresMigrationError(migration.name, "identity-mismatch", fmt.Errorf("stored migration identity differs"))
		}
		if !row.name.Valid {
			legacy = append(legacy, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return postgresMigrationError("schema_migrations", "verify", err)
	}
	if err := rows.Close(); err != nil {
		return postgresMigrationError("schema_migrations", "verify", err)
	}
	if len(legacy) > 0 && !allow {
		return postgresMigrationError("schema_migrations", "legacy-backfill-required", fmt.Errorf("version-only migration ledger requires an audited backfill"))
	}
	for _, row := range legacy {
		migration := known[row.version]
		result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET name = $2, sha256 = $3 WHERE version = $1 AND name IS NULL AND sha256 IS NULL`, ledger), row.version, migration.name, migration.digest)
		if err != nil {
			return postgresMigrationError(migration.name, "backfill", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return postgresMigrationError(migration.name, "backfill", fmt.Errorf("migration ledger changed during backfill"))
		}
	}
	return nil
}

func beginPostgresMigration(ctx context.Context, db *sql.DB, name, schema string) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, postgresMigrationError(name, "begin", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, postgresMigrationAdvisoryLock); err != nil {
		tx.Rollback()
		return nil, postgresMigrationError(name, "lock", err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema+`, pg_catalog`); err != nil {
		tx.Rollback()
		return nil, postgresMigrationError(name, "schema", err)
	}
	return tx, nil
}
