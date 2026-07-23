package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestProductionBookmarksUseSessionOwnerState(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	ctx := context.Background()

	userA, cookieA := createSessionUser(t, st, "owner-a@example.com", "session-a")
	userB, cookieB := createSessionUser(t, st, "owner-b@example.com", "session-b")
	postingID := mustUpsert(t, st, listingPosting("shared-bookmark", "공유 북마크 공고"))
	seedProductionScoredPosting(t, st, postingID, userA, userB)
	if err := st.AddBookmark(ctx, userB, postingID); err != nil {
		t.Fatalf("seed userB bookmark: %v", err)
	}

	assertPageMissing(t, srv, cookieA, "/bookmarks", "공유 북마크 공고")
	assertPageContains(t, srv, cookieB, "/bookmarks", "공유 북마크 공고")

	putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/bookmark/%d", postingID), nil)
	putReq.AddCookie(cookieA)
	addCSRFToRequest(putReq, srv, cookieA)
	putRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("bookmark add status = %d, want 200", putRec.Code)
	}
	assertBookmarkedForUser(t, st, userA, postingID, true)
	assertBookmarkedForUser(t, st, userB, postingID, true)
	assertPageContains(t, srv, cookieA, "/bookmarks", "공유 북마크 공고")

	deleteReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/bookmark/%d", postingID), nil)
	deleteReq.AddCookie(cookieA)
	addCSRFToRequest(deleteReq, srv, cookieA)
	deleteRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("bookmark delete status = %d, want 200", deleteRec.Code)
	}
	assertBookmarkedForUser(t, st, userA, postingID, false)
	assertBookmarkedForUser(t, st, userB, postingID, true)
	assertPageMissing(t, srv, cookieA, "/bookmarks", "공유 북마크 공고")
	assertPageContains(t, srv, cookieB, "/bookmarks", "공유 북마크 공고")

	userBOnlyPostingID := mustUpsert(t, st, listingPosting("user-b-bookmark", "유저B 전용 북마크"))
	seedProductionScoredPosting(t, st, userBOnlyPostingID, userA, userB)
	putBReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/bookmark/%d", userBOnlyPostingID), nil)
	putBReq.AddCookie(cookieB)
	addCSRFToRequest(putBReq, srv, cookieB)
	putBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(putBRec, putBReq)
	if putBRec.Code != http.StatusOK {
		t.Fatalf("userB bookmark add status = %d, want 200", putBRec.Code)
	}
	assertBookmarkedForUser(t, st, userA, userBOnlyPostingID, false)
	assertBookmarkedForUser(t, st, userB, userBOnlyPostingID, true)
	assertPageMissing(t, srv, cookieA, "/bookmarks", "유저B 전용 북마크")
	assertPageContains(t, srv, cookieB, "/bookmarks", "유저B 전용 북마크")
}

func TestPostgresDemoNeverMergesAnotherUsersAICache(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetDemoMode(true)
	ctx := context.Background()
	userA := insertAIRuntimeTestUser(t, st, "demo-cache-a@example.invalid")
	userB := insertAIRuntimeTestUser(t, st, "demo-cache-b@example.invalid")
	saveAIRuntimeProfile(t, st, userA, profile.Profile{CareerYears: 0})
	saveAIRuntimeProfile(t, st, userB, profile.Profile{CareerYears: 0})
	postingID := mustUpsert(t, st, listingPosting("demo-cache-isolation", "Demo cache isolation"))
	if err := st.UpsertAIScore(ctx, userA, postingID, "hash-a", "version-a", ai.Delta{
		Items: []ai.DeltaItem{{Signal: "private-a", Delta: 7, Evidence: "private-a"}}, NetDelta: 7,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("seed user A AI score: %v", err)
	}

	if _, err := srv.RescoreAll(ctx, userB, nil); err != nil {
		t.Fatalf("RescoreAll user B: %v", err)
	}
	scores, err := st.ScoresByPostingID(ctx, userB)
	if err != nil {
		t.Fatalf("ScoresByPostingID user B: %v", err)
	}
	if strings.Contains(scores[postingID].BreakdownJSON, "AI 분석") || strings.Contains(scores[postingID].BreakdownJSON, "private-a") {
		t.Fatalf("user B score merged user A demo cache: %s", scores[postingID].BreakdownJSON)
	}
}

func TestProductionProfileSaveIsRejectedWhileScoringOperationRuns(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-lock@example.invalid", "profile-lock-session")
	saveAIRuntimeProfile(t, st, userID, profile.Profile{JobLikes: "unchanged goal"})
	lease := srv.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		t.Fatal("failed to arrange in-flight scoring operation")
	}
	defer lease.release()

	form := url.Values{"job_likes": {"must not persist"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("profile save status = %d, want 409", rec.Code)
	}
	got, _, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok || !strings.Contains(got, "unchanged goal") || strings.Contains(got, "must not persist") {
		t.Fatalf("profile changed while lock held: ok=%v err=%v profile=%s", ok, err, got)
	}
}

func TestProductionUserASaveLeavesAllUserBStateAndRuntimeUnchanged(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userA, cookieA := createSessionUser(t, st, "save-isolation-a@example.invalid", "save-isolation-a-session")
	userB := insertAIRuntimeTestUser(t, st, "save-isolation-b@example.invalid")
	cipher := newAIRuntimeTestCipher(t, 0x62)
	srv.SetCredentialCipher(cipher)
	srv.newAIProvider = func(provider, key, _ string, _ time.Duration) (ai.Provider, error) {
		return &fingerprintProvider{name: provider, keyFingerprint: keyFingerprint(key)}, nil
	}
	zero := 0
	saveAIRuntimeProfile(t, st, userA, profile.Profile{CareerYears: 0, MinScore: &zero, AIProvider: "anthropic", AIModel: "model-a", JobLikes: "goal-a"})
	saveAIRuntimeProfile(t, st, userB, profile.Profile{CareerYears: 0, MinScore: &zero, AIProvider: "anthropic", AIModel: "model-b", JobLikes: "goal-b"})
	saveAIRuntimeCredential(t, st, cipher, userA, "anthropic", "synthetic-key-a")
	saveAIRuntimeCredential(t, st, cipher, userB, "anthropic", "synthetic-key-b")
	p := listingPosting("save-isolation", "저장 격리 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	mustUpsert(t, st, p)
	runtimeB, err := srv.aiRuntimeForUser(context.Background(), userB)
	if err != nil {
		t.Fatalf("aiRuntimeForUser B before: %v", err)
	}
	if _, err := srv.RescoreAll(context.Background(), userB, runtimeB); err != nil {
		t.Fatalf("RescoreAll B before: %v", err)
	}
	profileBBefore, hashBBefore, _, _ := st.ProfileForUser(context.Background(), userB)
	credentialBBefore, ok, err := st.UserAICredential(context.Background(), userB, "anthropic")
	if err != nil || !ok {
		t.Fatalf("UserAICredential B before: ok=%v err=%v", ok, err)
	}
	scoresBBefore, err := st.ScoresByPostingID(context.Background(), userB)
	if err != nil {
		t.Fatalf("ScoresByPostingID B before: %v", err)
	}
	fingerprintBBefore := runtimeB.Provider.(*fingerprintProvider).keyFingerprint

	form := url.Values{
		"career_years": {"0"}, "min_score": {"0"}, "job_likes": {"goal-a-updated"},
		"ai_provider": {"anthropic"}, "ai_model": {"model-a-updated"}, "ai_key": {"synthetic-key-a-updated"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieA)
	addCSRFToRequest(req, srv, cookieA)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("user A profile save status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}

	profileBAfter, hashBAfter, _, _ := st.ProfileForUser(context.Background(), userB)
	credentialBAfter, ok, err := st.UserAICredential(context.Background(), userB, "anthropic")
	if err != nil || !ok {
		t.Fatalf("UserAICredential B after: ok=%v err=%v", ok, err)
	}
	scoresBAfter, err := st.ScoresByPostingID(context.Background(), userB)
	if err != nil {
		t.Fatalf("ScoresByPostingID B after: %v", err)
	}
	runtimeBAfter, err := srv.aiRuntimeForUser(context.Background(), userB)
	if err != nil {
		t.Fatalf("aiRuntimeForUser B after: %v", err)
	}
	if profileBAfter != profileBBefore || hashBAfter != hashBBefore {
		t.Fatalf("user B profile changed: before=%s/%s after=%s/%s", profileBBefore, hashBBefore, profileBAfter, hashBAfter)
	}
	if !bytes.Equal(credentialBAfter.Ciphertext, credentialBBefore.Ciphertext) ||
		!bytes.Equal(credentialBAfter.Nonce, credentialBBefore.Nonce) ||
		credentialBAfter.EncryptionVersion != credentialBBefore.EncryptionVersion {
		t.Fatal("user B encrypted credential bytes/version changed after user A save")
	}
	if !reflect.DeepEqual(scoresBAfter, scoresBBefore) {
		t.Fatalf("user B score rows changed: before=%+v after=%+v", scoresBBefore, scoresBAfter)
	}
	if got := runtimeBAfter.Provider.(*fingerprintProvider).keyFingerprint; got != fingerprintBBefore {
		t.Fatalf("user B runtime fingerprint changed: before=%s after=%s", fingerprintBBefore, got)
	}
}

func TestProductionRerateSSEPublishesTerminalStatusAndReusesCache(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "rerate-sse@example.invalid", "rerate-sse-session")
	cipher := newAIRuntimeTestCipher(t, 0x72)
	srv.SetCredentialCipher(cipher)
	stub := rerateStub()
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return stub, nil }
	zero := 0
	saveAIRuntimeProfile(t, st, userID, profile.Profile{
		CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발",
		AIProvider: "anthropic", AIModel: "test-model",
	})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "synthetic-rerate-key")
	now := time.Now().UTC()
	for _, sourceID := range []string{"rerate-sse-1", "rerate-sse-2"} {
		p := listingPosting(sourceID, "신입 백엔드 개발자")
		p.Description = "백엔드 서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(context.Background(), p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	runtime, err := srv.aiRuntimeForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("aiRuntimeForUser: %v", err)
	}
	if _, err := srv.RescoreAll(context.Background(), userID, runtime); err != nil {
		t.Fatalf("RescoreAll: %v", err)
	}

	run := func(wantOutcome rerateOutcome) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/rerate?surface=today&entry=entry-token-00000001", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("rerate status = %d, want 200; body=%q", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "event: run") || !strings.Contains(body, "event: done") {
			t.Fatalf("rerate SSE lifecycle missing: %s", body)
		}

		statusReq := httptest.NewRequest(http.MethodGet, "/api/rerate/status?surface=today", nil)
		statusReq.AddCookie(cookie)
		statusRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(statusRec, statusReq)
		var status rerateStatus
		if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode rerate status: %v", err)
		}
		if status.State != rerateStateDone || status.RunID == 0 || status.Outcome != wantOutcome {
			t.Fatalf("terminal rerate status = %+v, want done/%s", status, wantOutcome)
		}
	}

	run(rerateOutcomeChanged)
	callsAfterFirst := stub.ScoreDeltaCalls
	run(rerateOutcomeCached)
	if stub.ScoreDeltaCalls != callsAfterFirst {
		t.Fatalf("cached rerate provider calls = %d, want unchanged %d", stub.ScoreDeltaCalls, callsAfterFirst)
	}
}

func TestRerateSSEEmitsDone(t *testing.T) {
	srv, cookie, _ := newProductionRerateFixture(t, 2, 0)
	rec, _ := runAuthenticatedRerate(t, srv, cookie, "entry-token-00000001")
	if !strings.Contains(rec.Body.String(), "event: done") {
		t.Fatalf("SSE stream missing terminal done: %s", rec.Body.String())
	}
}

func TestRerateSSEPublishesTerminalStatus(t *testing.T) {
	srv, cookie, _ := newProductionRerateFixture(t, 2, 0)
	_, status := runAuthenticatedRerate(t, srv, cookie, "entry-token-00000001")
	if status.State != rerateStateDone || status.RunID == 0 || status.Message == "" || status.Outcome != rerateOutcomeChanged {
		t.Fatalf("terminal status = %+v, want done/changed", status)
	}
}

func TestRerateSSEPublishesCachedAndPartialOutcomes(t *testing.T) {
	t.Run("cached", func(t *testing.T) {
		srv, cookie, stub := newProductionRerateFixture(t, 2, 0)
		_, _ = runAuthenticatedRerate(t, srv, cookie, "entry-token-00000001")
		calls := stub.ScoreDeltaCalls
		_, status := runAuthenticatedRerate(t, srv, cookie, "entry-token-00000002")
		if status.Outcome != rerateOutcomeCached || stub.ScoreDeltaCalls != calls {
			t.Fatalf("cached status=%+v calls=%d, want cached and %d", status, stub.ScoreDeltaCalls, calls)
		}
	})
	t.Run("partial", func(t *testing.T) {
		srv, cookie, _ := newProductionRerateFixture(t, 2, 1)
		_, status := runAuthenticatedRerate(t, srv, cookie, "entry-token-00000001")
		if status.Outcome != rerateOutcomePartial || !strings.Contains(status.Message, "1/2") {
			t.Fatalf("partial status = %+v, want partial 1/2", status)
		}
	})
	t.Run("empty", func(t *testing.T) {
		srv, cookie, _ := newProductionRerateFixture(t, 0, 0)
		_, status := runAuthenticatedRerate(t, srv, cookie, "entry-token-00000001")
		if status.Outcome != rerateOutcomeEmpty {
			t.Fatalf("empty status = %+v, want empty", status)
		}
	})
}

func TestRerateStatusPublishesServerAuthoritativeHistoryOwner(t *testing.T) {
	srv, cookie, _ := newProductionRerateFixture(t, 1, 0)
	const entry = "entry-token-1234567890"
	_, status := runAuthenticatedRerate(t, srv, cookie, entry)
	if status.RunID == 0 || status.RunToken == "" || status.OwnerEntry != entry {
		t.Fatalf("server lifecycle identity = %+v", status)
	}
}

func newProductionRerateFixture(t *testing.T, postingCount, perCallCap int) (*Server, *http.Cookie, *ai.StubProvider) {
	t.Helper()
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, fmt.Sprintf("rerate-fixture-%d@example.invalid", rand.Int64()), fmt.Sprintf("rerate-session-%d", rand.Int64()))
	cipher := newAIRuntimeTestCipher(t, 0x73)
	srv.SetCredentialCipher(cipher)
	stub := rerateStub()
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return stub, nil }
	zero := 0
	saveAIRuntimeProfile(t, st, userID, profile.Profile{
		CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발",
		AIProvider: "anthropic", AIModel: "test-model", AIPerCallCap: perCallCap,
	})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "synthetic-rerate-fixture-key")
	now := time.Now().UTC()
	for i := 0; i < postingCount; i++ {
		p := listingPosting(fmt.Sprintf("rerate-fixture-%d", i), "신입 백엔드 개발자")
		p.Description = "백엔드 서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(context.Background(), p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	runtime, err := srv.aiRuntimeForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("aiRuntimeForUser: %v", err)
	}
	if _, err := srv.RescoreAll(context.Background(), userID, runtime); err != nil {
		t.Fatalf("RescoreAll: %v", err)
	}
	return srv, cookie, stub
}

func runAuthenticatedRerate(t *testing.T, srv *Server, cookie *http.Cookie, entry string) (*httptest.ResponseRecorder, rerateStatus) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/rerate?surface=today&entry="+entry, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rerate status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	statusReq := httptest.NewRequest(http.MethodGet, "/api/rerate/status?surface=today", nil)
	statusReq.AddCookie(cookie)
	statusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec, statusReq)
	var status rerateStatus
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode rerate status: %v", err)
	}
	return rec, status
}

func TestProductionManualScrapeDegradesCredentialFailureToRules(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("manual-runtime-fallback", "수동 규칙 점수 공고")}}
	srv, st := newPostgresTestServer(t, f)
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "manual-fallback@example.invalid", "manual-fallback-session")
	encryptingCipher := newAIRuntimeTestCipher(t, 0x31)
	srv.SetCredentialCipher(newAIRuntimeTestCipher(t, 0x32))
	zero := 0
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, MinScore: &zero, AIProvider: "anthropic"})
	saveAIRuntimeCredential(t, st, encryptingCipher, userID, "anthropic", "synthetic-manual-key")

	req := httptest.NewRequest(http.MethodGet, "/api/scrape", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual scrape status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "규칙 기반 점수로 계속") || !strings.Contains(body, "event: done") {
		t.Fatalf("manual fallback SSE missing warning/done: %s", body)
	}
	postings, err := st.AllPostings(context.Background())
	if err != nil || len(postings) != 1 {
		t.Fatalf("manual fallback postings = %d err=%v, want 1", len(postings), err)
	}
	scores, err := st.ScoresByPostingID(context.Background(), userID)
	if err != nil || len(scores) != 1 {
		t.Fatalf("manual fallback scores = %d err=%v, want 1", len(scores), err)
	}
}

func TestProductionProfileSaveRedirectsAfterPostCommitRuntimeFailure(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-runtime-fallback@example.invalid", "profile-runtime-fallback-session")
	encryptingCipher := newAIRuntimeTestCipher(t, 0x41)
	srv.SetCredentialCipher(newAIRuntimeTestCipher(t, 0x42))
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, JobLikes: "old goal", AIProvider: "anthropic"})
	saveAIRuntimeCredential(t, st, encryptingCipher, userID, "anthropic", "synthetic-old-key")
	p := listingPosting("profile-runtime-fallback", "프로필 저장 규칙 점수 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	postingID := mustUpsert(t, st, p)

	form := url.Values{"career_years": {"0"}, "job_likes": {"new committed goal"}, "ai_provider": {"anthropic"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("profile save status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}
	gotProfile, gotHash, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok || !strings.Contains(gotProfile, "new committed goal") {
		t.Fatalf("committed profile = %s ok=%v err=%v", gotProfile, ok, err)
	}
	scores, err := st.ScoresByPostingID(context.Background(), userID)
	if err != nil || scores[postingID].ProfileHash != gotHash {
		t.Fatalf("rule fallback score = %+v err=%v, want profile hash %s", scores[postingID], err, gotHash)
	}
}

func TestProductionProfileSaveRedirectsAfterPostCommitAIRescoreFailure(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-rescore-fallback@example.invalid", "profile-rescore-fallback-session")
	cipher := newAIRuntimeTestCipher(t, 0x43)
	srv.SetCredentialCipher(cipher)
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return rerateStub(), nil }
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, JobLikes: "old goal", AIProvider: "anthropic"})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "synthetic-rescore-key")
	p := listingPosting("profile-rescore-fallback", "프로필 재점수 복구 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	postingID := mustUpsert(t, st, p)
	if _, err := st.SQLDB().Exec(`DROP TABLE ai_extractions`); err != nil {
		t.Fatalf("drop AI extraction cache: %v", err)
	}

	form := url.Values{"career_years": {"0"}, "job_likes": {"new rescore goal"}, "ai_provider": {"anthropic"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("profile save status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}
	_, gotHash, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok {
		t.Fatalf("committed profile ok=%v err=%v", ok, err)
	}
	scores, err := st.ScoresByPostingID(context.Background(), userID)
	if err != nil || scores[postingID].ProfileHash != gotHash {
		t.Fatalf("rescore fallback score = %+v err=%v, want profile hash %s", scores[postingID], err, gotHash)
	}
}

func TestProductionRenderSuppressesStaleProfileHash(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	userID := insertAIRuntimeTestUser(t, st, "stale-render@example.invalid")
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0})
	p := listingPosting("stale-render", "오래된 점수 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	postingID := mustUpsert(t, st, p)
	if err := st.UpsertScoreForUser(context.Background(), userID, storage.Score{
		PostingID: postingID, ProfileHash: "stale-profile-hash", Total: 99,
		BreakdownJSON: `{"Total":99}`, ComputedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale score: %v", err)
	}

	b, err := srv.buildBriefing(context.Background(), time.Now(), userID)
	if err != nil {
		t.Fatalf("buildBriefing: %v", err)
	}
	for _, row := range append(b.Today, b.Excluded...) {
		if row.Posting.ID == postingID && row.Total == 99 {
			t.Fatalf("stale score rendered: %+v", row)
		}
	}
}

func TestProductionRenderUsesOneProfileSnapshotForScoreFilteringAndLayout(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "render-snapshot@example.invalid")
	zero, hundred := 0, 100
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, MinScore: &zero})
	p := listingPosting("render-snapshot", "동일 스냅샷 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	postingID := mustUpsert(t, st, p)
	if _, err := srv.RescoreAll(ctx, userID, nil); err != nil {
		t.Fatalf("seed current score: %v", err)
	}

	snapshotReady := make(chan struct{})
	resumeRender := make(chan struct{})
	srv.afterRenderScoreSnapshot = func() {
		close(snapshotReady)
		<-resumeRender
	}
	type renderResult struct {
		briefing briefing
		err      error
	}
	rendered := make(chan renderResult, 1)
	go func() {
		b, err := srv.buildBriefing(ctx, time.Now(), userID)
		rendered <- renderResult{briefing: b, err: err}
	}()
	select {
	case <-snapshotReady:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not reach the score/profile snapshot boundary")
	}

	// Simulate a committed save whose best-effort rescore then fails. The
	// in-flight render may use the old snapshot, but must not combine the old
	// score with this new profile's MinScore.
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, MinScore: &hundred})
	close(resumeRender)
	result := <-rendered
	if result.err != nil {
		t.Fatalf("buildBriefing: %v", result.err)
	}
	if !contains(result.briefing.Today, p.Title) {
		t.Fatalf("render mixed profile snapshots: today=%+v excluded=%+v posting=%d", result.briefing.Today, result.briefing.Excluded, postingID)
	}
	if contains(result.briefing.Excluded, p.Title) {
		t.Fatalf("old-hash score rendered under the newly committed profile")
	}
}

func TestSQLiteRenderRejectsScoresCommittedForANewerProfileSnapshot(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero, hundred := 0, 100
	saveAIRuntimeProfile(t, st, 0, profile.Profile{CareerYears: 0, MinScore: &zero})
	p := listingPosting("sqlite-render-snapshot", "SQLite 동일 스냅샷 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	mustUpsert(t, st, p)
	if _, err := srv.RescoreAll(ctx, 0, nil); err != nil {
		t.Fatalf("seed current score: %v", err)
	}

	profileSnapshotReady := make(chan struct{})
	resumeRender := make(chan struct{})
	srv.afterRenderProfileSnapshot = func() {
		close(profileSnapshotReady)
		<-resumeRender
	}
	type renderResult struct {
		briefing briefing
		err      error
	}
	rendered := make(chan renderResult, 1)
	go func() {
		b, err := srv.buildBriefing(ctx, time.Now())
		rendered <- renderResult{briefing: b, err: err}
	}()
	select {
	case <-profileSnapshotReady:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not capture its profile snapshot")
	}

	// Simulate profile save committing its new rule scores while this render is
	// still using the old profile snapshot.
	saveAIRuntimeProfile(t, st, 0, profile.Profile{CareerYears: 0, MinScore: &hundred})
	if _, err := srv.RescoreAll(ctx, 0, nil); err != nil {
		t.Fatalf("rescore newer profile: %v", err)
	}
	close(resumeRender)
	result := <-rendered
	if result.err != nil {
		t.Fatalf("buildBriefing: %v", result.err)
	}
	if contains(result.briefing.Today, p.Title) || contains(result.briefing.Excluded, p.Title) {
		t.Fatalf("new-profile score rendered with old profile snapshot: %+v", result.briefing)
	}
}

func TestProductionScoredSurfacesOmitPostingsWithStaleProfileHash(t *testing.T) {
	tests := []struct {
		name   string
		seed   func(context.Context, *storage.Store, int64, int64) error
		render func(context.Context, *Server, int64, string) (bool, error)
	}{
		{
			name: "archive",
			seed: func(context.Context, *storage.Store, int64, int64) error { return nil },
			render: func(ctx context.Context, srv *Server, userID int64, _ string) (bool, error) {
				view, err := srv.buildArchive(ctx, time.Now(), userID)
				if err != nil {
					return false, err
				}
				return view.Total != 0 || len(view.Days) != 0 || len(view.Excluded) != 0, nil
			},
		},
		{
			name: "bookmarks",
			seed: func(ctx context.Context, st *storage.Store, userID, postingID int64) error {
				return st.AddBookmark(ctx, userID, postingID)
			},
			render: func(ctx context.Context, srv *Server, userID int64, title string) (bool, error) {
				view, err := srv.buildBookmarks(ctx, time.Now(), userID)
				return contains(view.Postings, title), err
			},
		},
		{
			name: "hidden",
			seed: func(ctx context.Context, st *storage.Store, userID, postingID int64) error {
				return st.AddNotInterested(ctx, userID, postingID, time.Now().UTC())
			},
			render: func(ctx context.Context, srv *Server, userID int64, title string) (bool, error) {
				view, err := srv.buildHidden(ctx, time.Now(), userID)
				return contains(view.Postings, title), err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, st := newPostgresTestServer(t, &fakeScraper{})
			ctx := context.Background()
			userID := insertAIRuntimeTestUser(t, st, "stale-"+tt.name+"@example.invalid")
			zero, hundred := 0, 100
			saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, MinScore: &zero})
			p := listingPosting("stale-"+tt.name, "오래된 "+tt.name+" 점수 공고")
			p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
			postingID := mustUpsert(t, st, p)
			if err := tt.seed(ctx, st, userID, postingID); err != nil {
				t.Fatalf("seed %s state: %v", tt.name, err)
			}
			if _, err := srv.RescoreAll(ctx, userID, nil); err != nil {
				t.Fatalf("seed current score: %v", err)
			}
			// Commit a new profile without the best-effort rescore succeeding.
			saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, MinScore: &hundred})

			rendered, err := tt.render(ctx, srv, userID, p.Title)
			if err != nil {
				t.Fatalf("render %s: %v", tt.name, err)
			}
			if rendered {
				t.Fatalf("%s rendered a stale or fabricated zero-score card for posting %d", tt.name, postingID)
			}
		})
	}
}

type countingCipher struct {
	inner credential.Cipher
	opens atomic.Int32
}

type failingSealCipher struct{ err error }

func (c failingSealCipher) Seal(int64, string, string) ([]byte, []byte, int16, error) {
	return nil, nil, 0, c.err
}

type sealCountingCipher struct {
	inner credential.Cipher
	seals atomic.Int32
}

func (c *sealCountingCipher) Seal(userID int64, provider, plaintext string) ([]byte, []byte, int16, error) {
	c.seals.Add(1)
	return c.inner.Seal(userID, provider, plaintext)
}
func (c *sealCountingCipher) Open(userID int64, provider string, ciphertext, nonce []byte, version int16) (string, error) {
	return c.inner.Open(userID, provider, ciphertext, nonce, version)
}
func (c failingSealCipher) Open(int64, string, []byte, []byte, int16) (string, error) {
	return "", c.err
}

func TestProductionProfileSaveEncryptsKeyAndBlankKeepsCredential(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-key@example.invalid", "profile-key-session")
	cipher := newAIRuntimeTestCipher(t, 0x51)
	srv.SetCredentialCipher(cipher)
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return rerateStub(), nil }

	postProfile := func(key, likes string) *httptest.ResponseRecorder {
		form := url.Values{
			"ai_provider": {"anthropic"},
			"ai_model":    {"claude-haiku-4-5-20251001"},
			"ai_key":      {key},
			"job_likes":   {likes},
			"min_score":   {"0"},
		}
		req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		addCSRFToRequest(req, srv, cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := postProfile("synthetic-profile-provider-key", "first goal"); rec.Code != http.StatusSeeOther {
		t.Fatalf("new-key save status=%d body=%q", rec.Code, rec.Body.String())
	}
	before, ok, err := st.UserAICredential(context.Background(), userID, "anthropic")
	if err != nil || !ok {
		t.Fatalf("UserAICredential after save: ok=%v err=%v", ok, err)
	}
	opened, err := cipher.Open(userID, "anthropic", before.Ciphertext, before.Nonce, before.EncryptionVersion)
	if err != nil || opened != "synthetic-profile-provider-key" {
		t.Fatalf("stored credential did not decrypt to submitted value: matched=%v err=%v", opened == "synthetic-profile-provider-key", err)
	}
	if rec := postProfile("", "second goal"); rec.Code != http.StatusSeeOther {
		t.Fatalf("blank-key save status=%d body=%q", rec.Code, rec.Body.String())
	}
	after, ok, err := st.UserAICredential(context.Background(), userID, "anthropic")
	if err != nil || !ok || string(after.Ciphertext) != string(before.Ciphertext) || string(after.Nonce) != string(before.Nonce) || after.EncryptionVersion != before.EncryptionVersion {
		t.Fatalf("blank key changed credential: ok=%v err=%v", ok, err)
	}
}

func TestProductionProfileSaveEncryptionFailureChangesNothing(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-seal-fail@example.invalid", "profile-seal-fail-session")
	saveAIRuntimeProfile(t, st, userID, profile.Profile{JobLikes: "unchanged goal"})
	srv.SetCredentialCipher(failingSealCipher{err: errors.New("synthetic seal failure")})
	form := url.Values{"ai_provider": {"anthropic"}, "ai_key": {"synthetic-new-key"}, "job_likes": {"must not persist"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "synthetic-new-key") {
		t.Fatalf("seal failure status=%d body=%q", rec.Code, rec.Body.String())
	}
	got, _, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok || !strings.Contains(got, "unchanged goal") || strings.Contains(got, "must not persist") {
		t.Fatalf("profile changed after encryption failure: ok=%v err=%v profile=%s", ok, err, got)
	}
	if _, ok, err := st.UserAICredential(context.Background(), userID, "anthropic"); err != nil || ok {
		t.Fatalf("credential persisted after encryption failure: ok=%v err=%v", ok, err)
	}
}

func TestProductionProfileFailureAfterKeyPreparationRollsBackCredential(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-rollback@example.invalid", "profile-rollback-session")
	base := newAIRuntimeTestCipher(t, 0x52)
	saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic", JobLikes: "old goal"})
	saveAIRuntimeCredential(t, st, base, userID, "anthropic", "synthetic-old-key")
	before, _, _ := st.UserAICredential(context.Background(), userID, "anthropic")
	spy := &sealCountingCipher{inner: base}
	srv.SetCredentialCipher(spy)
	if _, err := st.SQLDB().ExecContext(context.Background(), `
CREATE FUNCTION fail_handler_profile_save() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'synthetic profile failure'; END $$;
CREATE TRIGGER fail_handler_profile_save
BEFORE INSERT OR UPDATE ON profiles
FOR EACH ROW EXECUTE FUNCTION fail_handler_profile_save()`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	form := url.Values{"ai_provider": {"anthropic"}, "ai_key": {"synthetic-new-key"}, "job_likes": {"new goal"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || spy.seals.Load() != 1 || strings.Contains(rec.Body.String(), "synthetic-new-key") {
		t.Fatalf("profile failure status=%d seals=%d body=%q", rec.Code, spy.seals.Load(), rec.Body.String())
	}
	after, ok, err := st.UserAICredential(context.Background(), userID, "anthropic")
	if err != nil || !ok || string(after.Ciphertext) != string(before.Ciphertext) || string(after.Nonce) != string(before.Nonce) || after.EncryptionVersion != before.EncryptionVersion {
		t.Fatalf("credential changed after profile failure: ok=%v err=%v", ok, err)
	}
	got, _, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok || !strings.Contains(got, "old goal") || strings.Contains(got, "new goal") {
		t.Fatalf("profile changed after rollback: ok=%v err=%v profile=%s", ok, err, got)
	}
}

func TestProductionProfileDealbreakerChangeIsProviderFreeAndInvalidatesRerateStatus(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "profile-dealbreaker@example.invalid", "profile-dealbreaker-session")
	cipher := newAIRuntimeTestCipher(t, 0x63)
	srv.SetCredentialCipher(cipher)
	saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic", AIModel: "shared-model", Dealbreakers: []string{"야근"}})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "synthetic-profile-key")
	provider := &ai.StubProvider{NameVal: "anthropic"}
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return provider, nil }
	run := srv.rerates.start(userID, "today", "entry-token-00000001")
	srv.rerates.complete(userID, "today", run.RunID, rerateOutcomeCached, "cached")

	form := url.Values{
		"ai_provider":  {"anthropic"},
		"ai_model":     {"shared-model"},
		"dealbreakers": {"리서치"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("profile save status=%d body=%q", rec.Code, rec.Body.String())
	}
	if provider.Calls != 0 || provider.ValidateDealbreakersCalls != 0 || provider.ScoreDeltaCalls != 0 {
		t.Fatalf("profile save made paid calls: extract=%d validation=%d stage2=%d", provider.Calls, provider.ValidateDealbreakersCalls, provider.ScoreDeltaCalls)
	}
	if _, ok := srv.rerates.snapshot(userID, "today"); ok {
		t.Fatal("changed dealbreaker profile retained stale rerate status")
	}
}

func (c *countingCipher) Seal(userID int64, provider, plaintext string) ([]byte, []byte, int16, error) {
	return c.inner.Seal(userID, provider, plaintext)
}

func (c *countingCipher) Open(userID int64, provider string, ciphertext, nonce []byte, version int16) (string, error) {
	c.opens.Add(1)
	return c.inner.Open(userID, provider, ciphertext, nonce, version)
}

func TestProductionRerateDecryptsCredentialOnceForMultipleRows(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "decrypt-once@example.invalid")
	prof := profile.Profile{CareerYears: 0, JobLikes: "백엔드 서버 개발", AIProvider: "anthropic", AIModel: "synthetic-model"}
	zero := 0
	prof.MinScore = &zero
	saveAIRuntimeProfile(t, st, userID, prof)
	base := newAIRuntimeTestCipher(t, 0x61)
	saveAIRuntimeCredential(t, st, base, userID, "anthropic", "synthetic-decrypt-once-key")
	spy := &countingCipher{inner: base}
	srv.SetCredentialCipher(spy)
	provider := rerateStub()
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return provider, nil }
	now := time.Now().UTC()
	for _, sourceID := range []string{"decrypt-a", "decrypt-b", "decrypt-c"} {
		p := listingPosting(sourceID, "신입 백엔드 개발자")
		p.Description = "서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		mustUpsert(t, st, p)
	}
	runtime, err := srv.aiRuntimeForUser(ctx, userID)
	if err != nil {
		t.Fatalf("aiRuntimeForUser: %v", err)
	}
	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}
	if _, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime); err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if got := spy.opens.Load(); got != 1 {
		t.Fatalf("credential decrypts = %d, want exactly 1 for the operation", got)
	}
	if provider.ScoreDeltaCalls != 3 {
		t.Fatalf("ScoreDelta calls = %d, want 3 rows", provider.ScoreDeltaCalls)
	}
}

type isolatedProvider struct {
	name  string
	delta int
	mu    sync.Mutex
	calls int
}

func (p *isolatedProvider) Name() string { return p.name }
func (p *isolatedProvider) Extract(context.Context, string) (ai.Extraction, ai.Usage, error) {
	return ai.Extraction{}, ai.Usage{}, ai.ErrNotImplemented
}
func (p *isolatedProvider) ValidateDealbreakers(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
	return nil, ai.Usage{}, ai.ErrNotImplemented
}
func (p *isolatedProvider) ScoreDelta(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return []ai.RawDeltaItem{{Signal: p.name, Kind: ai.KindPresence, Delta: p.delta, Quote: "서버 개발자를 찾습니다"}}, ai.Usage{InputTokens: 10, OutputTokens: 2}, nil
}

func TestProductionConcurrentReratesIsolateUserRuntimeScoresAndUsage(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userA := insertAIRuntimeTestUser(t, st, "concurrent-a@example.invalid")
	userB := insertAIRuntimeTestUser(t, st, "concurrent-b@example.invalid")
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	saveAIRuntimeProfile(t, st, userA, prof)
	saveAIRuntimeProfile(t, st, userB, prof)
	now := time.Now().UTC()
	p := listingPosting("concurrent-shared", "신입 백엔드 개발자")
	p.Description = "서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	postingID := mustUpsert(t, st, p)
	p.ID = postingID
	providerA := &isolatedProvider{name: "user-a-provider", delta: 7}
	providerB := &isolatedProvider{name: "user-b-provider", delta: -4}
	runtimeA := testAIRuntime(userA, providerA, "model-a")
	runtimeB := testAIRuntime(userB, providerB, "model-b")
	if _, err := srv.scoreAll(ctx, userA, runtimeA); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoreAll(ctx, userB, runtimeB); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, op := range []struct {
		userID  int64
		runtime *AIRuntime
	}{{userA, runtimeA}, {userB, runtimeB}} {
		wg.Add(1)
		go func(userID int64, runtime *AIRuntime) {
			defer wg.Done()
			_, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime)
			errs <- err
		}(op.userID, op.runtime)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent rerate: %v", err)
		}
	}
	scoreA, ok, err := st.ScoreByPostingIDForUser(ctx, userA, postingID)
	if err != nil || !ok {
		t.Fatalf("user A score: ok=%v err=%v", ok, err)
	}
	scoreB, ok, err := st.ScoreByPostingIDForUser(ctx, userB, postingID)
	if err != nil || !ok {
		t.Fatalf("user B score: ok=%v err=%v", ok, err)
	}
	if scoreA.Total == scoreB.Total || scoreA.Total <= scoreB.Total {
		t.Fatalf("user scores crossed: A=%s B=%s", scoreA.BreakdownJSON, scoreB.BreakdownJSON)
	}
	hash := profile.AIInputHash(prof)
	deltaA, ok, err := st.AIScore(ctx, userA, postingID, hash, runtimeA.ScoreVersion)
	if err != nil || !ok || deltaA.NetDelta != 7 {
		t.Fatalf("user A delta=%+v ok=%v err=%v", deltaA, ok, err)
	}
	deltaB, ok, err := st.AIScore(ctx, userB, postingID, hash, runtimeB.ScoreVersion)
	if err != nil || !ok || deltaB.NetDelta != -4 {
		t.Fatalf("user B delta=%+v ok=%v err=%v", deltaB, ok, err)
	}
	day := time.Now().UTC().Format("2006-01-02")
	for _, userID := range []int64{userA, userB} {
		in, out, err := st.AIUsageForDay(ctx, userID, day)
		if err != nil || in != 10 || out != 2 {
			t.Fatalf("user %d daily usage=(%d,%d) err=%v, want (10,2)", userID, in, out, err)
		}
		monthIn, monthOut, err := st.AIUsageForMonth(ctx, userID, day[:7])
		if err != nil || monthIn != 10 || monthOut != 2 {
			t.Fatalf("user %d monthly usage=(%d,%d) err=%v, want (10,2)", userID, monthIn, monthOut, err)
		}
	}
}

func TestProductionDealbreakerValidationIsolatesUserProfiles(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userA := insertAIRuntimeTestUser(t, st, "dealbreaker-a@example.invalid")
	userB := insertAIRuntimeTestUser(t, st, "dealbreaker-b@example.invalid")
	profiles := map[int64]profile.Profile{
		userA: {Dealbreakers: []string{"리서치"}},
		userB: {Dealbreakers: []string{"야근"}},
	}
	now := time.Now().UTC()
	p := listingPosting("shared-dealbreaker", "신입 리서치 개발자")
	p.Description = "리서치와 야근 업무를 담당합니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	postingID := mustUpsert(t, st, p)
	p.ID = postingID

	for userID, prof := range profiles {
		saveAIRuntimeProfile(t, st, userID, prof)
		provider := &ai.StubProvider{
			NameVal: "stub",
			ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
				return []ai.DealbreakerValidation{{
					CandidateID: candidates[0].ID,
					Verdict:     ai.DealbreakerApplies,
					Evidence:    candidates[0].Phrase,
				}}, ai.Usage{InputTokens: 10, OutputTokens: 2}, nil
			},
		}
		runtime := testAIRuntime(userID, provider, "shared-model")
		budget := srv.newAIBudget(ctx, userID, runtime)
		if calls, err := srv.validateDealbreakers(ctx, userID, []scraper.Posting{p}, prof, runtime, budget, &callCap{max: 1}, noopEmit); err != nil || calls != 1 {
			t.Fatalf("user %d validateDealbreakers calls=%d err=%v", userID, calls, err)
		}
		if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
			t.Fatalf("user %d scoreAll: %v", userID, err)
		}
	}

	for userID, phrase := range map[int64]string{userA: "리서치", userB: "야근"} {
		runtime := testAIRuntime(userID, &ai.StubProvider{NameVal: "stub"}, "shared-model")
		rows, err := st.AIDealbreakerValidationsByPostingID(ctx, userID, runtime.DealbreakerVersion)
		if err != nil || len(rows[postingID]) != 1 {
			t.Fatalf("user %d validation rows=%v err=%v", userID, rows[postingID], err)
		}
		score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
		if err != nil || !ok || !strings.Contains(score.BreakdownJSON, "제외 키워드: "+phrase) {
			t.Fatalf("user %d score=%q ok=%v err=%v", userID, score.BreakdownJSON, ok, err)
		}
	}
}

func TestProductionScoreAllIgnoresStaleDealbreakerContentHash(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "dealbreaker-stale@example.invalid")
	prof := profile.Profile{Dealbreakers: []string{"리서치"}}
	saveAIRuntimeProfile(t, st, userID, prof)
	p := listingPosting("dealbreaker-stale", "신입 리서치 개발자")
	p.Description = "리서치 아님"
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	postingID := mustUpsert(t, st, p)
	p.ID = postingID
	runtime := testAIRuntime(userID, &ai.StubProvider{NameVal: "stub"}, "shared-model")
	candidate := scoring.DealbreakerCandidates(p, prof)[0]
	validation := ai.DealbreakerValidation{CandidateID: candidate.ID, Verdict: ai.DealbreakerNotApplicable, Evidence: "리서치 아님"}
	if err := st.UpsertAIDealbreakerValidation(ctx, userID, postingID, "stale-content-hash", runtime.DealbreakerVersion, candidate.ID, validation, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatal(err)
	}
	score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
	if err != nil || !ok || score.Total != -1 || !strings.Contains(score.BreakdownJSON, `"confidence":"unverified"`) {
		t.Fatalf("stale validation affected score=%+v ok=%v err=%v", score, ok, err)
	}
}

func TestProductionDealbreakerCacheMissesOnRuntimeVersionChange(t *testing.T) {
	cases := []struct {
		name       string
		oldRuntime func(int64) *AIRuntime
		newRuntime func(int64, ai.Provider) *AIRuntime
	}{
		{
			name: "provider",
			oldRuntime: func(userID int64) *AIRuntime {
				return testAIRuntime(userID, &ai.StubProvider{NameVal: "provider-a"}, "shared-model")
			},
			newRuntime: func(userID int64, provider ai.Provider) *AIRuntime {
				return testAIRuntime(userID, provider, "shared-model")
			},
		},
		{
			name: "model",
			oldRuntime: func(userID int64) *AIRuntime {
				return testAIRuntime(userID, &ai.StubProvider{NameVal: "stub"}, "model-a")
			},
			newRuntime: func(userID int64, provider ai.Provider) *AIRuntime {
				return testAIRuntime(userID, provider, "model-b")
			},
		},
		{
			name: "prompt-version",
			oldRuntime: func(userID int64) *AIRuntime {
				return testAIRuntime(userID, &ai.StubProvider{NameVal: "stub"}, "shared-model")
			},
			newRuntime: func(userID int64, provider ai.Provider) *AIRuntime {
				runtime := testAIRuntime(userID, provider, "shared-model")
				runtime.DealbreakerVersion += "-next-prompt"
				return runtime
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newPostgresTestServer(t, &fakeScraper{})
			ctx := context.Background()
			userID := insertAIRuntimeTestUser(t, st, "dealbreaker-version-"+tc.name+"@example.invalid")
			prof := profile.Profile{Dealbreakers: []string{"리서치"}}
			saveAIRuntimeProfile(t, st, userID, prof)
			p := listingPosting("dealbreaker-version-"+tc.name, "신입 리서치 개발자")
			p.Description = "리서치 업무를 수행합니다"
			p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
			p.ID = mustUpsert(t, st, p)
			_, contentHash, _ := ai.ModelInput(p)
			candidate := scoring.DealbreakerCandidates(p, prof)[0]
			oldRuntime := tc.oldRuntime(userID)
			validation := ai.DealbreakerValidation{CandidateID: candidate.ID, Verdict: ai.DealbreakerApplies, Evidence: "리서치 업무"}
			if err := st.UpsertAIDealbreakerValidation(ctx, userID, p.ID, contentHash, oldRuntime.DealbreakerVersion, candidate.ID, validation, time.Now()); err != nil {
				t.Fatal(err)
			}

			providerName := "stub"
			if tc.name == "provider" {
				providerName = "provider-b"
			}
			provider := &ai.StubProvider{
				NameVal: providerName,
				ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
					return []ai.DealbreakerValidation{{CandidateID: candidates[0].ID, Verdict: ai.DealbreakerApplies, Evidence: "리서치 업무"}}, ai.Usage{}, nil
				},
			}
			newRuntime := tc.newRuntime(userID, provider)
			calls, err := srv.validateDealbreakers(ctx, userID, []scraper.Posting{p}, prof, newRuntime, srv.newAIBudget(ctx, userID, newRuntime), &callCap{max: 1}, noopEmit)
			if err != nil || calls != 1 || provider.ValidateDealbreakersCalls != 1 {
				t.Fatalf("runtime-version miss calls=%d provider-calls=%d err=%v, want 1/1/nil", calls, provider.ValidateDealbreakersCalls, err)
			}
		})
	}
}

func TestProductionHiddenUseSessionOwnerState(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	ctx := context.Background()

	userA, cookieA := createSessionUser(t, st, "owner-a@example.com", "session-a")
	userB, cookieB := createSessionUser(t, st, "owner-b@example.com", "session-b")
	postingID := mustUpsert(t, st, listingPosting("shared-hidden", "공유 숨김 공고"))
	seedProductionScoredPosting(t, st, postingID, userA, userB)
	mutedAt := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	if err := st.AddNotInterested(ctx, userB, postingID, mutedAt); err != nil {
		t.Fatalf("seed userB hidden row: %v", err)
	}

	assertPageMissing(t, srv, cookieA, "/hidden", "공유 숨김 공고")
	assertPageContains(t, srv, cookieB, "/hidden", "공유 숨김 공고")

	putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/not-interested/%d", postingID), nil)
	putReq.AddCookie(cookieA)
	addCSRFToRequest(putReq, srv, cookieA)
	putRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("not-interested add status = %d, want 200", putRec.Code)
	}
	assertHiddenForUser(t, st, userA, postingID, true)
	assertHiddenForUser(t, st, userB, postingID, true)
	assertPageContains(t, srv, cookieA, "/hidden", "공유 숨김 공고")

	deleteReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/not-interested/%d", postingID), nil)
	deleteReq.AddCookie(cookieA)
	addCSRFToRequest(deleteReq, srv, cookieA)
	deleteRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("not-interested delete status = %d, want 200", deleteRec.Code)
	}
	assertHiddenForUser(t, st, userA, postingID, false)
	assertHiddenForUser(t, st, userB, postingID, true)
	assertPageMissing(t, srv, cookieA, "/hidden", "공유 숨김 공고")
	assertPageContains(t, srv, cookieB, "/hidden", "공유 숨김 공고")

	userBOnlyPostingID := mustUpsert(t, st, listingPosting("user-b-hidden", "유저B 전용 숨김"))
	seedProductionScoredPosting(t, st, userBOnlyPostingID, userA, userB)
	putBReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/not-interested/%d", userBOnlyPostingID), nil)
	putBReq.AddCookie(cookieB)
	addCSRFToRequest(putBReq, srv, cookieB)
	putBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(putBRec, putBReq)
	if putBRec.Code != http.StatusOK {
		t.Fatalf("userB not-interested add status = %d, want 200", putBRec.Code)
	}
	assertHiddenForUser(t, st, userA, userBOnlyPostingID, false)
	assertHiddenForUser(t, st, userB, userBOnlyPostingID, true)
	assertPageMissing(t, srv, cookieA, "/hidden", "유저B 전용 숨김")
	assertPageContains(t, srv, cookieB, "/hidden", "유저B 전용 숨김")
}

func TestProductionProfileUsesSessionOwnerState(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	ctx := context.Background()

	userA, cookieA := createSessionUser(t, st, "owner-a@example.com", "session-a")
	userB, cookieB := createSessionUser(t, st, "owner-b@example.com", "session-b")
	if _, _, err := st.SaveProfileForUser(ctx, userA, `{"career_years":0,"job_likes":"유저A 기존 목표"}`); err != nil {
		t.Fatalf("seed userA profile: %v", err)
	}
	if _, _, err := st.SaveProfileForUser(ctx, userB, `{"career_years":0,"job_likes":"유저B 기존 목표"}`); err != nil {
		t.Fatalf("seed userB profile: %v", err)
	}

	assertPageContains(t, srv, cookieA, "/profile", "유저A 기존 목표")
	assertPageMissing(t, srv, cookieA, "/profile", "유저B 기존 목표")
	assertPageContains(t, srv, cookieB, "/profile", "유저B 기존 목표")
	assertPageMissing(t, srv, cookieB, "/profile", "유저A 기존 목표")

	form := url.Values{}
	form.Set("career_years", "0")
	form.Set("job_likes", "유저A 새 목표")
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieA)
	addCSRFToRequest(req, srv, cookieA)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("profile save status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}

	gotA, _, ok, err := st.ProfileForUser(ctx, userA)
	if err != nil || !ok {
		t.Fatalf("ProfileForUser userA: ok=%v err=%v", ok, err)
	}
	gotB, _, ok, err := st.ProfileForUser(ctx, userB)
	if err != nil || !ok {
		t.Fatalf("ProfileForUser userB: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(gotA, "유저A 새 목표") {
		t.Fatalf("userA profile = %s, want updated goal", gotA)
	}
	if !strings.Contains(gotB, "유저B 기존 목표") || strings.Contains(gotB, "유저A 새 목표") {
		t.Fatalf("userB profile = %s, want unchanged userB goal only", gotB)
	}

	assertPageContains(t, srv, cookieA, "/profile", "유저A 새 목표")
	assertPageMissing(t, srv, cookieA, "/profile", "유저B 기존 목표")
	assertPageContains(t, srv, cookieB, "/profile", "유저B 기존 목표")
	assertPageMissing(t, srv, cookieB, "/profile", "유저A 새 목표")

	form = url.Values{}
	form.Set("career_years", "0")
	form.Set("job_likes", "유저B 새 목표")
	form.Set("ai_daily_token_cap", "222222")
	req = httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieB)
	addCSRFToRequest(req, srv, cookieB)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("userB profile save status = %d, want 303; body=%q", rec.Code, rec.Body.String())
	}

	gotA, _, ok, err = st.ProfileForUser(ctx, userA)
	if err != nil || !ok {
		t.Fatalf("ProfileForUser userA after userB save: ok=%v err=%v", ok, err)
	}
	gotB, _, ok, err = st.ProfileForUser(ctx, userB)
	if err != nil || !ok {
		t.Fatalf("ProfileForUser userB after userB save: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(gotA, "유저A 새 목표") || strings.Contains(gotA, "유저B 새 목표") {
		t.Fatalf("userA profile = %s, want unchanged userA goal only", gotA)
	}
	if !strings.Contains(gotB, "유저B 새 목표") {
		t.Fatalf("userB profile = %s, want updated userB goal", gotB)
	}
	if p, err := profile.Unmarshal(gotB); err != nil || p.EffectiveAIDailyTokenCap() != 222222 {
		t.Fatalf("userB effective AI daily cap not persisted: profile=%s err=%v", gotB, err)
	}

	assertPageContains(t, srv, cookieA, "/profile", "유저A 새 목표")
	assertPageMissing(t, srv, cookieA, "/profile", "유저B 새 목표")
	assertPageContains(t, srv, cookieB, "/profile", "유저B 새 목표")
	assertPageMissing(t, srv, cookieB, "/profile", "유저A 새 목표")
}

func newPostgresTestServer(t *testing.T, f *fakeScraper) (*Server, *storage.Store) {
	t.Helper()
	databaseURL := os.Getenv("JOBCRON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("JOBCRON_TEST_POSTGRES_URL not set")
	}
	schema := postgresTestSchemaName(t)
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create postgres schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})

	st, err := storage.OpenPostgres(databaseURLWithSearchPath(databaseURL, schema))
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, f), st
}

func createSessionUser(t *testing.T, st *storage.Store, email, rawToken string) (int64, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	var userID int64
	if err := st.SQLDB().QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES ($1, $2, now(), now())
RETURNING id`, email, passwordHash).Scan(&userID); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	if err := st.CreateSession(ctx, userID, auth.HashSessionToken(rawToken), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession %s: %v", email, err)
	}
	return userID, &http.Cookie{Name: sessionCookieName, Value: rawToken}
}

func seedProductionScoredPosting(t *testing.T, st *storage.Store, postingID int64, userIDs ...int64) {
	t.Helper()
	ctx := context.Background()
	for _, userID := range userIDs {
		_, hash, found, err := st.ProfileForUser(ctx, userID)
		if err != nil {
			t.Fatalf("ProfileForUser(%d): %v", userID, err)
		}
		if !found {
			saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0})
			_, hash, found, err = st.ProfileForUser(ctx, userID)
			if err != nil || !found {
				t.Fatalf("seed ProfileForUser(%d): found=%v err=%v", userID, found, err)
			}
		}
		if err := st.UpsertScoreForUser(ctx, userID, storage.Score{
			PostingID: postingID, ProfileHash: hash, Total: 50,
			BreakdownJSON: `{"Total":50}`, ComputedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed score user=%d posting=%d: %v", userID, postingID, err)
		}
	}
}

func addCSRFToRequest(req *http.Request, srv *Server, sessionCookie *http.Cookie) {
	const csrfCookieValue = "test-csrf-cookie"
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfCookieValue})
	req.Header.Set(csrfHeaderName, srv.csrfToken(csrfCookieValue, sessionCookie.Value))
}

func assertBookmarkedForUser(t *testing.T, st *storage.Store, userID, postingID int64, want bool) {
	t.Helper()
	got, err := st.IsBookmarkedForUser(context.Background(), userID, postingID)
	if err != nil {
		t.Fatalf("IsBookmarkedForUser(%d,%d): %v", userID, postingID, err)
	}
	if got != want {
		t.Fatalf("IsBookmarkedForUser(%d,%d) = %v, want %v", userID, postingID, got, want)
	}
}

func assertHiddenForUser(t *testing.T, st *storage.Store, userID, postingID int64, want bool) {
	t.Helper()
	got, err := st.IsNotInterestedForUser(context.Background(), userID, postingID)
	if err != nil {
		t.Fatalf("IsNotInterestedForUser(%d,%d): %v", userID, postingID, err)
	}
	if got != want {
		t.Fatalf("IsNotInterestedForUser(%d,%d) = %v, want %v", userID, postingID, got, want)
	}
}

func assertPageContains(t *testing.T, srv *Server, cookie *http.Cookie, path, want string) {
	t.Helper()
	body := authedPageBody(t, srv, cookie, path)
	if !strings.Contains(body, want) {
		t.Fatalf("%s body missing %q\n%s", path, want, body)
	}
}

func assertPageMissing(t *testing.T, srv *Server, cookie *http.Cookie, path, unwanted string) {
	t.Helper()
	body := authedPageBody(t, srv, cookie, path)
	if strings.Contains(body, unwanted) {
		t.Fatalf("%s body unexpectedly contains %q\n%s", path, unwanted, body)
	}
}

func authedPageBody(t *testing.T, srv *Server, cookie *http.Cookie, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%q", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

var nonSchemaChars = regexp.MustCompile(`[^a-z0-9_]`)

func postgresTestSchemaName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "_")
	name = nonSchemaChars.ReplaceAllString(name, "_")
	return fmt.Sprintf("test_server_%s_%d_%d", name, time.Now().UnixNano(), rand.Uint64())
}

func databaseURLWithSearchPath(databaseURL, schema string) string {
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	return databaseURL + separator + "search_path=" + schema
}
