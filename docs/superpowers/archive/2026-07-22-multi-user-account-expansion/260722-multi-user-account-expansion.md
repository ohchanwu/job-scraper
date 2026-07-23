# Multi-User Account Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the access-code-gated SSAFY cohort release with independent accounts,
owner-sponsored global Stage-1 extraction, sequential per-user analysis, and BYOK Anthropic,
OpenAI, and Gemini support.

**Architecture:** Keep PostgreSQL as the single writable store and the opaque database-backed
session as the authentication boundary. Split the current single-user scrape pipeline into one
global collection/Stage-1 phase and one user-scoped analysis phase. Resolve Stage-1 funding only
from the explicitly configured sponsor user; resolve Stage-1B and Stage-2 funding separately for
each analyzed user. Keep `ai.Provider` and the existing pinned HTTP chassis, adding OpenAI and
Gemini through the smallest compatible adapters.

**Tech Stack:** Go 1.26, `net/http`, `html/template`, PostgreSQL 18, pgx stdlib,
Argon2id, AES-GCM credential envelopes, embedded HTML/CSS/JavaScript, Docker Compose,
gstack `/browse` and `frontend-qa`.

## Global Constraints

- Implement in two reviewable waves. Wave A is the cohort/multi-user core using Anthropic; Wave B
  adds OpenAI and Gemini. Wave B must not destabilize or block Wave A.
- Do not add application roles, organizations, social auth, public password recovery, email
  verification, LangChain, a job queue, provider plugins, or remote model discovery.
- Do not add provider SDK dependencies. The current `providerSpec` chassis already preserves the
  pinned transport, request pacing, bounded responses, usage accounting, stable errors, and
  deterministic offline tests with less code than an SDK wrapper would require.
- Use OpenAI Chat Completions directly. Use Gemini's documented OpenAI-compatible endpoint because
  both services fit one small request/response adapter without weakening Jobcron's transport or
  accounting controls. Do not change Anthropic's Messages API contract.
- Treat every tracked file as public. Never commit real access codes, passwords, API keys,
  database URLs, session secrets, cloud identifiers, or provider responses containing user data.
- Do not apply Terraform, touch live AWS/Cloudflare resources, deploy, push, create a PR, or run a
  live provider test unless its explicit test key is already present.
- Preserve the existing global scrape lock. Scheduled user analysis is sequential.
- Keep local no-login preview behavior. Production HTTP requests always derive `userID` from the
  opaque session; never infer it from account order.
- Use TDD for every behavior change. Run the narrow failing test before implementation, then the
  narrow passing test, then the package suite.
- Frontend changes require Tier C verification. Use at most two concurrent browser-verification
  workers, default to one, and never start more than three; each browser consumes substantial CPU
  and memory. Use gstack `/browse`, never the user's default browser.
- The new account pages contain no content images or video, so `media-load-fade` is intentionally
  not applicable. If implementation adds content media, stop and apply that skill from its file.
- Commit at meaningful, reviewable checkpoints. The message listed for each task is a suggested
  checkpoint, not a one-commit limit. Use as many coherent commits as the work needs; squash only
  temporary WIP or fixup noise, and review the exact base-to-tip range. Never push.

---

## Wave A — Cohort And Multi-User Core

### Task 1: Canonical account identity and general user storage

**Files:**

- Create: `internal/auth/identity.go`
- Create: `internal/auth/identity_test.go`
- Create: `internal/storage/postgres_migrations/0018_multi_user_accounts.sql`
- Modify: `internal/storage/users.go`
- Modify: `internal/storage/users_test.go`
- Modify: `internal/storage/postgres_integration_test.go`

- [x] **Step 1: Write failing identity-policy tests**

Add table-driven tests for:

- `NormalizeEmail("  Student@Example.COM ") == "student@example.com"`;
- normalized addresses pass server-side email syntax validation while display-name forms fail;
- passwords shorter than 15 Unicode characters fail;
- a 15-character passphrase succeeds;
- long passphrases succeed up to a 1,024-byte request bound; and
- passwords over 1,024 bytes fail before Argon2id work begins.

Define this small shared API in `internal/auth/identity.go`:

```go
const MinPasswordCharacters = 15
const MaxPasswordBytes = 1024

func NormalizeEmail(value string) string
func ValidateEmail(value string) error
func ValidatePassword(value string) error
```

Use `strings.TrimSpace`, `strings.ToLower`, `net/mail.ParseAddress`, and
`utf8.RuneCountInString`. Accept only a bare parsed address, not a display-name form. Do not add
password composition rules.

Run:

```sh
go test ./internal/auth -run 'NormalizeEmail|ValidatePassword' -count=1
```

Expected: FAIL because the new API does not exist.

- [x] **Step 2: Write failing PostgreSQL user tests**

Add tests proving:

- `CreateUser` permits two distinct accounts;
- mixed-case/whitespace variants collide after normalization;
- two concurrent attempts for the same normalized address create exactly one row;
- `UserByEmail` normalizes its input;
- `UserByID` finds one exact positive user;
- `UserIDs` returns every positive ID in ascending order; and
- `CreateOwnerUser` still rejects a database that already contains any account.

The target storage API is:

```go
var ErrEmailAlreadyExists = errors.New("storage: email already exists")

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error)
func (s *Store) UserByID(ctx context.Context, userID int64) (User, bool, error)
func (s *Store) UserIDs(ctx context.Context) ([]int64, error)
```

Run:

```sh
test -n "${JOBCRON_TEST_POSTGRES_URL:-}"
go test ./internal/storage -run 'CreateUser|UserByEmail|UserByID|UserIDs|CreateOwnerUser' \
  -count=1
```

Expected: FAIL on missing methods and normalization behavior.

- [x] **Step 3: Add the canonical-email migration**

In migration `0018`:

1. normalize existing addresses with `lower(btrim(email))`;
2. add a named check constraint requiring a non-empty canonical stored address; and
3. retain the existing `UNIQUE(email)` constraint as the final concurrency guard.

Do not add a role column. The migration must fail rather than silently merge rows if a historical
database contains two addresses that collapse to the same canonical value.

- [x] **Step 4: Implement the general storage methods**

Normalize inside storage even when the HTTP/CLI caller already normalized. Map PostgreSQL unique
violation `23505` for the users email constraint to `ErrEmailAlreadyExists` with
`errors.As(..., *pgconn.PgError)`. Keep `CreateOwnerUser` serializable and table-locked, but share
its insert helper with `CreateUser`.

Remove the phantom `User.Role`, `ownerRole`, and `scanOwnerUser`; replace the scanner with
`scanUser`. The product has no roles, so storage must not manufacture one.

- [x] **Step 5: Run narrow and package verification**

```sh
go test ./internal/auth -count=1
test -n "${JOBCRON_TEST_POSTGRES_URL:-}"
go test ./internal/storage -count=1
```

Expected: PASS, including the PostgreSQL migration and concurrency tests.

- [x] **Step 6: Commit**

```sh
git add internal/auth/identity.go internal/auth/identity_test.go \
  internal/storage/postgres_migrations/0018_multi_user_accounts.sql \
  internal/storage/users.go internal/storage/users_test.go \
  internal/storage/postgres_integration_test.go
git commit -m "feat(auth): add canonical multi-user accounts"
```

### Task 2: Transactional password, session, deletion, and operator primitives

**Files:**

- Modify: `internal/storage/users.go`
- Modify: `internal/storage/sessions.go`
- Create: `internal/storage/account_lifecycle_test.go`
- Modify: `cmd/jobcron-user/main.go`
- Modify: `cmd/jobcron-user/main_test.go`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`

- [x] **Step 1: Write failing account-lifecycle storage tests**

Cover these exact operations:

```go
func (s *Store) ChangePassword(
    ctx context.Context,
    userID int64,
    passwordHash string,
    keepSessionHash string,
) error

func (s *Store) ResetUserPassword(
    ctx context.Context,
    email string,
    passwordHash string,
) (User, error)

func (s *Store) DeleteUser(ctx context.Context, userID int64) (bool, error)
```

Prove `ChangePassword` updates the hash and deletes every session except the current hashed token.
Prove `ResetUserPassword` normalizes the email and revokes every session. Prove `DeleteUser`
returns false for a missing ID and cascades sessions, profile, bookmarks, hidden jobs, scores,
AI scores, AI usage, credentials, imports, and dealbreaker validations while preserving postings,
global `ai_extractions`, and `scrape_runs`.

Run the targeted PostgreSQL tests. Expected: FAIL before implementation.

- [x] **Step 2: Implement each operation as one transaction**

For password mutation, update the user row and revoke sessions in the same transaction. Callers
pass only the SHA-256 session-token hash, never the raw bearer token. For deletion, rely on the
already verified `ON DELETE CASCADE` foreign keys and delete only the exact `users.id` row.

- [x] **Step 3: Generalize the operator CLI**

Keep `create-owner` as the one-time first-account bootstrap. Change `reset-password` to target one
normalized email regardless of user count and revoke all of that user's sessions. Add:

```text
jobcron-user delete-user \
  --database-url "$DATABASE_URL" \
  --email "$USER_EMAIL" \
  --confirm-email "$USER_EMAIL"
```

Use `JOBCRON_USER_PASSWORD` for `reset-password`; keep `JOBCRON_OWNER_PASSWORD` for
`create-owner`. Apply the shared email and password validators to both commands. Do not print
password hashes or database URLs. Refuse deletion unless `--confirm-email` normalizes to exactly
the selected address.

- [x] **Step 4: Test the real CLI contract**

Test missing flags, mismatched confirmation, normalized selection, missing users, reset session
revocation, exact deletion, and output that includes only the normalized email and user ID.

Run:

```sh
go test ./cmd/jobcron-user -count=1
test -n "${JOBCRON_TEST_POSTGRES_URL:-}"
go test ./internal/storage ./cmd/jobcron-user -count=1
```

Expected: PASS.

- [x] **Step 5: Update only the operator command examples**

Document the generalized reset and delete commands with placeholders. State that reset revokes all
sessions and deletion cascades private state. Do not add real tunneled URLs or account addresses.

- [x] **Step 6: Commit**

```sh
git add internal/storage/users.go internal/storage/sessions.go \
  internal/storage/account_lifecycle_test.go cmd/jobcron-user/main.go \
  cmd/jobcron-user/main_test.go deploy/production/HUMAN_DEPLOY_GUIDE.md
git commit -m "feat(accounts): add operator recovery and deletion"
```

### Task 3: Runtime configuration and production startup without a sole owner

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/jobcron/main.go`
- Modify: `cmd/jobcron/main_test.go`
- Modify: `internal/server/server.go`
- Modify: `deploy/production/compose.yaml`
- Modify: `deploy/production/compose_test.go`

- [x] **Step 1: Write failing configuration tests**

Add fields:

```go
SignupAccessCode   string
Stage1SponsorUserID int64
```

Load them from `JOBCRON_SIGNUP_ACCESS_CODE` and `JOBCRON_STAGE1_SPONSOR_USER_ID`. The sponsor ID
is optional but, when present, must parse as a positive base-10 integer. The signup code is optional
at process start because an unset value closes signup rather than crashing the app. Change the
application default `JOBCRON_DAILY_SCRAPE_TIME` from `08:00` to `05:00`.

Expected failing command:

```sh
go test ./internal/config -run 'Signup|Sponsor|DailyScrape' -count=1
```

- [x] **Step 2: Write failing startup tests**

Prove:

- production can open an empty or multi-user PostgreSQL database;
- production returns `UserID == 0` and never calls `SoleOwnerUserID`;
- non-production explicit PostgreSQL keeps its exact-one-local-user contract; and
- managed local startup still uses `EnsureManagedLocalOwner`.

Delete `SoleOwnerUserID` from `runtimeUserStore`. In `resolvePostgresRuntime`, branch on production
before resolving a no-login local user.

- [x] **Step 3: Wire server configuration**

Add immutable startup setters:

```go
func (s *Server) SetSignupAccessCode(code string)
func (s *Server) SetStage1SponsorUserID(userID int64)
```

Call both from `cmd/jobcron/main.go` before serving or starting the scheduler. Do not log either
value. Preserve `NewForLocalUser` only for non-production no-login operation.

- [x] **Step 4: Pass the new variables through production Compose**

Add both variable names to the app service environment allowlist. Preserve unset values as unset;
do not add defaults or sample secrets to tracked files. Extend the rendered Compose contract tests.

- [x] **Step 5: Verify and commit**

```sh
go test ./internal/config ./cmd/jobcron ./deploy/production -count=1
git add internal/config/config.go internal/config/config_test.go cmd/jobcron/main.go \
  cmd/jobcron/main_test.go internal/server/server.go deploy/production/compose.yaml \
  deploy/production/compose_test.go
git commit -m "feat(runtime): configure cohort signup and stage1 sponsor"
```

### Task 4: Access-code-gated signup and automatic login

**Files:**

- Create: `internal/server/signup.go`
- Create: `internal/server/signup_test.go`
- Modify: `internal/server/auth.go`
- Modify: `internal/server/auth_test.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/rate_limit.go`
- Modify: `internal/server/rate_limit_test.go`
- Create: `web/signup.html`
- Modify: `web/login.html`
- Modify: `web/styles.css`

- [x] **Step 1: Write failing public-route and closed-gate tests**

Register `GET /signup` and `POST /signup` as public auth routes, while retaining CSRF protection on
POST. Prove an unset access code renders a closed cohort notice and every POST creates neither a
user nor a session. Prove an incorrect code does the same.

- [x] **Step 2: Write failing successful and invalid signup tests**

The form fields are `email`, `password`, `password_confirm`, and `access_code`. Test canonical
email storage, server-side email syntax validation, the 15-character policy, confirmation
mismatch, malformed form input, successful Argon2id hashing, opaque session creation, secure
cookie flags, and a `303 /profile` redirect.

Extract the existing login token/cookie creation into one unexported helper and reuse it:

```go
func (s *Server) startSession(w http.ResponseWriter, ctx context.Context, userID int64) error
```

- [x] **Step 3: Reuse the current limiter for a separate signup budget**

Add a second `*loginRateLimiter` instance rather than a new limiter abstraction. Key it by the
trusted client-address helper plus normalized email. Keep login and signup counters independent so
one flow cannot consume the other's allowance. Assert the sixth attempt inside the existing
15-minute window returns `429` before Argon2id hashing.

- [x] **Step 4: Implement constant-time access-code comparison**

Hash the configured and submitted strings with SHA-256, then compare the fixed-size digests with
`subtle.ConstantTimeCompare`. Never place the code in a URL, log line, template data, database row,
or error body.

- [x] **Step 5: Handle duplicates without account-existence wording**

On `ErrEmailAlreadyExists`, return the same generic signup failure status and body used for other
unprocessable account-creation failures. Do not render “already registered,” the stored address,
or any database error. Test the response body and logs. Successful creation still auto-logs in as
required; the cohort gate is the current abuse boundary, not full public-signup anonymity.

- [x] **Step 6: Build the signup UI**

Reuse the login page's visual language. State plainly that email ownership is not verified and
forgotten-password recovery requires contacting the operator. Link login and signup in both
directions. Keep the layout usable at 320 CSS pixels without adding a frontend dependency.

- [x] **Step 7: Verify and commit**

```sh
go test ./internal/server -run 'Signup|PublicAuth|RateLimit|Session' -count=1
go test ./internal/server -count=1
git add internal/server/signup.go internal/server/signup_test.go internal/server/auth.go \
  internal/server/auth_test.go internal/server/handlers.go internal/server/rate_limit.go \
  internal/server/rate_limit_test.go web/signup.html web/login.html web/styles.css
git commit -m "feat(auth): add cohort self-service signup"
```

### Task 5: Signed-in password change and self-service account deletion

**Files:**

- Create: `internal/server/account.go`
- Create: `internal/server/account_test.go`
- Modify: `internal/server/handlers.go`
- Create: `web/account.html`
- Modify: `web/_nav.html`
- Modify: `web/styles.css`

- [x] **Step 1: Write failing password-change handler tests**

Register `GET /account` and `POST /account/password`. Require the authenticated current password,
new password, and confirmation. Test wrong-current-password, password-policy failure, mismatch,
CSRF rejection, successful Argon2id replacement, preservation of the current session, revocation
of other sessions, and no effect on another user.

- [x] **Step 2: Write failing account-deletion handler tests**

Register `POST /account/delete`. Require the current password and a `confirm_email` value that
normalizes to the signed-in user's email. Test wrong password, mismatched confirmation, CSRF,
foreign-user isolation, cascade results, preserved global rows, expired browser cookie, and a
`303 /login` redirect.

- [x] **Step 3: Implement the handlers without a service layer**

Keep orchestration in `account.go`: load the session user, verify the current Argon2id hash, call
the transactional storage method, and rotate/expire cookies. There is no second consumer that
justifies an account service abstraction.

- [x] **Step 4: Build the account UI**

Add an Account navigation link. Separate password change from a clearly labelled destructive
section. Explain that deletion is permanent and removes saved state and stored provider keys.
Require typing the account email; do not rely only on JavaScript confirmation.

- [x] **Step 5: Verify and commit**

```sh
go test ./internal/server -run 'Account|PasswordChange|DeleteUser|SessionRevocation' -count=1
go test ./internal/server -count=1
git add internal/server/account.go internal/server/account_test.go internal/server/handlers.go \
  web/account.html web/_nav.html web/styles.css
git commit -m "feat(accounts): add password change and self deletion"
```

### Task 6: Make Stage-1 cache identity provider-independent

**Files:**

- Modify: `internal/ai/version.go`
- Modify: `internal/ai/version_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/ai_runtime_test.go`
- Modify: `internal/server/ai_config_test.go`
- Modify: `internal/server/ai_scrape_test.go`
- Modify: `internal/server/ai_rerate_test.go`
- Modify: `internal/server/live_dealbreakers_test.go`

- [x] **Step 1: Write failing cache-identity tests**

Replace `EligibilityVersion(provider, model)` with:

```go
func ExtractionContractVersion() string
```

The returned value depends only on the Stage-1 fact contract, output schema, and validation rules.
It must not derive from provider, model, base URL, transport, output-token settings, response mode,
or non-semantic prompt-copy versions. Tests must prove those changes do not partition the cache,
while an explicit contract-version bump does. Keep `DealbreakerVersion` and `ScoreVersion`
provider/model-specific because those outputs are user-scoped judgments.

- [x] **Step 2: Remove the version from `AIRuntime`**

Delete `AIRuntime.EligibilityVersion`. Every global extraction lookup/upsert calls
`ai.ExtractionContractVersion()` directly. Continue storing it in the existing `ai_version`
column; do not add or rename a database column.

- [x] **Step 3: Lock cache behavior with tests**

Prove the key is exactly `(posting_id, content_hash, extraction_contract_version)`: same content
hits after provider, model, transport, response-setting, and non-semantic prompt-copy changes;
changed content misses; contract-version change misses; and provider errors or malformed output
write no row.

- [x] **Step 4: Verify and commit**

```sh
go test ./internal/ai ./internal/server -run 'Version|Extraction|Cache' -count=1
go test ./internal/ai ./internal/server -count=1
git add internal/ai/version.go internal/ai/version_test.go internal/server/server.go \
  internal/server/ai_runtime_test.go internal/server/ai_config_test.go \
  internal/server/ai_scrape_test.go internal/server/ai_rerate_test.go \
  internal/server/live_dealbreakers_test.go
git commit -m "refactor(ai): make stage1 cache globally reusable"
```

### Task 7: Route every Stage-1 miss to the configured sponsor

**Files:**

- Modify: `internal/server/server.go`
- Modify: `internal/server/rerate.go`
- Create: `internal/server/stage1_sponsor_test.go`
- Modify: `internal/server/ai_scrape_test.go`
- Modify: `internal/server/ai_rerate_test.go`
- Modify: `internal/server/production_user_scope_test.go`

- [x] **Step 1: Write the payer matrix as failing tests**

For scheduled scrapes, interactive scrapes, rerates, backfills, and retries, assert:

- one Stage-1 miss calls only the sponsor provider and debits only sponsor `ai_usage`;
- a cache hit calls no provider and debits no user;
- the triggering user's key is never used for Stage-1;
- missing, zero, unknown, deleted, unconfigured, undecryptable, rejected, and exhausted sponsors
  produce deterministic fallback without charging another user; and
- Stage-1B/dealbreaker and Stage-2 calls still debit only the current analyzed user.

- [x] **Step 2: Add one cohesive funding value**

Use this internal shape instead of passing three loosely coupled arguments:

```go
type stage1Funding struct {
    userID  int64
    runtime *AIRuntime
    budget  *aiBudget
}

func (s *Server) resolveStage1Funding(ctx context.Context) (*stage1Funding, error)
```

Resolve only `s.stage1SponsorUserID`, verify the user exists, construct that user's runtime, and
create that user's budget. Never fall back to `firstUserID`, `SoleOwnerUserID`, or the triggering
user.

- [x] **Step 3: Change `extractStage1` to accept sponsor funding**

Its order is:

1. build model input and content hash;
2. check the global extraction cache;
3. if it misses, require valid sponsor funding and budget headroom;
4. call the sponsor provider;
5. debit the sponsor ledger; and
6. persist only a validated extraction.

Checking the cache first ensures existing global facts remain usable when sponsorship is
temporarily unavailable.

- [x] **Step 4: Separate rerate budgets**

In `runRerate`, use sponsor funding only for the Stage-1 backfill loop. Create the signed-in user's
existing budget and call cap separately for contextual validation and Stage-2. Do not combine the
ledgers or allow sponsor exhaustion to disable user-funded Stage-1B/Stage-2.

- [x] **Step 5: Bound failure reporting**

Return or log a stable sponsor-unavailable category plus user ID, never the provider response body,
key, email, or ciphertext. Limit persisted/logged summaries to the existing 500-character scrape
history bound.

- [x] **Step 6: Verify and commit**

```sh
go test ./internal/server -run 'Stage1Sponsor|Sponsor|Rerate.*Stage1|AIUsage' -count=1
go test ./internal/server -count=1
git add internal/server/server.go internal/server/rerate.go \
  internal/server/stage1_sponsor_test.go internal/server/ai_scrape_test.go \
  internal/server/ai_rerate_test.go internal/server/production_user_scope_test.go
git commit -m "feat(ai): bill global stage1 misses to sponsor"
```

### Task 8: Split global collection from sequential per-user analysis

**Status:** Corrected candidate `e638d5d` passed fresh external exact-range review with no
findings and was integrated locally as the patch-equivalent commit `8683bac`.

**Files:**

- Modify: `internal/server/server.go`
- Modify: `internal/server/scheduler.go`
- Modify: `internal/server/scheduler_test.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/unscored_test.go`
- Modify: `cmd/jobcron/main.go`
- Modify: `cmd/jobcron/main_test.go`

- [x] **Step 1: Write failing two-user scheduler tests**

Seed two users plus one user with no profile. Assert one scheduled invocation:

- fetches each registered source once;
- attempts each Stage-1 cache miss once through sponsor funding;
- analyzes profiled users in ascending ID order;
- skips the missing profile;
- filters paid per-user work to that profile's enabled sources;
- writes separate scores, contextual validations, deltas, and usage rows;
- continues after one user's bad credential; and
- records a bounded warning summary without marking a successful global fetch as failed.

- [x] **Step 2: Extract a global collection phase**

Introduce one internal result value:

```go
type scrapeCollection struct {
    result   ScrapeResult
    detailed []scraper.Posting
    now      time.Time
}
```

Move robots checks, listing/detail fetch, upsert, sponsor-funded Stage-1 extraction, sweeping, and
cross-portal deduplication into `collectPostings`. Accept a source predicate: interactive scrapes
use the signed-in profile's `SourceEnabled`; scheduled scrapes pass all registered sources.

- [x] **Step 3: Extract a user analysis phase**

Move contextual dealbreaker validation, deterministic `scoreAll`, visible-posting selection, and
Stage-2 auto-rate into `analyzeUserCollection`. It receives one explicit `userID`, that user's
runtime/budget, and the shared collection. It must not fetch a source or populate Stage-1.

- [x] **Step 4: Rebuild interactive and scheduled orchestration**

Interactive scrape remains one user's flow: collect once, then analyze that user. Scheduled scrape
collects all sources once, obtains `UserIDs`, and analyzes each profiled user sequentially while
holding the existing `scrapeAllKey` lock. Sum successful score writes in scheduled history; isolate
and summarize per-user failures.

Replace `RescoreSoleOwner` with:

```go
func (s *Server) RescoreUsers(ctx context.Context) (int, error)
```

Production startup loops all profiled users and performs cache-only/rule-based recovery. Local
SQLite/no-login mode retains its single local-user behavior.

- [x] **Step 5: Verify the clock and direct workflow**

Keep scheduler clock tests deterministic and assert `05:00` KST. Invoke `runScheduledScrape`
directly; never wait for wall-clock time in tests.

Run:

```sh
go test ./internal/server -run 'Scheduler|Scheduled|RescoreUsers|RunScrape' -count=1
go test ./cmd/jobcron ./internal/server -count=1
```

Expected: PASS with one global collection and sequential isolated analysis.

- [x] **Step 6: Commit Wave A runtime**

```sh
git add internal/server/server.go internal/server/scheduler.go \
  internal/server/scheduler_test.go internal/server/server_test.go \
  internal/server/unscored_test.go cmd/jobcron/main.go cmd/jobcron/main_test.go
git commit -m "feat(scheduler): score all users from one daily scrape"
```

### Task 9: Verify Wave A end to end before provider expansion

**Status:** Candidate `65b1b78` passed fresh external exact-range review with no findings and was
integrated locally as the patch-equivalent commit `7cc5460`.

**Files:**

- Modify: `internal/server/production_user_scope_test.go`
- Modify: `scripts/preview_interactive_test.go`
- Modify as needed for defects found in Wave A only

- [x] **Step 1: Add the complete two-account HTTP isolation test**

Through real HTTP handlers and real PostgreSQL sessions, create two accounts and prove they cannot
read or mutate one another's profile, bookmarks, hidden jobs, scores, AI usage, contextual
validations, or credentials. Include password change and deletion.

- [x] **Step 2: Run Wave A automated verification**

```sh
go fmt ./...
go vet ./...
go test ./... -count=1
test -n "${JOBCRON_TEST_POSTGRES_URL:-}"
go test ./... -count=1
go test -race ./internal/auth ./internal/server ./internal/storage -count=1
```

Expected: all PASS. Stop and fix Wave A before starting providers.

- [x] **Step 3: Run a production-auth browser pass**

Use a disposable PostgreSQL database and generated test-only session/encryption secrets. Start the
app with `--no-open` on loopback. Use `frontend-qa` and gstack `/browse` with one browser worker to
walk signup, login, profile setup, password change, logout, second-account isolation, and account
deletion at desktop and mobile widths. Verify no console errors and leave the preview available for
human inspection until the implementation report is delivered.

- [x] **Step 4: Commit only fixes or added regression tests**

```sh
git add internal/server/production_user_scope_test.go \
  scripts/preview_interactive_test.go
git commit -m "test(accounts): verify cohort flow end to end"
```

If no tracked fixes/tests were needed, do not create an empty commit.

---

## Wave B — OpenAI And Gemini Provider Expansion

### Task 10: Add one OpenAI-compatible HTTP adapter for OpenAI and Gemini

**Status:** Corrected two-commit candidate `a2e5c67` passed final external exact-range rereview
with no findings and was integrated locally as patch-equivalent commits `a62b6c3` and `c1b6560`.

**Files:**

- Create: `internal/ai/openai_compatible.go`
- Create: `internal/ai/openai_compatible_test.go`
- Modify: `internal/ai/client.go`
- Modify: `internal/ai/client_test.go`
- Modify: `internal/ai/provider.go`
- Modify: `internal/ai/provider_test.go`
- Modify: `internal/ai/integration_test.go`

- [x] **Step 1: Write failing offline contract tests**

For both providers, use `httptest.Server` and assert:

- POST path and pinned host;
- bearer authentication;
- system and user messages;
- JSON-only response request;
- `max_completion_tokens`;
- assistant text extraction;
- prompt/completion token normalization into `ai.Usage`;
- malformed envelope handling;
- bounded non-2xx `APIError`; and
- pacing cancellation.

- [x] **Step 2: Implement the shared wire shape**

In `openai_compatible.go`, define the Chat Completions request/response structs and one constructor:

```go
func newChatCompletionsSpec(name, baseURL string) providerSpec
```

Register:

- OpenAI at `https://api.openai.com` plus `/v1/chat/completions`; and
- Gemini at `https://generativelanguage.googleapis.com/v1beta/openai` plus
  `/chat/completions`.

Both use `Authorization: Bearer`. Keep all provider-specific JSON below `ai.Provider`. Do not add a
generic provider framework beyond this one shared wire format.

- [x] **Step 3: Add the smallest current model registry**

At implementation time, re-check the official model pages before editing because identifiers are
volatile. Pin one inexpensive default initially:

- OpenAI: `gpt-5.6-luna`;
- Gemini: `gemini-3.5-flash-lite`.

Keep the existing Anthropic list. Add more models only when a real user choice is justified; one
working default per new provider satisfies this milestone better than a stale catalog.

- [x] **Step 4: Keep live tests opt-in**

Extend the live contract test to run only when `JOBCRON_TEST_OPENAI_API_KEY` or
`JOBCRON_TEST_GEMINI_API_KEY` is present. Assert structured extraction and non-zero usage without
printing prompts, responses, or credentials. Ordinary `go test ./...` must make no paid calls.

- [x] **Step 5: Verify and commit**

```sh
go test ./internal/ai -run 'OpenAI|Gemini|Provider|Models|HTTP' -count=1
go test ./internal/ai -count=1
git add internal/ai/openai_compatible.go internal/ai/openai_compatible_test.go \
  internal/ai/client.go internal/ai/client_test.go internal/ai/provider.go \
  internal/ai/provider_test.go internal/ai/integration_test.go
git commit -m "feat(ai): add OpenAI and Gemini providers"
```

### Task 11: Complete three-provider credential management and UI

**Status:** Complete at reviewed tip `91f865b`; the fresh final rereview approved the cumulative
three-commit range with no Critical, Important, or Minor findings.

**Files:**

- Modify: `internal/credential/cipher.go`
- Modify: `internal/credential/cipher_test.go`
- Modify: `internal/profile/profile.go`
- Modify: `internal/profile/profile_test.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/production_user_scope_test.go`
- Modify: `internal/server/ai_provider_switch_test.go`
- Modify: `web/profile.html`
- Modify: `web/ai-model-select.js`
- Modify: `web/styles.css`

- [x] **Step 1: Generalize provider validation and form data**

Accept only `anthropic`, `openai`, and `gemini` after normalization. Replace hard-coded template
options with the server-provided `ai.Providers()` list and display labels. Validate that the chosen
model belongs to `ai.ModelsForProvider(provider)` before saving.

- [x] **Step 2: Preserve one encrypted key per user/provider**

Test that switching providers with a blank key preserves each existing provider row, submitting a
new key replaces only the selected row, and user A cannot read/change user B's three rows. Keep the
envelope associated data `(userID, provider)` unchanged.

- [x] **Step 3: Add explicit key deletion**

Register `POST /profile/ai-key/delete`. Require the selected provider plus an explicit confirmation
field. Delete only the authenticated user's matching provider row. Leave the profile selection in
place so the UI clearly shows “no key saved” and AI safely remains unavailable until
replacement.

- [x] **Step 4: Fix hosted credential copy**

Replace the obsolete local `0600` file statement with accurate copy: keys are encrypted before
storage in PostgreSQL and are never rendered back. Add provider-specific key placeholders only as
hints; never validate secrets by prefix.

- [x] **Step 5: Verify provider switching and failures**

Cover 400/404 model mismatch, 401/403 invalid key, 429 quota/rate limiting, malformed provider
output, blank-key preservation, replacement, deletion confirmation, and cross-user isolation.

Run:

```sh
go test ./internal/credential ./internal/profile ./internal/server \
  -run 'Provider|Credential|AIKey|Profile' -count=1
go test ./internal/credential ./internal/profile ./internal/server -count=1
```

- [x] **Step 6: Commit**

```sh
git add internal/credential/cipher.go internal/credential/cipher_test.go \
  internal/profile/profile.go internal/profile/profile_test.go internal/server/handlers.go \
  internal/server/production_user_scope_test.go internal/server/ai_provider_switch_test.go \
  web/profile.html web/ai-model-select.js web/styles.css
git commit -m "feat(profile): manage three BYOK providers"
```

### Task 12: Documentation, production interface, and final verification

**Status:** Complete. Final reviewed implementation tip:
`0d2a713e4eca92dd5cdf095e4695dc55f30829d8`; independently approved with no
Critical, Important, or Minor findings.

**Files:**

- Modify: `README.md`
- Modify: `README.ko.md`
- Modify: `docs/architecture.md`
- Modify: `deploy/production/README.md`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- Modify:
  `docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md`
- Modify: `docs/superpowers/README.md`
- Move on completion: `docs/superpowers/specs/260715-multi-user-account-expansion.md`
- Move on completion: `docs/superpowers/plans/260722-multi-user-account-expansion.md`

- [x] **Step 1: Update durable architecture**

Document:

- account/session/cascade boundaries;
- canonical email and password policy;
- one global collection plus sequential user analysis;
- global Stage-1 versus user-scoped Stage-1B/Stage-2 data;
- sponsor failure behavior;
- three provider adapters and encrypted credential rows; and
- the 05:00 KST scheduler.

Remove owner-only and Anthropic-only claims that are no longer true.

- [x] **Step 2: Update the runtime-secret interface**

Add placeholder-only documentation for `JOBCRON_SIGNUP_ACCESS_CODE` and
`JOBCRON_STAGE1_SPONSOR_USER_ID`. Explain that the sponsor ID is billing assignment, not a role.
Update the Terraform spec's application-secret contract, but do not write Terraform or expose a
real ID/value.

Do not add, replace, or recapture root README screenshots during this documentation pass.

- [x] **Step 3: Document cohort limitations**

State that the access-code cohort does not verify email ownership and has operator-assisted
recovery. Link the spec's “Truly Public Signup Follow-Up”; do not imply public signup is
complete.

- [x] **Step 4: Run the complete automated gate**

```sh
go fmt ./...
go vet ./...
go test ./... -count=1
test -n "${JOBCRON_TEST_POSTGRES_URL:-}"
go test ./... -count=1
go test -race ./internal/auth ./internal/ai ./internal/server ./internal/storage -count=1
```

If provider test keys are present, run each opt-in live contract test individually and report the
provider/model used. If absent, report “not run: credential unavailable”; do not invent success.

- [x] **Step 5: Run Tier C real-browser verification**

Start or reuse one disposable no-open production-auth preview. With at most two browser workers,
walk every page at desktop and mobile widths, light and dark themes when available, and verify no
console errors. Exercise signup, login, profile setup, each provider selection, key replacement,
key deletion, scrape, rerate, bookmarks, hidden jobs, password change, logout, cross-account
isolation, and account deletion. Click real posting destinations and verify the destination content
matches the source record rather than only checking HTTP status.

- [x] **Step 6: Review the complete public diff and scan secrets**

```sh
git diff --check
git diff --stat HEAD
git diff HEAD
gitleaks git --redact --no-banner
```

Expected: no whitespace errors, no unreviewed changes, and no real secrets/personal data. Manually
review the complete diff even when Gitleaks passes.

- [x] **Step 7: Archive completed planning material and update the index**

Move the completed spec and this plan into:

```text
docs/superpowers/archive/2026-07-22-multi-user-account-expansion/
```

Update `docs/superpowers/README.md` so neither remains under Active Work and both are linked under
Recently Archived. Preserve the public-signup follow-up in the archived spec; create a new active
spec later only when that milestone is approved.

- [x] **Step 8: Commit documentation and lifecycle changes**

```sh
git add README.md README.ko.md docs/architecture.md deploy/production/README.md \
  deploy/production/HUMAN_DEPLOY_GUIDE.md \
  docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md \
  docs/superpowers/README.md docs/superpowers/archive/2026-07-22-multi-user-account-expansion
git commit -m "docs: publish multi-user architecture and operations"
```

- [x] **Step 9: Verify final repository state**

```sh
git status --short --branch
git log --oneline --decorate -12
```

Expected: a clean local branch ahead of its remote, with no push performed. Report commit hashes,
automated checks, live-test omissions, browser routes/viewports, preview URL, and any independent
implementation decisions.

## Dependency And Review Order

1. Tasks 1-3 establish storage and runtime contracts.
2. Tasks 4-5 ship the cohort-facing account lifecycle.
3. Tasks 6-8 change cache billing and scheduled orchestration.
4. Task 9 is the hard Wave A gate. Do not start provider expansion while it is red.
5. Tasks 10-11 add providers without changing account/scheduler semantics.
6. Task 12 updates durable documentation and executes the final release-quality gate.

The first cohort can launch after Wave A plus deployment approval, using Anthropic only. Wave B is
a separately reviewable capability expansion, not a prerequisite for validating multi-user safety.
