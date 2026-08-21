package scripts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const runtimeHelper = "deploy/production/jobcron-runtime.sh"
const systemdDir = "../deploy/production/systemd"

var runtimeSecretFields = map[string]string{
	"JOBCRON_IMAGE":                     "ghcr.io/example/jobcron@sha256:" + strings.Repeat("a", 64),
	"DATABASE_URL":                      "postgres://app:db-secret@db.example.invalid/jobcron?sslmode=require",
	"SESSION_SECRET":                    "session-secret-at-least-32-bytes",
	"JOBCRON_CREDENTIAL_ENCRYPTION_KEY": "credential-key-secret",
	"JOBCRON_SIGNUP_ACCESS_CODE":        "signup-code-secret",
	"JOBCRON_STAGE1_SPONSOR_USER_ID":    "42",
	"JOBCRON_PROXY_SECRET":              "proxy-secret-value",
	"ORIGIN_CA_CERT":                    "-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----",
	"ORIGIN_CA_KEY":                     "-----BEGIN PRIVATE KEY-----\nsynthetic\n-----END PRIVATE KEY-----",
}

type runtimeFixture struct {
	root, runDir, etcDir, deployDir, homeDir, binDir, logPath string
	env                                                       []string
}

func TestJobcronRuntimePrepareFailsClosed(t *testing.T) {
	fixture := newRuntimeFixture(t)

	t.Run("valid secret is written privately without disclosure", func(t *testing.T) {
		result := fixture.run(t, validRuntimeSecret(), "prepare")
		if result.err != nil {
			t.Fatalf("prepare failed: %v\n%s", result.err, result.output)
		}
		assertNoRuntimeSecret(t, result.output)
		assertMode(t, fixture.runDir, 0o700)
		assertMode(t, filepath.Join(fixture.runDir, "compose.env"), 0o600)
		assertMode(t, filepath.Join(fixture.runDir, "caddy", "origin.crt"), 0o600)
		assertMode(t, filepath.Join(fixture.runDir, "caddy", "origin.key"), 0o600)
		composeEnv := readFile(t, filepath.Join(fixture.runDir, "compose.env"))
		for key, value := range runtimeSecretFields {
			if strings.HasPrefix(key, "ORIGIN_CA_") {
				continue
			}
			if !strings.Contains(composeEnv, key+"="+value) {
				t.Errorf("compose.env missing %s", key)
			}
		}
	})

	for _, test := range []struct {
		name   string
		secret string
	}{
		{name: "missing field", secret: `{"JOBCRON_IMAGE":"missing-the-rest"}`},
		{name: "malformed JSON", secret: `{`},
		{name: "null field", secret: replaceRuntimeSecret(t, "SESSION_SECRET", nil)},
		{name: "empty field", secret: replaceRuntimeSecret(t, "SESSION_SECRET", "")},
		{name: "wrong type", secret: replaceRuntimeSecret(t, "SESSION_SECRET", 7)},
		{name: "unexpected field", secret: strings.TrimSuffix(validRuntimeSecret(), "}") + `,"UNEXPECTED":"value"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeFile(t, filepath.Join(fixture.runDir, "compose.env"), "STALE=must-not-survive\n", 0o600)
			result := fixture.run(t, test.secret, "prepare")
			if result.err == nil {
				t.Fatalf("prepare accepted %s", test.name)
			}
			assertNoRuntimeSecret(t, result.output)
			if _, err := os.Stat(filepath.Join(fixture.runDir, "compose.env")); !os.IsNotExist(err) {
				t.Fatalf("failed prepare retained stale compose.env: %v", err)
			}
		})
	}
}

func TestJobcronRuntimePrepareValidatesIdentifier(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    os.FileMode
		content string
		missing bool
	}{
		{name: "missing", missing: true},
		{name: "world readable", mode: 0o644, content: "synthetic-id\n"},
		{name: "empty", mode: 0o600},
		{name: "multi line", mode: 0o600, content: "one\ntwo\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeFixture(t)
			idPath := filepath.Join(fixture.etcDir, "runtime-secret-id")
			if test.missing {
				if err := os.Remove(idPath); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, idPath, test.content, test.mode)
			}
			result := fixture.run(t, validRuntimeSecret(), "prepare")
			if result.err == nil {
				t.Fatalf("prepare accepted %s identifier", test.name)
			}
			assertNoRuntimeSecret(t, result.output)
		})
	}
}

func TestJobcronRuntimePullUsesImageOwnerAndTransientCredentials(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	token := "registry-token-secret"
	writeFile(t, filepath.Join(fixture.runDir, "registry-token"), token+"\n", 0o600)
	result := fixture.run(t, validRuntimeSecret(), "pull")
	if result.err != nil {
		t.Fatalf("pull failed: %v\n%s", result.err, result.output)
	}
	assertNoRuntimeSecret(t, result.output+token)
	log := readFile(t, fixture.logPath)
	if !strings.Contains(log, "login ghcr.io --username example --password-stdin") ||
		!strings.Contains(log, "pull "+runtimeSecretFields["JOBCRON_IMAGE"]) ||
		!strings.Contains(log, "logout ghcr.io") {
		t.Fatalf("unexpected docker operations:\n%s", log)
	}
	if strings.Contains(log, token) {
		t.Fatal("registry token reached command arguments")
	}
	for _, path := range []string{
		filepath.Join(fixture.runDir, "registry-token"),
		filepath.Join(fixture.runDir, "docker"),
		filepath.Join(fixture.homeDir, ".docker", "config.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("transient credential path remained: %s (%v)", path, err)
		}
	}
}

func TestJobcronRuntimePrepareRejectsUnapprovedImageShape(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("b", 64)
	for _, image := range []string{
		"registry.example.invalid/example/jobcron" + digest,
		"ghcr.io/example/other" + digest,
		"ghcr.io/example/nested/jobcron" + digest,
	} {
		t.Run(image, func(t *testing.T) {
			fixture := newRuntimeFixture(t)
			result := fixture.run(t, replaceRuntimeSecret(t, "JOBCRON_IMAGE", image), "prepare")
			if result.err == nil {
				t.Fatalf("prepare accepted unapproved image %q", image)
			}
			assertNoRuntimeSecret(t, result.output)
		})
	}
}

func TestJobcronRuntimePullCleansCredentialsAfterFailure(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	token := "registry-token-secret"
	writeFile(t, filepath.Join(fixture.runDir, "registry-token"), token+"\n", 0o600)
	fixture.env = append(fixture.env, "FAKE_DOCKER_PULL_FAIL=1")
	result := fixture.run(t, validRuntimeSecret(), "pull")
	if result.err == nil {
		t.Fatal("pull succeeded despite synthetic Docker failure")
	}
	if strings.Contains(result.output, token) || strings.Contains(readFile(t, fixture.logPath), token) {
		t.Fatal("failed pull disclosed registry token")
	}
	for _, path := range []string{
		filepath.Join(fixture.runDir, "registry-token"),
		filepath.Join(fixture.runDir, "docker"),
		filepath.Join(fixture.homeDir, ".docker", "config.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed pull retained credential path: %s (%v)", path, err)
		}
	}
}

func TestJobcronRuntimeCleanupPreservesUnconsumedRegistryToken(t *testing.T) {
	fixture := newRuntimeFixture(t)
	tokenPath := filepath.Join(fixture.runDir, "registry-token")
	writeFile(t, tokenPath, "registry-token-secret\n", 0o600)
	writeFile(t, filepath.Join(fixture.runDir, "compose.env"), "synthetic\n", 0o600)
	for _, dir := range []string{"caddy", "docker", "archive"} {
		if err := os.MkdirAll(filepath.Join(fixture.runDir, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(fixture.runDir, "caddy", "origin.key"), "synthetic\n", 0o600)
	writeFile(t, filepath.Join(fixture.runDir, "docker", "config.json"), "{}\n", 0o600)
	writeFile(t, filepath.Join(fixture.runDir, "archive", "database.dump"), "synthetic\n", 0o600)

	result := fixture.run(t, validRuntimeSecret(), "cleanup")
	if result.err != nil {
		t.Fatalf("cleanup failed: %v\n%s", result.err, result.output)
	}
	if got := readFile(t, tokenPath); got != "registry-token-secret\n" {
		t.Fatalf("cleanup changed the unconsumed registry token")
	}
	assertMode(t, tokenPath, 0o600)
	for _, path := range []string{
		filepath.Join(fixture.runDir, "compose.env"),
		filepath.Join(fixture.runDir, "caddy"),
		filepath.Join(fixture.runDir, "docker"),
		filepath.Join(fixture.runDir, "archive"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup retained generated runtime path: %s (%v)", path, err)
		}
	}

	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	if result := fixture.run(t, validRuntimeSecret(), "cleanup"); result.err != nil {
		t.Fatalf("final cleanup failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(fixture.runDir); !os.IsNotExist(err) {
		t.Fatalf("empty runtime directory remained: %v", err)
	}
}

func TestJobcronRuntimePullNeedsNoTokenForPresentDigest(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	fixture.env = append(fixture.env, "FAKE_IMAGE_PRESENT=1")
	result := fixture.run(t, validRuntimeSecret(), "pull")
	if result.err != nil {
		t.Fatalf("present digest required credentials: %v\n%s", result.err, result.output)
	}
	if log := readFile(t, fixture.logPath); strings.Contains(log, "login ") {
		t.Fatalf("present digest caused registry login:\n%s", log)
	}
}

func TestJobcronRuntimeArchiveIsWriteOnlyAndSanitized(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	result := fixture.run(t, validRuntimeSecret(), "archive")
	if result.err != nil {
		t.Fatalf("archive failed: %v\n%s", result.err, result.output)
	}
	assertNoRuntimeSecret(t, result.output)
	log := readFile(t, fixture.logPath)
	if !strings.Contains(log, "pg_dump -Fc") {
		t.Fatalf("pg_dump was not custom format:\n%s", log)
	}
	for _, want := range []string{
		"docker compose --env-file " + filepath.Join(fixture.runDir, "compose.env") + " logs --no-color app",
		"docker compose --env-file " + filepath.Join(fixture.runDir, "compose.env") + " logs --no-color caddy",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("archive missing exact Compose environment: %q\n%s", want, log)
		}
	}
	for _, forbidden := range []string{" s3 ls ", " s3api ", " s3 rm ", " s3 mv "} {
		if strings.Contains(" "+log+" ", forbidden) {
			t.Fatalf("archive used forbidden S3 operation %q:\n%s", forbidden, log)
		}
	}
	lines := strings.Split(strings.TrimSpace(log), "\n")
	var uploads []string
	for _, line := range lines {
		if strings.HasPrefix(line, "aws s3 cp ") {
			uploads = append(uploads, line)
		}
	}
	if len(uploads) != 6 {
		t.Fatalf("archive uploaded %d objects, want 6:\n%s", len(uploads), strings.Join(uploads, "\n"))
	}
	for _, upload := range uploads[3:] {
		if !strings.Contains(upload, ".sha256 ") {
			t.Fatalf("manifests were not uploaded last:\n%s", strings.Join(uploads, "\n"))
		}
	}
	for _, upload := range uploads {
		if !strings.Contains(upload, "/jobcron/20260728T120000Z/") {
			t.Fatalf("archive key is not immutable UTC path: %s", upload)
		}
	}
	for _, name := range []string{"jobcron.log", "caddy.log"} {
		sanitized := readFile(t, filepath.Join(fixture.runDir, "archive", name))
		for _, forbidden := range []string{
			"bearer-header-secret",
			"cookie-header-secret",
			"password-header-secret",
			"secret-json-secret",
			"token-json-secret",
			"password-key-value-secret",
			"postgres-url-secret",
			"postgresql-url-secret",
		} {
			if strings.Contains(sanitized, forbidden) {
				t.Fatalf("%s retained %q: %s", name, forbidden, sanitized)
			}
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(fixture.runDir, "archive", "jobcron.log")), "INFO ready") {
		t.Fatalf("manifests were not uploaded last:\n%s", strings.Join(uploads, "\n"))
	}
}

func TestJobcronRuntimeArchiveFailsClosedWhenComposeLogsFail(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	fixture.env = append(fixture.env, "FAKE_DOCKER_LOGS_FAIL=1")
	result := fixture.run(t, validRuntimeSecret(), "archive")
	if result.err == nil {
		t.Fatal("archive succeeded despite synthetic Compose log failure")
	}
	if log := readFile(t, fixture.logPath); strings.Contains(log, "aws s3 cp ") {
		t.Fatalf("failed archive uploaded partial artifacts:\n%s", log)
	}
	err := filepath.WalkDir(filepath.Join(fixture.runDir, "archive"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && strings.Contains(entry.Name(), ".raw") {
			t.Errorf("failed archive retained raw log: %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestJobcronRuntimeVerifyLocalStateIsValueBlind(t *testing.T) {
	fixture := newRuntimeFixture(t)
	if result := fixture.run(t, validRuntimeSecret(), "prepare"); result.err != nil {
		t.Fatalf("prepare: %v\n%s", result.err, result.output)
	}
	fixture.env = append(fixture.env, "FAKE_IMAGE_PRESENT=1")
	result := fixture.run(t, validRuntimeSecret(), "verify-local-state")
	if result.err != nil {
		t.Fatalf("verify-local-state failed: %v\n%s", result.err, result.output)
	}
	assertNoRuntimeSecret(t, result.output)
	for _, line := range strings.Split(strings.TrimSpace(result.output), "\n") {
		if !strings.Contains(line, "=") {
			t.Fatalf("non-summary output: %q", line)
		}
		value := strings.SplitN(line, "=", 2)[1]
		if value != "true" && value != "false" && !onlyDigits(value) {
			t.Fatalf("verification disclosed non-boolean/count value: %q", line)
		}
	}
	for _, want := range []string{
		"docker_json_file_logging=true",
		"docker_log_rotation=true",
		"current_digest_count=1",
		"previous_digest_count=1",
	} {
		if !strings.Contains(result.output, want+"\n") {
			t.Errorf("verification missing %q:\n%s", want, result.output)
		}
	}
	if strings.Contains(result.output, runtimeSecretFields["JOBCRON_IMAGE"]) ||
		strings.Contains(result.output, strings.Repeat("b", 64)) {
		t.Fatalf("verification disclosed an image digest:\n%s", result.output)
	}
}

func TestJobcronSystemdMainUnitFailsClosed(t *testing.T) {
	unit := readFile(t, filepath.Join(systemdDir, "jobcron.service"))
	for _, want := range []string{
		"After=network-online.target docker.service",
		"Requires=docker.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"WorkingDirectory=/opt/jobcron",
	} {
		if !strings.Contains(unit, want+"\n") {
			t.Errorf("jobcron.service missing %q", want)
		}
	}

	wantPreStart := []string{
		"/bin/sh -c 'if [ -f /run/jobcron/compose.env ]; then exec /usr/bin/docker compose --env-file /run/jobcron/compose.env down --remove-orphans; fi'",
		"/opt/jobcron/jobcron-runtime.sh prepare",
		"/opt/jobcron/jobcron-runtime.sh pull",
	}
	if got := unitValues(unit, "ExecStartPre"); strings.Join(got, "\n") != strings.Join(wantPreStart, "\n") {
		t.Fatalf("ExecStartPre order = %q, want %q", got, wantPreStart)
	}
	if got := unitValues(unit, "ExecStart"); len(got) != 1 ||
		got[0] != "/usr/bin/docker compose --env-file /run/jobcron/compose.env up -d --remove-orphans" {
		t.Fatalf("ExecStart = %q", got)
	}
	for _, directive := range append(unitValues(unit, "ExecStartPre"), unitValues(unit, "ExecStart")...) {
		if strings.HasPrefix(directive, "-") {
			t.Fatalf("startup action ignores failure: %q", directive)
		}
	}
	firstBootStop := strings.TrimSuffix(strings.TrimPrefix(wantPreStart[0], "/bin/sh -c '"), "'")
	tempDir := t.TempDir()
	composeEnv := filepath.Join(tempDir, "compose.env")
	dockerLog := filepath.Join(tempDir, "docker.log")
	fakeDocker := filepath.Join(tempDir, "docker")
	writeExecutable(t, fakeDocker, "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\""+dockerLog+"\"\nexit \"${FAKE_DOCKER_EXIT:-0}\"\n")
	firstBootStop = strings.ReplaceAll(firstBootStop, "/run/jobcron/compose.env", composeEnv)
	firstBootStop = strings.ReplaceAll(firstBootStop, "/usr/bin/docker", fakeDocker)

	cmd := exec.Command("sh", "-c", firstBootStop)
	cmd.Env = append(os.Environ(), "FAKE_DOCKER_EXIT=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("missing compose.env was not a clean first boot: %v\n%s", err, output)
	}
	if log := readOptionalFile(t, dockerLog); log != "" {
		t.Fatalf("missing compose.env invoked Docker: %s", log)
	}
	writeFile(t, composeEnv, "synthetic\n", 0o600)
	cmd = exec.Command("sh", "-c", firstBootStop)
	cmd.Env = append(os.Environ(), "FAKE_DOCKER_EXIT=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("existing compose.env ignored Compose down failure")
	}
	if got := unitValues(unit, "ExecStop"); len(got) != 1 ||
		got[0] != "/usr/bin/docker compose --env-file /run/jobcron/compose.env down --remove-orphans" {
		t.Fatalf("ExecStop = %q", got)
	}
	if got := unitValues(unit, "ExecStopPost"); len(got) != 1 ||
		got[0] != "/opt/jobcron/jobcron-runtime.sh cleanup" {
		t.Fatalf("ExecStopPost = %q", got)
	}
	for _, want := range []string{
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"PrivateTmp=true",
		"ReadWritePaths=/run/jobcron",
	} {
		if !strings.Contains(unit, want+"\n") {
			t.Errorf("jobcron.service missing sandbox directive %q", want)
		}
	}
	assertUnitIsValueBlind(t, unit)
}

func TestJobcronSystemdRecoveryUnitAndTimer(t *testing.T) {
	service := readFile(t, filepath.Join(systemdDir, "jobcron-recovery.service"))
	if got := unitValues(service, "ExecStart"); len(got) != 1 ||
		got[0] != "/opt/jobcron/jobcron-runtime.sh archive" {
		t.Fatalf("recovery ExecStart = %q", got)
	}
	if !strings.Contains(service, "Type=oneshot\n") {
		t.Fatal("recovery service is not single-instance oneshot")
	}
	assertUnitIsValueBlind(t, service)

	timer := readFile(t, filepath.Join(systemdDir, "jobcron-recovery.timer"))
	for _, want := range []string{
		"OnCalendar=daily",
		"Persistent=true",
		"RandomizedDelaySec=30m",
		"Unit=jobcron-recovery.service",
	} {
		if !strings.Contains(timer, want+"\n") {
			t.Errorf("recovery timer missing %q", want)
		}
	}
}

func assertUnitIsValueBlind(t *testing.T, unit string) {
	t.Helper()
	for _, forbidden := range []string{"Environment=", "EnvironmentFile=", "set -x", "printenv", "/usr/bin/env"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("unit contains value-bearing directive %q", forbidden)
		}
	}
}

func unitValues(unit, name string) []string {
	var values []string
	prefix := name + "="
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, prefix) {
			values = append(values, strings.TrimPrefix(line, prefix))
		}
	}
	return values
}

func newRuntimeFixture(t *testing.T) runtimeFixture {
	t.Helper()
	root := t.TempDir()
	fixture := runtimeFixture{
		root:      root,
		runDir:    filepath.Join(root, "run", "jobcron"),
		etcDir:    filepath.Join(root, "etc", "jobcron"),
		deployDir: filepath.Join(root, "opt", "jobcron"),
		homeDir:   filepath.Join(root, "home"),
		binDir:    filepath.Join(root, "bin"),
		logPath:   filepath.Join(root, "commands.log"),
	}
	for _, dir := range []string{fixture.runDir, fixture.etcDir, fixture.deployDir, fixture.homeDir, fixture.binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFile(t, filepath.Join(fixture.etcDir, "runtime-secret-id"), "synthetic-id\n", 0o600)
	writeFile(t, filepath.Join(fixture.deployDir, "compose.yaml"), "logging:\n  driver: json-file\n  options:\n    max-size: 10m\n    max-file: 3\n", 0o600)
	installRuntimeFakes(t, fixture)
	fixture.env = append(os.Environ(),
		"PATH="+fixture.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+fixture.homeDir,
		"JOBCRON_ETC_DIR="+fixture.etcDir,
		"JOBCRON_RUN_DIR="+fixture.runDir,
		"JOBCRON_DEPLOY_DIR="+fixture.deployDir,
		"JOBCRON_RECOVERY_BUCKET=synthetic-recovery",
		"JOBCRON_NOW=20260728T120000Z",
		"FAKE_COMMAND_LOG="+fixture.logPath,
	)
	return fixture
}

type commandResult struct {
	output string
	err    error
}

func (f runtimeFixture) run(t *testing.T, secret string, args ...string) commandResult {
	t.Helper()
	cmd := exec.Command("sh", append([]string{runtimeHelper}, args...)...)
	cmd.Dir = filepath.Clean(filepath.Join(".."))
	cmd.Env = append(f.env, "FAKE_SECRET_JSON="+secret)
	output, err := cmd.CombinedOutput()
	return commandResult{output: string(output), err: err}
}

func installRuntimeFakes(t *testing.T, f runtimeFixture) {
	t.Helper()
	realJQ, err := exec.LookPath("jq")
	if err != nil {
		t.Fatal("jq is required for runtime helper tests")
	}
	realSHA, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Fatal("sha256sum is required for runtime helper tests")
	}
	writeExecutable(t, filepath.Join(f.binDir, "jq"), fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", realJQ))
	writeExecutable(t, filepath.Join(f.binDir, "sha256sum"), fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", realSHA))
	writeExecutable(t, filepath.Join(f.binDir, "aws"), `#!/bin/sh
printf 'aws %s\n' "$*" >>"$FAKE_COMMAND_LOG"
if [ "$1" = secretsmanager ]; then printf '%s\n' "$FAKE_SECRET_JSON"; exit 0; fi
exit 0
`)
	writeExecutable(t, filepath.Join(f.binDir, "docker"), `#!/bin/sh
printf 'docker %s\n' "$*" >>"$FAKE_COMMAND_LOG"
if [ "$1" = image ] && [ "$2" = inspect ]; then
  [ "${FAKE_IMAGE_PRESENT:-0}" = 1 ]
  exit
fi
if [ "$1" = image ] && [ "$2" = ls ]; then
  printf '%s\n' \
    'ghcr.io/example/jobcron@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
    'ghcr.io/example/jobcron@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
  exit 0
fi
if [ "$1" = login ]; then cat >/dev/null; mkdir -p "$DOCKER_CONFIG"; printf '{}' >"$DOCKER_CONFIG/config.json"; fi
if [ "$1" = pull ] && [ "${FAKE_DOCKER_PULL_FAIL:-0}" = 1 ]; then exit 1; fi
if [ "$1" = compose ] && [ "$2" = config ]; then
  printf '%s\n' '{"services":{"app":{"logging":{"driver":"json-file","options":{"max-size":"10m","max-file":"3"}}},"caddy":{"logging":{"driver":"json-file","options":{"max-size":"10m","max-file":"3"}}}}}'
  exit 0
fi
if [ "$1" = compose ] && printf '%s\n' "$*" | grep -q ' logs --no-color '; then
  [ "${FAKE_DOCKER_LOGS_FAIL:-0}" != 1 ] || exit 1
  printf '%s\n' \
    '2026-07-28T12:00:00Z app INFO ready Authorization: Bearer bearer-header-secret' \
    '2026-07-28T12:00:01Z app WARN Cookie: session=cookie-header-secret Password: password-header-secret' \
    '2026-07-28T12:00:02Z app WARN {"secret":"secret-json-secret","token":"token-json-secret"}' \
    '2026-07-28T12:00:03Z app WARN password=password-key-value-secret' \
    '2026-07-28T12:00:04Z app WARN postgres://jobcron:postgres-url-secret@db.example.invalid/jobcron?sslmode=require' \
    '2026-07-28T12:00:05Z app WARN postgresql://jobcron:postgresql-url-secret@db.example.invalid/jobcron?sslmode=verify-full'
fi
exit 0
`)
	writeExecutable(t, filepath.Join(f.binDir, "pg_dump"), `#!/bin/sh
printf 'pg_dump %s\n' "$*" >>"$FAKE_COMMAND_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = -f ]; then shift; printf 'synthetic dump\n' >"$1"; exit 0; fi
  shift
done
exit 1
`)
}

func validRuntimeSecret() string {
	var fields []string
	for key, value := range runtimeSecretFields {
		fields = append(fields, fmt.Sprintf("%q:%q", key, value))
	}
	return "{" + strings.Join(fields, ",") + "}"
}

func replaceRuntimeSecret(t *testing.T, key string, value any) string {
	t.Helper()
	clone := make(map[string]any, len(runtimeSecretFields))
	for field, current := range runtimeSecretFields {
		clone[field] = current
	}
	clone[key] = value
	var fields []string
	for field, current := range clone {
		switch typed := current.(type) {
		case nil:
			fields = append(fields, fmt.Sprintf("%q:null", field))
		case string:
			fields = append(fields, fmt.Sprintf("%q:%q", field, typed))
		default:
			fields = append(fields, fmt.Sprintf("%q:%v", field, typed))
		}
	}
	return "{" + strings.Join(fields, ",") + "}"
}

func assertNoRuntimeSecret(t *testing.T, output string) {
	t.Helper()
	for key, value := range runtimeSecretFields {
		if key == "JOBCRON_STAGE1_SPONSOR_USER_ID" {
			continue
		}
		for _, line := range strings.Split(value, "\n") {
			if line != "" && strings.Contains(output, line) {
				t.Fatalf("output disclosed synthetic secret fragment %q", line)
			}
		}
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	writeFile(t, path, contents, 0o700)
}

func onlyDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
