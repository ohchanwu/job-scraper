package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	stage1SponsorFixture = "fixture-sponsor"
	stage1TriggerFixture = "fixture-trigger"
)

func TestStage1SponsorMissBillsOnlySponsor(t *testing.T) {
	for _, trigger := range []string{
		storage.ScrapeTriggerManual,
		storage.ScrapeTriggerScheduled,
	} {
		t.Run(trigger, func(t *testing.T) {
			srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(
				t,
				&fakeScraper{
					listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
					details: map[string]scraper.Posting{
						"1": listingPosting("1", "백엔드 신입"),
					},
				},
			)

			if _, err := srv.runScrapeForTrigger(
				context.Background(),
				trigger,
				noopEmit,
				triggerID,
				nil,
			); err != nil {
				t.Fatalf("runScrapeForTrigger: %v", err)
			}

			if sponsor.Calls != 1 {
				t.Fatalf("sponsor Extract calls = %d, want 1", sponsor.Calls)
			}
			if triggerProvider.Calls != 0 {
				t.Fatalf("trigger-user Extract calls = %d, want 0", triggerProvider.Calls)
			}
			assertStage1Usage(t, st, sponsorID, 120)
			assertStage1Usage(t, st, triggerID, 0)
			if n := aiExtractionCount(t, srv); n != 1 {
				t.Fatalf("ai_extractions rows = %d, want 1", n)
			}
		})
	}
}

func TestStage1SponsorProviderRejectionDoesNotSwitchPayers(t *testing.T) {
	srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(
		t,
		&fakeScraper{
			listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
			details: map[string]scraper.Posting{
				"1": listingPosting("1", "백엔드 신입"),
			},
		},
	)
	sponsor.ExtractFn = func(context.Context, string) (ai.Extraction, ai.Usage, error) {
		return ai.Extraction{}, ai.Usage{InputTokens: 100, OutputTokens: 20},
			errors.New("synthetic provider rejection")
	}

	if _, err := srv.runScrape(context.Background(), noopEmit, triggerID, nil); err != nil {
		t.Fatalf("runScrape: %v", err)
	}

	if sponsor.Calls != 1 {
		t.Fatalf("sponsor Extract calls = %d, want 1", sponsor.Calls)
	}
	if triggerProvider.Calls != 0 {
		t.Fatalf("trigger-user Extract calls = %d, want 0", triggerProvider.Calls)
	}
	assertStage1Usage(t, st, sponsorID, 120)
	assertStage1Usage(t, st, triggerID, 0)
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Fatalf("ai_extractions rows = %d, want 0 after provider rejection", n)
	}
}

func TestStage1SponsorScheduledUSDCapEmitsFallbackStatus(t *testing.T) {
	srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(
		t,
		&fakeScraper{
			listing: []scraper.Posting{
				listingPosting("1", "백엔드 신입"),
				listingPosting("2", "서버 신입"),
			},
			details: map[string]scraper.Posting{
				"1": listingPosting("1", "백엔드 신입"),
				"2": listingPosting("2", "서버 신입"),
			},
		},
	)
	saveAIRuntimeProfile(t, st, sponsorID, profile.Profile{
		CareerYears:        0,
		AIProvider:         "anthropic",
		AIModel:            "sponsor-model",
		AIRunUSDCapCents:   1,
		AIDailyUSDCapCents: profile.DefaultAIDailyUSDCents,
	})
	spend := aiRunTokenCapForUSDCents(1) + 1
	sponsor.ExtractFn = func(context.Context, string) (ai.Extraction, ai.Usage, error) {
		return ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone},
			ai.Usage{InputTokens: spend}, nil
	}
	var statuses []string
	emit := func(event, data string) {
		if event == "status" {
			statuses = append(statuses, data)
		}
	}

	if _, err := srv.runScrapeForTrigger(
		context.Background(),
		storage.ScrapeTriggerScheduled,
		emit,
		triggerID,
		nil,
	); err != nil {
		t.Fatalf("scheduled runScrapeForTrigger: %v", err)
	}
	if sponsor.Calls != 1 {
		t.Fatalf("sponsor Extract calls = %d, want 1", sponsor.Calls)
	}
	if triggerProvider.Calls != 0 {
		t.Fatalf("trigger-user Extract calls = %d, want 0", triggerProvider.Calls)
	}
	assertStage1Usage(t, st, sponsorID, spend)
	assertStage1Usage(t, st, triggerID, 0)
	body := strings.Join(statuses, "\n")
	if !strings.Contains(body, "일반 점수로 분석했어요") {
		t.Fatalf("scheduled status missing sponsor budget fallback: %q", body)
	}
	if strings.Contains(body, "프로필 설정") {
		t.Fatalf("sponsor-only fallback incorrectly points to analyzed-user settings: %q", body)
	}
}

func TestStage1SponsorUnavailableFallsBackWithoutCrossUserCharge(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *Server, *storage.Store)
	}{
		{
			name: "missing-or-zero",
			configure: func(_ *testing.T, srv *Server, _ *storage.Store) {
				srv.stage1SponsorUserID = 0
			},
		},
		{
			name: "unknown-or-deleted",
			configure: func(_ *testing.T, srv *Server, _ *storage.Store) {
				srv.stage1SponsorUserID = 999_999
			},
		},
		{
			name: "AI not configured",
			configure: func(t *testing.T, srv *Server, st *storage.Store) {
				userID := insertAIRuntimeTestUser(t, st, "unconfigured-sponsor@example.invalid")
				saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0})
				srv.stage1SponsorUserID = userID
			},
		},
		{
			name: "undecryptable credential",
			configure: func(t *testing.T, srv *Server, st *storage.Store) {
				userID := insertAIRuntimeTestUser(t, st, "undecryptable-sponsor@example.invalid")
				saveAIRuntimeProfile(t, st, userID, profile.Profile{
					CareerYears: 0,
					AIProvider:  "anthropic",
				})
				saveAIRuntimeCredential(
					t,
					st,
					newAIRuntimeTestCipher(t, 0x31),
					userID,
					"anthropic",
					stage1SponsorFixture,
				)
				srv.SetCredentialCipher(newAIRuntimeTestCipher(t, 0x32))
				srv.stage1SponsorUserID = userID
			},
		},
		{
			name: "provider construction rejected",
			configure: func(t *testing.T, srv *Server, st *storage.Store) {
				userID := insertAIRuntimeTestUser(t, st, "rejected-sponsor@example.invalid")
				cipher := newAIRuntimeTestCipher(t, 0x33)
				saveAIRuntimeProfile(t, st, userID, profile.Profile{
					CareerYears: 0,
					AIProvider:  "anthropic",
				})
				saveAIRuntimeCredential(
					t,
					st,
					cipher,
					userID,
					"anthropic",
					stage1SponsorFixture,
				)
				srv.SetCredentialCipher(cipher)
				srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) {
					return nil, errors.New("synthetic provider rejected")
				}
				srv.stage1SponsorUserID = userID
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newPostgresTestServer(t, &fakeScraper{})
			triggerID := insertAIRuntimeTestUser(
				t,
				st,
				strings.ToLower(strings.ReplaceAll(tc.name, " ", "-"))+"-trigger@example.invalid",
			)
			tc.configure(t, srv, st)

			funding, err := srv.resolveStage1Funding(context.Background())
			if err == nil || funding != nil {
				t.Fatalf("resolveStage1Funding = funding:%v err:%v, want unavailable", funding, err)
			}
			if got := err.Error(); !strings.Contains(got, "stage1 sponsor unavailable") ||
				strings.Contains(got, stage1SponsorFixture) ||
				strings.Contains(got, "@example.invalid") {
				t.Fatalf("unsafe or unstable sponsor error = %q", got)
			}

			p := listingPosting("fallback", "신입 백엔드")
			id := mustUpsert(t, st, p)
			srv.extractStage1(context.Background(), id, p, time.Now().UTC(), funding)
			if n := aiExtractionCount(t, srv); n != 0 {
				t.Fatalf("ai_extractions rows = %d, want deterministic fallback", n)
			}
			assertStage1Usage(t, st, triggerID, 0)
		})
	}
}

func TestStage1SponsorCacheHitNeedsNoAvailableSponsor(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	saveSinipProfile(t, srv)
	p := listingPosting("cache-hit", "백엔드 엔지니어")
	p.Description = "경력 5년 이상"
	p.MinCareer = 5
	id := mustUpsert(t, st, p)
	stored, err := st.AllPostings(ctx)
	if err != nil || len(stored) != 1 {
		t.Fatalf("AllPostings: got %d err=%v", len(stored), err)
	}
	p = stored[0]
	zero := 0
	_, contentHash, _ := ai.ModelInput(p)
	if err := st.UpsertAIExtraction(
		ctx,
		id,
		contentHash,
		ai.ExtractionContractVersion(),
		ai.Extraction{
			MinCareer:      0,
			MaxCareer:      &zero,
			Newcomer:       true,
			EducationEnum:  ai.EduNone,
			CareerEvidence: "신입 지원 가능",
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("UpsertAIExtraction: %v", err)
	}
	srv.stage1SponsorUserID = 999_999
	funding, err := srv.resolveStage1Funding(ctx)
	if err == nil || funding != nil {
		t.Fatalf("resolveStage1Funding = funding:%v err:%v, want unavailable", funding, err)
	}

	srv.extractStage1(ctx, id, p, time.Now().UTC(), funding)
	if _, err := srv.scoreAll(ctx, 1, nil); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}
	score, ok, err := st.ScoreByPostingID(ctx, id)
	if err != nil || !ok || score.Total != profile.DefaultCareerWeight {
		t.Fatalf("cache-hit score = %+v ok=%v err=%v", score, ok, err)
	}
	assertStage1Usage(t, st, 1, 0)
}

func TestStage1SponsorBudgetAndCallHeadroomNeverSwitchPayers(t *testing.T) {
	t.Run("token budget exhausted", func(t *testing.T) {
		srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(
			t,
			&fakeScraper{
				listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
				details: map[string]scraper.Posting{
					"1": listingPosting("1", "백엔드 신입"),
				},
			},
		)
		saveAIRuntimeProfile(t, st, sponsorID, profile.Profile{
			CareerYears:     0,
			AIProvider:      "anthropic",
			AIModel:         "sponsor-model",
			AIDailyTokenCap: 1,
		})
		day := time.Now().UTC().Format("2006-01-02")
		if err := st.AddAIUsage(context.Background(), sponsorID, day, 1, 0); err != nil {
			t.Fatalf("seed sponsor usage: %v", err)
		}

		if _, err := srv.runScrape(context.Background(), noopEmit, triggerID, nil); err != nil {
			t.Fatalf("runScrape: %v", err)
		}
		if sponsor.Calls != 0 || triggerProvider.Calls != 0 {
			t.Fatalf("provider calls sponsor=%d trigger=%d, want zero", sponsor.Calls, triggerProvider.Calls)
		}
		assertStage1Usage(t, st, sponsorID, 1)
		assertStage1Usage(t, st, triggerID, 0)
	})

	t.Run("call headroom exhausted", func(t *testing.T) {
		f := &fakeScraper{
			listing: []scraper.Posting{
				listingPosting("1", "백엔드 신입"),
				listingPosting("2", "서버 신입"),
			},
			details: map[string]scraper.Posting{
				"1": listingPosting("1", "백엔드 신입"),
				"2": listingPosting("2", "서버 신입"),
			},
		}
		srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(t, f)
		saveAIRuntimeProfile(t, st, sponsorID, profile.Profile{
			CareerYears:  0,
			AIProvider:   "anthropic",
			AIModel:      "sponsor-model",
			AIPerCallCap: 1,
		})

		if _, err := srv.runScrape(context.Background(), noopEmit, triggerID, nil); err != nil {
			t.Fatalf("runScrape: %v", err)
		}
		if sponsor.Calls != 1 || triggerProvider.Calls != 0 {
			t.Fatalf("provider calls sponsor=%d trigger=%d, want 1/0", sponsor.Calls, triggerProvider.Calls)
		}
		if n := aiExtractionCount(t, srv); n != 1 {
			t.Fatalf("ai_extractions rows = %d, want 1", n)
		}
		assertStage1Usage(t, st, sponsorID, 120)
		assertStage1Usage(t, st, triggerID, 0)
	})
}

func TestStage1SponsorRetryBackfillKeepsPayersSeparate(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("retry", "백엔드 신입")},
		details: map[string]scraper.Posting{
			"retry": listingPosting("retry", "백엔드 신입"),
		},
	}
	srv, st, sponsorID, triggerID, sponsor, triggerProvider := newStage1SponsorTestServer(t, f)
	sponsor.ExtractFn = func(context.Context, string) (ai.Extraction, ai.Usage, error) {
		return ai.Extraction{}, ai.Usage{}, errors.New("synthetic first-attempt rejection")
	}
	if _, err := srv.runScrape(context.Background(), noopEmit, triggerID, nil); err != nil {
		t.Fatalf("initial runScrape: %v", err)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Fatalf("initial ai_extractions rows = %d, want 0", n)
	}

	success := newcomerStub()
	sponsor.ExtractFn = success.ExtractFn
	triggerProvider.ScoreDeltaFn = func(context.Context, string, string) (
		[]ai.RawDeltaItem,
		ai.Usage,
		error,
	) {
		return nil, ai.Usage{}, nil
	}
	runtime, err := srv.aiRuntimeForUser(context.Background(), triggerID)
	if err != nil || runtime == nil {
		t.Fatalf("trigger aiRuntimeForUser = runtime:%v err:%v", runtime, err)
	}
	if _, err := srv.runRerate(context.Background(), "today", noopEmit, triggerID, runtime); err != nil {
		t.Fatalf("runRerate retry: %v", err)
	}

	if sponsor.Calls != 2 {
		t.Fatalf("sponsor Extract calls = %d, want rejection plus retry", sponsor.Calls)
	}
	if triggerProvider.Calls != 0 {
		t.Fatalf("trigger-user Extract calls = %d, want 0", triggerProvider.Calls)
	}
	if n := aiExtractionCount(t, srv); n != 1 {
		t.Fatalf("retry ai_extractions rows = %d, want 1", n)
	}
	assertStage1Usage(t, st, sponsorID, 120)
	assertStage1Usage(t, st, triggerID, 0)
}

func newStage1SponsorTestServer(
	t *testing.T,
	f *fakeScraper,
) (*Server, *storage.Store, int64, int64, *ai.StubProvider, *ai.StubProvider) {
	t.Helper()
	srv, st := newPostgresTestServer(t, f)
	sponsorID := insertAIRuntimeTestUser(t, st, "stage1-sponsor@example.invalid")
	triggerID := insertAIRuntimeTestUser(t, st, "stage1-trigger@example.invalid")
	cipher := newAIRuntimeTestCipher(t, 0x57)
	srv.SetCredentialCipher(cipher)
	saveAIRuntimeProfile(t, st, sponsorID, profile.Profile{
		CareerYears: 0,
		AIProvider:  "anthropic",
		AIModel:     "sponsor-model",
	})
	saveAIRuntimeProfile(t, st, triggerID, profile.Profile{
		CareerYears: 0,
		AIProvider:  "anthropic",
		AIModel:     "trigger-model",
	})
	saveAIRuntimeCredential(
		t,
		st,
		cipher,
		sponsorID,
		"anthropic",
		stage1SponsorFixture,
	)
	saveAIRuntimeCredential(
		t,
		st,
		cipher,
		triggerID,
		"anthropic",
		stage1TriggerFixture,
	)

	sponsor := newcomerStub()
	sponsor.NameVal = "anthropic"
	triggerProvider := newcomerStub()
	triggerProvider.NameVal = "anthropic"
	srv.newAIProvider = func(_ string, key string, _ string, _ time.Duration) (ai.Provider, error) {
		switch key {
		case stage1SponsorFixture:
			return sponsor, nil
		case stage1TriggerFixture:
			return triggerProvider, nil
		default:
			return nil, errors.New("unexpected synthetic credential")
		}
	}
	srv.stage1SponsorUserID = sponsorID
	return srv, st, sponsorID, triggerID, sponsor, triggerProvider
}

func assertStage1Usage(t *testing.T, st *storage.Store, userID int64, want int) {
	t.Helper()
	day := time.Now().UTC().Format("2006-01-02")
	in, out, err := st.AIUsageForDay(context.Background(), userID, day)
	if err != nil {
		t.Fatalf("AIUsageForDay(%d): %v", userID, err)
	}
	if got := in + out; got != want {
		t.Fatalf("AI usage user %d = %d, want %d", userID, got, want)
	}
}
