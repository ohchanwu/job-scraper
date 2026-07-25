package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
)

// AIDealbreakerValidation pairs the immutable server-derived match with the
// model's independent judgment. Match is provenance the provider can never
// replace; Validation holds only what the model returned.
type AIDealbreakerValidation struct {
	PostingID   int64
	ContentHash string
	AIVersion   string
	KeywordHash string
	Match       ai.DealbreakerMatch
	Validation  ai.DealbreakerValidation
	ComputedAt  time.Time
}

func (s *Store) UpsertAIDealbreakerValidation(
	ctx context.Context,
	userID int64,
	postingID int64,
	contentHash string,
	aiVersion string,
	keywordHash string,
	match ai.DealbreakerMatch,
	validation ai.DealbreakerValidation,
	computedAt time.Time,
) error {
	if err := s.validateAIDealbreakerStore(userID); err != nil {
		return err
	}
	if validation.CandidateID != keywordHash {
		return errors.New("storage: dealbreaker validation candidate does not match keyword hash")
	}
	matchJSON, err := json.Marshal(match)
	if err != nil {
		return fmt.Errorf("storage: marshal dealbreaker match: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO ai_dealbreaker_validations (
    user_id, posting_id, content_hash, ai_version, keyword_hash,
    verdict, match_json, reason_code, reason_evidence, computed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (user_id, posting_id, content_hash, ai_version, keyword_hash) DO UPDATE SET
    verdict = EXCLUDED.verdict,
    match_json = EXCLUDED.match_json,
    reason_code = EXCLUDED.reason_code,
    reason_evidence = EXCLUDED.reason_evidence,
    computed_at = EXCLUDED.computed_at`,
		userID, postingID, contentHash, aiVersion, keywordHash,
		validation.Verdict, string(matchJSON), validation.ReasonCode, validation.ReasonEvidence,
		computedAt.UTC())
	if err != nil {
		return fmt.Errorf("storage: upsert ai dealbreaker validation: %w", err)
	}
	return nil
}

func (s *Store) AIDealbreakerValidationsByPostingID(
	ctx context.Context,
	userID int64,
	aiVersion string,
) (map[int64]map[string]AIDealbreakerValidation, error) {
	if err := s.validateAIDealbreakerStore(userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT posting_id, content_hash, ai_version, keyword_hash,
       verdict, match_json, reason_code, reason_evidence, computed_at
  FROM ai_dealbreaker_validations
 WHERE user_id = $1 AND ai_version = $2`, userID, aiVersion)
	if err != nil {
		return nil, fmt.Errorf("storage: query ai dealbreaker validations: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]map[string]AIDealbreakerValidation)
	for rows.Next() {
		var row AIDealbreakerValidation
		var matchJSON string
		if err := rows.Scan(
			&row.PostingID,
			&row.ContentHash,
			&row.AIVersion,
			&row.KeywordHash,
			&row.Validation.Verdict,
			&matchJSON,
			&row.Validation.ReasonCode,
			&row.Validation.ReasonEvidence,
			&row.ComputedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan ai dealbreaker validation: %w", err)
		}
		if err := json.Unmarshal([]byte(matchJSON), &row.Match); err != nil {
			return nil, fmt.Errorf("storage: decode dealbreaker match for posting %d keyword %s: %w",
				row.PostingID, row.KeywordHash, err)
		}
		row.Validation.CandidateID = row.KeywordHash
		if out[row.PostingID] == nil {
			out[row.PostingID] = make(map[string]AIDealbreakerValidation)
		}
		out[row.PostingID][row.ContentHash+"\x00"+row.KeywordHash] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: query ai dealbreaker validations: %w", err)
	}
	return out, nil
}

func (s *Store) validateAIDealbreakerStore(userID int64) error {
	if userID <= 0 {
		return errors.New("storage: dealbreaker validation user ID must be positive")
	}
	if s.dialect != DialectPostgres {
		return errors.New("storage: dealbreaker validations require PostgreSQL")
	}
	return nil
}
