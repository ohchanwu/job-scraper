package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
)

// TestInternGuardEndToEnd (R3): scoreAll reads a bad cached extraction
// (newcomer=false, min_career=2) and, for an 인턴 role, restores the 신입 award
// via the intern guard — while a non-intern role with the SAME bad extraction
// keeps D2's correction (no award). Covers the full DB → scoreAll → render path
// the scoring unit test (TestScoreCareerInternGuard) can't see: it proves the
// extraction is actually read from the cache (not a hash-mismatch false pass)
// because the non-intern control's award IS dropped by that same extraction.
func TestInternGuardEndToEnd(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	pj, _ := profile.Marshal(profile.Profile{CareerYears: 0, MinScore: &zero})
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	runtime := testAIRuntime(1, newcomerStub(), "test-model")
	now := time.Now().UTC()
	bad := ai.Extraction{MinCareer: 2, MaxCareer: nil, Newcomer: false, EducationEnum: ai.EduNone}

	seed := func(srcID, title string) int64 {
		p := listingPosting(srcID, title)
		p.Newcomer = true // source correctly marks it new-grad-eligible
		p.Description = "풀스택 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		id, _, err := st.UpsertPosting(ctx, p)
		if err != nil {
			t.Fatalf("UpsertPosting(%s): %v", srcID, err)
		}
		_, contentHash, _ := ai.ModelInput(p)
		if err := st.UpsertAIExtraction(ctx, id, contentHash, ai.ExtractionContractVersion(), bad, now); err != nil {
			t.Fatalf("seed extraction(%s): %v", srcID, err)
		}
		return id
	}
	internID := seed("intern1", "[인턴] 풀스택 개발자")
	seniorID := seed("senior1", "백엔드 개발자")

	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}
	scores, err := st.ScoresByPostingID(ctx)
	if err != nil {
		t.Fatalf("ScoresByPostingID: %v", err)
	}
	if want := profile.DefaultCareerWeight; scores[internID].Total != want {
		t.Errorf("intern Total = %d, want %d (신입 guard restores the award)", scores[internID].Total, want)
	}
	if scores[seniorID].Total != 0 {
		t.Errorf("non-intern Total = %d, want 0 (D2 correction stands; the bad extraction drops the award)", scores[seniorID].Total)
	}

	// Render: the briefing shows the intern posting with its 신입 chip.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "[인턴] 풀스택 개발자") || !strings.Contains(body, "신입") {
		t.Error("briefing did not render the intern posting with its 신입 chip")
	}
}
