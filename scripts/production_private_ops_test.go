package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	rdsRoleHelper  = "production-rds-role.sh"
	recoveryHelper = "pull-production-recovery.sh"
)

func TestProductionPrivateOpsRDSRejectsNonLocalOrWeakTLS(t *testing.T) {
	requirePrivateOpsHelper(t, rdsRoleHelper)
	for _, databaseURL := range []string{
		"postgres://master@db.example.invalid:5432/jobcron?sslmode=require",
		"postgres://master@127.0.0.1:15432/jobcron?sslmode=disable",
		"postgres://master@localhost:15432/jobcron?sslmode=require",
	} {
		t.Run(databaseURL, func(t *testing.T) {
			fixture := newPrivateOpsFixture(t)
			fixture.env = append(fixture.env, "JOBCRON_MASTER_DATABASE_URL="+databaseURL)
			result := fixture.run(t, rdsRoleHelper, "master-password\napplication-password\n")
			if result.err == nil {
				t.Fatalf("RDS helper accepted %q", databaseURL)
			}
			if log := readOptionalFile(t, fixture.commandLog); strings.Contains(log, "psql ") {
				t.Fatalf("invalid URL reached psql:\n%s", log)
			}
		})
	}
}

func TestProductionPrivateOpsRDSUsesOneLeastPrivilegeTransaction(t *testing.T) {
	requirePrivateOpsHelper(t, rdsRoleHelper)
	fixture := newPrivateOpsFixture(t)
	masterPassword := "master-password-secret"
	applicationPassword := "application-password-secret"
	result := fixture.run(t, rdsRoleHelper, masterPassword+"\n"+applicationPassword+"\n")
	if result.err != nil {
		t.Fatalf("RDS helper failed: %v\n%s", result.err, result.output)
	}
	commandLog := readFile(t, fixture.commandLog)
	if strings.Contains(result.output+commandLog, masterPassword) ||
		strings.Contains(result.output+commandLog, applicationPassword) {
		t.Fatal("database password reached output or command arguments")
	}
	sql := readFile(t, fixture.sqlLog)
	for _, want := range []string{
		"BEGIN;",
		"IF NOT EXISTS",
		"CREATE ROLE jobcron_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION;",
		"ALTER ROLE jobcron_app PASSWORD '" + applicationPassword + "';",
		"GRANT CONNECT ON DATABASE jobcron TO jobcron_app;",
		"GRANT USAGE ON SCHEMA public TO jobcron_app;",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO jobcron_app;",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO jobcron_app;",
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO jobcron_app;",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO jobcron_app;",
		"COMMIT;",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL missing %q:\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{
		"CREATE DATABASE",
		"CREATE EXTENSION",
		"GRANT ALL",
		" TO PUBLIC",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("SQL contains forbidden privilege %q:\n%s", forbidden, sql)
		}
	}
	if got := readFile(t, fixture.roleEnv); got != "DATABASE_ROLE_READY=true\n" {
		t.Fatalf("database-role.env = %q", got)
	}
	assertMode(t, fixture.roleEnv, 0o600)
	assertMode(t, fixture.runtimeSecret, 0o600)
	var runtime map[string]string
	if err := json.Unmarshal([]byte(readFile(t, fixture.runtimeSecret)), &runtime); err != nil {
		t.Fatal(err)
	}
	if got := runtime["DATABASE_URL"]; !strings.Contains(got, "jobcron_app:application-password-secret@127.0.0.1:15432/jobcron") ||
		!strings.Contains(got, "sslmode=require") {
		t.Fatalf("private runtime DATABASE_URL not updated: %q", got)
	}

	secondPassword := "rotated-application-password"
	result = fixture.run(t, rdsRoleHelper, masterPassword+"\n"+secondPassword+"\n")
	if result.err != nil {
		t.Fatalf("RDS helper rerun failed: %v\n%s", result.err, result.output)
	}
	secondSQL := readFile(t, fixture.sqlLog)
	if !strings.Contains(secondSQL, "IF NOT EXISTS") ||
		!strings.Contains(secondSQL, "ALTER ROLE jobcron_app PASSWORD '"+secondPassword+"';") {
		t.Fatalf("rerun was not idempotent password rotation:\n%s", secondSQL)
	}
}

func TestProductionPrivateOpsRecoveryDownloadsMissingThenTagsVerified(t *testing.T) {
	requirePrivateOpsHelper(t, recoveryHelper)
	fixture := newPrivateOpsFixture(t)
	fixture.installRecoveryObjects(t, false)
	prefixDir := filepath.Join(fixture.recoveryDir, "20260728T120000Z")
	if err := os.MkdirAll(prefixDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prefixDir, "jobcron.log"), "jobcron-log\n", 0o600)

	result := fixture.run(t, recoveryHelper, "")
	if result.err != nil {
		t.Fatalf("recovery helper failed: %v\n%s\n%s", result.err, result.output, readOptionalFile(t, fixture.commandLog))
	}
	log := readFile(t, fixture.commandLog)
	if strings.Contains(log, " s3 cp s3://synthetic-recovery/jobcron/20260728T120000Z/jobcron.log ") {
		t.Fatalf("recovery re-downloaded an existing object:\n%s", log)
	}
	if got := strings.Count(log, "s3api put-object-tagging"); got != 6 {
		t.Fatalf("tagged %d objects, want 6:\n%s", got, log)
	}
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "put-object-tagging") &&
			!strings.Contains(line, "--tagging TagSet=[{Key=macbook-copy,Value=verified}]") {
			t.Fatalf("unexpected object tag operation: %s", line)
		}
		if strings.Contains(line, "delete") || strings.Contains(line, " s3 rm ") {
			t.Fatalf("recovery used destructive AWS operation: %s", line)
		}
	}
	if !strings.Contains(result.output, "recovery_verified=true") {
		t.Fatalf("missing value-blind success result: %q", result.output)
	}
}

func TestProductionPrivateOpsRecoveryNeverTagsFailedVerification(t *testing.T) {
	requirePrivateOpsHelper(t, recoveryHelper)
	fixture := newPrivateOpsFixture(t)
	fixture.installRecoveryObjects(t, true)
	result := fixture.run(t, recoveryHelper, "")
	if result.err == nil {
		t.Fatal("recovery succeeded with a corrupt manifest")
	}
	log := readFile(t, fixture.commandLog)
	if strings.Contains(log, "put-object-tagging") {
		t.Fatalf("failed verification tagged objects:\n%s", log)
	}
	if strings.Contains(log, "delete") || strings.Contains(log, " s3 rm ") {
		t.Fatalf("failed verification deleted data:\n%s", log)
	}
}

type privateOpsFixture struct {
	root, binDir, commandLog, sqlLog, roleEnv, runtimeSecret string
	recoveryRemote, recoveryDir                              string
	env                                                      []string
}

func newPrivateOpsFixture(t *testing.T) privateOpsFixture {
	t.Helper()
	root := t.TempDir()
	fixture := privateOpsFixture{
		root:           root,
		binDir:         filepath.Join(root, "bin"),
		commandLog:     filepath.Join(root, "commands.log"),
		sqlLog:         filepath.Join(root, "sql.log"),
		roleEnv:        filepath.Join(root, "database-role.env"),
		runtimeSecret:  filepath.Join(root, "runtime-secret.json"),
		recoveryRemote: filepath.Join(root, "remote"),
		recoveryDir:    filepath.Join(root, "recovery"),
	}
	for _, dir := range []string{fixture.binDir, fixture.recoveryRemote, fixture.recoveryDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, fixture.runtimeSecret, `{"SESSION_SECRET":"synthetic-existing"}`+"\n", 0o600)
	writeExecutable(t, filepath.Join(fixture.binDir, "psql"), `#!/bin/sh
printf 'psql %s\n' "$*" >>"$PRIVATE_OPS_COMMAND_LOG"
cat >"$PRIVATE_OPS_SQL_LOG"
exit "${FAKE_PSQL_EXIT:-0}"
`)
	writeExecutable(t, filepath.Join(fixture.binDir, "aws"), `#!/bin/sh
printf 'aws %s\n' "$*" >>"$PRIVATE_OPS_COMMAND_LOG"
if [ "$1" = sts ] && [ "$2" = get-caller-identity ]; then exit 0; fi
if [ "$1" = s3api ] && [ "$2" = list-objects-v2 ]; then
  printf '%s\n' "$FAKE_RECOVERY_KEYS"
  exit 0
fi
if [ "$1" = s3 ] && [ "$2" = cp ]; then
  key=${3#s3://*/}
  /bin/cp "$FAKE_RECOVERY_REMOTE/$key" "$4"
  exit
fi
if [ "$1" = s3api ] && [ "$2" = put-object-tagging ]; then exit 0; fi
exit 1
`)
	fixture.env = append(os.Environ(),
		"PATH="+fixture.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRIVATE_OPS_COMMAND_LOG="+fixture.commandLog,
		"PRIVATE_OPS_SQL_LOG="+fixture.sqlLog,
		"JOBCRON_MASTER_DATABASE_URL=postgres://master@127.0.0.1:15432/jobcron?sslmode=require",
		"JOBCRON_APP_DATABASE_USER=jobcron_app",
		"JOBCRON_DATABASE_ROLE_ENV="+fixture.roleEnv,
		"JOBCRON_RUNTIME_SECRET_JSON="+fixture.runtimeSecret,
		"JOBCRON_RECOVERY_BUCKET=synthetic-recovery",
		"JOBCRON_RECOVERY_PREFIX=jobcron/20260728T120000Z",
		"JOBCRON_RECOVERY_DIR="+fixture.recoveryDir,
		"FAKE_RECOVERY_REMOTE="+fixture.recoveryRemote,
	)
	return fixture
}

func (f privateOpsFixture) run(t *testing.T, script, input string) commandResult {
	t.Helper()
	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = f.env
	output, err := cmd.CombinedOutput()
	return commandResult{output: string(output), err: err}
}

func (f *privateOpsFixture) installRecoveryObjects(t *testing.T, corrupt bool) {
	t.Helper()
	prefix := "jobcron/20260728T120000Z"
	var keys []string
	for _, object := range []struct {
		name    string
		content string
	}{
		{name: "database.dump", content: "database-dump\n"},
		{name: "jobcron.log", content: "jobcron-log\n"},
		{name: "caddy.log", content: "caddy-log\n"},
	} {
		dataPath := filepath.Join(f.recoveryRemote, prefix, object.name)
		writeFile(t, dataPath, object.content, 0o600)
		sum := sha256.Sum256([]byte(object.content))
		manifest := hex.EncodeToString(sum[:]) + "  " + object.name + "\n"
		if corrupt && object.name == "database.dump" {
			manifest = strings.Repeat("0", 64) + "  " + object.name + "\n"
		}
		writeFile(t, dataPath+".sha256", manifest, 0o600)
		keys = append(keys, prefix+"/"+object.name, prefix+"/"+object.name+".sha256")
	}
	f.env = append(f.env, "FAKE_RECOVERY_KEYS="+strings.Join(keys, "\t"))
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func requirePrivateOpsHelper(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("required helper %s: %v", path, err)
	}
}
