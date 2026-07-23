package server

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/profile"
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
		`href="/account" class="active" aria-current="page"`,
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

func TestAccountPasswordChangeAllowsMaximumValidUnicodePasswords(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	current := strings.Repeat("가", 341)
	replacement := strings.Repeat("나", 341)
	target := createAccountTestUser(t, st, "member@example.com", current, "current-session")
	form := passwordChangeForm(current, replacement, replacement)
	if encoded := len(form.Encode()); encoded <= 4<<10 || encoded > 16<<10 {
		t.Fatalf("encoded form bytes=%d, want >4 KiB and <=16 KiB", encoded)
	}

	req := accountFormRequest(t, srv, "/account/password", "current-session", form)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
		t.Fatalf("status=%d Location=%q body=%q, want 303 /account", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	assertAccountPassword(t, st, target.ID, replacement, true)
}

func TestAccountPasswordChangeRejectsFormsOver16KiB(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
	form := passwordChangeForm(accountCurrentPassword, strings.Repeat("a", 16<<10), strings.Repeat("a", 16<<10))

	req := accountFormRequest(t, srv, "/account/password", "current-session", form)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q, want 400", rec.Code, rec.Body.String())
	}
	assertAccountPassword(t, st, target.ID, accountCurrentPassword, true)
}

func TestAccountMutationsRejectSaturatedPasswordWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		form url.Values
	}{
		{name: "password change", path: "/account/password", form: passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword)},
		{name: "account deletion", path: "/account/delete", form: accountDeleteForm(accountCurrentPassword, "member@example.com")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			srv.SetProductionMode(true)
			target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
			for range cap(srv.passwordWorkSlots) {
				srv.passwordWorkSlots <- struct{}{}
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, accountFormRequest(t, srv, tc.path, "current-session", tc.form))

			if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), accountErrorCopy) {
				t.Fatalf("status=%d body=%q, want generic 429", rec.Code, rec.Body.String())
			}
			assertAccountUserExists(t, st, target.ID, true)
			assertAccountPassword(t, st, target.ID, accountCurrentPassword, true)
			assertAccountSessionValid(t, st, "current-session", true)
		})
	}
}

func TestAccountMutationAttemptWindows(t *testing.T) {
	const remoteAddr = "198.51.100.10:1234"

	t.Run("wrong password reaches shared window without mutation or slot", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		srv.SetProductionMode(true)
		target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
		srv.passwordWorkSlots = make(chan struct{}, 1)

		for i := 0; i < loginRateLimitMaxFailures; i++ {
			rec := postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
				passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("wrong attempt %d status=%d, want 422", i+1, rec.Code)
			}
		}
		rec := postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
			passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
		if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), accountErrorCopy) {
			t.Fatalf("limited status=%d body=%q, want generic 429", rec.Code, rec.Body.String())
		}
		if got := len(srv.passwordWorkSlots); got != 0 {
			t.Fatalf("password work slots held after rejection=%d, want 0", got)
		}
		assertAccountPassword(t, st, target.ID, accountCurrentPassword, true)
	})

	t.Run("success resets identity failures but not absolute IP attempts", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		srv.SetProductionMode(true)
		target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
		postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
			passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
		rec := postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
			passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("successful change status=%d body=%q, want 303", rec.Code, rec.Body.String())
		}

		ip := remoteHost(remoteAddr)
		identityKey := loginRateLimitKey(ip, strconv.FormatInt(target.ID, 10))
		if srv.accountMutationLimiter.attempts[identityKey] != nil {
			t.Fatal("successful re-authentication did not reset identity failures")
		}
		ipAttempts := srv.accountMutationIPLimiter.attempts[loginRateLimitKey(ip, "")]
		if ipAttempts == nil || ipAttempts.Value.(*loginAttempts).count != 2 {
			t.Fatalf("absolute IP attempts after failure and success=%v, want count 2", ipAttempts)
		}

		current := accountNewPassword
		for _, replacement := range []string{"replacement-password-2", "replacement-password-3", "replacement-password-4"} {
			rec = postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
				passwordChangeForm(current, replacement, replacement))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("successful change to %q status=%d body=%q, want 303", replacement, rec.Code, rec.Body.String())
			}
			current = replacement
		}
		rec = postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
			passwordChangeForm(current, "replacement-password-5", "replacement-password-5"))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("absolute IP window status=%d, want 429", rec.Code)
		}
		assertAccountPassword(t, st, target.ID, current, true)
	})

	t.Run("different users do not share identity failures", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		srv.SetProductionMode(true)
		createAccountTestUser(t, st, "first@example.com", accountCurrentPassword, "first-session")
		createAccountTestUser(t, st, "second@example.com", accountCurrentPassword, "second-session")

		for i := 0; i < loginRateLimitMaxFailures; i++ {
			rec := postAccountMutation(t, srv, remoteAddr, "/account/password", "first-session",
				passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("first user attempt %d status=%d, want 422", i+1, rec.Code)
			}
			srv.accountMutationIPLimiter.resetIP(remoteHost(remoteAddr))
		}
		rec := postAccountMutation(t, srv, remoteAddr, "/account/password", "second-session",
			passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("second user status=%d, want 422", rec.Code)
		}
		srv.accountMutationIPLimiter.resetIP(remoteHost(remoteAddr))
		rec = postAccountMutation(t, srv, remoteAddr, "/account/password", "first-session",
			passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("limited first user status=%d, want 429", rec.Code)
		}
	})

	t.Run("absolute IP attempts span users and mutation routes", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		srv.SetProductionMode(true)
		first := createAccountTestUser(t, st, "first@example.com", accountCurrentPassword, "first-session")
		second := createAccountTestUser(t, st, "second@example.com", accountCurrentPassword, "second-session")
		srv.passwordWorkSlots = make(chan struct{}, 1)
		attempts := []struct {
			path    string
			session string
			form    url.Values
		}{
			{"/account/password", "first-session", passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword)},
			{"/account/delete", "second-session", accountDeleteForm("wrong-password", "second@example.com")},
			{"/account/delete", "first-session", accountDeleteForm("wrong-password", "first@example.com")},
			{"/account/password", "second-session", passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword)},
			{"/account/password", "first-session", passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword)},
		}
		for i, attempt := range attempts {
			rec := postAccountMutation(t, srv, remoteAddr, attempt.path, attempt.session, attempt.form)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d status=%d, want 422", i+1, rec.Code)
			}
		}
		rec := postAccountMutation(t, srv, remoteAddr, "/account/delete", "second-session",
			accountDeleteForm("wrong-password", "second@example.com"))
		if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), accountErrorCopy) {
			t.Fatalf("shared IP status=%d body=%q, want generic 429", rec.Code, rec.Body.String())
		}
		if got := len(srv.passwordWorkSlots); got != 0 {
			t.Fatalf("password work slots held after shared-IP rejection=%d, want 0", got)
		}
		assertAccountUserExists(t, st, first.ID, true)
		assertAccountUserExists(t, st, second.ID, true)
		assertAccountPassword(t, st, first.ID, accountCurrentPassword, true)
		assertAccountPassword(t, st, second.ID, accountCurrentPassword, true)
	})

	t.Run("password change and deletion share identity failures", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		srv.SetProductionMode(true)
		createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")

		for i := 0; i < loginRateLimitMaxFailures-1; i++ {
			postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
				passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
			srv.accountMutationIPLimiter.resetIP(remoteHost(remoteAddr))
		}
		rec := postAccountMutation(t, srv, remoteAddr, "/account/delete", "current-session",
			accountDeleteForm("wrong-password", "member@example.com"))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("final allowed deletion status=%d, want 422", rec.Code)
		}
		srv.accountMutationIPLimiter.resetIP(remoteHost(remoteAddr))
		rec = postAccountMutation(t, srv, remoteAddr, "/account/password", "current-session",
			passwordChangeForm("wrong-password", accountNewPassword, accountNewPassword))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("shared-window status=%d, want 429", rec.Code)
		}
	})
}

func TestAccountDeleteWaitsForRunningOperation(t *testing.T) {
	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	srv, st := newTestServer(t, &fakeScraper{
		listingStarted: operationStarted,
		listingRelease: releaseOperation,
	})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
	if _, _, err := st.SaveProfileForUser(context.Background(), target.ID, `{"career_years":0}`); err != nil {
		t.Fatalf("SaveProfileForUser: %v", err)
	}
	operationDone := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/scrape", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		close(operationDone)
	}()
	<-operationStarted
	srv.passwordWorkSlots = make(chan struct{}, 1)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelDone := startAccountDeletion(t, srv, cancelCtx)
	waitForAccountPasswordWork(t, srv)
	cancel()
	select {
	case rec := <-cancelDone:
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), accountErrorCopy) {
			t.Fatalf("cancelled deletion status=%d body=%q, want generic 422", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled deletion did not return")
	}
	assertAccountUserExists(t, st, target.ID, true)

	done := startAccountDeletion(t, srv, context.Background())
	waitForAccountPasswordWork(t, srv)

	select {
	case rec := <-done:
		t.Fatalf("deletion completed while operation gate held: status=%d", rec.Code)
	default:
	}
	assertAccountUserExists(t, st, target.ID, true)
	close(releaseOperation)
	select {
	case <-operationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape did not finish after release")
	}

	select {
	case rec := <-done:
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("status=%d Location=%q, want 303 /login", rec.Code, rec.Header().Get("Location"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not finish after operation gate release")
	}
	assertAccountUserExists(t, st, target.ID, false)
}

func TestAccountDeleteCancellationWhileWaiting(t *testing.T) {
	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	srv, st := newPostgresTestServer(t, &fakeScraper{
		listingStarted: operationStarted,
		listingRelease: releaseOperation,
	})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
	cipher := newAIRuntimeTestCipher(t, 0x76)
	srv.SetCredentialCipher(cipher)
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) {
		return rerateStub(), nil
	}
	zero := 0
	saveAIRuntimeProfile(t, st, target.ID, profile.Profile{
		CareerYears: 0, MinScore: &zero, AIProvider: "anthropic", AIModel: "test-model",
	})
	saveAIRuntimeCredential(t, st, cipher, target.ID, "anthropic", "synthetic-cancel-key")
	if _, err := st.SQLDB().Exec(`
INSERT INTO ai_usage (user_id, day, input_tokens, output_tokens)
VALUES ($1, CURRENT_DATE, 7, 3)`, target.ID); err != nil {
		t.Fatalf("seed AI usage: %v", err)
	}

	operationDone := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/scrape", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		close(operationDone)
	}()
	<-operationStarted
	before := accountPrivateStateSnapshot(t, st, target.ID)
	srv.passwordWorkSlots = make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := startAccountDeletion(t, srv, ctx)
	waitForAccountPasswordWork(t, srv)
	cancel()
	select {
	case rec := <-done:
		if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("Location") != "" ||
			!strings.Contains(rec.Body.String(), accountErrorCopy) {
			t.Fatalf("cancelled deletion status=%d Location=%q body=%q, want generic 422 without redirect",
				rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled deletion did not return")
	}
	if after := accountPrivateStateSnapshot(t, st, target.ID); after != before {
		t.Fatalf("private account state changed after cancellation:\nbefore=%+v\nafter=%+v", before, after)
	}
	close(releaseOperation)
	select {
	case <-operationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape did not finish after release")
	}
}

func TestAccountDeleteWaitsForRerateUsageDebit(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
	cipher := newAIRuntimeTestCipher(t, 0x75)
	srv.SetCredentialCipher(cipher)
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	provider := &ai.StubProvider{
		NameVal: "stub",
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			close(providerStarted)
			<-releaseProvider
			return []ai.RawDeltaItem{{Signal: "백엔드", Kind: ai.KindPresence, Delta: 1, Quote: "서버 개발"}},
				ai.Usage{InputTokens: 5, OutputTokens: 1}, nil
		},
	}
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return provider, nil }
	zero := 0
	saveAIRuntimeProfile(t, st, target.ID, profile.Profile{
		CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발",
		AIProvider: "anthropic", AIModel: "test-model",
	})
	saveAIRuntimeCredential(t, st, cipher, target.ID, "anthropic", "synthetic-rerate-key")
	p := listingPosting("account-delete-rerate", "신입 백엔드 개발자")
	p.Description = "백엔드 서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	if _, _, err := st.UpsertPosting(context.Background(), p); err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	runtime, err := srv.aiRuntimeForUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("aiRuntimeForUser: %v", err)
	}
	if _, err := srv.RescoreAll(context.Background(), target.ID, runtime); err != nil {
		t.Fatalf("RescoreAll: %v", err)
	}
	if _, err := st.SQLDB().Exec(`
CREATE FUNCTION require_account_test_usage_before_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM ai_usage
		 WHERE user_id = OLD.id AND input_tokens >= 5 AND output_tokens >= 1
	) THEN
		RAISE EXCEPTION 'account deleted before AI usage debit';
	END IF;
	RETURN OLD;
END;
$$;
CREATE TRIGGER require_account_test_usage_before_delete
BEFORE DELETE ON users
FOR EACH ROW EXECUTE FUNCTION require_account_test_usage_before_delete()`); err != nil {
		t.Fatalf("install usage-before-delete trigger: %v", err)
	}

	operationDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/rerate?surface=today&entry=entry-token-00000001", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "current-session"})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		operationDone <- rec
	}()
	<-providerStarted
	srv.passwordWorkSlots = make(chan struct{}, 1)
	deleteDone := startAccountDeletion(t, srv, context.Background())
	waitForAccountPasswordWork(t, srv)
	select {
	case rec := <-deleteDone:
		t.Fatalf("deletion completed while rerate held operation gate: status=%d body=%q", rec.Code, rec.Body.String())
	default:
	}
	close(releaseProvider)
	select {
	case rec := <-operationDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("rerate status=%d body=%q, want 200", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rerate did not finish after provider release")
	}
	select {
	case rec := <-deleteDone:
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("deletion status=%d Location=%q body=%q, want 303 /login", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not finish after rerate")
	}
	assertAccountUserExists(t, st, target.ID, false)
}

func startAccountDeletion(t *testing.T, srv *Server, ctx context.Context) <-chan *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := accountFormRequest(t, srv, "/account/delete", "current-session",
			accountDeleteForm(accountCurrentPassword, "member@example.com")).WithContext(ctx)
		srv.Handler().ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

func waitForAccountPasswordWork(t *testing.T, srv *Server) {
	t.Helper()
	select {
	case <-srv.passwordWorkSlots:
	case <-time.After(5 * time.Second):
		t.Fatal("account handler did not acquire password-work capacity")
	}
	srv.passwordWorkSlots <- struct{}{}
	srv.passwordWorkSlots <- struct{}{} // blocks until the handler releases its slot
	<-srv.passwordWorkSlots
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

func TestAccountMutationsRejectExpiredSubmittingSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		form url.Values
	}{
		{name: "password change", path: "/account/password", form: passwordChangeForm(accountCurrentPassword, accountNewPassword, accountNewPassword)},
		{name: "account deletion", path: "/account/delete", form: accountDeleteForm(accountCurrentPassword, "member@example.com")},
	} {
		for _, backend := range []string{"SQLite", "PostgreSQL"} {
			t.Run(tc.name+"/"+backend, func(t *testing.T) {
				var srv *Server
				var st *storage.Store
				if backend == "PostgreSQL" {
					srv, st = newPostgresTestServer(t, &fakeScraper{})
				} else {
					srv, st = newTestServer(t, &fakeScraper{})
				}
				srv.SetProductionMode(true)
				target := createAccountTestUser(t, st, "member@example.com", accountCurrentPassword, "current-session")
				const profileJSON = `{"career_years":0}`
				if _, _, err := st.SaveProfileForUser(context.Background(), target.ID, profileJSON); err != nil {
					t.Fatalf("SaveProfileForUser: %v", err)
				}
				profileBefore := accountProfileSnapshot(t, st, target.ID)
				expireAccountSessionAfterHandlerLookup(t, st, backend == "PostgreSQL")

				req := accountFormRequest(t, srv, tc.path, "current-session", tc.form)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), accountErrorCopy) {
					t.Fatalf("status=%d body=%q, want generic 422", rec.Code, rec.Body.String())
				}
				assertAccountUserExists(t, st, target.ID, true)
				if got, found, err := st.UserByID(context.Background(), target.ID); err != nil || !found || got != target {
					t.Fatalf("UserByID after rejected mutation: got=%+v found=%v err=%v want=%+v", got, found, err, target)
				}
				assertAccountPassword(t, st, target.ID, accountCurrentPassword, true)
				assertAccountSessionCount(t, st, target.ID, 1)
				assertAccountSessionMatchesExpirySnapshot(t, st)
				if got := accountProfileSnapshot(t, st, target.ID); got != profileBefore {
					t.Fatalf("profile row changed: before=%+v after=%+v", profileBefore, got)
				}
			})
		}
	}
}

func expireAccountSessionAfterHandlerLookup(t *testing.T, st *storage.Store, postgres bool) {
	t.Helper()
	query := `
CREATE TABLE account_test_session_lookups (count INTEGER NOT NULL);
INSERT INTO account_test_session_lookups VALUES (0);
CREATE TABLE account_test_expected_session (
    id INTEGER, user_id INTEGER, session_token_hash TEXT,
    created_at TEXT, expires_at TEXT, last_seen_at TEXT
);
CREATE TRIGGER expire_account_session_after_handler_lookup
AFTER UPDATE OF last_seen_at ON sessions
BEGIN
    UPDATE account_test_session_lookups SET count = count + 1;
    UPDATE sessions
       SET expires_at = '1970-01-01 00:00:00+00:00'
     WHERE session_token_hash = NEW.session_token_hash
       AND (SELECT count FROM account_test_session_lookups) = 2;
	INSERT INTO account_test_expected_session
	SELECT id, user_id, session_token_hash, CAST(created_at AS TEXT),
	       CAST(expires_at AS TEXT), CAST(last_seen_at AS TEXT)
	  FROM sessions
	 WHERE session_token_hash = NEW.session_token_hash
	   AND (SELECT count FROM account_test_session_lookups) = 2;
	END`
	if postgres {
		query = `
CREATE TABLE account_test_session_lookups (count INTEGER NOT NULL);
INSERT INTO account_test_session_lookups VALUES (0);
CREATE TABLE account_test_expected_session (
    id BIGINT, user_id BIGINT, session_token_hash TEXT,
    created_at TEXT, expires_at TEXT, last_seen_at TEXT
);
CREATE FUNCTION expire_account_session_after_handler_lookup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE account_test_session_lookups SET count = count + 1;
	IF (SELECT count FROM account_test_session_lookups) = 2 THEN
		UPDATE sessions
		   SET expires_at = TIMESTAMPTZ '1970-01-01 00:00:00+00'
		 WHERE session_token_hash = NEW.session_token_hash;
		INSERT INTO account_test_expected_session
		SELECT id, user_id, session_token_hash, created_at::text,
		       expires_at::text, last_seen_at::text
		  FROM sessions
		 WHERE session_token_hash = NEW.session_token_hash;
	END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER expire_account_session_after_handler_lookup
AFTER UPDATE OF last_seen_at ON sessions
FOR EACH ROW EXECUTE FUNCTION expire_account_session_after_handler_lookup()`
	}
	_, err := st.SQLDB().Exec(query)
	if err != nil {
		t.Fatalf("install account session expiry trigger: %v", err)
	}
}

type accountSessionSnapshot struct {
	ID, UserID                                  int64
	TokenHash, CreatedAt, ExpiresAt, LastSeenAt string
}

func assertAccountSessionMatchesExpirySnapshot(t *testing.T, st *storage.Store) {
	t.Helper()
	read := func(query string) accountSessionSnapshot {
		var got accountSessionSnapshot
		if err := st.SQLDB().QueryRowContext(context.Background(), query).Scan(
			&got.ID, &got.UserID, &got.TokenHash, &got.CreatedAt, &got.ExpiresAt, &got.LastSeenAt,
		); err != nil {
			t.Fatalf("read session snapshot: %v", err)
		}
		return got
	}
	want := read(`SELECT id, user_id, session_token_hash, created_at, expires_at, last_seen_at FROM account_test_expected_session`)
	got := read(`SELECT id, user_id, session_token_hash, CAST(created_at AS TEXT), CAST(expires_at AS TEXT), CAST(last_seen_at AS TEXT) FROM sessions`)
	if got != want {
		t.Fatalf("session row changed after expiry snapshot: want=%+v got=%+v", want, got)
	}
}

type accountProfileRow struct {
	JSON, Hash, UpdatedAt string
}

type accountPrivateState struct {
	User                                            storage.User
	Session                                         accountSessionSnapshot
	Profile                                         accountProfileRow
	CredentialProvider, CredentialCiphertext, Nonce string
	CredentialVersion                               int16
	UsageDay                                        string
	UsageInputTokens, UsageOutputTokens             int64
}

func accountPrivateStateSnapshot(t *testing.T, st *storage.Store, userID int64) accountPrivateState {
	t.Helper()
	got := accountPrivateState{Profile: accountProfileSnapshot(t, st, userID)}
	var found bool
	var err error
	got.User, found, err = st.UserByID(context.Background(), userID)
	if err != nil || !found {
		t.Fatalf("UserByID snapshot: found=%v err=%v", found, err)
	}
	if err := st.SQLDB().QueryRowContext(context.Background(), `
SELECT id, user_id, session_token_hash, created_at::text, expires_at::text, last_seen_at::text
  FROM sessions WHERE user_id = $1`, userID).Scan(
		&got.Session.ID, &got.Session.UserID, &got.Session.TokenHash,
		&got.Session.CreatedAt, &got.Session.ExpiresAt, &got.Session.LastSeenAt,
	); err != nil {
		t.Fatalf("session snapshot: %v", err)
	}
	// Authentication legitimately refreshes last_seen_at; deletion cancellation
	// must preserve the submitting token identity and expiry.
	got.Session.LastSeenAt = ""
	credential, found, err := st.UserAICredential(context.Background(), userID, "anthropic")
	if err != nil || !found {
		t.Fatalf("UserAICredential snapshot: found=%v err=%v", found, err)
	}
	got.CredentialProvider = credential.Provider
	got.CredentialCiphertext = hex.EncodeToString(credential.Ciphertext)
	got.Nonce = hex.EncodeToString(credential.Nonce)
	got.CredentialVersion = credential.EncryptionVersion
	if err := st.SQLDB().QueryRowContext(context.Background(), `
SELECT day::text, input_tokens, output_tokens
  FROM ai_usage WHERE user_id = $1`, userID).Scan(
		&got.UsageDay, &got.UsageInputTokens, &got.UsageOutputTokens,
	); err != nil {
		t.Fatalf("AI usage snapshot: %v", err)
	}
	return got
}

func accountProfileSnapshot(t *testing.T, st *storage.Store, userID int64) accountProfileRow {
	t.Helper()
	query := `SELECT profile_json, profile_hash, CAST(updated_at AS TEXT) FROM profile WHERE id = 1`
	args := []any{}
	if st.Dialect() == storage.DialectPostgres {
		query = `SELECT profile_json, profile_hash, updated_at::text FROM profiles WHERE user_id = $1`
		args = append(args, userID)
	}
	var got accountProfileRow
	if err := st.SQLDB().QueryRowContext(context.Background(), query, args...).Scan(&got.JSON, &got.Hash, &got.UpdatedAt); err != nil {
		t.Fatalf("read profile snapshot: %v", err)
	}
	return got
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

func postAccountMutation(t *testing.T, srv *Server, remoteAddr, path, session string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := accountFormRequest(t, srv, path, session, form)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
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
	if err := st.SQLDB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&got); err != nil {
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
