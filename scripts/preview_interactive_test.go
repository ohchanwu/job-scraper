package scripts

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	statePrefix    = "Preview state: "
	databasePrefix = "Preview database: "
	keyPrefix      = "Preview key: "
	urlPrefix      = "Preview URL: "
)

var previewDatabasePattern = regexp.MustCompile(`^jobcron_preview_[a-z0-9]+$`)

type previewProcess struct {
	cmd      *exec.Cmd
	done     chan struct{}
	scanDone chan struct{}
	lines    chan string
	mu       sync.Mutex
	waitErr  error
	output   []string
	stateDir string
	database string
	keyPath  string
	url      string
}

type fakePreviewConfig struct {
	port            int
	suffix          string
	owner           string
	ncExit          int
	createdbExit    int
	bootstrapOutput string
	bootstrapExit   int
	secondExit      int
	signalAfterLock bool
	signalCreatedb  bool
	rmdirFail       bool
	portRace        bool
}

type fakePreviewResult struct {
	output string
	log    string
	err    error
}

type previewStartupError struct {
	waitErr      error
	output       string
	expectedBusy string
}

func (e *previewStartupError) Error() string {
	return fmt.Sprintf("preview exited before startup metadata: %v\n%s", e.waitErr, e.output)
}

func TestPreviewInteractiveScriptSyntax(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	cmd := exec.Command("sh", "-n", "scripts/preview-interactive.sh")
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n scripts/preview-interactive.sh: %v\n%s", err, output)
	}
}

func TestPreviewUsesUniquePostgresDatabase(t *testing.T) {
	postgresURL := requirePreviewPostgres(t)
	first := startPreview(t, false)
	second := startPreview(t, false)

	if first.database == second.database {
		t.Fatalf("two previews used the same database %q", first.database)
	}
	for _, database := range []string{first.database, second.database} {
		if !previewDatabasePattern.MatchString(database) {
			t.Fatalf("preview database = %q, want %s", database, previewDatabasePattern)
		}
		if !databaseExists(t, postgresURL, database) {
			t.Fatalf("preview database %q does not exist while preview is running", database)
		}
	}
}

func TestPreviewWritesDoNotReachJobcronDevOrSecondPreview(t *testing.T) {
	postgresURL := requirePreviewPostgres(t)
	first := startPreview(t, false)
	second := startPreview(t, false)
	marker := fmt.Sprintf("preview-isolation-%d", time.Now().UnixNano())

	form := url.Values{
		"career_years": {"0"},
		"job_likes":    {marker},
		"min_score":    {"40"},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create preview cookie jar: %v", err)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Jar:     jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	profileResponse, err := client.Get(first.url + "/profile")
	if err != nil {
		t.Fatalf("load preview profile form: %v", err)
	}
	profileBody, err := io.ReadAll(profileResponse.Body)
	_ = profileResponse.Body.Close()
	if err != nil {
		t.Fatalf("read preview profile form: %v", err)
	}
	csrfMatch := regexp.MustCompile(`name="csrf_token"[^>]*value="([^"]+)"`).FindSubmatch(profileBody)
	if len(csrfMatch) != 2 {
		t.Fatalf("preview profile form did not contain csrf_token")
	}
	form.Set("csrf_token", string(csrfMatch[1]))
	response, err := client.PostForm(first.url+"/profile", form)
	if err != nil {
		t.Fatalf("submit preview profile: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("submit preview profile status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}

	if got := profileMarkerCount(t, postgresURL, first.database, marker); got != 1 {
		t.Fatalf("first preview marker rows = %d, want 1", got)
	}
	if got := profileMarkerCount(t, postgresURL, "jobcron_dev", marker); got != 0 {
		t.Fatalf("jobcron_dev marker rows = %d, want 0", got)
	}
	if got := profileMarkerCount(t, postgresURL, second.database, marker); got != 0 {
		t.Fatalf("second preview marker rows = %d, want 0", got)
	}
}

func TestPreviewDropsDatabaseAndKeyOnExit(t *testing.T) {
	postgresURL := requirePreviewPostgres(t)
	preview := startPreview(t, false)
	stateDir, database, keyPath := preview.stateDir, preview.database, preview.keyPath

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read preview key: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyBytes)))
	if err != nil || len(decoded) != 32 {
		t.Fatalf("preview key is not base64 for 32 bytes: decoded=%d err=%v", len(decoded), err)
	}

	preview.stop(t)
	if databaseExists(t, postgresURL, database) {
		t.Fatalf("preview database %q still exists after exit", database)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("preview key %q still exists after exit: %v", keyPath, err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("preview state %q still exists after exit: %v", stateDir, err)
	}
	if lockPath := previewLockPath(t, previewPort(t, preview.url)); pathExists(lockPath) {
		t.Fatalf("preview port lock %q still exists after signal exit", lockPath)
	}
}

func TestPreviewKeepRetainsAndPrintsDatabaseAndKeyLocations(t *testing.T) {
	postgresURL := requirePreviewPostgres(t)
	quotedTMPDIR := filepath.Join(t.TempDir(), "tmp'quoted\nnewline")
	if err := os.MkdirAll(quotedTMPDIR, 0o700); err != nil {
		t.Fatalf("create quoted TMPDIR: %v", err)
	}
	preview := startPreviewWithEnv(t, true, "TMPDIR="+quotedTMPDIR)
	stateMatches, err := filepath.Glob(filepath.Join(quotedTMPDIR, "jobcron-preview.*"))
	if err != nil || len(stateMatches) != 1 {
		t.Fatalf("discover quoted preview state: matches=%v err=%v", stateMatches, err)
	}
	preview.stateDir = stateMatches[0]
	preview.keyPath = filepath.Join(preview.stateDir, "credential-encryption.key")
	stateDir, database, keyPath := preview.stateDir, preview.database, preview.keyPath

	preview.stop(t)
	if !databaseExists(t, postgresURL, database) {
		t.Fatalf("kept preview database %q was dropped", database)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("kept preview key %q: %v", keyPath, err)
	}
	output := preview.combinedOutput()
	composePath, err := filepath.Abs(filepath.Join("..", "deploy", "local", "compose.yaml"))
	if err != nil {
		t.Fatalf("resolve compose path: %v", err)
	}
	dropCommand := "docker compose -p jobcron-local -f " + shellQuote(composePath) +
		" exec -T postgres dropdb --if-exists --force " + database + " -U postgres"
	stateCommand := "rm -rf " + shellQuote(stateDir)
	for _, want := range []string{
		"Preview retained",
		"  " + dropCommand,
		"  " + stateCommand,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("launcher output does not contain %q:\n%s", want, output)
		}
	}

	drop := exec.Command("sh", "-c", dropCommand)
	drop.Dir = filepath.Clean(filepath.Join(".."))
	if output, err := drop.CombinedOutput(); err != nil {
		t.Fatalf("execute printed database cleanup: %v\n%s", err, output)
	}
	remove := exec.Command("sh", "-c", stateCommand)
	if output, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("execute printed state cleanup: %v\n%s", err, output)
	}
	if databaseExists(t, postgresURL, database) {
		t.Fatalf("printed cleanup left database %q", database)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("printed cleanup left state %q: %v", stateDir, err)
	}
}

func TestPreviewRejectsProductionDatabaseURL(t *testing.T) {
	requirePreviewPostgres(t)
	port := reserveLoopbackPort(t)
	cmd := exec.Command("sh", "scripts/preview-interactive.sh", strconv.Itoa(port))
	cmd.Dir = filepath.Clean(filepath.Join(".."))
	cmd.Env = append(withoutEnv(os.Environ(), "DATABASE_URL"),
		"DATABASE_URL=postgres://production.example.invalid/jobcron")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("preview accepted inherited production DATABASE_URL:\n%s", output)
	}
	if !strings.Contains(string(output), "refusing inherited DATABASE_URL") {
		t.Fatalf("preview rejection output = %q, want inherited DATABASE_URL refusal", output)
	}
	if strings.Contains(string(output), databasePrefix) {
		t.Fatalf("preview created a database before rejecting production URL:\n%s", output)
	}
}

func TestPreviewClearsInheritedProductionAndSchedulerEnvironment(t *testing.T) {
	requirePreviewPostgres(t)
	preview := startPreviewWithEnv(t, false,
		"JOBCRON_ENV=production",
		"JOBCRON_SCHEDULER_ENABLED=1",
		"JOBCRON_DAILY_SCRAPE_TIME=not-a-time",
	)
	if preview.url == "" {
		t.Fatal("preview did not start after inherited production and scheduler settings")
	}
}

func TestPreviewRejectsInvalidGeneratedSuffixBeforeDatabaseUse(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:       "not-hex",
		createdbExit: 1,
	})
	if result.err == nil || !strings.Contains(result.output, "invalid generated database suffix") {
		t.Fatalf("invalid suffix result = %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.log, " createdb ") || strings.Contains(result.log, " dropdb ") {
		t.Fatalf("invalid suffix reached database commands:\n%s", result.log)
	}
}

func TestPreviewCreatedbFailureNeverDropsExistingDatabase(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:       "0123456789abcdef",
		createdbExit: 1,
	})
	if result.err == nil {
		t.Fatalf("createdb failure unexpectedly succeeded:\n%s", result.output)
	}
	if !strings.Contains(result.log, " createdb ") {
		t.Fatalf("createdb was not attempted:\n%s", result.log)
	}
	if strings.Contains(result.log, " dropdb ") {
		t.Fatalf("createdb failure attempted to drop a pre-existing database:\n%s", result.log)
	}
}

func TestPreviewRejectsForeignPortOwnerBeforeCreatedb(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix: "0123456789abcdef",
		owner:  "foreign|postgres|foreign-postgres-1",
	})
	if result.err == nil || !strings.Contains(result.output, "refusing foreign or ambiguous owner") {
		t.Fatalf("foreign owner result = %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.log, " createdb ") {
		t.Fatalf("foreign owner reached createdb:\n%s", result.log)
	}
}

func TestPreviewRejectsUnreachableHostPortBeforeCreatedb(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix: "0123456789abcdef",
		ncExit: 1,
	})
	if result.err == nil || !strings.Contains(result.output, "unreachable at 127.0.0.1:55432") {
		t.Fatalf("unreachable host result = %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.log, " createdb ") {
		t.Fatalf("unreachable host reached createdb:\n%s", result.log)
	}
}

func TestPreviewBootstrapUnrelatedFailureNeverSeedsOwner(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:          "0123456789abcdef",
		bootstrapOutput: "unrelated bootstrap failure",
		bootstrapExit:   1,
	})
	if result.err == nil || !strings.Contains(result.output, "failed before the expected missing-owner gate") {
		t.Fatalf("unrelated bootstrap result = %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.log, " psql ") {
		t.Fatalf("unrelated bootstrap failure seeded an owner:\n%s", result.log)
	}
}

func TestPreviewPropagatesStrictPortToBootstrapAndServer(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:          "0123456789abcdef",
		bootstrapOutput: "explicit DATABASE_URL requires exactly one existing user",
		bootstrapExit:   1,
		secondExit:      0,
	})
	if result.err != nil {
		t.Fatalf("preview with fake app: %v\n%s", result.err, result.output)
	}
	if got := strings.Count(result.log, " strict-port=1 "); got != 2 {
		t.Fatalf("strict-port propagation count = %d, want bootstrap and server:\n%s", got, result.log)
	}
	if got := strings.Count(result.log, " environment= scheduler=0 daily= "); got != 2 {
		t.Fatalf("preview environment isolation count = %d, want bootstrap and server:\n%s", got, result.log)
	}
}

func TestPreviewPortRaceFailsInsteadOfFallingBack(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:          "0123456789abcdef",
		bootstrapOutput: "explicit DATABASE_URL requires exactly one existing user",
		bootstrapExit:   1,
		portRace:        true,
	})
	if result.err == nil {
		t.Fatalf("preview succeeded after requested port was occupied:\n%s", result.output)
	}
	if !strings.Contains(result.log, " requested-port-occupied ") {
		t.Fatalf("port-race holder was not installed after preflight:\n%s", result.log)
	}
	if strings.Contains(result.log, " fallback-port ") {
		t.Fatalf("preview served a fallback port after requested-port race:\n%s", result.log)
	}
}

func TestPreviewRejectsBusyRequestedPortBeforeCreatingStateOrDatabase(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold busy loopback port: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:            port,
		suffix:          "0123456789abcdef",
		bootstrapOutput: "unrelated bootstrap failure",
		bootstrapExit:   1,
	})
	if result.err == nil || !strings.Contains(result.output, "requested loopback port is already in use") {
		t.Fatalf("busy port result = %v\n%s", result.err, result.output)
	}
	for _, forbidden := range []string{"jobcron-preview.", " createdb ", " app start "} {
		if strings.Contains(result.log, forbidden) {
			t.Fatalf("busy port reached forbidden operation %q:\n%s", forbidden, result.log)
		}
	}
}

func TestPreviewConcurrentSamePortAllowsOnlyOnePastLock(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatalf("create concurrent fake bin: %v", err)
	}
	logPath := filepath.Join(root, "commands.log")
	writeFakeCommand(t, fakeBin, "nc", "#!/bin/sh\nexit 1\n")
	writeFakeCommand(t, fakeBin, "docker", `#!/bin/sh
printf ' docker %s \n' "$*" >>"$PREVIEW_FAKE_LOG"
if [ "$1" = "compose" ]; then
	sleep 2
	exit 1
fi
exit 1
`)
	port := reserveLoopbackPort(t)
	start := func() (*exec.Cmd, *strings.Builder) {
		output := &strings.Builder{}
		cmd := exec.Command("sh", "scripts/preview-interactive.sh", strconv.Itoa(port))
		cmd.Dir = filepath.Clean(filepath.Join(".."))
		cmd.Env = append(withoutEnv(os.Environ(), "DATABASE_URL", "JOBCRON_PREVIEW_KEEP"),
			"PATH="+fakeBin+":/usr/bin:/bin", "PREVIEW_FAKE_LOG="+logPath)
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start concurrent preview: %v", err)
		}
		return cmd, output
	}
	first, firstOutput := start()
	second, secondOutput := start()
	_ = first.Wait()
	_ = second.Wait()
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read concurrent command log: %v", err)
	}
	logText := string(logBytes)
	if got := strings.Count(logText, " compose "); got != 1 {
		t.Fatalf("same-port previews reached Compose %d times, want 1:\n%s", got, logText)
	}
	combined := firstOutput.String() + secondOutput.String()
	if strings.Count(combined, "requested loopback port is already in use") != 1 {
		t.Fatalf("same-port loser did not get one busy refusal:\n%s", combined)
	}
	if strings.Contains(combined, statePrefix) || strings.Contains(combined, databasePrefix) {
		t.Fatalf("concurrent lock test created preview state/database:\n%s", combined)
	}
	if lockPath := previewLockPath(t, port); pathExists(lockPath) {
		t.Fatalf("same-port lock %q remained after launcher exits", lockPath)
	}
}

func TestPreviewLockReleasedAfterNormalExit(t *testing.T) {
	port := reserveLoopbackPort(t)
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:            port,
		suffix:          "0123456789abcdef",
		bootstrapOutput: "explicit DATABASE_URL requires exactly one existing user",
		bootstrapExit:   1,
		secondExit:      0,
	})
	if result.err != nil {
		t.Fatalf("normal fake preview exit: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.log, " lock observed ") {
		t.Fatalf("normal preview never held its port lock:\n%s", result.log)
	}
	if lockPath := previewLockPath(t, port); pathExists(lockPath) {
		t.Fatalf("normal exit left port lock %q", lockPath)
	}
}

func TestPreviewDifferentPortLocksDoNotConflict(t *testing.T) {
	lockedPort := reserveLoopbackPort(t)
	otherPort := reserveLoopbackPort(t)
	for otherPort == lockedPort {
		otherPort = reserveLoopbackPort(t)
	}
	lockedPath := previewLockPath(t, lockedPort)
	if err := os.MkdirAll(lockedPath, 0o700); err != nil {
		t.Fatalf("create other-port lock: %v", err)
	}
	siblingPort := reserveLoopbackPort(t)
	for siblingPort == lockedPort || siblingPort == otherPort {
		siblingPort = reserveLoopbackPort(t)
	}
	siblingPath := previewLockPath(t, siblingPort)
	if err := os.Mkdir(siblingPath, 0o700); err != nil {
		t.Fatalf("create unrelated sibling lock: %v", err)
	}
	t.Cleanup(func() {
		if !pathExists(siblingPath) {
			t.Errorf("different-port cleanup removed unrelated sibling lock %q", siblingPath)
			return
		}
		_ = os.Remove(siblingPath)
	})
	t.Cleanup(func() { _ = os.Remove(lockedPath) })
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:            otherPort,
		suffix:          "0123456789abcdef",
		bootstrapOutput: "explicit DATABASE_URL requires exactly one existing user",
		bootstrapExit:   1,
		secondExit:      0,
	})
	if result.err != nil {
		t.Fatalf("different port lock blocked preview: %v\n%s", result.err, result.output)
	}
}

func TestPreviewStaleLockPrintsQuotedExecutableRemediationWithoutStealing(t *testing.T) {
	port := reserveLoopbackPort(t)
	lockPath := previewLockPath(t, port)
	if err := os.MkdirAll(lockPath, 0o700); err != nil {
		t.Fatalf("create stale lock: %v", err)
	}
	if err := os.WriteFile(lockPath+".owner.pid", []byte("999999999\n"), 0o600); err != nil {
		t.Fatalf("write stale owner PID: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(lockPath)
		_ = os.Remove(lockPath + ".owner.pid")
	})

	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:   port,
		suffix: "0123456789abcdef",
	})
	if result.err == nil {
		t.Fatalf("stale-lock preview unexpectedly succeeded:\n%s", result.output)
	}
	canonical := fmt.Sprintf("preview: requested loopback port is already in use: 127.0.0.1:%d", port)
	remediation := "preview: stale port lock; remove it manually: rmdir " + shellQuote(lockPath)
	for _, want := range []string{canonical, remediation} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("stale-lock output missing %q:\n%s", want, result.output)
		}
	}
	if !pathExists(lockPath) {
		t.Fatalf("launcher stole stale lock %q", lockPath)
	}
}

func TestPreviewStartupRetryRequiresExactBusyFailure(t *testing.T) {
	exitErr := commandExitError(t)
	canonical := "preview: requested loopback port is already in use: 127.0.0.1:17778"

	t.Run("exact busy output retries to the bound", func(t *testing.T) {
		attempts := 0
		_, err := retryPreviewStartup(3, func() (*previewProcess, error) {
			attempts++
			return nil, &previewStartupError{waitErr: exitErr, output: canonical + "\n", expectedBusy: canonical}
		})
		if err == nil {
			t.Fatal("bounded busy retries unexpectedly succeeded")
		}
		if attempts != 3 {
			t.Fatalf("exact busy attempts = %d, want 3", attempts)
		}
	})

	t.Run("additional output is not retryable", func(t *testing.T) {
		attempts := 0
		_, err := retryPreviewStartup(3, func() (*previewProcess, error) {
			attempts++
			return nil, &previewStartupError{
				waitErr:      exitErr,
				output:       "unrelated failure containing phrase\n" + canonical + "\n",
				expectedBusy: canonical,
			}
		})
		if err == nil {
			t.Fatal("unrelated startup failure unexpectedly succeeded")
		}
		if attempts != 1 {
			t.Fatalf("unrelated startup attempts = %d, want 1", attempts)
		}
	})

	t.Run("matching text without process exit is not retryable", func(t *testing.T) {
		attempts := 0
		_, err := retryPreviewStartup(3, func() (*previewProcess, error) {
			attempts++
			return nil, &previewStartupError{
				waitErr:      fmt.Errorf("not an exit error"),
				output:       canonical,
				expectedBusy: canonical,
			}
		})
		if err == nil {
			t.Fatal("non-process startup failure unexpectedly succeeded")
		}
		if attempts != 1 {
			t.Fatalf("non-process startup attempts = %d, want 1", attempts)
		}
	})
}

func TestPreviewSignalAfterPortLockAcquisitionReleasesLock(t *testing.T) {
	port := reserveLoopbackPort(t)
	lockPath := previewLockPath(t, port)
	t.Cleanup(func() { _ = os.RemoveAll(lockPath) })
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:            port,
		suffix:          "0123456789abcdef",
		signalAfterLock: true,
	})
	if result.err != nil {
		t.Fatalf("signal after lock acquisition: %v\n%s", result.err, result.output)
	}
	if pathExists(lockPath) {
		t.Fatalf("signal in lock ownership window left %q", lockPath)
	}
	for _, forbidden := range []string{" compose ", " createdb ", "jobcron-preview."} {
		if strings.Contains(result.log, forbidden) {
			t.Fatalf("signal after port lock reached forbidden operation %q:\n%s", forbidden, result.log)
		}
	}
}

func TestPreviewSignalAfterCreatedbSuccessDropsOwnedDatabase(t *testing.T) {
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		suffix:         "0123456789abcdef",
		signalCreatedb: true,
	})
	if result.err != nil {
		t.Fatalf("signal after createdb success: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.log, " createdb ") {
		t.Fatalf("signal test never reached createdb:\n%s", result.log)
	}
	if !strings.Contains(result.log, " dropdb ") {
		t.Fatalf("signal in createdb ownership window did not drop the database:\n%s", result.log)
	}
}

func TestPreviewLockRemovalFailurePrintsExecutableCleanup(t *testing.T) {
	port := reserveLoopbackPort(t)
	lockPath := previewLockPath(t, port)
	t.Cleanup(func() { _ = os.RemoveAll(lockPath) })
	result := runPreviewWithFakeCommands(t, fakePreviewConfig{
		port:            port,
		suffix:          "0123456789abcdef",
		bootstrapOutput: "explicit DATABASE_URL requires exactly one existing user",
		bootstrapExit:   1,
		secondExit:      0,
		rmdirFail:       true,
	})
	if result.err == nil {
		t.Fatalf("preview with simulated rmdir failure exited successfully:\n%s", result.output)
	}
	var exitErr *exec.ExitError
	if !errors.As(result.err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("preview with simulated rmdir failure did not exit nonzero: %v\n%s", result.err, result.output)
	}
	if !pathExists(lockPath) {
		t.Fatalf("simulated rmdir failure did not retain lock %q", lockPath)
	}
	manualCommand := "rmdir " + shellQuote(lockPath)
	for _, want := range []string{
		"preview: failed to release port lock: " + lockPath,
		"preview: remove it manually: " + manualCommand,
	} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("rmdir failure output missing %q:\n%s", want, result.output)
		}
	}
	cmd := exec.Command("sh", "-c", manualCommand)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute printed lock cleanup: %v\n%s", err, output)
	}
	if pathExists(lockPath) {
		t.Fatalf("printed cleanup left port lock %q", lockPath)
	}
}

func commandExitError(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit 1").Run()
	if err == nil {
		t.Fatal("expected helper command to fail")
	}
	return err
}

func requirePreviewPostgres(t *testing.T) string {
	t.Helper()
	postgresURL := strings.TrimSpace(os.Getenv("JOBCRON_TEST_POSTGRES_URL"))
	if postgresURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL is not set; preview lifecycle tests require disposable PostgreSQL")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	if output, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("Docker Compose is unavailable: %v: %s", err, output)
	}
	if output, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("Docker daemon is unavailable: %v: %s", err, output)
	}
	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		t.Skipf("PostgreSQL test URL is unavailable: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("PostgreSQL test URL is unreachable: %v", err)
	}
	return postgresURL
}

func runPreviewWithFakeCommands(t *testing.T, cfg fakePreviewConfig) fakePreviewResult {
	t.Helper()
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatalf("create fake command directory: %v", err)
	}
	logPath := filepath.Join(root, "commands.log")
	writeFakeCommand(t, fakeBin, "docker", `#!/bin/sh
printf ' docker %s \n' "$*" >>"$PREVIEW_FAKE_LOG"
if [ "$1" = "ps" ]; then
	printf '%s\n' "$PREVIEW_FAKE_OWNER"
	exit 0
fi
if [ "$1" = "compose" ]; then
	if [ -n "$PREVIEW_EXPECT_LOCK" ] && [ -d "$PREVIEW_EXPECT_LOCK" ]; then
		printf ' lock observed \n' >>"$PREVIEW_FAKE_LOG"
	fi
	case " $* " in
		*" createdb "*)
			if [ "$PREVIEW_FAKE_CREATEDB_EXIT" -eq 0 ] && [ "$PREVIEW_SIGNAL_CREATEDB" = "1" ]; then
				kill -TERM "$PPID"
			fi
			exit "$PREVIEW_FAKE_CREATEDB_EXIT"
			;;
		*" psql "*) exit 0 ;;
		*" dropdb "*) exit 0 ;;
	esac
	exit 0
fi
exit 1
`)
	writeFakeCommand(t, fakeBin, "mkdir", `#!/bin/sh
last=
for arg do
	last=$arg
done
"$PREVIEW_REAL_MKDIR" "$@"
rc=$?
case "$last" in
	*.lock)
		if [ "$rc" -eq 0 ] && [ "$PREVIEW_SIGNAL_AFTER_LOCK" = "1" ]; then
			kill -TERM "$PPID"
		fi
		;;
esac
exit "$rc"
`)
	writeFakeCommand(t, fakeBin, "rmdir", `#!/bin/sh
if [ "$PREVIEW_FAKE_RMDIR_FAIL" = "1" ]; then
	exit 1
fi
exec "$PREVIEW_REAL_RMDIR" "$@"
`)
	writeFakeCommand(t, fakeBin, "nc", `#!/bin/sh
printf ' nc %s \n' "$*" >>"$PREVIEW_FAKE_LOG"
if [ "$3" != "55432" ]; then
	exec "$PREVIEW_REAL_NC" "$@"
fi
exit "$PREVIEW_FAKE_NC_EXIT"
`)
	writeFakeCommand(t, fakeBin, "od", `#!/bin/sh
printf '%s\n' "$PREVIEW_FAKE_SUFFIX"
`)
	writeFakeCommand(t, fakeBin, "go", `#!/bin/sh
printf ' go %s \n' "$*" >>"$PREVIEW_FAKE_LOG"
out=
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-o" ]; then
		shift
		out=$1
		break
	fi
	shift
done
if [ -z "$out" ]; then
	exit 2
fi
	printf '%s\n' '#!/bin/sh' 'printf " app start strict-port=%s environment=%s scheduler=%s daily=%s \\n" "${JOBCRON_STRICT_PORT:-}" "${JOBCRON_ENV:-}" "${JOBCRON_SCHEDULER_ENABLED:-}" "${JOBCRON_DAILY_SCRAPE_TIME:-}" >>"$PREVIEW_FAKE_LOG"' 'if [ ! -e "$PREVIEW_FAKE_APP_COUNT" ]; then' '  : >"$PREVIEW_FAKE_APP_COUNT"' '  printf "%s\\n" "$PREVIEW_FAKE_BOOTSTRAP_OUTPUT" >&2' '  exit "$PREVIEW_FAKE_BOOTSTRAP_EXIT"' 'fi' 'if [ "$PREVIEW_FAKE_PORT_RACE" = "1" ]; then' '  "$PREVIEW_REAL_NC" -l 127.0.0.1 "$JOBCRON_PORT" >/dev/null 2>&1 &' '  occupier=$!' '  tries=0' '  until "$PREVIEW_REAL_NC" -z 127.0.0.1 "$JOBCRON_PORT" >/dev/null 2>&1; do tries=$((tries + 1)); if [ "$tries" -ge 50 ]; then kill "$occupier" 2>/dev/null || true; exit 97; fi; sleep 0.02; done' '  printf " requested-port-occupied \\n" >>"$PREVIEW_FAKE_LOG"' '  if [ "${JOBCRON_STRICT_PORT:-}" != "1" ]; then printf " fallback-port \\n" >>"$PREVIEW_FAKE_LOG"; kill "$occupier" 2>/dev/null || true; wait "$occupier" 2>/dev/null || true; exit 0; fi' '  kill "$occupier" 2>/dev/null || true' '  wait "$occupier" 2>/dev/null || true' '  exit 98' 'fi' 'exit "$PREVIEW_FAKE_SECOND_EXIT"' >"$out"
chmod +x "$out"
`)
	writeFakeCommand(t, fakeBin, "mktemp", `#!/bin/sh
printf ' mktemp %s \n' "$*" >>"$PREVIEW_FAKE_LOG"
exec /usr/bin/mktemp "$@"
`)
	owner := cfg.owner
	if owner == "" {
		owner = "jobcron-local|postgres|jobcron-local-postgres-1"
	}
	port := cfg.port
	if port == 0 {
		port = reserveLoopbackPort(t)
	}
	realNC, err := exec.LookPath("nc")
	if err != nil {
		t.Fatalf("find real nc command: %v", err)
	}
	realMkdir, err := exec.LookPath("mkdir")
	if err != nil {
		t.Fatalf("find real mkdir command: %v", err)
	}
	realRmdir, err := exec.LookPath("rmdir")
	if err != nil {
		t.Fatalf("find real rmdir command: %v", err)
	}
	cmd := exec.Command("sh", "scripts/preview-interactive.sh", strconv.Itoa(port))
	cmd.Dir = filepath.Clean(filepath.Join(".."))
	cmd.Env = append(withoutEnv(os.Environ(),
		"DATABASE_URL", "JOBCRON_PREVIEW_KEEP", "JOBCRON_ENV",
		"JOBCRON_SCHEDULER_ENABLED", "JOBCRON_DAILY_SCRAPE_TIME"),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"TMPDIR="+root,
		"JOBCRON_ENV=production",
		"JOBCRON_SCHEDULER_ENABLED=1",
		"JOBCRON_DAILY_SCRAPE_TIME=not-a-time",
		"PREVIEW_FAKE_LOG="+logPath,
		"PREVIEW_FAKE_SUFFIX="+cfg.suffix,
		"PREVIEW_FAKE_OWNER="+owner,
		"PREVIEW_FAKE_NC_EXIT="+strconv.Itoa(cfg.ncExit),
		"PREVIEW_FAKE_CREATEDB_EXIT="+strconv.Itoa(cfg.createdbExit),
		"PREVIEW_FAKE_BOOTSTRAP_OUTPUT="+cfg.bootstrapOutput,
		"PREVIEW_FAKE_BOOTSTRAP_EXIT="+strconv.Itoa(cfg.bootstrapExit),
		"PREVIEW_FAKE_SECOND_EXIT="+strconv.Itoa(cfg.secondExit),
		"PREVIEW_FAKE_APP_COUNT="+filepath.Join(root, "app-count"),
		"PREVIEW_EXPECT_LOCK="+previewLockPath(t, port),
		"PREVIEW_REAL_NC="+realNC,
		"PREVIEW_REAL_MKDIR="+realMkdir,
		"PREVIEW_REAL_RMDIR="+realRmdir,
		"PREVIEW_SIGNAL_AFTER_LOCK="+boolEnv(cfg.signalAfterLock),
		"PREVIEW_SIGNAL_CREATEDB="+boolEnv(cfg.signalCreatedb),
		"PREVIEW_FAKE_RMDIR_FAIL="+boolEnv(cfg.rmdirFail),
		"PREVIEW_FAKE_PORT_RACE="+boolEnv(cfg.portRace),
	)
	output, err := cmd.CombinedOutput()
	logBytes, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read fake command log: %v", readErr)
	}
	return fakePreviewResult{output: string(output), log: string(logBytes), err: err}
}

func boolEnv(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func writeFakeCommand(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake %s command: %v", name, err)
	}
}

func startPreview(t *testing.T, keep bool) *previewProcess {
	t.Helper()
	return startPreviewWithEnv(t, keep)
}

func startPreviewWithEnv(t *testing.T, keep bool, extraEnv ...string) *previewProcess {
	t.Helper()
	const maxAttempts = 3
	preview, err := retryPreviewStartup(maxAttempts, func() (*previewProcess, error) {
		return startPreviewAttempt(t, keep, extraEnv...)
	})
	if err != nil {
		t.Fatalf("preview startup: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-preview.done:
		default:
			preview.stop(t)
		}
	})
	waitForPreview(t, preview)
	return preview
}

func retryPreviewStartup(maxAttempts int, attempt func() (*previewProcess, error)) (*previewProcess, error) {
	var lastErr error
	for try := 1; try <= maxAttempts; try++ {
		preview, err := attempt()
		if err == nil {
			return preview, nil
		}
		lastErr = err
		if !isRetryablePreviewStartup(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("preview startup exhausted %d busy-port attempts: %w", maxAttempts, lastErr)
}

func isRetryablePreviewStartup(err error) bool {
	var startupErr *previewStartupError
	if !errors.As(err, &startupErr) {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(startupErr.waitErr, &exitErr) &&
		strings.TrimSpace(startupErr.output) == startupErr.expectedBusy
}

func startPreviewAttempt(t *testing.T, keep bool, extraEnv ...string) (*previewProcess, error) {
	t.Helper()
	port := reserveLoopbackPort(t)
	expectedBusy := fmt.Sprintf("preview: requested loopback port is already in use: 127.0.0.1:%d", port)
	cmd := exec.Command("sh", "scripts/preview-interactive.sh", strconv.Itoa(port))
	cmd.Dir = filepath.Clean(filepath.Join(".."))
	cmd.Env = withoutEnv(os.Environ(), "DATABASE_URL", "JOBCRON_PREVIEW_KEEP")
	if keep {
		cmd.Env = append(cmd.Env, "JOBCRON_PREVIEW_KEEP=1")
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start preview: %w", err)
	}
	preview := &previewProcess{
		cmd:      cmd,
		done:     make(chan struct{}),
		scanDone: make(chan struct{}),
		lines:    make(chan string, 256),
	}
	go func() {
		err := cmd.Wait()
		preview.mu.Lock()
		preview.waitErr = err
		preview.mu.Unlock()
		_ = writer.Close()
		close(preview.done)
	}()
	go preview.scan(reader)

	deadline := time.After(90 * time.Second)
	for preview.stateDir == "" || preview.database == "" || preview.keyPath == "" || preview.url == "" {
		select {
		case line := <-preview.lines:
			switch {
			case strings.HasPrefix(line, statePrefix):
				preview.stateDir = strings.TrimSpace(strings.TrimPrefix(line, statePrefix))
			case strings.HasPrefix(line, databasePrefix):
				preview.database = strings.TrimSpace(strings.TrimPrefix(line, databasePrefix))
			case strings.HasPrefix(line, keyPrefix):
				preview.keyPath = strings.TrimSpace(strings.TrimPrefix(line, keyPrefix))
			case strings.HasPrefix(line, urlPrefix):
				preview.url = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, urlPrefix)), "/")
			}
		case <-preview.done:
			<-preview.scanDone
			return nil, &previewStartupError{
				waitErr:      preview.waitError(),
				output:       preview.combinedOutput(),
				expectedBusy: expectedBusy,
			}
		case <-deadline:
			_ = preview.cmd.Process.Kill()
			<-preview.done
			<-preview.scanDone
			return nil, fmt.Errorf("timed out waiting for preview metadata:\n%s", preview.combinedOutput())
		}
	}
	return preview, nil
}

func (p *previewProcess) scan(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		p.mu.Lock()
		p.output = append(p.output, line)
		p.mu.Unlock()
		p.lines <- line
	}
	close(p.lines)
	close(p.scanDone)
}

func (p *previewProcess) stop(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal preview: %v", err)
	}
	select {
	case <-p.done:
		<-p.scanDone
		err := p.waitError()
		if err != nil {
			t.Fatalf("preview exit after signal: %v\n%s", err, p.combinedOutput())
		}
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatalf("preview did not exit after interrupt:\n%s", p.combinedOutput())
	}
}

func (p *previewProcess) waitError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *previewProcess) combinedOutput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.output, "\n")
}

func waitForPreview(t *testing.T, preview *previewProcess) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(20 * time.Second)
	for {
		response, err := client.Get(preview.url + "/profile")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("preview never became reachable at %s: %v\n%s", preview.url, err, preview.combinedOutput())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}
	return port
}

func previewPort(t *testing.T, rawURL string) int {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse preview URL %q: %v", rawURL, err)
	}
	_, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split preview URL host %q: %v", parsed.Host, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse preview port %q: %v", portText, err)
	}
	return port
}

func previewLockPath(t *testing.T, port int) string {
	t.Helper()
	output, err := exec.Command("id", "-u").Output()
	if err != nil {
		t.Fatalf("resolve current user ID: %v", err)
	}
	uid := strings.TrimSpace(string(output))
	if uid == "" {
		t.Fatal("current user ID is empty")
	}
	return filepath.Join("/tmp", "jobcron-preview-locks-"+uid, fmt.Sprintf("port-%d.lock", port))
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func withoutEnv(environ []string, names ...string) []string {
	blocked := make(map[string]bool, len(names))
	for _, name := range names {
		blocked[name] = true
	}
	result := make([]string, 0, len(environ))
	for _, item := range environ {
		name, _, _ := strings.Cut(item, "=")
		if !blocked[name] {
			result = append(result, item)
		}
	}
	return result
}

func databaseURL(t *testing.T, baseURL, database string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

func databaseExists(t *testing.T, postgresURL, database string) bool {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL(t, postgresURL, "postgres"))
	if err != nil {
		t.Fatalf("open PostgreSQL admin: %v", err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, database).Scan(&exists); err != nil {
		t.Fatalf("query database %q existence: %v", database, err)
	}
	return exists
}

func profileMarkerCount(t *testing.T, postgresURL, database, marker string) int {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL(t, postgresURL, database))
	if err != nil {
		t.Fatalf("open database %q: %v", database, err)
	}
	defer db.Close()
	var profilesTableExists bool
	if err := db.QueryRow(`SELECT to_regclass('public.profiles') IS NOT NULL`).Scan(&profilesTableExists); err != nil {
		t.Fatalf("query profiles table in %q: %v", database, err)
	}
	if !profilesTableExists {
		return 0
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM profiles WHERE profile_json LIKE $1`, "%"+marker+"%").Scan(&count); err != nil {
		t.Fatalf("query profile marker in %q: %v", database, err)
	}
	return count
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
