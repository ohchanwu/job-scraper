# Contextual Dealbreaker Match-Provenance Contract

**Status:** Approved design; implementation plan awaiting approval

**Created:** 2026-07-25

**Supersedes:** The Stage 1B model-output and evidence-validation contract in
[Stage 1 contextual dealbreaker validation and exclusion evidence][prior-spec]

## Summary

The current contextual-dealbreaker contract asks the AI model to return a
verdict and an evidence quote that contains the matched dealbreaker phrase.
That requirement is internally inconsistent when the correct verdict is
`not_applicable`: the useful explanation may be elsewhere in the posting, or
the matched phrase may come from structured metadata rather than the visible
job description.

The revised contract separates two different kinds of evidence:

1. The server records **match evidence** proving why deterministic matching
   selected the posting.
2. The model returns a **semantic verdict** and a reason code explaining what
   the matched phrase means in context.

The server validates those responsibilities independently. A correct semantic
verdict no longer fails merely because the model did not copy the dealbreaker
phrase into its explanation.

## Problem

`internal/scoring/match.go` currently identifies contextual candidates by
searching the combined title, company, and description text. It returns only a
candidate ID and phrase.

`internal/ai/dealbreakers.go` then asks the model for:

- `candidate_id`
- `verdict`
- `evidence`

The parser accepts a row only when `evidence` is an exact substring of the model
input and contains the candidate phrase. This gate checks that the model copied
the match, but it does not prove that the verdict is semantically correct.

It creates two failure modes:

- A correct `not_applicable` verdict is rejected when the model cites a
  benefit, negation, or other contextual sentence that does not repeat the
  phrase.
- A wrong verdict can pass if the model copies a phrase-bearing sentence, even
  when that sentence logically contradicts the verdict.

The recent `병역특례` incident exposed the first failure mode. The deterministic
match came from a structured welfare tag appended to the stored description,
while the visible job description did not contain the phrase. The model
correctly classified the match as not applicable, but its revisions were
rejected because the explanation did not repeat `병역특례`. The candidates
therefore remained unresolved and conservatively excluded through repeated
re-rates.

## Goals

- Preserve deterministic candidate detection and its current token semantics.
- Make the server the source of truth for why a phrase matched.
- Make the model responsible only for contextual classification.
- Accept correct verdicts without requiring keyword-bearing model evidence.
- Preserve strict grounding, user isolation, cache identity, AI budgets, and
  conservative exclusion.
- Expose enough deterministic provenance to explain hidden structured matches.
- Prevent repeated re-rate attempts caused solely by evidence-copy failures.

## Non-goals

- Discovering synonyms or semantic matches beyond deterministic phrase matching.
- Redesigning the profile or dealbreaker-editing experience, apart from
  rejecting a dealbreaker line longer than the contract's 240-code-point
  evidence limit. The saved phrase is never silently truncated.
- Invalidating or rerunning Stage 1A career and education extractions.
- Changing Stage 2 scoring.
- Redesigning source-specific scrapers or storing raw provider payloads.
- Adding manual overrides or feedback-training workflows.
- Weakening conservative fallback for unavailable or invalid AI results.
- Changing existing tokenizer semantics as part of this work.

## Design Principles

### The detector owns match proof

The component that finds a deterministic match has the exact text and source
needed to prove that match. It must preserve that proof instead of asking the
model to reproduce it.

### The model owns semantic classification

The model receives the full normalized posting text plus the server-selected
match. It decides whether the phrase describes a role condition, is
non-applicable context, or remains uncertain.

### Validation follows ownership

The server validates match evidence as server-derived data. It validates the
model response for known IDs, allowed verdicts, compatible reason codes, and
optional grounded explanation text. One validation cannot substitute for the
other.

## Revised Candidate Input

The server extends each contextual candidate with one canonical match:

```go
type DealbreakerMatchSource string

const (
	DealbreakerMatchTitle         DealbreakerMatchSource = "title"
	DealbreakerMatchCompany       DealbreakerMatchSource = "company"
	DealbreakerMatchDescription   DealbreakerMatchSource = "description"
	DealbreakerMatchStructuredTag DealbreakerMatchSource = "structured_tag"
	DealbreakerMatchCombined      DealbreakerMatchSource = "combined_fields"
)

type DealbreakerMatch struct {
	Evidence string                 `json:"evidence"`
	Source   DealbreakerMatchSource `json:"source"`
	Category string                 `json:"category,omitempty"`
}

type DealbreakerCandidate struct {
	ID     string           `json:"candidate_id"`
	Phrase string           `json:"phrase"`
	Match  DealbreakerMatch `json:"match"`
}
```

`Category` identifies a structured-tag category when one exists, such as
`welfare`. It is not required for ordinary title, company, or description
matches.

### Detection compatibility

Candidate detection must not broaden:

1. Determine whether the candidate matches using the existing combined
   `Title + Company + Description` semantics.
2. Only after a match exists, locate its canonical source for provenance.

Structured data may improve provenance only when the candidate already matched
under the old combined-text rule. It must not create a new candidate by itself.

### Canonical match selection

When a candidate matches in more than one place, choose one match
deterministically in this order:

1. A structured tag already represented in the combined description.
2. Title.
3. Company.
4. Description.
5. `combined_fields` when the existing concatenation caused a cross-field match
   that cannot be attributed to one field.

The priority favors the most explanatory source while preserving existing
detection behavior. The model still receives the full posting and must consider
all occurrences, not only the canonical one.

### Evidence construction

Match evidence must:

- be derived from the selected source;
- be non-empty;
- contain the candidate phrase under the existing token matcher; and
- be no more than 240 Unicode code points.

For a short title, company name, or structured tag, use the entire value. For a
description, use the shortest line containing the occurrence. If that line is
too long, use a deterministic, occurrence-centered excerpt. Use
`combined_fields` only for the compatibility fallback described above.

For the incident case, the expected match is equivalent to:

```json
{
  "evidence": "병역특례 가능",
  "source": "structured_tag",
  "category": "welfare"
}
```

## Revised Model Output

The provider-independent response becomes:

```go
type DealbreakerReasonCode string

type DealbreakerValidation struct {
	CandidateID   string
	Verdict       DealbreakerVerdict
	ReasonCode    DealbreakerReasonCode
	ReasonEvidence string
}
```

Wire format:

```json
{
  "checks": [
    {
      "candidate_id": "candidate-1",
      "verdict": "not_applicable",
      "reason_code": "benefit_or_eligibility",
      "reason_evidence": ""
    }
  ]
}
```

### Verdicts

`applies`

: At least one occurrence asserts that the candidate condition is required,
performed, or expected for the role.

`not_applicable`

: Every occurrence is negated, describes a benefit or eligibility option, is
incidental metadata, or otherwise does not assert the condition as part of
the role.

`uncertain`

: The supplied text does not support either conclusion with adequate
confidence.

### Reason codes

Allowed with `applies`:

- `requirement`
- `responsibility`
- `expected_condition`

Allowed with `not_applicable`:

- `benefit_or_eligibility`
- `explicitly_negated`
- `incidental_or_metadata`

Allowed with `uncertain`:

- `insufficient_context`

The prompt must define the matrix directly and include examples where
`not_applicable` is correct even though the semantic explanation does not
repeat the candidate phrase.

### Multiple occurrences

The model must evaluate every occurrence in the full posting:

- If any occurrence applies, return `applies`.
- Return `not_applicable` only when every occurrence is non-applicable.
- Return `uncertain` when the occurrences are mixed or genuinely undecidable
  without stronger evidence.

The existing multi-candidate rule remains unchanged: a posting clears
contextual exclusion only when every candidate is `not_applicable`.

## Response Validation

The parser validates each returned row independently:

- `candidate_id` must be known and appear exactly once.
- `verdict` must be one of the three allowed values.
- `reason_code` must be allowed for that verdict.
- `uncertain` must use `insufficient_context` and empty `reason_evidence`.
- The server-owned candidate match must remain valid and phrase-bearing.
- The provider must not echo or replace match evidence.

`reason_evidence` is optional. When present, it must:

- be no more than 240 Unicode code points; and
- be an exact substring of the normalized model input.

It does not need to contain the candidate phrase. If optional reason evidence
is missing, too long, or not grounded, discard that field while retaining an
otherwise valid verdict and reason code. Unknown IDs, duplicate IDs, invalid
verdicts, and invalid verdict/reason-code combinations leave that candidate
unresolved.

A malformed response envelope remains an operation-level error. Valid rows in a
well-formed envelope are still accepted independently from invalid rows.

Posting and candidate text remain untrusted data, never instructions. This is
the existing prompt-injection boundary: hostile posting text attempting to
influence the model must not alter the system contract.

## Conservative Fallback

The safety behavior does not change:

- `applies` remains excluded.
- `uncertain` remains excluded.
- Missing, invalid, unavailable, budget-limited, or unresolved results remain
  excluded.
- A posting is restored only after every contextual candidate has a persisted
  `not_applicable` result for the current cache identity.

This revision removes false unresolved states; it does not turn uncertainty into
inclusion.

## Persistence and Cache Versioning

Add migration `0019` to replace the legacy evidence field in
`ai_dealbreaker_validations`:

```sql
ALTER TABLE ai_dealbreaker_validations
    ADD COLUMN match_json TEXT NOT NULL DEFAULT '{}',
    ADD COLUMN reason_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN reason_evidence TEXT NOT NULL DEFAULT '',
    DROP COLUMN evidence;
```

The exact PostgreSQL syntax may be split into separate statements to match
project migration conventions.

Migration and cache requirements:

- Version 2 reads and writes only the structured match and reason fields.
- Remove all storage and test dependencies on the legacy `evidence` column
  before the migration is considered validated.
- Old-binary compatibility is intentionally not supported. The application has
  not been deployed and has no production users, so retaining duplicate data
  solely for rollback would add dead schema and code.
- Do not backfill version 1 rows.
- Increment `DealbreakerPromptVersion` from `"1"` to `"2"`.
- Version 1 rows remain stored but cannot satisfy a version 2 cache lookup.

The version bump marks every Stage 1B contextual validation as pending without
making an automatic provider call. The user initiates the replacement pass with
the existing AI-evaluation control. Each press considers all stored postings but
still obeys the configured per-call cap and token budget, so completing the
manual rerun may require multiple presses. Stage 1A career and education
extractions remain cached and are not rerun.

Subsequent version 2 cache hits remain free. Cache keys continue to isolate
user, posting content hash, provider, model, prompt version, and keyword hash.

## Rerate and UI Behavior

`internal/server/rerate.go` persists every independently valid response and
leaves only invalid or missing candidates pending. Correct
`not_applicable` results therefore resolve on the first successful provider
response even when optional reason evidence is absent.

No UI redesign is required. Wherever existing score or exclusion details show
dealbreaker evidence, they should show the deterministic server match and its
source rather than a model-generated quote. A reason code may appear as a short
localized label only in an existing reason surface; adding a new explanation
interface is out of scope.

The existing no-progress and blocker surfacing remains in place. It should now
report genuine unresolved results rather than repeated evidence-copy failures.

## Expected File Impact

- `internal/scoring/match.go`
- `internal/tokenmatch/` if the smallest implementation needs a reusable span
  or excerpt helper
- `internal/ai/provider.go`
- `internal/ai/dealbreakers.go`
- `internal/ai/version.go`
- `internal/storage/postgres_migrations/0019_*.sql`
- `internal/storage/ai_dealbreakers.go`
- `internal/server/rerate.go`
- `internal/server/server.go`
- Existing score or exclusion view-model, template, and test files as needed
- `docs/architecture.md` after implementation

The implementation plan must name exact files after confirming the smallest
change surface.

## Acceptance Criteria

1. A phrase found in a structured welfare tag produces deterministic match
   evidence with source `structured_tag`.
2. `병역특례 가능` can persist as `not_applicable` with reason
   `benefit_or_eligibility` and empty reason evidence.
3. A phrase asserted as a role requirement persists as `applies` and remains
   excluded.
4. Optional reason evidence that is ungrounded or too long is dropped without
   rejecting an otherwise valid verdict. Semantic relevance remains the
   model's responsibility and is not inferred by the parser.
5. Unknown or duplicate candidate IDs, invalid verdicts, and incompatible
   reason codes remain unresolved.
6. `uncertain` requires `insufficient_context` and retains exclusion.
7. Every canonical server match is source-derived, bounded, and contains the
   candidate phrase under the existing token matcher.
8. Provider output cannot alter the canonical match or its source.
9. Multiple candidates clear exclusion only when all are `not_applicable`.
10. Multiple occurrences return `applies` when any occurrence applies and
    `uncertain` when the full text is genuinely undecidable.
11. Version 1 rows do not satisfy version 2 lookups; valid version 2 rows do.
12. User, content, provider, model, prompt-version, and keyword-hash isolation
    remain unchanged.
13. Startup and profile saving make no paid calls; existing rerate budgets and
    caps remain unchanged.
14. Existing UI evidence surfaces show deterministic match provenance, not a
    hallucinated or provider-selected quote.
15. Re-running AI evaluation after valid version 2 responses does not leave
    candidates pending solely because reason evidence lacks the phrase.
16. Migration `0019` upgrades a database at migration `0018`, removes the
    legacy `evidence` column, and leaves no runtime query dependent on it.
17. The version bump queues all Stage 1B validations for user-triggered rerun
    while preserving existing Stage 1A career and education extractions.

## Verification Plan

### Unit tests

- Canonical match selection for title, company, description, structured tags,
  and `combined_fields`.
- Normalization, Korean particles, multiple occurrences, and 240-code-point
  excerpt boundaries.
- Every verdict/reason-code combination.
- Optional reason-evidence acceptance and discard behavior.
- Unknown, duplicate, missing, and independently valid response rows.
- The adversarial counterexample where a phrase-bearing quote contradicts the
  verdict, demonstrating that copying is no longer treated as semantic proof.

### Storage integration tests

- Migration from schema version `0018`, including removal of `evidence`.
- Version 2 insert, upsert, bulk read, and exact cache identity.
- Coexistence of version 1 and version 2 rows.
- Absence of runtime SQL and storage types that depend on `evidence`.
- Per-user isolation.

### Server integration tests

- A fixture with a hidden structured welfare tag and no phrase in the visible
  description.
- A prompt-version bump leaves Stage 1A extraction rows untouched while making
  every Stage 1B validation pending.
- First rerate resolves a valid `not_applicable` candidate and restores the
  posting.
- `applies`, `uncertain`, invalid, unavailable, and budget-limited cases remain
  excluded.
- Multiple candidate and multiple occurrence behavior.
- No-progress reporting reflects only candidates that are genuinely pending.

### Provider and browser verification

- Run the opt-in live-provider gate with test credentials and confirm the
  provider follows the version 2 schema.
- In the local preview, manually run the existing AI-evaluation flow until the
  Stage 1B pending count reaches zero, using multiple presses when the per-call
  cap or token budget requires it.
- Inspect a restored posting and an excluded posting to confirm their displayed
  deterministic evidence and source are correct.
- Verify the browser console remains free of errors on the affected flow.

### Project gates

- Formatting
- Full unit and integration test suite
- PostgreSQL-backed tests
- Race detector where supported
- Static analysis and build
- Documentation link and publication-safety checks

## Rollout and Recovery

Apply migration `0019` with the version 2 binary after the full migration,
storage, server, provider, and browser gates pass. The application makes no
automatic replacement calls; the user manually reruns Stage 1B from the
AI-evaluation control.

Restoring the old binary after migration `0019` is intentionally unsupported
because that binary requires the removed `evidence` column. Pre-launch recovery
is a forward fix, restoring the local database from a pre-migration backup, or
resetting the disposable local database. Do not add a compatibility column or
down migration solely to preserve old-binary rollback.

[prior-spec]: ../archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence.md
