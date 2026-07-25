# AI Re-rate Blocker Surfacing Plan

Date: 2026-07-25

Status: Active; investigation complete, implementation not started.

## What is happening

The `8` is not a Stage-1 extraction backlog. It is eight postings with twelve
unresolved contextual dealbreaker checks. The live rows have current content
hashes and candidate IDs, so this is not stale data or a cache-key mismatch.

`ai.Provider.ValidateDealbreakers` accepts only citation-gated checks. A missing
candidate, duplicate candidate ID, invalid verdict, non-verbatim evidence, or
evidence that does not match the candidate phrase is omitted. If every check in
a successful provider response is omitted, the method currently returns an
empty result with no error. The re-rate flow therefore:

1. spends tokens and displays per-posting progress;
2. stores none of the rejected checks;
3. completes and reloads the page;
4. calculates the same eight pending postings again.

Rejected checks and rejection reasons are not persisted or logged, so the exact
reasons from the three completed runs cannot be reconstructed after the fact.

Current pending postings:

- #46 `(주)에이핀아이앤씨` — 1 unresolved check
- #58 `페이타랩` — 2 unresolved checks
- #79 `페이타랩` — 1 unresolved check
- #89 `주식회사 당코` — 1 unresolved check
- #136 `당근` — 1 unresolved check
- #137 `당근` — 3 unresolved checks
- #138 `당근` — 2 unresolved checks
- #167 `페이타랩` — 1 unresolved check

## Goal

After a re-rate, tell the user whether the pending count fell. If it did not,
show one short persistent explanation and do not describe the run as a
successful evaluation.

Recommended Korean copy:

> 8개는 AI가 근거를 확인하지 못했어요. 지금 다시 눌러도 같은 결과일 수 있어요.

The detailed reason can remain behind a small disclosure:

> AI 응답의 근거 문장이 공고 원문과 일치하지 않았거나 응답에서 빠졌어요.

## Implementation tasks

### 1. Return structured validation results

Files:

- `internal/ai/dealbreakers.go`
- `internal/ai/dealbreakers_test.go`
- `internal/ai/provider.go`

Change the parser/validator result to count accepted, rejected, and missing
candidate checks. Preserve the safe citation gate. Do not store raw model
responses or user profile text.

Treat a provider response with zero accepted checks and at least one requested
candidate as a typed no-valid-results outcome, not a successful empty result.

Tests:

- all checks rejected by evidence gating;
- a candidate omitted from the response;
- duplicate and unknown candidate IDs;
- partial acceptance;
- explicit `uncertain` remains a valid stored result.

### 2. Measure before/after progress in the re-rate summary

Files:

- `internal/server/rerate.go`
- `internal/server/ai_rerate_test.go`
- `internal/server/production_user_scope_test.go`

Extend `rerateSummary` with contextual-validation counts:

- pending before;
- attempted;
- resolved;
- rejected or missing;
- pending after;
- stop reason: provider, budget, per-call cap, or invalid evidence.

Only emit `문맥 확인 중` after a call is actually attempted. Do not count an
empty accepted result as progress. Compare `pending before` with `pending after`
before selecting the terminal outcome.

### 3. Add a persistent `no_progress` terminal outcome

Files:

- `internal/server/rerate_status.go`
- `internal/server/rerate_status_test.go`
- `web/ai-rerate.js`
- `web/_rerate.html`

Reuse the existing in-memory re-rate tracker and the existing session-storage
reload notice; no schema is needed for the first version.

Add `no_progress` when pending contextual checks remain and the count did not
decrease. Preserve its exact server message after reload instead of replacing it
with the generic “new results applied” message.

Use blocker-specific concise copy:

- invalid/missing evidence: the recommended copy above;
- provider/key/model error: existing provider error copy;
- daily budget: existing profile-limit copy and link;
- per-call cap with real progress: `N개 확인, M개 남음` and allow another press.

Do not permanently disable retry. The message should set expectations while a
later model response or profile edit can still resolve the checks.

### 4. Separate the two counters

Files:

- `internal/server/rerate.go`
- `web/_rerate.html`
- related rendering tests

`StaleCount` currently combines pending contextual validation and stale Stage-2
scores while the label describes only contextual validation. Split it into:

- `PendingContextCount`;
- `PendingScoreCount`.

Render only the relevant causes. This prevents a future Stage-2 backlog from
being mislabeled as a context problem.

## Acceptance criteria

- A successful provider response with zero accepted validations ends as
  `no_progress`, not `changed`.
- The page reload preserves the concise reason.
- Repeated presses cannot look like successful work when the pending count is
  unchanged.
- Partial success reports the actual reduction.
- Budget, provider, cap, and citation-gate blockers have distinct messages.
- No raw provider response, credential, or profile text is persisted or logged.
- Server tests, full PostgreSQL and fallback suites, `go vet`, and build pass.
- Desktop and mobile browser QA verify copy wrapping, reload persistence, retry,
  and absence of console errors.
