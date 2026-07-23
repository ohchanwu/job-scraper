package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const testSignupAccessCode = "cohort-only-code"

func TestSignupBrowserFormUsesRenderedCSRFToken(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/signup", nil))
	csrfCookie := cookieNamed(t, get, csrfCookieName)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if len(match) != 2 || match[1] == "" {
		t.Fatalf("signup form has no rendered CSRF token: %q", get.Body.String())
	}
	form := validSignupForm("wrong-code")
	form.Set(csrfFieldName, match[1])
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("browser-form status = %d, want 422 after CSRF acceptance; body=%q", rec.Code, rec.Body.String())
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupRejectsMissingOrWrongCSRFWithoutCreatingAccount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*http.Request)
	}{
		{name: "missing"},
		{name: "wrong", setup: func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-cookie"})
			req.Header.Set(csrfHeaderName, "wrong")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			srv.SetProductionMode(true)
			srv.SetSignupAccessCode(testSignupAccessCode)
			req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(validSignupForm(testSignupAccessCode).Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.setup != nil {
				tc.setup(req)
			}
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			assertAccountCounts(t, st, 0, 0)
		})
	}
}

func TestSignupBoundsBrowserFormBeforeCSRFParsing(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	form := validSignupForm(testSignupAccessCode)
	form.Set("email", strings.Repeat("a", int(signupMaxFormBytes)))
	form.Set(csrfFieldName, srv.csrfToken("csrf-cookie", ""))
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-cookie"})
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 before oversized form parsing", rec.Code)
	}
	if got := len(srv.signupLimiter.attempts); got != 0 {
		t.Fatalf("limiter entries = %d, want 0", got)
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupBodyLimitAllowsMaximumValidUnicodePassword(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	password := strings.Repeat("가", 341)
	form := validSignupForm(testSignupAccessCode)
	form.Set("password", password)
	form.Set("password_confirm", password)
	form.Set(csrfFieldName, srv.csrfToken("csrf-cookie", ""))
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-cookie"})
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 for %d-byte password", rec.Code, len(password))
	}
	assertAccountCounts(t, st, 1, 1)
}

func TestSignupClosedGate(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/signup", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /signup status = %d, want 200", get.Code)
	}
	if body := get.Body.String(); !strings.Contains(body, "현재 가입을 받고 있지 않아요") {
		t.Fatalf("closed signup body = %q, want closed-cohort notice", body)
	}

	post := postSignup(t, srv, validSignupForm("anything"))
	if post.Code != http.StatusForbidden {
		t.Fatalf("closed POST /signup status = %d, want 403", post.Code)
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupRejectsIncorrectAccessCode(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)

	rec := postSignup(t, srv, validSignupForm("wrong-code"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testSignupAccessCode) {
		t.Fatal("signup response revealed configured access code")
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupValidatesInput(t *testing.T) {
	tests := []struct {
		name   string
		change func(url.Values)
	}{
		{name: "email syntax", change: func(v url.Values) { v.Set("email", "not-an-email") }},
		{name: "minimum password length", change: func(v url.Values) {
			v.Set("password", "too short")
			v.Set("password_confirm", "too short")
		}},
		{name: "password confirmation", change: func(v url.Values) {
			v.Set("password_confirm", "different long password")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			srv.SetProductionMode(true)
			srv.SetSignupAccessCode(testSignupAccessCode)
			form := validSignupForm(testSignupAccessCode)
			tt.change(form)

			rec := postSignup(t, srv, form)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			assertAccountCounts(t, st, 0, 0)
		})
	}
}

func TestSignupRejectsMalformedForm(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader("email=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRF(t, srv, req, "")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupCreatesCanonicalUserAndSession(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	form := validSignupForm(testSignupAccessCode)
	form.Set("email", "  New.User@Example.COM ")

	rec := postSignup(t, srv, form)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("signup = status %d location %q, want 303 /profile; body=%q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	user, ok, err := st.UserByEmail(context.Background(), "new.user@example.com")
	if err != nil || !ok {
		t.Fatalf("UserByEmail: ok=%v err=%v", ok, err)
	}
	if user.Email != "new.user@example.com" {
		t.Fatalf("stored email = %q, want canonical email", user.Email)
	}
	if matches, err := auth.VerifyPassword(user.PasswordHash, form.Get("password")); err != nil || !matches {
		t.Fatalf("stored password is not a matching Argon2id hash: matches=%v err=%v", matches, err)
	}
	cookie := cookieNamed(t, rec, sessionCookieName)
	if cookie.Value == "" || cookie.Value == form.Get("password") || cookie.Value == form.Get("access_code") {
		t.Fatal("session cookie is empty or not opaque")
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("session cookie flags = %+v", cookie)
	}
	if _, ok, err := st.UserBySessionToken(context.Background(), cookie.Value); err != nil || !ok {
		t.Fatalf("UserBySessionToken: ok=%v err=%v", ok, err)
	}
	assertAccountCounts(t, st, 1, 1)
}

func TestSignupRollsBackUserWhenInitialSessionFails(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	if _, err := st.SQLDB().Exec(`
CREATE TRIGGER fail_signup_session
BEFORE INSERT ON sessions
BEGIN
  SELECT RAISE(FAIL, 'forced session failure');
END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if rec := postSignup(t, srv, validSignupForm(testSignupAccessCode)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestSignupReleasesPasswordWorkBeforeSessionPersistence(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	srv.passwordWorkSlots = make(chan struct{}, 1)

	hash, err := auth.HashPassword("login-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.CreateOwnerUser(context.Background(), "owner@example.com", hash); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}

	tx, err := st.SQLDB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := tx.Exec(`LOCK TABLE sessions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock sessions: %v", err)
	}

	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- postSignup(t, srv, validSignupForm(testSignupAccessCode)) }()
	barrierReleased := false
	signupFinished := false
	defer func() {
		if !barrierReleased {
			_ = tx.Rollback()
		}
		if !signupFinished {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
			}
		}
	}()
	if err := waitForSignupSessionLock(context.Background(), st, result); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.passwordWorkSlots); got != 0 {
		t.Fatalf("password-work slots held during signup persistence = %d, want 0", got)
	}
	if rec := postLogin(t, srv, "198.51.100.30:1234", "owner@example.com", "wrong-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("concurrent login status = %d, want 401", rec.Code)
	}
	select {
	case rec := <-result:
		t.Fatalf("signup completed while session persistence was blocked: status=%d", rec.Code)
	default:
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("release session barrier: %v", err)
	}
	barrierReleased = true
	select {
	case rec := <-result:
		signupFinished = true
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("signup status = %d, want 303", rec.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signup did not finish after session barrier released")
	}
	if got := len(srv.passwordWorkSlots); got != 0 {
		t.Fatalf("password-work slots after signup = %d, want 0", got)
	}
}

func waitForSignupSessionLock(
	ctx context.Context,
	st *storage.Store,
	result <-chan *httptest.ResponseRecorder,
) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(result) > 0 {
			return fmt.Errorf("signup completed before session barrier")
		}

		var blocked bool
		err := st.SQLDB().QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity AS activity
				JOIN pg_locks AS locks ON locks.pid = activity.pid
				WHERE activity.pid <> pg_backend_pid()
				  AND activity.datname = current_database()
				  AND activity.state = 'active'
				  AND activity.query LIKE '%INSERT INTO sessions%'
				  AND locks.locktype = 'relation'
				  AND locks.relation = to_regclass('sessions')
				  AND NOT locks.granted
			)`).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for signup session lock")
}

func TestSignupBoundsConcurrentPasswordHashing(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	for range cap(srv.passwordWorkSlots) {
		srv.passwordWorkSlots <- struct{}{}
	}

	if rec := postSignup(t, srv, validSignupForm(testSignupAccessCode)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 while password hashing is saturated", rec.Code)
	}
	assertAccountCounts(t, st, 0, 0)
}

func TestPasswordWorkCapacityRejectsCancelledAcquisition(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if srv.acquirePasswordWork(ctx) {
		t.Fatal("password-work acquisition succeeded after cancellation")
	}
	if got := len(srv.passwordWorkSlots); got != 0 {
		t.Fatalf("password-work slots in use = %d, want 0", got)
	}
}

func TestSignupDuplicateUsesGenericFailureWithoutLoggingIdentity(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	srv.SetSignupAccessCode(testSignupAccessCode)
	first := postSignup(t, srv, validSignupForm(testSignupAccessCode))
	if first.Code != http.StatusSeeOther {
		t.Fatalf("first signup status = %d, want 303", first.Code)
	}

	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	duplicate := postSignup(t, srv, validSignupForm(testSignupAccessCode))

	if duplicate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate status = %d, want 422", duplicate.Code)
	}
	for _, forbidden := range []string{"new@example.com", "already", "exists", "registered", "storage:"} {
		if strings.Contains(strings.ToLower(duplicate.Body.String()), forbidden) || strings.Contains(strings.ToLower(logs.String()), forbidden) {
			t.Fatalf("duplicate response or logs revealed account details: body=%q logs=%q", duplicate.Body.String(), logs.String())
		}
	}
	assertAccountCounts(t, st, 1, 1)
}

func validSignupForm(code string) url.Values {
	return url.Values{
		"email":            {"new@example.com"},
		"password":         {"correct horse battery staple"},
		"password_confirm": {"correct horse battery staple"},
		"access_code":      {code},
	}
}

func postSignup(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRF(t, srv, req, "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func assertAccountCounts(t *testing.T, st *storage.Store, wantUsers, wantSessions int) {
	t.Helper()
	for table, want := range map[string]int{"users": wantUsers, "sessions": wantSessions} {
		var got int
		if err := st.SQLDB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response has no %s cookie", name)
	return nil
}
