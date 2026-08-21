package storage

import (
	"context"
	"database/sql"
	"embed"
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

const postgresMigrationAdvisoryLock int64 = 0x4a4f4243524f4e

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
	statements []byte
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
		versions[version] = name
		migrations = append(migrations, postgresMigration{name: name, version: version, statements: statements})
	}
	return migrations, nil
}

func embeddedPostgresMigrationManifest() ([]postgresMigration, error) {
	source, err := fs.Sub(postgresMigrationsFS, "postgres_migrations")
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres migration manifest: %w", err)
	}
	return postgresMigrationManifest(source)
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

func openPostgres(ctx context.Context, databaseURL string, prepare func(context.Context, *sql.DB) error) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: open postgres: %w", err)
	}
	if err := prepare(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dialect: DialectPostgres}, nil
}

func verifyPostgresMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := embeddedPostgresMigrationManifest()
	if err != nil {
		return err
	}
	return verifyPostgresMigrationSet(ctx, db, migrations)
}

func appliedPostgresMigrationVersions(ctx context.Context, db *sql.DB, migrations []postgresMigration) (map[int]struct{}, error) {
	known := make(map[int]struct{}, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, postgresMigrationError("schema_migrations", "verify", err)
	}
	defer rows.Close()
	applied := make(map[int]struct{}, len(migrations))
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, postgresMigrationError("schema_migrations", "verify", err)
		}
		if _, exists := known[version]; !exists {
			return nil, postgresMigrationError("schema_migrations", "unknown-version", fmt.Errorf("unknown version %d", version))
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, postgresMigrationError("schema_migrations", "verify", err)
	}
	return applied, nil
}

func verifyPostgresMigrationSet(ctx context.Context, db *sql.DB, migrations []postgresMigration) error {
	applied, err := appliedPostgresMigrationVersions(ctx, db, migrations)
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

func migratePostgres(ctx context.Context, db *sql.DB) error {
	migrations, err := embeddedPostgresMigrationManifest()
	if err != nil {
		return err
	}
	tx, err := beginPostgresMigration(ctx, db, "schema_migrations")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		tx.Rollback()
		return postgresMigrationError("schema_migrations", "prepare", err)
	}
	if err := tx.Commit(); err != nil {
		return postgresMigrationError("schema_migrations", "commit", err)
	}
	if _, err := appliedPostgresMigrationVersions(ctx, db, migrations); err != nil {
		return err
	}
	for _, migration := range migrations {
		tx, err := beginPostgresMigration(ctx, db, migration.name)
		if err != nil {
			return err
		}
		var applied int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, migration.version).Scan(&applied); err == nil {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())`, migration.version); err != nil {
			tx.Rollback()
			return postgresMigrationError(migration.name, "record", err)
		}
		if err := tx.Commit(); err != nil {
			return postgresMigrationError(migration.name, "commit", err)
		}
	}
	return verifyPostgresMigrationSet(ctx, db, migrations)
}

func beginPostgresMigration(ctx context.Context, db *sql.DB, name string) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, postgresMigrationError(name, "begin", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, postgresMigrationAdvisoryLock); err != nil {
		tx.Rollback()
		return nil, postgresMigrationError(name, "lock", err)
	}
	return tx, nil
}
