package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestMigrationDatabaseURL(t *testing.T) {
	const password = "database secret with :/@"
	got, err := migrationDatabaseURL(
		"postgres://master@127.0.0.1:15432/jobcron?sslmode=require",
		password,
	)
	if err != nil {
		t.Fatalf("migrationDatabaseURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.User.Username() != "master" {
		t.Fatalf("username = %q, want master", parsed.User.Username())
	}
	if gotPassword, ok := parsed.User.Password(); !ok || gotPassword != password {
		t.Fatalf("password round trip ok=%v value=%q", ok, gotPassword)
	}
}

func TestMigrationDatabaseURLRejectsUnsafeInputsWithoutDisclosure(t *testing.T) {
	const secret = "must not be disclosed"
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"embedded password", "postgres://master:embedded@127.0.0.1:15432/jobcron?sslmode=require"},
		{"remote host", "postgres://master@db.example.invalid:5432/jobcron?sslmode=require"},
		{"missing port", "postgres://master@127.0.0.1/jobcron?sslmode=require"},
		{"weak TLS", "postgres://master@127.0.0.1:15432/jobcron?sslmode=disable"},
		{"extra query", "postgres://master@127.0.0.1:15432/jobcron?sslmode=require&application_name=test"},
		{"missing user", "postgres://127.0.0.1:15432/jobcron?sslmode=require"},
		{"missing database", "postgres://master@127.0.0.1:15432/?sslmode=require"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := migrationDatabaseURL(tc.raw, secret)
			if err == nil {
				t.Fatal("migrationDatabaseURL error = nil")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), tc.raw) {
				t.Fatalf("error disclosed private input: %q", err)
			}
		})
	}
}

func TestRunMigrateRequiresPassword(t *testing.T) {
	err := run(context.Background(), []string{
		"migrate",
		"--database-url", "postgres://master@127.0.0.1:15432/jobcron?sslmode=require",
	}, envMap{}, strings.NewReader("\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "database password is required") {
		t.Fatalf("run error = %v, want database password requirement", err)
	}
}

func TestRunMigrateRedactsDatabaseFailure(t *testing.T) {
	const (
		databaseURL = "postgres://master@127.0.0.1:1/jobcron?sslmode=require"
		password    = "private database password"
	)
	var out bytes.Buffer
	err := run(context.Background(), []string{
		"migrate", "--database-url", databaseURL,
	}, envMap{"JOBCRON_DATABASE_PASSWORD": password}, nil, &out)
	if err == nil {
		t.Fatal("run error = nil, want database-open error")
	}
	if got, want := err.Error(), "user: open PostgreSQL database"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), databaseURL) || out.Len() != 0 {
		t.Fatalf("failure disclosed private input: error=%q output=%q", err, out.String())
	}
}

func TestRunCreateOwnerUsesPasswordFromEnv(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := createUserCLITestSchema(t, postgresURL)
	databaseURL := databaseURLWithSearchPath(postgresURL, schema)

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"create-owner",
		"--database-url", databaseURL,
		"--email", "owner@example.com",
	}, envMap{"JOBCRON_OWNER_PASSWORD": "top secret password"}, nil, &out)
	if err != nil {
		t.Fatalf("run create-owner: %v", err)
	}
	if !strings.Contains(out.String(), "created owner user owner@example.com") {
		t.Fatalf("output = %q", out.String())
	}

	st, err := storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres verify: %v", err)
	}
	defer st.Close()
	var encodedHash string
	if err := st.SQLDB().QueryRow(`SELECT password_hash FROM users WHERE email = $1`, "owner@example.com").Scan(&encodedHash); err != nil {
		t.Fatalf("query owner password hash: %v", err)
	}
	ok, err := auth.VerifyPassword(encodedHash, "top secret password")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword ok=%v err=%v", ok, err)
	}
}

func TestRunCreateOwnerFailsWhenOwnerExistsUntilResetPassword(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := createUserCLITestSchema(t, postgresURL)
	databaseURL := databaseURLWithSearchPath(postgresURL, schema)

	env := envMap{"JOBCRON_OWNER_PASSWORD": "first password long"}
	if err := run(context.Background(), []string{
		"create-owner",
		"--database-url", databaseURL,
		"--email", "owner@example.com",
	}, env, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("first create-owner: %v", err)
	}

	err := run(context.Background(), []string{
		"create-owner",
		"--database-url", databaseURL,
		"--email", "owner@example.com",
	}, envMap{"JOBCRON_OWNER_PASSWORD": "second password long"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("second create-owner error = nil, want owner-exists error")
	}
	if !strings.Contains(err.Error(), "owner user already exists") {
		t.Fatalf("second create-owner error = %v", err)
	}

	if err := run(context.Background(), []string{
		"reset-password",
		"--database-url", databaseURL,
		"--email", "owner@example.com",
	}, envMap{"JOBCRON_USER_PASSWORD": "second password long"}, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("reset-password: %v", err)
	}

	st, err := storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres verify: %v", err)
	}
	defer st.Close()
	var encodedHash string
	if err := st.SQLDB().QueryRow(`SELECT password_hash FROM users WHERE email = $1`, "owner@example.com").Scan(&encodedHash); err != nil {
		t.Fatalf("query owner password hash: %v", err)
	}
	ok, err := auth.VerifyPassword(encodedHash, "second password long")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword after reset ok=%v err=%v", ok, err)
	}
}

func TestRunResetPasswordWithWrongEmailFailsWithoutRenamingOwner(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := createUserCLITestSchema(t, postgresURL)
	databaseURL := databaseURLWithSearchPath(postgresURL, schema)

	if err := run(context.Background(), []string{
		"create-owner",
		"--database-url", databaseURL,
		"--email", "owner@example.com",
	}, envMap{"JOBCRON_OWNER_PASSWORD": "first password long"}, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("create-owner: %v", err)
	}

	err := run(context.Background(), []string{
		"reset-password",
		"--database-url", databaseURL,
		"--email", "wrong@example.com",
	}, envMap{"JOBCRON_USER_PASSWORD": "second password long"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("reset-password error = nil, want email mismatch error")
	}
	if !strings.Contains(err.Error(), "user does not exist") {
		t.Fatalf("reset-password error = %v", err)
	}

	st, err := storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres verify: %v", err)
	}
	defer st.Close()
	var email, encodedHash string
	if err := st.SQLDB().QueryRow(`SELECT email, password_hash FROM users`).Scan(&email, &encodedHash); err != nil {
		t.Fatalf("query owner after failed reset: %v", err)
	}
	if email != "owner@example.com" {
		t.Fatalf("owner email = %q, want owner@example.com", email)
	}
	ok, err := auth.VerifyPassword(encodedHash, "first password long")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword original after failed reset ok=%v err=%v", ok, err)
	}
}

func TestRunCreateOwnerRequiresPassword(t *testing.T) {
	err := run(context.Background(), []string{
		"create-owner",
		"--database-url", "postgres://example.invalid/jobs",
		"--email", "owner@example.com",
	}, envMap{}, strings.NewReader("\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("run error = nil, want password required error")
	}
	if !strings.Contains(err.Error(), "owner password is required") {
		t.Fatalf("run error = %v", err)
	}
}

func TestOwnerPasswordReadsFromNonTerminalReader(t *testing.T) {
	var out bytes.Buffer
	password, err := commandPassword(envMap{}, "JOBCRON_OWNER_PASSWORD", "Owner", strings.NewReader("typed password\n"), &out)
	if err != nil {
		t.Fatalf("ownerPassword: %v", err)
	}
	if password != "typed password" {
		t.Fatalf("password = %q", password)
	}
	if out.String() != "Owner password: " {
		t.Fatalf("prompt output = %q", out.String())
	}
}

func TestRunResetPasswordTargetsNormalizedEmailAndRevokesSessions(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	databaseURL := databaseURLWithSearchPath(postgresURL, createUserCLITestSchema(t, postgresURL))
	st, err := storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres seed: %v", err)
	}
	targetHash, _ := auth.HashPassword("old target password")
	otherHash, _ := auth.HashPassword("old other password")
	target, err := st.CreateUser(context.Background(), "target@example.com", targetHash)
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	other, err := st.CreateUser(context.Background(), "other@example.com", otherHash)
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	for i, userID := range []int64{target.ID, target.ID, other.ID} {
		hash := fmt.Sprintf("session-%d-%d", userID, i)
		if err := st.CreateSession(context.Background(), userID, hash, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	var out bytes.Buffer
	err = run(context.Background(), []string{
		"reset-password",
		"--database-url", databaseURL,
		"--email", " TARGET@EXAMPLE.COM ",
	}, envMap{"JOBCRON_USER_PASSWORD": "replacement password"}, nil, &out)
	if err != nil {
		t.Fatalf("run reset-password: %v", err)
	}
	wantOutput := fmt.Sprintf("reset password for target@example.com (user ID %d)\n", target.ID)
	if out.String() != wantOutput {
		t.Fatalf("output = %q, want %q", out.String(), wantOutput)
	}

	st, err = storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres verify: %v", err)
	}
	defer st.Close()
	updated, ok, err := st.UserByID(context.Background(), target.ID)
	if err != nil || !ok {
		t.Fatalf("UserByID target ok=%v err=%v", ok, err)
	}
	if ok, err := auth.VerifyPassword(updated.PasswordHash, "replacement password"); err != nil || !ok {
		t.Fatalf("VerifyPassword replacement ok=%v err=%v", ok, err)
	}
	assertCLIRowCount(t, st, "sessions", target.ID, 0)
	assertCLIRowCount(t, st, "sessions", other.ID, 1)
}

func TestRunResetPasswordRejectsMissingUser(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	databaseURL := databaseURLWithSearchPath(postgresURL, createUserCLITestSchema(t, postgresURL))
	err := run(context.Background(), []string{
		"reset-password",
		"--database-url", databaseURL,
		"--email", "missing@example.com",
	}, envMap{"JOBCRON_USER_PASSWORD": "replacement password"}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "user does not exist") {
		t.Fatalf("reset missing user error = %v", err)
	}
}

func TestRunDeleteUserRequiresExactNormalizedConfirmation(t *testing.T) {
	err := run(context.Background(), []string{
		"delete-user",
		"--database-url", "postgres://example.invalid/jobs",
		"--email", "target@example.com",
		"--confirm-email", "other@example.com",
	}, envMap{}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("delete-user confirmation error = %v", err)
	}
}

func TestRunDeleteUserDeletesOnlyNormalizedTarget(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	databaseURL := databaseURLWithSearchPath(postgresURL, createUserCLITestSchema(t, postgresURL))
	st, err := storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres seed: %v", err)
	}
	target, err := st.CreateUser(context.Background(), "target@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	other, err := st.CreateUser(context.Background(), "other@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	var out bytes.Buffer
	err = run(context.Background(), []string{
		"delete-user",
		"--database-url", databaseURL,
		"--email", " TARGET@EXAMPLE.COM ",
		"--confirm-email", " target@example.com ",
	}, envMap{}, nil, &out)
	if err != nil {
		t.Fatalf("run delete-user: %v", err)
	}
	wantOutput := fmt.Sprintf("deleted user target@example.com (user ID %d)\n", target.ID)
	if out.String() != wantOutput {
		t.Fatalf("output = %q, want %q", out.String(), wantOutput)
	}

	st, err = storage.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres verify: %v", err)
	}
	defer st.Close()
	if _, ok, err := st.UserByID(context.Background(), target.ID); err != nil || ok {
		t.Fatalf("deleted target ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.UserByID(context.Background(), other.ID); err != nil || !ok {
		t.Fatalf("surviving user ok=%v err=%v", ok, err)
	}
}

func TestRunDeleteUserRejectsMissingUser(t *testing.T) {
	postgresURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	databaseURL := databaseURLWithSearchPath(postgresURL, createUserCLITestSchema(t, postgresURL))
	err := run(context.Background(), []string{
		"delete-user",
		"--database-url", databaseURL,
		"--email", "missing@example.com",
		"--confirm-email", "missing@example.com",
	}, envMap{}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "user does not exist") {
		t.Fatalf("delete missing user error = %v", err)
	}
}

func TestRunDeleteUserRequiresFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"database URL", []string{"delete-user", "--email", "a@example.com", "--confirm-email", "a@example.com"}, "--database-url"},
		{"email", []string{"delete-user", "--database-url", "unused", "--confirm-email", "a@example.com"}, "--email"},
		{"confirmation", []string{"delete-user", "--database-url", "unused", "--email", "a@example.com"}, "--confirm-email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, envMap{}, nil, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRunPasswordCommandsRequireFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"migrate database URL", []string{"migrate"}, "--database-url"},
		{"reset database URL", []string{"reset-password", "--email", "a@example.com"}, "--database-url"},
		{"reset email", []string{"reset-password", "--database-url", "unused"}, "--email"},
		{"create database URL", []string{"create-owner", "--email", "a@example.com"}, "--database-url"},
		{"create email", []string{"create-owner", "--database-url", "unused"}, "--email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, envMap{}, nil, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRunPasswordCommandsValidateIdentityBeforeOpeningDatabase(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  envMap
		want string
	}{
		{
			"create email",
			[]string{"create-owner", "--database-url", "unused", "--email", "not-an-email"},
			envMap{"JOBCRON_OWNER_PASSWORD": "valid owner password"},
			"invalid email address",
		},
		{
			"reset email",
			[]string{"reset-password", "--database-url", "unused", "--email", "not-an-email"},
			envMap{"JOBCRON_USER_PASSWORD": "valid reset password"},
			"invalid email address",
		},
		{
			"create password",
			[]string{"create-owner", "--database-url", "unused", "--email", "owner@example.com"},
			envMap{"JOBCRON_OWNER_PASSWORD": "short"},
			"at least 15 characters",
		},
		{
			"reset password",
			[]string{"reset-password", "--database-url", "unused", "--email", "user@example.com"},
			envMap{"JOBCRON_USER_PASSWORD": "short"},
			"at least 15 characters",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, tc.env, nil, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRunDatabaseOpenErrorsDoNotDiscloseConnectionCoordinates(t *testing.T) {
	const (
		coordinate = "private-coordinate.invalid"
		wantError  = "user: open PostgreSQL database"
	)
	databaseURL := "postgres://operator@" + coordinate + ":notaport/jobs"
	tests := []struct {
		name string
		args []string
		env  envMap
	}{
		{
			name: "create owner",
			args: []string{"create-owner", "--database-url", databaseURL, "--email", "owner@example.com"},
			env:  envMap{"JOBCRON_OWNER_PASSWORD": "valid owner password"},
		},
		{
			name: "reset password",
			args: []string{"reset-password", "--database-url", databaseURL, "--email", "owner@example.com"},
			env:  envMap{"JOBCRON_USER_PASSWORD": "valid reset password"},
		},
		{
			name: "delete user",
			args: []string{
				"delete-user", "--database-url", databaseURL,
				"--email", "owner@example.com", "--confirm-email", "owner@example.com",
			},
			env: envMap{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, tc.env, nil, &bytes.Buffer{})
			if err == nil {
				t.Fatal("run error = nil, want fixed database-open error")
			}
			if got := err.Error(); got != wantError {
				t.Fatalf("error = %q, want %q", got, wantError)
			}
			if strings.Contains(err.Error(), coordinate) || strings.Contains(err.Error(), databaseURL) {
				t.Fatalf("error disclosed connection coordinates: %q", err)
			}
		})
	}
}

func assertCLIRowCount(t *testing.T, st *storage.Store, table string, userID int64, want int) {
	t.Helper()
	var got int
	if err := st.SQLDB().QueryRow("SELECT count(*) FROM "+table+" WHERE user_id = $1", userID).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func createUserCLITestSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	schema := postgresTestSchemaName(t)
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

func postgresTestSchemaName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "_")
	return "test_user_cli_" + strings.NewReplacer(" ", "_", "-", "_").Replace(name)
}

func databaseURLWithSearchPath(databaseURL, schema string) string {
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	return databaseURL + separator + "search_path=" + schema
}
