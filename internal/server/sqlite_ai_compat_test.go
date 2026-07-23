package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestLegacySQLiteRenderAndRescoreNeverReadOrModifyAIKeysFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "ai_keys.json")
	sentinel := []byte(`{"anthropic":"legacy-key-must-stay-untouched"}`)
	if err := os.WriteFile(keyPath, sentinel, 0o600); err != nil {
		t.Fatalf("write legacy key sentinel: %v", err)
	}
	st, err := storage.OpenSQLiteAt(filepath.Join(dir, "jobs.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	defer st.Close()
	srv := New(st, &fakeScraper{})
	providerCalls := 0
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) {
		providerCalls++
		return rerateStub(), nil
	}
	zero := 0
	profJSON, _ := profile.Marshal(profile.Profile{
		CareerYears: 0, MinScore: &zero, AIProvider: "anthropic", AIModel: "legacy-model",
	})
	if _, _, err := st.SaveProfile(context.Background(), profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	p := listingPosting("legacy-key-sentinel", "SQLite 규칙 점수 공고")
	p.FirstSeenAt, p.LastSeenAt = time.Now().UTC(), time.Now().UTC()
	if _, _, err := st.UpsertPosting(context.Background(), p); err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}

	if _, err := srv.RescoreUsers(context.Background()); err != nil {
		t.Fatalf("RescoreUsers: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("briefing status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if providerCalls != 0 {
		t.Fatalf("provider construction calls = %d, want 0", providerCalls)
	}
	got, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read legacy key sentinel: %v", err)
	}
	if string(got) != string(sentinel) {
		t.Fatal("legacy ai_keys.json changed during SQLite render/rescore")
	}
}
