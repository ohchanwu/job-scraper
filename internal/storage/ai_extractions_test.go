package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
)

func ptr(n int) *int { return &n }

func TestMigration0008AppliesTo8(t *testing.T) {
	st := newTestStore(t)
	var v int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	// Latest migration is 0012 (scrape run history); 0006 is intentionally
	// skipped. Bump this when a new migration lands.
	if v != 12 {
		t.Fatalf("user_version = %d, want 12 after 0012 applies (0006 skipped)", v)
	}
}

func TestUpsertAIExtractionRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, _, err := st.UpsertPosting(ctx, samplePosting())
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	const ver = "abc123def456"
	now := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)

	t.Run("open upper bound (nil max)", func(t *testing.T) {
		ext := ai.Extraction{MinCareer: 3, MaxCareer: nil, Newcomer: false, EducationEnum: ai.EduBachelor, CareerEvidence: "경력 3년 이상"}
		if err := st.UpsertAIExtraction(ctx, id, "hashA", ver, ext, now); err != nil {
			t.Fatalf("UpsertAIExtraction: %v", err)
		}
		got, ok, err := st.AIExtraction(ctx, id, "hashA", ver)
		if err != nil || !ok {
			t.Fatalf("AIExtraction: ok=%v err=%v", ok, err)
		}
		if got.MinCareer != 3 || got.MaxCareer != nil || got.Newcomer || got.EducationEnum != ai.EduBachelor || got.CareerEvidence != "경력 3년 이상" || got.EducationEvidence != "" {
			t.Fatalf("round trip = %+v", got)
		}
	})

	t.Run("legacy SQLite rejects education evidence", func(t *testing.T) {
		ext := ai.Extraction{EducationEnum: ai.EduBachelor, CareerEvidence: "경력", EducationEvidence: "학사 이상"}
		if err := st.UpsertAIExtraction(ctx, id, "education", ver, ext, now); err == nil {
			t.Fatal("SQLite write discarded education evidence")
		}
	})

	t.Run("bounded max round-trips", func(t *testing.T) {
		ext := ai.Extraction{MinCareer: 0, MaxCareer: ptr(0), Newcomer: true, EducationEnum: ai.EduNone, CareerEvidence: "신입"}
		if err := st.UpsertAIExtraction(ctx, id, "hashB", ver, ext, now); err != nil {
			t.Fatalf("UpsertAIExtraction: %v", err)
		}
		got, ok, err := st.AIExtraction(ctx, id, "hashB", ver)
		if err != nil || !ok {
			t.Fatalf("AIExtraction: ok=%v err=%v", ok, err)
		}
		if got.MaxCareer == nil || *got.MaxCareer != 0 || !got.Newcomer {
			t.Fatalf("bounded max round trip = %+v", got)
		}
	})

	t.Run("PK conflict updates in place", func(t *testing.T) {
		ext := ai.Extraction{MinCareer: 9, MaxCareer: ptr(9), Newcomer: false, EducationEnum: ai.EduMaster, CareerEvidence: "v2"}
		if err := st.UpsertAIExtraction(ctx, id, "hashA", ver, ext, now.Add(time.Hour)); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
		got, _, _ := st.AIExtraction(ctx, id, "hashA", ver)
		if got.MinCareer != 9 || got.EducationEnum != ai.EduMaster {
			t.Fatalf("conflict update = %+v, want updated values", got)
		}
		var n int
		st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_extractions WHERE posting_id=? AND content_hash='hashA' AND ai_version=?`, id, ver).Scan(&n)
		if n != 1 {
			t.Fatalf("conflict produced %d rows, want 1 (upsert, not duplicate)", n)
		}
	})

	t.Run("miss returns ok=false", func(t *testing.T) {
		_, ok, err := st.AIExtraction(ctx, id, "no-such-hash", ver)
		if err != nil || ok {
			t.Fatalf("miss: ok=%v err=%v", ok, err)
		}
	})
}

func TestPostgresAIExtractionRoundTrip(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	ctx := context.Background()
	postingID := insertMigrationTestPosting(t, st, "extraction-evidence")
	ext := ai.Extraction{
		MinCareer:         2,
		EducationEnum:     ai.EduBachelor,
		CareerEvidence:    "경력 2년 이상",
		EducationEvidence: "학사 이상",
	}
	if err := st.UpsertAIExtraction(ctx, postingID, "content", "ai-v1", ext, time.Now()); err != nil {
		t.Fatalf("UpsertAIExtraction: %v", err)
	}
	got, ok, err := st.AIExtraction(ctx, postingID, "content", "ai-v1")
	if err != nil || !ok {
		t.Fatalf("AIExtraction: ok=%v err=%v", ok, err)
	}
	if got.CareerEvidence != ext.CareerEvidence || got.EducationEvidence != ext.EducationEvidence {
		t.Fatalf("evidence = career:%q education:%q", got.CareerEvidence, got.EducationEvidence)
	}
	ext.CareerEvidence = "경력 3년 이상"
	ext.EducationEvidence = "석사 이상"
	if err := st.UpsertAIExtraction(ctx, postingID, "content", "ai-v1", ext, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("update extraction: %v", err)
	}
	batched, err := st.AIExtractionsByPostingID(ctx, map[int64]string{postingID: "content"}, "ai-v1")
	if err != nil {
		t.Fatalf("AIExtractionsByPostingID: %v", err)
	}
	if got := batched[postingID]; got.CareerEvidence != ext.CareerEvidence || got.EducationEvidence != ext.EducationEvidence {
		t.Fatalf("batched evidence = career:%q education:%q", got.CareerEvidence, got.EducationEvidence)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM ai_extractions WHERE posting_id = $1`, postingID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("upsert row count = %d, want 1", count)
	}
}

func TestAIExtractionsByPostingIDBatchedByExactContent(t *testing.T) {
	testAIExtractionsByPostingIDBatchedByExactContent(t, newTestStore(t))
}

func TestPostgresAIExtractionsByPostingIDBatchedByExactContent(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	testAIExtractionsByPostingIDBatchedByExactContent(t, st)
}

func TestAIExtractionsByPostingIDSQLiteBoundary(t *testing.T) {
	st := newTestStore(t)
	got, err := st.AIExtractionsByPostingID(
		context.Background(), fakeContentHashes(16_383), "ver000000001",
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("boundary read = %d rows, err=%v; want empty result", len(got), err)
	}
}

func TestPostgresAIExtractionsByPostingIDBoundary(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	got, err := st.AIExtractionsByPostingID(
		context.Background(), fakeContentHashes(32_768), "ver000000001",
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("boundary read = %d rows, err=%v; want empty result", len(got), err)
	}
}

func TestAIExtractionsByPostingIDMultiChunkMerge(t *testing.T) {
	testAIExtractionsByPostingIDMultiChunkMerge(t, newTestStore(t))
}

func TestPostgresAIExtractionsByPostingIDMultiChunkMerge(t *testing.T) {
	st, _ := newPostgresTestStoreWithSchema(t)
	testAIExtractionsByPostingIDMultiChunkMerge(t, st)
}

func testAIExtractionsByPostingIDMultiChunkMerge(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	const ver = "ver000000001"
	if aiExtractionBatchSize != 8_000 {
		t.Fatalf("aiExtractionBatchSize = %d, want 8000", aiExtractionBatchSize)
	}

	first, _, _ := st.UpsertPosting(ctx, samplePosting())
	secondPosting := samplePosting()
	secondPosting.SourcePostingID = "multi-chunk-second"
	second, _, _ := st.UpsertPosting(ctx, secondPosting)
	const lastID int64 = 20_000
	if _, err := st.db.ExecContext(
		ctx, st.query("UPDATE postings SET id = ? WHERE id = ?"), lastID, second,
	); err != nil {
		t.Fatalf("move second posting: %v", err)
	}

	if err := st.UpsertAIExtraction(ctx, first, "first", ver, ai.Extraction{CareerEvidence: "first"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAIExtraction(ctx, lastID, "last", ver, ai.Extraction{CareerEvidence: "last"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	requested := map[int64]string{first: "first", lastID: "last"}
	for id := int64(1_000); len(requested) < aiExtractionBatchSize+1; id++ {
		requested[id] = fmt.Sprintf("fake-%d", id)
	}
	got, err := st.AIExtractionsByPostingID(ctx, requested, ver)
	if err != nil {
		t.Fatalf("multi-chunk read: %v", err)
	}
	if len(got) != 2 || got[first].CareerEvidence != "first" || got[lastID].CareerEvidence != "last" {
		t.Fatalf("multi-chunk result = %+v, want both real rows", got)
	}
}

func fakeContentHashes(n int) map[int64]string {
	hashes := make(map[int64]string, n)
	for i := range n {
		hashes[int64(i+1)] = fmt.Sprintf("fake-%d", i)
	}
	return hashes
}

func testAIExtractionsByPostingIDBatchedByExactContent(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	const ver = "ver000000001"

	p1 := samplePosting()
	id1, _, _ := st.UpsertPosting(ctx, p1)
	p2 := samplePosting()
	p2.SourcePostingID = "999"
	id2, _, _ := st.UpsertPosting(ctx, p2)

	t0 := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	for i := range 10 {
		hash := fmt.Sprintf("old-%d", i)
		if err := st.UpsertAIExtraction(ctx, id1, hash, ver, ai.Extraction{MinCareer: i, EducationEnum: ai.EduBachelor, CareerEvidence: hash}, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertAIExtraction(ctx, id1, "new", ver, ai.Extraction{MinCareer: 0, Newcomer: true, EducationEnum: ai.EduNone, CareerEvidence: "new"}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAIExtraction(ctx, id2, "h", ver, ai.Extraction{MinCareer: 2, EducationEnum: ai.EduAssociate, CareerEvidence: "two"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAIExtraction(ctx, id1, "h", ver, ai.Extraction{MinCareer: 30, EducationEnum: ai.EduDoctorate, CareerEvidence: "cross-wrong"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAIExtraction(ctx, id2, "new", ver, ai.Extraction{MinCareer: 30, EducationEnum: ai.EduDoctorate, CareerEvidence: "cross-wrong"}, t0); err != nil {
		t.Fatal(err)
	}
	// A row under a DIFFERENT version must not leak into this version's read.
	if err := st.UpsertAIExtraction(ctx, id2, "h", "otherversion", ai.Extraction{MinCareer: 40, EducationEnum: ai.EduDoctorate, CareerEvidence: "wrong"}, t0); err != nil {
		t.Fatal(err)
	}

	empty, err := st.AIExtractionsByPostingID(ctx, nil, ver)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty AIExtractionsByPostingID = %+v, err=%v", empty, err)
	}

	m, err := st.AIExtractionsByPostingID(ctx, map[int64]string{id1: "new", id2: "h"}, ver)
	if err != nil {
		t.Fatalf("AIExtractionsByPostingID: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("map has %d entries, want 2", len(m))
	}
	if m[id1].CareerEvidence != "new" || !m[id1].Newcomer {
		t.Fatalf("id1 = %+v, want only current content row", m[id1])
	}
	if m[id2].MinCareer != 2 || m[id2].EducationEnum != ai.EduAssociate {
		t.Fatalf("id2 = %+v, want this-version row, not the otherversion one", m[id2])
	}
	if historical, ok, err := st.AIExtraction(ctx, id1, "old-0", ver); err != nil || !ok || historical.CareerEvidence != "old-0" {
		t.Fatalf("historical row = %+v, ok=%v err=%v; want retained exact lookup", historical, ok, err)
	}
}

func TestAIExtractionCascadeOnPostingDelete(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	const ver = "cascadetest1"

	t.Run("direct delete cascades", func(t *testing.T) {
		id, _, _ := st.UpsertPosting(ctx, samplePosting())
		if err := st.UpsertAIExtraction(ctx, id, "h", ver, ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `DELETE FROM postings WHERE id = ?`, id); err != nil {
			t.Fatalf("delete posting: %v", err)
		}
		var n int
		st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_extractions WHERE posting_id = ?`, id).Scan(&n)
		if n != 0 {
			t.Fatalf("extraction row outlived its posting (%d rows) — ON DELETE CASCADE not engaged", n)
		}
	})

	t.Run("sweep cascades", func(t *testing.T) {
		// A posting last seen long before the per-source baseline gets swept;
		// its extraction must go with it.
		p := samplePosting()
		p.SourcePostingID = "sweepme"
		p.FirstSeenAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		p.LastSeenAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		id, _, _ := st.UpsertPosting(ctx, p)
		// A fresh posting moves the source baseline far ahead of the stale one.
		fresh := samplePosting()
		fresh.SourcePostingID = "freshrow"
		fresh.LastSeenAt = time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
		st.UpsertPosting(ctx, fresh)
		if err := st.UpsertAIExtraction(ctx, id, "h", ver, ai.Extraction{EducationEnum: ai.EduNone}, time.Now()); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
		if _, err := st.SweepStalePostings(ctx, now, 3*24*time.Hour, 90*24*time.Hour, nil); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		var n int
		st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_extractions WHERE posting_id = ?`, id).Scan(&n)
		if n != 0 {
			t.Fatalf("extraction survived the sweep of its posting (%d rows)", n)
		}
	})
}
