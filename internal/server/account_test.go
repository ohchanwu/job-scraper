package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	accountCurrentPassword = "current-password"
	accountNewPassword     = "replacement-password"
)

func TestAccountRoutesRequireAuthentication(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/account"},
		{http.MethodPost, "/account/password"},
		{http.MethodPost, "/account/delete"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
				t.Fatalf("status=%d Location=%q, want 303 /login", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestAccountPageShowsMaintenanceAndDestructiveForms(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")

	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`<title>계정 — 오늘의 채용 브리핑</title>`,
		`href="/account" class="active"`,
		`action="/account/password"`,
		`name="current_password"`,
		`name="new_password"`,
		`action="/account/delete"`,
		`name="confirm_email"`,
		`member@example.com`,
		`name="csrf_token" value="`,
		`삭제하면 되돌릴 수 없어요`,
		`저장한 정보와 AI 제공자 키가 모두 삭제돼요`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("account page missing %q", want)
		}
	}
}

func TestAccountPasswordChangeRejectsInvalidInputWithoutMutation(t *testing.T) {
	tests := []struct {
		name       string
		form       url.Values
		body       string
		setupCSRF  func(*testing.T, *Server, *http.Request)
		wantStatus int
	}{
		{name: "wrong current password", form: passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword), wantStatus: http.StatusUnprocessableEntity},
		{name: "password policy", form: passwordChangeForm(accountCurrentPassword, "too-short", "too-short"), wantStatus: http.StatusUnprocessableEntity},
		{name: "confirmation mismatch", form: passwordChangeForm(accountCurrentPassword, accountNewPassword, "different-password"), wantStatus: http.StatusUnprocessableEntity},
		{name: "malformed form", body: "%zz", setupCSRF: addAccountCSRFHeader, wantStatus: http.StatusBadRequest},
		{name: "missing csrf", form: passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword), setupCSRF: func(*testing.T, *Server, *http.Request) {}, wantStatus: http.StatusForbidden},
		{name: "wrong csrf", form: passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword), setupCSRF: addWrongAccountCSRF, wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			srv.SetProductionMode(true)
			target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session", "other-session")
			other := createAccountTestUser(t, st, "other@example.com", accountCurrentPassword, "foreign-session")

			body := tc.body
			if tc.form != nil {
				body = tc.form.Encode()
			}
			req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
			if tc.setupCSRF == nil {
				addCSRF(t, srv, req, "current-session")
			} else {
				tc.setupCSRF(t, srv, req)
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%q, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			assertAccountPassword(t, st, target.ID, accountCurrentPassword, true)
			assertAccountSessionCount(t, st, target.ID, 2)
			assertAccountPassword(t, st, other.ID, accountCurrentPassword, true)
			assertAccountSessionCount(t, st, other.ID, 1)
		})
	}
}

func TestAccountPasswordChangeKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session", "other-session")
	other := createAccountTestUser(t, st, "other@example.com", accountCurrentPassword, "foreign-session")

	req := accountFormRequest(t, srv, "/account/password", "current-session",
		passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
		t.Fatalf("status=%d Location=%q body=%q, want 303 /account", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	assertAccountPassword(t, st, target.ID, accountCurrentPassword, false)
	assertAccountPassword(t, st, target.ID, accountNewPassword, true)
	assertAccountSessionCount(t, st, target.ID, 1)
	assertAccountSessionValid(t, st, "current-session", true)
	assertAccountSessionValid(t, st, "other-session", false)
	assertAccountPassword(t, st, other.ID, accountCurrentPassword, true)
	assertAccountSessionValid(t, st, "foreign-session", true)
}

func TestAccountDeleteRejectsInvalidConfirmationWithoutMutation(t *testing.T) {
	tests := []struct {
		name       string
		form       url.Values
		body       string
		setupCSRF  func(*testing.T, *Server, *http.Request)
		wantStatus int
	}{
		{name: "wrong password", form: accountDeleteForm("wrong-password", "member@example.com"), wantStatus: http.StatusUnprocessableEntity},
		{name: "email mismatch", form: accountDeleteForm(accountCurrentPassword, "other@example.com"), wantStatus: http.StatusUnprocessableEntity},
		{name: "malformed form", body: "%zz", setupCSRF: addAccountCSRFHeader, wantStatus: http.StatusBadRequest},
		{name: "missing csrf", form: accountDeleteForm(accountCurrentPassword, "member@example.com"), setupCSRF: func(*testing.T, *Server, *http.Request) {}, wantStatus: http.StatusForbidden},
		{name: "wrong csrf", form: accountDeleteForm(accountCurrentPassword, "member@example.com"), setupCSRF: addWrongAccountCSRF, wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			srv.SetProductionMode(true)
			target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
			other := createAccountTestUser(t, st, "other@example.com", accountCurrentPassword, "foreign-session")

			body := tc.body
			if tc.form != nil {
				body = tc.form.Encode()
			}
			req := httptest.NewRequest(http.MethodPost, "/account/delete", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
			if tc.setupCSRF == nil {
				addCSRF(t, srv, req, "current-session")
			} else {
				tc.setupCSRF(t, srv, req)
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%q, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			assertAccountUserExists(t, st, target.ID, true)
			assertAccountUserExists(t, st, other.ID, true)
			assertAccountSessionValid(t, st, "current-session", true)
			assertAccountSessionValid(t, st, "foreign-session", true)
		})
	}
}

func TestAccountDeleteCascadesTargetAndExpiresBrowserSession(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
	other := createAccountTestUser(t, st, "other@example.com", accountCurrentPassword, "foreign-session")
	if _, _, err := st.SaveProfile(context.Background(), `{"career_years":0}`); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	req := accountFormRequest(t, srv, "/account/delete", "current-session",
		accountDeleteForm(accountCurrentPassword, "  MEMBER@example.com "))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d Location=%q body=%q, want 303 /login", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cookie := cookieNamed(t, rec, sessionCookieName)
	if cookie.MaxAge != -1 || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expired cookie=%+v, want logout attributes", cookie)
	}
	assertAccountUserExists(t, st, target.ID, false)
	assertAccountUserExists(t, st, other.ID, true)
	assertAccountSessionValid(t, st, "current-session", false)
	assertAccountSessionValid(t, st, "foreign-session", true)
	assertAccountGlobalRowCount(t, st, "profile", 1)
}

func passwordChangeForm(current, replacement, confirmation string) url.Values {
	return url.Values{
		"current_password": {current},
		"new_password":     {replacement},
		"password_confirm": {confirmation},
	}
}

func accountDeleteForm(password, email string) url.Values {
	return url.Values{"current_password": {password}, "confirm_email": {email}}
}

func accountFormRequest(t *testing.T, srv *Server, path, session string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	addCSRF(t, srv, req, session)
	return req
}

func addAccountCSRFHeader(t *testing.T, srv *Server, req *http.Request) {
	t.Helper()
	const cookie = "csrf-cookie"
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookie})
	req.Header.Set(csrfHeaderName, srv.csrfToken(cookie, "current-session"))
}

func addWrongAccountCSRF(t *testing.T, _ *Server, req *http.Request) {
	t.Helper()
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-cookie"})
	req.Header.Set(csrfHeaderName, "wrong")
}

func createAccountTestUser(t *testing.T, st *storage.Store, email, password string, sessions ...string) storage.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := st.CreateUser(context.Background(), email, hash)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", email, err)
	}
	for _, session := range sessions {
		if err := st.CreateSession(context.Background(), user.ID, auth.HashSessionToken(session), time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession(%q): %v", session, err)
		}
	}
	return user
}

func assertAccountPassword(t *testing.T, st *storage.Store, userID int64, password string, want bool) {
	t.Helper()
	user, found, err := st.UserByID(context.Background(), userID)
	if err != nil || !found {
		t.Fatalf("UserByID(%d): found=%v err=%v", userID, found, err)
	}
	got, err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if got != want {
		t.Fatalf("VerifyPassword(user=%d)=%v, want %v", userID, got, want)
	}
}

func assertAccountSessionCount(t *testing.T, st *storage.Store, userID int64, want int) {
	t.Helper()
	var got int
	if err := st.SQLDB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&got); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if got != want {
		t.Fatalf("sessions(user=%d)=%d, want %d", userID, got, want)
	}
}

func assertAccountSessionValid(t *testing.T, st *storage.Store, rawToken string, want bool) {
	t.Helper()
	_, got, err := st.UserBySessionToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("UserBySessionToken: %v", err)
	}
	if got != want {
		t.Fatalf("UserBySessionToken(%q) found=%v, want %v", rawToken, got, want)
	}
}

func assertAccountUserExists(t *testing.T, st *storage.Store, userID int64, want bool) {
	t.Helper()
	_, got, err := st.UserByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("UserByID(%d): %v", userID, err)
	}
	if got != want {
		t.Fatalf("UserByID(%d) found=%v, want %v", userID, got, want)
	}
}

func assertAccountGlobalRowCount(t *testing.T, st *storage.Store, table string, want int) {
	t.Helper()
	var got int
	if err := st.SQLDB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows=%d, want %d", table, got, want)
	}
}
