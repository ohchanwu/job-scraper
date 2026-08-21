// Command jobcron runs the 신입 IT Job Briefing — a local web app that
// scrapes several Korean job boards (점핏, 랠릿, 데모데이, the 그리팅 Korean-ATS
// tenants, the Greenhouse company boards 당근·크래프톤·몰로코·센드버드, and optionally
// 워크넷), scores new-grad IT job postings against a user profile, and renders a
// calm one-page daily briefing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/browser"

	"github.com/ohchanwu/jobcron/internal/config"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/localdb"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/scraper/demoday"
	"github.com/ohchanwu/jobcron/internal/scraper/greenhouse"
	"github.com/ohchanwu/jobcron/internal/scraper/greeting"
	"github.com/ohchanwu/jobcron/internal/scraper/jumpit"
	"github.com/ohchanwu/jobcron/internal/scraper/rallit"
	"github.com/ohchanwu/jobcron/internal/scraper/worknet"
	"github.com/ohchanwu/jobcron/internal/server"
	"github.com/ohchanwu/jobcron/internal/storage"
)

// version is the build version, overridden by GoReleaser via -ldflags.
var version = "dev"

type runtimeStorage struct {
	DatabaseURL             string
	CredentialEncryptionKey []byte
	ManagedLocal            bool
	UserID                  int64
}

type runtimeUserStore interface {
	EnsureManagedLocalOwner(context.Context) (int64, error)
	UserIDs(context.Context) ([]int64, error)
	Close() error
}

var (
	ensureLocalPostgres  = localdb.Ensure
	loadLocalMasterKey   = credential.LoadOrCreateLocalMasterKey
	openRuntimeUserStore = func(databaseURL string) (runtimeUserStore, error) {
		return storage.OpenPostgres(databaseURL)
	}
	openMigratingRuntimeUserStore = func(databaseURL string) (runtimeUserStore, error) {
		return storage.OpenPostgresMigrating(context.Background(), databaseURL)
	}
)

func main() {
	cfg, err := config.Load(os.Args[1:], environMap(os.Environ()))
	if err != nil {
		log.Fatalf("jobcron: %v", err)
	}
	if cfg.ShowHelp {
		config.WriteHelp(os.Stdout)
		return
	}
	if cfg.ShowVersion {
		fmt.Println("jobcron", version)
		return
	}
	var resolved runtimeStorage
	var store *storage.Store
	if cfg.DBPath != "" {
		store, err = storage.OpenSQLiteAt(cfg.DBPath)
	} else {
		resolved, err = resolvePostgresRuntime(context.Background(), cfg)
		if err == nil {
			store, err = openPostgresRuntime(resolved)
		}
	}
	if err != nil {
		log.Fatalf("jobcron: %v", err)
	}
	defer store.Close()

	// 당근·크래프톤·몰로코·센드버드 all ride the shared Greenhouse adapter — each
	// is one company board registered as its own source (own badge + toggle).
	// 그리팅 is a single aggregator source over a curated Korean-ATS tenant list.
	sources := []scraper.Scraper{
		jumpit.New(), rallit.New(), demoday.New(), greeting.New(),
		greenhouse.Daangn(), greenhouse.Krafton(), greenhouse.Moloco(), greenhouse.Sendbird(),
	}
	if cfg.WorknetKey != "" {
		wn, err := worknet.New(cfg.WorknetKey)
		if err != nil {
			log.Fatalf("jobcron: %v", err)
		}
		sources = append(sources, wn)
	} else {
		fmt.Println("jobcron: 워크넷 key가 없어 워크넷 출처는 꺼져 있어요",
			"(점핏·랠릿·데모데이·그리팅·당근·크래프톤·몰로코·센드버드는 켜져 있어요).",
			"워크넷도 보려면 --worknet-api-key 플래그나 JOBCRON_WORKNET_KEY 환경변수를 설정하세요.")
	}
	var srv *server.Server
	if store.Dialect() == storage.DialectSQLite {
		srv = server.New(store, sources...)
	} else if cfg.Production {
		srv = server.New(store, sources...)
	} else {
		srv = server.NewForLocalUser(store, resolved.UserID, sources...)
	}
	srv.SetSessionSecret(cfg.SessionSecret)
	srv.SetDemoMode(cfg.Demo)
	srv.SetProductionMode(cfg.Production)
	srv.SetSignupAccessCode(cfg.SignupAccessCode)
	srv.SetStage1SponsorUserID(cfg.Stage1SponsorUserID)
	srv.SetAdminToken(cfg.AdminToken)
	srv.SetProxySecret(cfg.ProxySecret)
	if store.Dialect() != storage.DialectSQLite {
		credentialCipher, err := credentialCipherForRuntime(resolved)
		if err != nil {
			log.Fatalf("jobcron: credential encryption: %v", err)
		}
		srv.SetCredentialCipher(credentialCipher)
	}
	// Heal interrupted score writes for every profiled user. This is cache-only
	// and falls back to rule scoring when one user's AI runtime is unavailable.
	if _, err := srv.RescoreUsers(context.Background()); err != nil {
		log.Printf("jobcron: 시작 시 점수 복구를 완료하지 못했어요: %v", err)
	}
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	if cfg.SchedulerEnabled {
		if err := server.StartScheduler(appCtx, server.SchedulerConfig{
			Server:          srv,
			DailyScrapeTime: cfg.DailyScrapeTime,
		}); err != nil {
			log.Fatalf("jobcron: scheduler: %v", err)
		}
		log.Printf("jobcron: 매일 %s KST에 자동 스크랩을 실행해요.", cfg.DailyScrapeTime)
	}

	ln, addr, err := listen(cfg.Host, cfg.Port, cfg.StrictPort)
	if err != nil {
		log.Fatalf("jobcron: %v", err)
	}
	url := "http://" + addr
	fmt.Printf("jobcron: %s 에서 실행 중입니다. 종료하려면 Ctrl+C를 누르세요.\n", url)

	if !cfg.NoOpen {
		_ = browser.OpenURL(url) // best effort — failure is non-fatal
	}

	// Graceful shutdown on Ctrl+C / SIGTERM. Without this, http.Serve never
	// returns and the process dies on the signal with exit 128+N — which the
	// shipping binary should not do (the user expects clean termination, and
	// scripts that run the binary do too). The five-second budget lets any
	// in-flight scrape SSE stream finish its current frame.
	httpSrv := &http.Server{Handler: srv.Handler()}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		fmt.Println("\njobcron: 종료 중...")
		appCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("jobcron: %v", err)
	}
}

func credentialCipherForRuntime(runtime runtimeStorage) (credential.Cipher, error) {
	return credential.NewAESGCMCipher(runtime.CredentialEncryptionKey)
}

func resolvePostgresRuntime(ctx context.Context, cfg config.Config) (resolved runtimeStorage, retErr error) {
	var masterKey []byte
	if cfg.Production {
		if cfg.DatabaseURL == "" {
			return runtimeStorage{}, fmt.Errorf("production requires DATABASE_URL")
		}
		if len(cfg.CredentialEncryptionKey) != credential.MasterKeyBytes {
			return runtimeStorage{}, fmt.Errorf("production requires JOBCRON_CREDENTIAL_ENCRYPTION_KEY with exactly %d decoded bytes", credential.MasterKeyBytes)
		}
		masterKey = append([]byte(nil), cfg.CredentialEncryptionKey...)
	} else {
		masterKey = append([]byte(nil), cfg.CredentialEncryptionKey...)
		if len(masterKey) == 0 {
			var err error
			masterKey, err = loadLocalMasterKey()
			if err != nil {
				return runtimeStorage{}, fmt.Errorf("load protected local master key: %w", err)
			}
		}
		if len(masterKey) != credential.MasterKeyBytes {
			return runtimeStorage{}, fmt.Errorf("local credential master key must be exactly %d bytes", credential.MasterKeyBytes)
		}
	}

	databaseURL := cfg.DatabaseURL
	managedLocal := !cfg.Production && databaseURL == ""
	if managedLocal {
		var err error
		databaseURL, err = ensureLocalPostgres(ctx)
		if err != nil {
			return runtimeStorage{}, err
		}
		if databaseURL != localdb.DatabaseURL {
			return runtimeStorage{}, fmt.Errorf("managed local PostgreSQL returned an unexpected database URL")
		}
	}
	openStore := openMigratingRuntimeUserStore
	if cfg.Production {
		openStore = openRuntimeUserStore
	}
	store, err := openStore(databaseURL)
	if err != nil {
		return runtimeStorage{}, fmt.Errorf("open PostgreSQL runtime store: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close PostgreSQL runtime store: %w", closeErr)
		}
	}()
	if cfg.Production {
		return runtimeStorage{
			DatabaseURL:             databaseURL,
			CredentialEncryptionKey: masterKey,
		}, nil
	}

	var userID int64
	if managedLocal {
		userID, err = store.EnsureManagedLocalOwner(ctx)
	} else {
		var userIDs []int64
		userIDs, err = store.UserIDs(ctx)
		if err == nil && len(userIDs) != 1 {
			err = fmt.Errorf("explicit DATABASE_URL requires exactly one existing user")
		} else if err == nil {
			userID = userIDs[0]
		}
	}
	if err != nil {
		return runtimeStorage{}, fmt.Errorf("resolve PostgreSQL owner: %w", err)
	}
	if userID <= 0 {
		return runtimeStorage{}, fmt.Errorf("resolved PostgreSQL owner must have a positive user ID")
	}
	return runtimeStorage{
		DatabaseURL:             databaseURL,
		CredentialEncryptionKey: masterKey,
		ManagedLocal:            managedLocal,
		UserID:                  userID,
	}, nil
}

func openPostgresRuntime(runtime runtimeStorage) (*storage.Store, error) {
	if runtime.DatabaseURL == "" {
		return nil, fmt.Errorf("PostgreSQL runtime requires a database URL")
	}
	return storage.OpenPostgres(runtime.DatabaseURL)
}

// listen binds host on the preferred port, falling back to the next ten
// if it is busy. It returns the listener and the bound "host:port" address.
func listen(host string, preferred int, strict bool) (net.Listener, string, error) {
	last := preferred + 10
	if strict {
		last = preferred
	}
	for p := preferred; p <= last; p++ {
		addr := net.JoinHostPort(host, fmt.Sprintf("%d", p))
		if ln, err := net.Listen("tcp", addr); err == nil {
			return ln, addr, nil
		}
	}
	if strict {
		return nil, "", fmt.Errorf("requested port %d is unavailable", preferred)
	}
	return nil, "", fmt.Errorf("no free port in %d..%d", preferred, preferred+10)
}

func environMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, item := range environ {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}
