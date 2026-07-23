package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ohchanwu/jobcron/internal/appdata"
	"github.com/ohchanwu/jobcron/internal/config"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/localdb"
)

type fakeRuntimeUserStore struct {
	managedID    int64
	managedErr   error
	userIDs      []int64
	userIDsErr   error
	managedCalls int
	userIDsCalls int
	closeErr     error
	closeCalls   int
}

func (s *fakeRuntimeUserStore) EnsureManagedLocalOwner(context.Context) (int64, error) {
	s.managedCalls++
	return s.managedID, s.managedErr
}

func (s *fakeRuntimeUserStore) UserIDs(context.Context) ([]int64, error) {
	s.userIDsCalls++
	return s.userIDs, s.userIDsErr
}

func (s *fakeRuntimeUserStore) Close() error {
	s.closeCalls++
	return s.closeErr
}

func stubRuntimeDependencies(
	t *testing.T,
	ensure func(context.Context) (string, error),
	loadKey func() ([]byte, error),
	openStore func(string) (runtimeUserStore, error),
) {
	t.Helper()
	oldEnsure := ensureLocalPostgres
	oldLoadKey := loadLocalMasterKey
	oldOpenStore := openRuntimeUserStore
	ensureLocalPostgres = ensure
	loadLocalMasterKey = loadKey
	openRuntimeUserStore = openStore
	t.Cleanup(func() {
		ensureLocalPostgres = oldEnsure
		loadLocalMasterKey = oldLoadKey
		openRuntimeUserStore = oldOpenStore
	})
}

func TestExplicitDatabaseURLNeverInvokesDocker(t *testing.T) {
	const explicitURL = "postgres://explicit.example.invalid/jobcron"
	ensureCalls := 0
	openedURL := ""
	store := &fakeRuntimeUserStore{userIDs: []int64{41}}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) {
			ensureCalls++
			return "", errors.New("managed local startup must be bypassed")
		},
		func() ([]byte, error) { return bytes.Repeat([]byte{0x11}, credential.MasterKeyBytes), nil },
		func(databaseURL string) (runtimeUserStore, error) {
			openedURL = databaseURL
			return store, nil
		},
	)

	runtime, err := resolvePostgresRuntime(context.Background(), config.Config{DatabaseURL: explicitURL})
	if err != nil {
		t.Fatalf("resolvePostgresRuntime: %v", err)
	}
	if ensureCalls != 0 {
		t.Fatalf("local Ensure calls = %d, want 0", ensureCalls)
	}
	if openedURL != explicitURL || runtime.DatabaseURL != explicitURL {
		t.Fatalf("selected URLs = opened %q runtime %q, want explicit URL", openedURL, runtime.DatabaseURL)
	}
	if runtime.ManagedLocal {
		t.Fatal("ManagedLocal = true for explicit URL")
	}
	if runtime.UserID != 41 {
		t.Fatalf("UserID = %d, want 41", runtime.UserID)
	}
	if store.userIDsCalls != 1 {
		t.Fatalf("UserIDs calls = %d, want 1", store.userIDsCalls)
	}
}

func TestResolveRuntimeReturnsStoreCloseErrorAfterSuccessfulResolution(t *testing.T) {
	store := &fakeRuntimeUserStore{
		userIDs:  []int64{41},
		closeErr: errors.New("close runtime owner store"),
	}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x11}, credential.MasterKeyBytes), nil },
		func(string) (runtimeUserStore, error) { return store, nil },
	)

	_, err := resolvePostgresRuntime(context.Background(), config.Config{
		DatabaseURL: "postgres://explicit.example.invalid/jobcron",
	})
	if err == nil || err.Error() != "close PostgreSQL runtime store: close runtime owner store" {
		t.Fatalf("resolvePostgresRuntime error = %v, want runtime-store close error", err)
	}
	if store.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", store.closeCalls)
	}
}

func TestResolveRuntimeWrapsOpenErrorAsRuntimeStore(t *testing.T) {
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
		func() ([]byte, error) { return nil, errors.New("unexpected local key load") },
		func(string) (runtimeUserStore, error) { return nil, errors.New("dial failed") },
	)

	_, err := resolvePostgresRuntime(context.Background(), config.Config{
		Production:              true,
		DatabaseURL:             "postgres://production.example.invalid/jobcron",
		CredentialEncryptionKey: bytes.Repeat([]byte{0x44}, credential.MasterKeyBytes),
	})
	if err == nil || err.Error() != "open PostgreSQL runtime store: dial failed" {
		t.Fatalf("resolvePostgresRuntime error = %v, want runtime-store open error", err)
	}
}

func TestResolveRuntimePreservesOwnerErrorWhenStoreCloseAlsoFails(t *testing.T) {
	store := &fakeRuntimeUserStore{
		userIDsErr: errors.New("resolve owner failed"),
		closeErr:   errors.New("close runtime owner store"),
	}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x11}, credential.MasterKeyBytes), nil },
		func(string) (runtimeUserStore, error) { return store, nil },
	)

	_, err := resolvePostgresRuntime(context.Background(), config.Config{
		DatabaseURL: "postgres://explicit.example.invalid/jobcron",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve owner failed") {
		t.Fatalf("resolvePostgresRuntime error = %v, want original owner error", err)
	}
	if strings.Contains(err.Error(), "close runtime owner store") {
		t.Fatalf("resolvePostgresRuntime error = %v, must not obscure original error", err)
	}
	if store.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", store.closeCalls)
	}
}

func TestResolveRuntimeProductionRequiresExplicitURL(t *testing.T) {
	ensureCalls, keyCalls, openCalls := 0, 0, 0
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { ensureCalls++; return "", nil },
		func() ([]byte, error) { keyCalls++; return nil, nil },
		func(string) (runtimeUserStore, error) { openCalls++; return nil, nil },
	)

	_, err := resolvePostgresRuntime(context.Background(), config.Config{Production: true})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("resolvePostgresRuntime error = %v, want DATABASE_URL requirement", err)
	}
	if ensureCalls != 0 || keyCalls != 0 || openCalls != 0 {
		t.Fatalf("dependency calls = ensure %d key %d open %d, want all zero", ensureCalls, keyCalls, openCalls)
	}
}

func TestResolveRuntimeLocalWithoutURLUsesManagedPostgres(t *testing.T) {
	const managedURL = localdb.DatabaseURL
	ensureCalls := 0
	store := &fakeRuntimeUserStore{managedID: 52}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { ensureCalls++; return managedURL, nil },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x22}, credential.MasterKeyBytes), nil },
		func(databaseURL string) (runtimeUserStore, error) {
			if databaseURL != managedURL {
				t.Fatalf("opened URL = %q, want managed URL", databaseURL)
			}
			return store, nil
		},
	)

	runtime, err := resolvePostgresRuntime(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("resolvePostgresRuntime: %v", err)
	}
	if ensureCalls != 1 || !runtime.ManagedLocal || runtime.DatabaseURL != managedURL {
		t.Fatalf("runtime URL = %q managed = %v ensure calls = %d, want managed URL and one Ensure",
			runtime.DatabaseURL, runtime.ManagedLocal, ensureCalls)
	}
	if store.managedCalls != 1 || store.userIDsCalls != 0 {
		t.Fatalf("user calls = managed %d list %d, want 1 and 0", store.managedCalls, store.userIDsCalls)
	}
}

func TestResolveRuntimeManagedOwnerIsLimitedToFixedDatabase(t *testing.T) {
	openCalls := 0
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return "postgres://foreign.invalid/jobcron", nil },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x23}, credential.MasterKeyBytes), nil },
		func(string) (runtimeUserStore, error) {
			openCalls++
			return &fakeRuntimeUserStore{managedID: 53}, nil
		},
	)

	_, err := resolvePostgresRuntime(context.Background(), config.Config{})
	if err == nil || !strings.Contains(err.Error(), "unexpected database URL") {
		t.Fatalf("resolvePostgresRuntime error = %v, want fixed-database refusal", err)
	}
	if openCalls != 0 {
		t.Fatalf("runtime store open calls = %d, want 0", openCalls)
	}
}

func TestResolveRuntimeLocalLoadsProtectedMasterKey(t *testing.T) {
	keyCalls := 0
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return localdb.DatabaseURL, nil },
		func() ([]byte, error) {
			keyCalls++
			return bytes.Repeat([]byte{0x33}, credential.MasterKeyBytes), nil
		},
		func(string) (runtimeUserStore, error) { return &fakeRuntimeUserStore{managedID: 63}, nil },
	)

	runtime, err := resolvePostgresRuntime(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("resolvePostgresRuntime: %v", err)
	}
	if keyCalls != 1 {
		t.Fatalf("local master-key loads = %d, want 1", keyCalls)
	}
	if len(runtime.CredentialEncryptionKey) != credential.MasterKeyBytes {
		t.Fatalf("master-key length = %d, want %d", len(runtime.CredentialEncryptionKey), credential.MasterKeyBytes)
	}
}

func TestResolveRuntimeProductionNeverCreatesLocalMasterKey(t *testing.T) {
	configuredKey := bytes.Repeat([]byte{0x44}, credential.MasterKeyBytes)
	keyCalls := 0
	store := &fakeRuntimeUserStore{userIDs: []int64{73}}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
		func() ([]byte, error) { keyCalls++; return nil, errors.New("unexpected local key load") },
		func(string) (runtimeUserStore, error) { return store, nil },
	)

	runtime, err := resolvePostgresRuntime(context.Background(), config.Config{
		Production:              true,
		DatabaseURL:             "postgres://production.example.invalid/jobcron",
		CredentialEncryptionKey: configuredKey,
	})
	if err != nil {
		t.Fatalf("resolvePostgresRuntime: %v", err)
	}
	if keyCalls != 0 {
		t.Fatalf("local master-key loads = %d, want 0", keyCalls)
	}
	if len(runtime.CredentialEncryptionKey) != credential.MasterKeyBytes {
		t.Fatalf("master-key length = %d, want %d", len(runtime.CredentialEncryptionKey), credential.MasterKeyBytes)
	}
	if runtime.UserID != 0 || runtime.ManagedLocal {
		t.Fatalf("production runtime user ID = %d managed = %v, want zero and false",
			runtime.UserID, runtime.ManagedLocal)
	}
	if store.userIDsCalls != 0 || store.managedCalls != 0 {
		t.Fatalf("production user calls = list %d managed %d, want both zero",
			store.userIDsCalls, store.managedCalls)
	}
}

func TestProductionExplicitURLAllowsEmptyOrMultipleUsers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store *fakeRuntimeUserStore
	}{
		{name: "zero users", store: &fakeRuntimeUserStore{}},
		{name: "multiple users", store: &fakeRuntimeUserStore{userIDs: []int64{7, 8}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubRuntimeDependencies(t,
				func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
				func() ([]byte, error) { return nil, errors.New("unexpected local key load") },
				func(string) (runtimeUserStore, error) { return tc.store, nil },
			)
			runtime, err := resolvePostgresRuntime(context.Background(), config.Config{
				Production:              true,
				DatabaseURL:             "postgres://production.example.invalid/jobcron",
				CredentialEncryptionKey: bytes.Repeat([]byte{0x45}, credential.MasterKeyBytes),
			})
			if err != nil {
				t.Fatalf("resolvePostgresRuntime: %v", err)
			}
			if runtime.UserID != 0 {
				t.Fatalf("UserID = %d, want 0", runtime.UserID)
			}
			if tc.store.userIDsCalls != 0 || tc.store.managedCalls != 0 {
				t.Fatalf("production user calls = list %d managed %d, want both zero",
					tc.store.userIDsCalls, tc.store.managedCalls)
			}
		})
	}
}

func TestMainWiresRuntimeModeAndCohortConfiguration(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"if cfg.Production {",
		"srv = server.New(store, sources...)",
		"srv = server.NewForLocalUser(store, resolved.UserID, sources...)",
		"srv.SetSignupAccessCode(cfg.SignupAccessCode)",
		"srv.SetStage1SponsorUserID(cfg.Stage1SponsorUserID)",
		"if _, err := srv.RescoreUsers",
	} {
		if !bytes.Contains(source, []byte(want)) {
			t.Errorf("main.go missing %q", want)
		}
	}
	for _, setter := range [][]byte{
		[]byte("srv.SetSignupAccessCode"),
		[]byte("srv.SetStage1SponsorUserID"),
	} {
		if bytes.Index(source, setter) > bytes.Index(source, []byte("server.StartScheduler")) {
			t.Errorf("%s is configured after scheduler startup", setter)
		}
	}
}

func TestManagedLocalStartupUsesStablePositiveUserID(t *testing.T) {
	store := &fakeRuntimeUserStore{managedID: 74}
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return localdb.DatabaseURL, nil },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x55}, credential.MasterKeyBytes), nil },
		func(string) (runtimeUserStore, error) { return store, nil },
	)

	first, err := resolvePostgresRuntime(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("first resolvePostgresRuntime: %v", err)
	}
	second, err := resolvePostgresRuntime(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("second resolvePostgresRuntime: %v", err)
	}
	if first.UserID <= 0 || second.UserID != first.UserID {
		t.Fatalf("managed user IDs = %d, %d; want stable positive ID", first.UserID, second.UserID)
	}
	if store.managedCalls != 2 || store.userIDsCalls != 0 {
		t.Fatalf("user calls = managed %d list %d, want 2 and 0", store.managedCalls, store.userIDsCalls)
	}
}

func TestExplicitURLRefusesAmbiguousUsers(t *testing.T) {
	tests := []struct {
		name    string
		store   *fakeRuntimeUserStore
		wantErr string
	}{
		{name: "zero users", store: &fakeRuntimeUserStore{}, wantErr: "exactly one existing user"},
		{name: "zero ID", store: &fakeRuntimeUserStore{userIDs: []int64{0}}, wantErr: "positive user ID"},
		{name: "multiple users", store: &fakeRuntimeUserStore{userIDs: []int64{1, 2}}, wantErr: "exactly one existing user"},
		{name: "list error", store: &fakeRuntimeUserStore{userIDsErr: errors.New("list users failed")}, wantErr: "list users failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubRuntimeDependencies(t,
				func(context.Context) (string, error) { return "", errors.New("unexpected Ensure") },
				func() ([]byte, error) { return bytes.Repeat([]byte{0x66}, credential.MasterKeyBytes), nil },
				func(string) (runtimeUserStore, error) { return tt.store, nil },
			)
			_, err := resolvePostgresRuntime(context.Background(), config.Config{
				DatabaseURL: "postgres://explicit.example.invalid/jobcron",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolvePostgresRuntime error = %v, want %q", err, tt.wantErr)
			}
			if tt.store.managedCalls != 0 || tt.store.userIDsCalls != 1 {
				t.Fatalf("user calls = managed %d list %d, want 0 and 1",
					tt.store.managedCalls, tt.store.userIDsCalls)
			}
		})
	}
}

func TestListenFallsBackWhenPortBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	ln, addr, err := listen("127.0.0.1", busyPort, false)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if addr == busy.Addr().String() {
		t.Errorf("listen returned the busy address %s; should have fallen back", addr)
	}
}

func TestListenStrictRefusesBusyPreferredPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	ln, addr, err := listen("127.0.0.1", busyPort, true)
	if err == nil {
		ln.Close()
		t.Fatalf("listen strict returned %q, want busy-port error", addr)
	}
	if ln != nil || addr != "" {
		t.Fatalf("listen strict = (%v, %q, %v), want no listener/address", ln, addr, err)
	}
}

func TestListenUsesConfiguredHost(t *testing.T) {
	ln, addr, err := listen("0.0.0.0", 0, false)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	if host != "0.0.0.0" {
		t.Fatalf("host = %q, want 0.0.0.0", host)
	}
}

func TestNormalLocalStartupDoesNotCreateSQLite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("APPDATA", filepath.Join(root, "AppData", "Roaming"))
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { return localdb.DatabaseURL, nil },
		func() ([]byte, error) { return bytes.Repeat([]byte{0x72}, credential.MasterKeyBytes), nil },
		func(string) (runtimeUserStore, error) { return &fakeRuntimeUserStore{managedID: 81}, nil },
	)
	if _, err := resolvePostgresRuntime(context.Background(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		path := filepath.Join(appdata.Dir(configRoot), "jobs.db"+suffix)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("normal local startup created legacy SQLite artifact %s: %v", path, err)
		}
	}
}

func TestHelpPrintsFlagsAndDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("APPDATA", filepath.Join(root, "AppData", "Roaming"))
	t.Setenv("JOBCRON_ENV", "production")
	runtimeCalls := 0
	stubRuntimeDependencies(t,
		func(context.Context) (string, error) { runtimeCalls++; return "", errors.New("unexpected Ensure") },
		func() ([]byte, error) { runtimeCalls++; return nil, errors.New("unexpected key load") },
		func(string) (runtimeUserStore, error) {
			runtimeCalls++
			return nil, errors.New("unexpected store open")
		},
	)

	oldArgs := os.Args
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"jobcron", "--help"}
	os.Stdout = writeEnd
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})
	main()
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	helpBytes, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}
	help := string(helpBytes)
	for _, want := range []string{"Usage: jobcron [flags]", "-db", "-port", "-no-open", "-worknet-api-key"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
	if runtimeCalls != 0 {
		t.Fatalf("runtime dependency calls during help = %d, want 0", runtimeCalls)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(appdata.Dir(configRoot)); !os.IsNotExist(err) {
		t.Fatalf("help touched application data: %v", err)
	}
}

func TestVersionDoesNotPrepareApplicationData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("APPDATA", filepath.Join(root, "AppData", "Roaming"))

	configRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyFile := filepath.Join(appdata.LegacyDir(configRoot), "nested", "jobs.db")
	if err := os.MkdirAll(filepath.Dir(legacyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"jobcron", "--version"}
	t.Cleanup(func() { os.Args = oldArgs })
	main()

	got, err := os.ReadFile(legacyFile)
	if err != nil || string(got) != "database" {
		t.Fatalf("legacy data after --version = %q, err = %v", got, err)
	}
	if _, err := os.Stat(appdata.Dir(configRoot)); !os.IsNotExist(err) {
		t.Fatalf("canonical path changed by --version: %v", err)
	}
}
