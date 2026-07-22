package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
)

func TestProductionCookieNamesUseJobcronPrefix(t *testing.T) {
	if sessionCookieName != "jobcron_session" {
		t.Fatalf("session cookie = %q, want jobcron_session", sessionCookieName)
	}
	if csrfCookieName != "jobcron_csrf" {
		t.Fatalf("csrf cookie = %q, want jobcron_csrf", csrfCookieName)
	}
}

func TestProductionAuthRedirectsAnonymousPageToLogin(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

func TestProductionAuthRejectsAnonymousAPIWithUnauthorizedJSON(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "scrape stream", method: http.MethodGet, target: "/api/scrape"},
		{name: "bookmark mutation", method: http.MethodPut, target: "/api/bookmark/1"},
		{name: "not interested mutation", method: http.MethodDelete, target: "/api/not-interested/1"},
		{name: "rerate stream", method: http.MethodGet, target: "/api/rerate?surface=today"},
		{name: "rerate status", method: http.MethodGet, target: "/api/rerate/status?surface=today"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.target, nil)
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Fatalf("Location = %q, want empty for API 401", loc)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body["error"] != "authentication required" {
				t.Fatalf("error = %q, want authentication required", body["error"])
			}
		})
	}
}

func TestProductionAuthKeepsSignupLoginAndStaticPublic(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	for _, path := range []string{"/signup", "/login", "/static/styles.css", "/favicon.ico"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/login" {
				t.Fatalf("%s redirected to login, want public", path)
			}
		})
	}
}

func TestDemoModeDoesNotRequireLogin(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetDemoMode(true)
	srv.SetProductionMode(true)
	ctx := context.Background()
	seedProfile(t, st, ctx)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestNonProductionPostgresUsesInjectedPositiveUser(t *testing.T) {
	_, st := newPostgresTestServer(t, &fakeScraper{})
	const localUserID int64 = 81
	srv := NewForLocalUser(st, localUserID, &fakeScraper{})

	got, err := srv.stateUserID(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("stateUserID: %v", err)
	}
	if got != localUserID {
		t.Fatalf("stateUserID = %d, want %d", got, localUserID)
	}
}

func TestNonProductionPostgresDemoUsesInjectedPositiveUser(t *testing.T) {
	_, st := newPostgresTestServer(t, &fakeScraper{})
	const localUserID int64 = 82
	srv := NewForLocalUser(st, localUserID, &fakeScraper{})
	srv.SetDemoMode(true)

	got, err := srv.stateUserID(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("stateUserID: %v", err)
	}
	if got != localUserID {
		t.Fatalf("stateUserID = %d, want %d", got, localUserID)
	}
}

func TestProductionPostgresDemoUsesResolvedPositiveOwner(t *testing.T) {
	_, st := newPostgresTestServer(t, &fakeScraper{})
	const resolvedOwnerID int64 = 83
	srv := NewForLocalUser(st, resolvedOwnerID, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetDemoMode(true)

	got, err := srv.stateUserID(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("stateUserID: %v", err)
	}
	if got != resolvedOwnerID {
		t.Fatalf("production PostgreSQL demo stateUserID = %d, want %d", got, resolvedOwnerID)
	}
}

func TestNonProductionPostgresDemoRefusesNonpositiveLocalUser(t *testing.T) {
	_, st := newPostgresTestServer(t, &fakeScraper{})
	for _, localUserID := range []int64{0, -1} {
		t.Run(fmt.Sprintf("user-%d", localUserID), func(t *testing.T) {
			srv := newServer(st, localUserID, &fakeScraper{})
			srv.SetDemoMode(true)
			got, err := srv.stateUserID(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
			if err == nil || got != 0 {
				t.Fatalf("stateUserID = %d err %v, want nonpositive-local-user error", got, err)
			}
		})
	}
}

func TestNonProductionPostgresRefusesMissingLocalUser(t *testing.T) {
	srv, _ := newPostgresTestServer(t, &fakeScraper{})
	got, err := srv.stateUserID(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err == nil || got != 0 {
		t.Fatalf("stateUserID = %d err %v, want missing-local-user error", got, err)
	}
}

func TestLoginFailureUsesGenericError(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.CreateOwnerUser(context.Background(), "owner@example.com", hash); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}

	form := url.Values{"email": {"missing@example.com"}, "password": {"wrong-password"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRF(t, srv, req, "")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "이메일 또는 비밀번호를 확인해주세요") {
		t.Fatalf("body = %q, want generic login error", body)
	}
	if strings.Contains(body, "missing@example.com") || strings.Contains(body, "존재") {
		t.Fatalf("body reveals account existence: %q", body)
	}
}

func TestLoginSuccessSetsSecureSessionCookie(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	seedProfile(t, st, context.Background())
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.CreateOwnerUser(context.Background(), "owner@example.com", hash); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}

	form := url.Values{"email": {"owner@example.com"}, "password": {"correct-password"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRF(t, srv, req, "")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/" {
		t.Fatalf("Location = %q, want /", rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != sessionCookieName {
		t.Fatalf("cookie name = %q, want %q", cookie.Name, sessionCookieName)
	}
	if cookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie HttpOnly=false")
	}
	if !cookie.Secure {
		t.Fatal("session cookie Secure=false in production")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("Path = %q, want /", cookie.Path)
	}

	page := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/", nil)
	pageReq.AddCookie(cookie)
	srv.Handler().ServeHTTP(page, pageReq)
	if page.Code != http.StatusOK {
		t.Fatalf("authenticated page status = %d, want 200", page.Code)
	}
}

func TestLoginVerifiedBeforePasswordResetCannotCreateSurvivingSession(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	ctx := context.Background()

	oldHash, err := auth.HashPassword("old-password")
	if err != nil {
		t.Fatalf("HashPassword(old): %v", err)
	}
	user, err := st.CreateOwnerUser(ctx, "owner@example.com", oldHash)
	if err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}
	newHash, err := auth.HashPassword("new-password")
	if err != nil {
		t.Fatalf("HashPassword(new): %v", err)
	}

	tx, err := st.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, newHash, user.ID); err != nil {
		t.Fatalf("stage password reset: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, user.ID); err != nil {
		t.Fatalf("stage session revocation: %v", err)
	}

	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		form := url.Values{"email": {user.Email}, "password": {"old-password"}}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		addCSRF(t, srv, req, "")
		srv.Handler().ServeHTTP(rec, req)
		result <- rec
	}()

	rec, blocked, err := waitForLoginResultOrSessionLock(ctx, st.SQLDB(), result)
	if err != nil {
		t.Fatalf("wait for login concurrency point: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit password reset: %v", err)
	}
	if blocked {
		select {
		case rec = <-result:
		case <-time.After(5 * time.Second):
			t.Fatal("login did not finish after password reset committed")
		}
	}

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old-password login status = %d, want 401", rec.Code)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			t.Fatalf("old-password login set %s cookie", sessionCookieName)
		}
	}
	var sessions int
	if err := st.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, user.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("surviving sessions = %d, want 0", sessions)
	}
}

func waitForLoginResultOrSessionLock(
	ctx context.Context,
	db *sql.DB,
	result <-chan *httptest.ResponseRecorder,
) (*httptest.ResponseRecorder, bool, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case rec := <-result:
			return rec, false, nil
		default:
		}

		var blocked bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE pid <> pg_backend_pid()
				  AND datname = current_database()
				  AND state = 'active'
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%WITH authenticated_user AS (%'
			)`).Scan(&blocked)
		if err != nil {
			return nil, false, err
		}
		if blocked {
			return nil, true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, false, fmt.Errorf("timed out waiting for login result or session lock")
}

func TestLogoutClearsSessionCookie(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	addCSRF(t, srv, req, "raw-session-token")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Name != sessionCookieName || cookies[0].MaxAge != -1 {
		t.Fatalf("logout cookie = %+v, want cleared session cookie", cookies[0])
	}
}

func TestLogoutRevokesSessionToken(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	ctx := context.Background()
	seedProfile(t, st, ctx)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.CreateOwnerUser(ctx, "owner@example.com", hash); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}

	form := url.Values{"email": {"owner@example.com"}, "password": {"correct-password"}}
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRF(t, srv, loginReq, "")
	srv.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303; body=%q", loginRec.Code, loginRec.Body.String())
	}
	sessionCookie := loginRec.Result().Cookies()[0]

	logoutRec := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	addCSRF(t, srv, logoutReq, sessionCookie.Value)
	srv.Handler().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", logoutRec.Code)
	}

	pageRec := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/", nil)
	pageReq.AddCookie(sessionCookie)
	srv.Handler().ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusSeeOther {
		t.Fatalf("post-logout page status = %d, want 303", pageRec.Code)
	}
	if loc := pageRec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("post-logout Location = %q, want /login", loc)
	}
}

func seedProfile(t *testing.T, st interface {
	SaveProfile(context.Context, string) (string, bool, error)
}, ctx context.Context) {
	t.Helper()
	if _, _, err := st.SaveProfile(ctx, `{"career_years":0}`); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

func addCSRF(t *testing.T, srv *Server, req *http.Request, sessionValue string) {
	t.Helper()
	const cookieValue = "csrf-cookie"
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieValue})
	req.Header.Set(csrfHeaderName, srv.csrfToken(cookieValue, sessionValue))
}
