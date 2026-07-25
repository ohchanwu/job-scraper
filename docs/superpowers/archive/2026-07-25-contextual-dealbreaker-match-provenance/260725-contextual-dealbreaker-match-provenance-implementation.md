# Contextual Dealbreaker Match-Provenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to execute this plan.

**Goal:** Replace the flawed Stage 1B evidence-copy contract with deterministic,
server-owned match provenance and independently validated AI semantic verdicts.

**Architecture:** Keep the existing token-exact detector as the candidate gate,
then attach one deterministic canonical match to each candidate. Stage 1B sends
the full normalized posting and those server-owned matches to the provider. The
provider returns only a verdict, compatible reason code, and optional grounded
reason text. PostgreSQL stores both responsibilities separately; scoring and the
existing exclusion UI always render the server match.

**Tech Stack:** Go, PostgreSQL migrations, `html/template`, existing AI provider
adapters, Go unit/integration/live-provider tests, GStack `/browse`.

**Approved specification:** [Contextual dealbreaker match-provenance
contract][spec]

## Global Constraints

- Use test-driven development: add each failing behavior test, observe the
  relevant failure, then write the smallest implementation that passes it.
- Preserve the existing candidate gate exactly:
  `tokenmatch.Contains(p.Title+" "+p.Company+" "+p.Description, phrase)`.
  Structured tags may identify provenance only after this gate succeeds.
- Preserve candidate IDs and profile order. Do not change tokenizer semantics.
- A server-derived `DealbreakerMatch` is immutable provider input. Provider
  output must never replace its evidence, source, or category.
- Enforce a maximum of 240 Unicode code points per saved dealbreaker line.
  This narrow, explicit exception to the profile-editing non-goal guarantees
  that every valid candidate can have bounded evidence containing the complete
  phrase; do not truncate saved phrases.
- Stage 1B receives the full NFC-normalized posting, including text beyond
  12,000 runes. Stage 1A and Stage 2 keep the existing 12,000-rune model input.
  All three continue to key content on the same full normalized-text hash.
- A manual AI-evaluation press considers Stage 1B candidates across all stored
  postings, with selected-surface rows first and remaining rows in stable
  storage order. Stage 1A extraction and Stage 2 scoring remain limited to the
  selected surface. The shared per-call cap and token budget still bound paid
  work, so multiple presses may be needed.
- Only `DealbreakerPromptVersion` changes from `"1"` to `"2"`. Do not change
  the Stage 1A extraction or Stage 2 prompt versions.
- Migration `0019` removes the legacy `evidence` column. Do not add a down
  migration, compatibility column, or version-1 backfill; the old binary is
  intentionally unsupported after migration.
- Preserve user, posting-content, provider, model, prompt-version, and
  keyword-hash cache isolation. Preserve independent valid-row persistence,
  conservative exclusion, no-progress reporting, and existing funding rules.
- Profile saving, startup, and cache inspection must make no paid calls.
- Do not add a new explanation interface or new CSS/JavaScript. Reuse the
  existing exclusion-reason surface on Today and Archive.
- Do not log or commit API keys, database URLs, user data, provider payloads, or
  other credentials. Never push or deploy from this execution.
- After the UI integration, use `frontend-qa` and GStack `/browse`; do not open
  the user's default browser.
- Update `docs/architecture.md` after implementation. When all gates pass,
  archive the completed spec and plan and update both documentation indexes.

---

## Task 1: Define the server-owned match contract and its input boundaries

**Files:**

- Modify: `internal/ai/provider.go`
- Modify: `internal/ai/extract.go`
- Modify: `internal/ai/extract_test.go`
- Modify: `internal/scoring/match.go`
- Modify: `internal/scoring/match_test.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/server_test.go`

### Interfaces to add

Add these provider-independent types in `internal/ai/provider.go`:

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

Add a Stage-1B-specific full input function in `internal/ai/extract.go`:

```go
func DealbreakerModelInput(p scraper.Posting) (text string, contentHash string)
```

Both `ModelInput` and `DealbreakerModelInput` must hash `rawModelText(p)`.
Only `ModelInput` applies `maxModelTextRunes`.

### Steps

- [x] Add failing `internal/ai/extract_test.go` cases proving:
  - `DealbreakerModelInput` returns NFC-normalized text beyond rune 12,000;
  - `ModelInput` remains truncated at the existing limit;
  - both functions return the same full-text content hash.
- [x] Run:

  ```bash
  go test ./internal/ai -run 'Test.*DealbreakerModelInput' -count=1
  ```

  Expected: compile or assertion failure because the new function does not
  exist.

- [x] Add failing table tests in `internal/scoring/match_test.go` for:
  - the detection gate rejecting a tag-only phrase absent from the combined
    title, company, and description;
  - a matched structured welfare tag represented in the description producing
    `{Source: "structured_tag", Category: "welfare"}`;
  - canonical priority:
    structured tag, title, company, description, then cross-field
    `combined_fields`;
  - whole-value evidence for a short title, company, or tag;
  - the shortest matching description line, with earliest line as a stable
    tie-break;
  - a deterministic occurrence-centered excerpt for a line over 240 runes;
  - evidence at most 240 runes that still matches the phrase through
    `tokenmatch.Contains`;
  - Korean normalization and particle behavior remaining unchanged;
  - multiple matches preserving profile order and the existing candidate ID.
- [x] Run:

  ```bash
  go test ./internal/scoring -run 'TestDealbreakerCandidates|TestCanonicalDealbreakerMatch' -count=1
  ```

  Expected: failures because candidates do not yet carry `Match`.

- [x] Add failing profile-save tests in `internal/server/server_test.go`:
  - a 240-rune dealbreaker line saves successfully;
  - a 241-rune line returns HTTP 400 with concise Korean guidance;
  - rejection leaves the previously saved profile unchanged;
  - Unicode length is counted in runes, not UTF-8 bytes.
- [x] Run:

  ```bash
  go test ./internal/server -run 'TestHandleProfileSave.*Dealbreaker' -count=1
  ```

  Expected: the overlong value is currently accepted.

- [x] Implement `DealbreakerModelInput` with one small shared helper for the
  full-text hash. Do not alter `rawModelText`, `maxModelTextRunes`, or existing
  Stage 1A/Stage 2 callers.
- [x] Implement canonical match selection in `internal/scoring/match.go`:
  1. Run the old combined-text gate first.
  2. Consider a structured tag only when its `Name` matches the phrase and the
     complete tag name matches within `p.Description` under
     `tokenmatch.Contains`.
  3. Otherwise select title, company, description, or the cross-field fallback
     in the approved priority.
  4. Use `tokenmatch.Find` for occurrence positions.
  5. Apply the same bounded occurrence-centered excerpt logic to any selected
     value over 240 runes. For `combined_fields`, excerpt the old combined
     string around the cross-field occurrence.
  6. Keep the excerpt helper private to `scoring`.
- [x] Validate `parseLines(r.FormValue("dealbreakers"))` before constructing or
  saving the profile. Reject any line over 240 runes; do not partially save,
  truncate, or invoke scoring.
- [x] Run:

  ```bash
  go test ./internal/ai ./internal/scoring ./internal/server -count=1
  ```

  Expected: all Task 1 tests pass and existing matching/profile tests remain
  green.

- [x] Review the cumulative diff for detection broadening and input mutation.
- [x] Commit:

  ```bash
  git add internal/ai/provider.go internal/ai/extract.go internal/ai/extract_test.go \
    internal/scoring/match.go internal/scoring/match_test.go \
    internal/server/handlers.go internal/server/server_test.go
  git commit -m "feat: attach canonical dealbreaker matches"
  ```

---

## Task 2: Implement the Stage 1B version-2 prompt and parser

**Files:**

- Modify: `internal/ai/provider.go`
- Modify: `internal/ai/dealbreakers.go`
- Modify: `internal/ai/dealbreakers_test.go`
- Modify: `internal/ai/injection_test.go`
- Modify: `internal/ai/provider_test.go`
- Modify: `internal/ai/stub.go`
- Modify: `internal/ai/version.go`
- Modify: `internal/ai/version_test.go`
- Modify: `internal/ai/anthropic_test.go`
- Modify: `internal/ai/openai_compatible_test.go`

### Interfaces to add

Replace model evidence with the reason contract:

```go
type DealbreakerReasonCode string

const (
	DealbreakerReasonRequirement          DealbreakerReasonCode = "requirement"
	DealbreakerReasonResponsibility       DealbreakerReasonCode = "responsibility"
	DealbreakerReasonExpectedCondition    DealbreakerReasonCode = "expected_condition"
	DealbreakerReasonBenefitOrEligibility DealbreakerReasonCode = "benefit_or_eligibility"
	DealbreakerReasonExplicitlyNegated    DealbreakerReasonCode = "explicitly_negated"
	DealbreakerReasonIncidentalOrMetadata DealbreakerReasonCode = "incidental_or_metadata"
	DealbreakerReasonInsufficientContext  DealbreakerReasonCode = "insufficient_context"
)

type DealbreakerValidation struct {
	CandidateID    string
	Verdict        DealbreakerVerdict
	ReasonCode     DealbreakerReasonCode
	ReasonEvidence string
}
```

The wire row becomes:

```go
type dealbreakerCheckWire struct {
	CandidateID    string                `json:"candidate_id"`
	Verdict        DealbreakerVerdict    `json:"verdict"`
	ReasonCode     DealbreakerReasonCode `json:"reason_code"`
	ReasonEvidence string                `json:"reason_evidence"`
}
```

### Steps

- [x] Replace the old evidence-copy tests with failing table tests covering all
  valid verdict/reason pairs:
  - `applies`: `requirement`, `responsibility`, `expected_condition`;
  - `not_applicable`: `benefit_or_eligibility`, `explicitly_negated`,
    `incidental_or_metadata`;
  - `uncertain`: `insufficient_context` with empty reason evidence.
- [x] Add failing parser tests proving:
  - `not_applicable` with empty reason evidence is valid;
  - optional reason evidence may omit the candidate phrase;
  - ungrounded or over-240-rune reason evidence is discarded while an otherwise
    valid applies/not-applicable row remains valid;
  - non-empty reason evidence on `uncertain` leaves that candidate unresolved;
  - unknown IDs, duplicate IDs, unknown verdicts, unknown reason codes, and
    incompatible verdict/reason pairs remain unresolved independently;
  - a malformed `checks` envelope is still an operation-level error;
  - valid rows beside invalid rows survive;
  - provider-echoed `match` data is ignored and cannot replace the candidate's
    server match;
  - an invalid server match (empty, overlong, unknown source, or not
    phrase-bearing) makes that candidate unresolved;
  - a phrase-bearing reason quote that contradicts its verdict no longer acts
    as semantic proof.
- [x] Add prompt tests asserting:
  - candidates are serialized with their server-owned `match`;
  - the verdict/reason matrix appears in the system prompt;
  - “any occurrence applies” and “all occurrences must be non-applicable” are
    explicit;
  - posting text and candidates remain untrusted user-message data;
  - the benefit/eligibility example allows empty or non-keyword reason evidence.
- [x] Run:

  ```bash
  go test ./internal/ai -run 'TestParseDealbreaker|TestDealbreakerPrompt' -count=1
  ```

  Expected: compile and assertion failures against the version-1 types and
  prompt.

- [x] Rewrite `dealbreakerSystemPrompt`, `buildDealbreakerUser`, and
  `parseDealbreakerValidations` to enforce the approved ownership split.
  Validate the server match before the model row. Trim and NFC-normalize optional
  reason evidence before the exact-substring check.
- [x] Keep independent row acceptance. Do not use `DisallowUnknownFields`;
  provider-echoed match fields should be inert data, not an operation failure.
- [x] Change only:

  ```go
  DealbreakerPromptVersion = "2"
  ```

- [x] Update Anthropic and OpenAI-compatible adapter fixtures to emit the new
  schema. The adapters should need no production branching because they share
  the provider-independent parser.
- [x] Run:

  ```bash
  go test ./internal/ai -count=1
  ```

  Expected: all AI contract, injection, adapter, and version tests pass.

- [x] Review the prompt for hidden posting instructions, ambiguous reason
  mappings, and accidental Stage 1A/Stage 2 version changes.
- [x] Commit:

  ```bash
  git add internal/ai
  git commit -m "feat(ai): revise contextual dealbreaker contract"
  ```

---

## Task 3: Migrate and persist match provenance

**Files:**

- Create:
  `internal/storage/postgres_migrations/0019_dealbreaker_match_provenance.sql`
- Modify: `internal/storage/ai_dealbreakers.go`
- Modify: `internal/storage/ai_dealbreakers_test.go`
- Modify: `internal/storage/postgres_integration_test.go`
- Modify: `internal/storage/store_test.go`
- Modify: `internal/storage/account_lifecycle_test.go`

### Storage contract

Extend the storage row with the immutable match:

```go
type AIDealbreakerValidation struct {
	PostingID   int64
	ContentHash string
	AIVersion   string
	KeywordHash string
	Match       ai.DealbreakerMatch
	Validation  ai.DealbreakerValidation
	ComputedAt  time.Time
}
```

Change the upsert signature so the caller supplies the match separately:

```go
func (s *Store) UpsertAIDealbreakerValidation(
	ctx context.Context,
	userID, postingID int64,
	contentHash, aiVersion, keywordHash string,
	match ai.DealbreakerMatch,
	validation ai.DealbreakerValidation,
	computedAt time.Time,
) error
```

### Steps

- [x] Add failing storage tests for:
  - insert/read round-trip of `Match`, `ReasonCode`, and `ReasonEvidence`;
  - upsert replacing all version-2 fields;
  - malformed `match_json` failing closed on read;
  - candidate ID still matching `keyword_hash`;
  - user isolation and the exact map key
    `content_hash + "\x00" + keyword_hash`;
  - one batch query behavior remaining unchanged.
- [x] Run:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./internal/storage -run 'TestAIDealbreaker' -count=1
  ```

  Expected: compile failures until the new fields and signature exist.

- [x] Add a migration integration test that creates a database through
  migration `0018`, seeds a version-1 row with legacy `evidence`, applies
  `0019`, and proves:
  - migration version 19 is recorded;
  - the row survives with default `{}`, empty reason code, and empty reason
    evidence;
  - the `evidence` column no longer exists;
  - a version-2 row can coexist under the unchanged primary key dimensions;
  - the version-1 row cannot satisfy a version-2 storage lookup.
- [x] Run:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./internal/storage \
    -run 'TestDealbreakerMatchProvenanceMigration' -count=1
  ```

  Expected: failure because migration `0019` does not exist.

- [x] Create migration `0019` with separate PostgreSQL statements:

  ```sql
  ALTER TABLE ai_dealbreaker_validations
      ADD COLUMN match_json TEXT NOT NULL DEFAULT '{}';
  ALTER TABLE ai_dealbreaker_validations
      ADD COLUMN reason_code TEXT NOT NULL DEFAULT '';
  ALTER TABLE ai_dealbreaker_validations
      ADD COLUMN reason_evidence TEXT NOT NULL DEFAULT '';
  ALTER TABLE ai_dealbreaker_validations
      DROP COLUMN evidence;
  ```

- [x] Marshal `match` once before the upsert. Write
  `verdict, match_json, reason_code, reason_evidence, computed_at`; read and
  unmarshal the same columns. Return a contextual storage error for invalid
  JSON. Do not reconstruct a match from provider reason evidence.
- [x] Update the schema-version expectation from 18 to 19 and remove legacy
  `evidence` SQL references from account-lifecycle tests.
- [x] Run:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./internal/storage -count=1
  ```

  Expected: all storage unit and PostgreSQL migration tests pass.

- [x] Search for obsolete runtime dependencies:

  ```bash
  rg -n '\bevidence\b' internal/storage \
    -g '*.go' -g '*.sql'
  ```

  Expected: no `ai_dealbreaker_validations.evidence` read, write, or fixture
  remains; unrelated extraction evidence is allowed.

- [x] Review the migration and exact cache-key/user filters.
- [x] Commit:

  ```bash
  git add internal/storage
  git commit -m "feat(storage): persist dealbreaker match provenance"
  ```

---

## Task 4: Integrate all-posting Stage 1B, scoring, and the existing UI

**Files:**

- Modify: `internal/server/rerate.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/ai_config_test.go`
- Modify: `internal/server/ai_rerate_test.go`
- Modify: `internal/server/ai_scrape_test.go`
- Modify: `internal/server/production_user_scope_test.go`
- Modify: `internal/server/rerate_status_test.go`
- Modify: `internal/server/scheduler_test.go`
- Modify: `internal/server/live_dealbreakers_test.go`
- Create: `internal/server/browser_fixture_test.go`
- Modify: `internal/scoring/engine.go`
- Modify: `internal/scoring/engine_test.go`
- Modify: `internal/server/exclusion_reason.go`
- Modify: `internal/server/exclusion_reason_test.go`
- Modify: `web/exclusion_reason.html`

### Integration rules

Add provenance fields to persisted scoring output:

```go
type ExclusionReason struct {
	Kind       string                    `json:"kind"`
	Label      string                    `json:"label"`
	Phrase     string                    `json:"phrase,omitempty"`
	Evidence   string                    `json:"evidence,omitempty"`
	Source     ai.DealbreakerMatchSource `json:"source,omitempty"`
	Category   string                    `json:"category,omitempty"`
	Confidence string                    `json:"confidence"`
}
```

Use these concise localized source labels:

- structured tag with `welfare`: `복지 태그`
- title: `제목`
- company: `회사`
- description: `공고 본문`
- combined fields: `공고 정보`

Append the label to the existing status text, for example
`AI 문맥 확인 · 복지 태그`; do not add another panel.

### Steps

- [x] Update scoring tests first. Prove:
  - `applies` and `uncertain` use `candidate.Match.Evidence`, never
    `validation.ReasonEvidence`;
  - source/category flow into `ExclusionReason`;
  - missing, invalid, and uncertain validation remains excluded;
  - `not_applicable` suppresses only its own candidate;
  - multiple candidates clear exclusion only when all are
    `not_applicable`;
  - existing education, career, and minimum-score reasons are unchanged.
- [x] Run:

  ```bash
  go test ./internal/scoring -run 'Test.*Exclusion|Test.*Dealbreaker' -count=1
  ```

  Expected: compile/assertion failures until scoring consumes `Match`.

- [x] Add server integration tests proving:
  - `validateDealbreakers` sends `DealbreakerModelInput`, including a match
    after rune 12,000;
  - each accepted validation is persisted with the exact server candidate
    match;
  - independently valid rows persist while invalid/missing rows remain pending;
  - correct `not_applicable` with empty reason evidence resolves on the first
    provider response;
  - current version-1 rows miss the version-2 lookup while Stage 1A extraction
    rows remain reusable;
  - provider, model, user, content, and keyword isolation remain unchanged;
  - no-progress, budget-blocked, call-cap-blocked, and provider-error summaries
    remain correct.
- [x] Add a `runRerate` test with one on-surface and one off-surface posting.
  Assert that one press:
  - loads all stored postings for Stage 1B;
  - evaluates selected-surface Stage 1B rows before off-surface rows when the
    shared call cap cannot cover both;
  - respects the shared cap/budget and leaves excess work pending;
  - runs Stage 1A and Stage 2 only for the selected surface;
  - makes no paid call for a current version-2 cache hit.
- [x] Run:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./internal/server \
    -run 'TestProductionDealbreaker|TestRunRerate.*Dealbreaker|Test.*ContextPending' \
    -count=1
  ```

  Expected: failures because rerate is currently surface-scoped and persists
  provider evidence.

- [x] Update `validateDealbreakers` to:
  - call `ai.DealbreakerModelInput`;
  - map unresolved IDs back to the exact `candidate.Match`;
  - pass that match separately to storage;
  - retain per-posting pending counts, independent rows, budget/cap behavior,
    and the existing progress line
    `공고 #<id> (<company>) 문맥 확인 중...`.
- [x] In `runRerate`, keep `candidatePostingsForRerate` for Stage 1A and the
  later visible Stage 2 set. Load `s.store.AllPostings(ctx)` separately and
  build a deduplicated Stage 1B list with the selected-surface candidates first
  and the remaining stored postings in `AllPostings` order. Share the same
  `budget` and `callCap`.
- [x] Update `scoreAll` cache wiring in `internal/server/server.go` to pass the
  version-2 validation map without weakening exact content/keyword matching.
- [x] Update UI/view tests first. Prove:
  - deterministic match evidence is escaped and token-marked;
  - the source label is deterministic;
  - unknown source/category values fall back calmly to `공고 정보`;
  - provider reason evidence is not rendered;
  - Today and Archive render the status/evidence while preserving bookmark,
    hidden, and external-link actions.
- [x] Add `SourceLabel` to `exclusionReasonView`, derive it only from the stored
  server match, and append it to the existing status line in
  `web/exclusion_reason.html`. Do not add CSS or client behavior.
- [x] Update all tests and stubs that construct `DealbreakerCandidate`,
  `DealbreakerValidation`, or `UpsertAIDealbreakerValidation` so they use valid
  version-2 server matches and reason codes.
- [x] Add `internal/server/browser_fixture_test.go` behind the
  `browserfixture` build tag. Reuse the production PostgreSQL test server and
  existing session/credential helpers to:
  - seed a synthetic owner, AI profile, and two incident postings;
  - inject a deterministic `ai.StubProvider` through `newAIProvider`;
  - preseed Stage 1A and Stage 2 so browser presses exercise only Stage 1B;
  - set per-call cap 1 so two presses are required;
  - expose a test-only login route and a test-only stop route;
  - print `BROWSER_FIXTURE_URL=<loopback-url>` and
    `BROWSER_FIXTURE_STOP_URL=<loopback-url>`, then keep serving until the stop
    route is called.
  This file must compile only in the test binary and add no production route or
  runtime flag.
- [x] Run:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./internal/scoring ./internal/server -count=1
  ```

  Expected: all scoring, rerate, isolation, blocker, and view tests pass.

- [x] Re-read the cumulative Task 4 diff for accidental Stage 1A/Stage 2
  broadening, provider-controlled evidence, or new UI surfaces.
- [x] Commit:

  ```bash
  git add internal/server internal/scoring web/exclusion_reason.html
  git commit -m "feat: integrate dealbreaker match provenance"
  ```

---

## Task 5: Document, verify, and browser-test the complete behavior

**Files:**

- Modify: `docs/architecture.md`
- Modify:
  `docs/superpowers/plans/260725-contextual-dealbreaker-match-provenance-implementation.md`
- Modify: `docs/superpowers/README.md`
- Modify: `docs/README.md`
- Move after all gates pass:
  `docs/superpowers/specs/260725-contextual-dealbreaker-match-provenance-contract.md`
- Move after all gates pass:
  `docs/superpowers/plans/260725-contextual-dealbreaker-match-provenance-implementation.md`

### Steps

- [x] Update `docs/architecture.md` to describe:
  - unchanged deterministic candidate detection;
  - canonical server match provenance;
  - full-text Stage 1B versus capped Stage 1A/Stage 2 inputs;
  - all-stored-posting manual Stage 1B passes;
  - verdict/reason validation and conservative fallback;
  - version-2 persistence and existing exclusion UI rendering.
- [x] Point active architecture links to the new contract while it remains
  active. Do not load or rewrite unrelated archived documents.
- [x] Run formatting, static analysis, and builds:

  ```bash
  test -z "$(gofmt -l .)"
  go vet ./...
  go build ./cmd/jobcron ./cmd/jobcron-import ./cmd/jobcron-user
  ```

- [x] Run the complete PostgreSQL-backed suite and race detector against a
  disposable test database:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test ./... -count=1 &&
    go test -race ./... -count=1
  ```

- [x] Run live scraper integration tests:

  ```bash
  go test -tags integration \
    ./internal/scraper/jumpit/ \
    ./internal/scraper/rallit/ \
    ./internal/scraper/demoday/ \
    ./internal/scraper/greeting/ \
    ./internal/scraper/greenhouse/
  ```

- [x] Run the opt-in live-provider gate with credentials supplied only through
  the local environment:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    test -n "${JOBCRON_ANTHROPIC_KEY:-}" &&
    go test -tags liveprovider ./internal/server \
    -run TestLiveStage1BContextualDealbreakers -count=1
  ```

  Verify the live test covers one `not_applicable` benefit/eligibility case,
  one applicable requirement, empty optional reason evidence, and persistence
  of the server match. If the provider is unavailable or rate-limited, record
  the exact sanitized blocker; do not claim the gate passed.

- [x] Read and apply the `frontend-qa` skill because the rendered UI changed.
  Start the deterministic test-only preview without opening the user's browser:

  ```bash
  test -n "${JOBCRON_TEST_POSTGRES_URL:-}" &&
    go test -tags browserfixture ./internal/server \
    -run TestDealbreakerProvenanceBrowserFixture -v -count=1 -timeout=0
  ```

  Run it in a dedicated terminal/session and use the printed
  `BROWSER_FIXTURE_URL`. It contains only synthetic data and a deterministic
  provider, requires no credential, and stays alive for human inspection.
- [x] Using GStack `/browse`, walk the real user path on desktop and mobile:
  1. Open the test-only login URL, then navigate through the real All Jobs UI.
  2. Confirm the initial Stage 1B pending count.
  3. Press `AI 평가` until the pending count reaches zero.
  4. Confirm the benefit posting is restored after
     `not_applicable`.
  5. Confirm the requirement posting remains excluded.
  6. Inspect Today and Archive and verify the exact server-derived evidence,
     `복지 태그`/source label, and highlighted phrase.
  7. Reload and confirm persisted results remain stable and cached reruns make
     no new provider call.
  8. Click the posting link and verify the destination is the claimed role,
     not merely a 200 response or generic page.
  9. Check that no browser console errors occur and adjacent bookmark/hidden
     actions still work.
- [x] Capture sanitized screenshots and browser notes under the ignored
  `.superpowers/sdd/260725-dealbreaker-match-provenance/` directory. Do not
  track them.
- [x] Leave the fixture preview running and report its URL for human
  inspection. Report the printed stop URL as the cleanup path; do not stop it
  before handoff unless verification fails.
- [x] Inspect the complete diff from the implementation base, then scan the
  staged documentation and code:

  ```bash
  git diff --check
  git diff --stat
  git diff
  gitleaks git --staged --redact --no-banner
  ```

  Manually review tracked text for credentials, personal data, private
  topology, raw provider payloads, and unnecessary machine-specific details.
- [x] When every gate passes, mark all plan checkboxes complete, change the
  specification status to implemented, and move the spec and plan into:

  ```text
  docs/superpowers/archive/2026-07-25-contextual-dealbreaker-match-provenance/
  ```

- [x] After moving, change this plan's `[spec]` target to the adjacent archived
  contract and change the contract's `[prior-spec]` target to its correct
  archive-relative path. Run a local-link check so neither move leaves a broken
  documentation link.
- [x] Update `docs/superpowers/README.md` and `docs/README.md`: remove the
  completed items from active work, add concise archive links, and keep
  `docs/architecture.md` pointing to the durable archived contract.
- [x] Stage only the intended documentation, inspect the staged diff again,
  rerun Gitleaks, and commit:

  ```bash
  git add docs
  git diff --cached
  gitleaks git --staged --redact --no-banner
  git commit -m "docs: record dealbreaker provenance architecture"
  ```

- [x] Confirm the final worktree contains no unintended tracked changes and
  report:
  - commit list;
  - exact verification commands and outcomes;
  - live-provider status;
  - preview URL;
  - browser-flow results;
  - any independent decisions or sanitized blockers.

[spec]: 260725-contextual-dealbreaker-match-provenance-contract.md
