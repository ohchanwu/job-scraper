ALTER TABLE ai_dealbreaker_validations
    ADD COLUMN match_json TEXT NOT NULL DEFAULT '{}';

ALTER TABLE ai_dealbreaker_validations
    ADD COLUMN reason_code TEXT NOT NULL DEFAULT '';

ALTER TABLE ai_dealbreaker_validations
    ADD COLUMN reason_evidence TEXT NOT NULL DEFAULT '';

ALTER TABLE ai_dealbreaker_validations
    DROP COLUMN evidence;
