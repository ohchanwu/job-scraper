# Architecture

Jobcron is a single Go web application that scrapes Korean job boards, stores normalized
postings in PostgreSQL, scores them for an authenticated or fixed local user, and serves an
embedded HTML interface.
Optional AI calls extract global eligibility facts, validate user-specific dealbreaker hits in
context, and enrich score explanations. The deterministic scoring path remains complete when AI
is disabled or unavailable.

This document describes the implemented architecture as of 2026-07-23. Approved future work is
listed separately so it is not mistaken for current behavior.

## System at a glance

```text
Browser
   |
   | HTTPS through Caddy in production; direct HTTP in local development
   v
jobcron process
   |-- net/http routes -> embedded templates, CSS, and JavaScript
   |-- in-process daily scheduler
   |-- scraper adapters -> public job-board endpoints
   |-- deterministic scoring -> optional Anthropic calls
   `-- storage repositories -> PostgreSQL
```

The production process is intentionally small. There is no separate frontend server, worker
queue, scheduler service, or AI service. Scrapes and rerates run inside the application process,
and a process-local singleflight lock prevents overlapping operations.

For user-facing behavior and local startup, start with the [project README](../README.md).

## Runtime modes

### Production

Production requires an explicit `DATABASE_URL`, a session secret, and a credential-encryption
master key. Cookie-session authentication and CSRF protection cover the writable HTTP surface.
The prepared deployment makes Caddy the only public listener and proxies requests to the private
application container backed by private Amazon RDS. The first production rollout is still pending.

Production startup opens PostgreSQL without resolving a sole owner. Authenticated requests resolve
their user from the session, so both empty and multi-user databases can start successfully.
Startup also skips the legacy sole-owner rescore pass.
Production and demo modes are mutually exclusive; configuration loading rejects their combination
whether demo mode comes from the environment or the command line.

`JOBCRON_SIGNUP_ACCESS_CODE` and `JOBCRON_STAGE1_SPONSOR_USER_ID` are optional. A configured
sponsor ID must be a positive base-10 integer. Startup loads both settings and wires them into the
server, while the production Compose file passes them through without defaults. The access code
opens cohort signup; leaving it unset keeps the signup page visible but closes account creation.
Sponsor-funded AI work remains part of later scheduler tasks.

The daily scheduler is enabled by configuration and runs inside the application process at
`JOBCRON_DAILY_SCRAPE_TIME` in Asia/Seoul (`05:00` by default). The current scheduled scrape
still resolves exactly one owner and records a skipped run when the owner is missing or ambiguous.

See the [production deployment reference](../deploy/production/README.md), the
[human rollout guide](../deploy/production/HUMAN_DEPLOY_GUIDE.md), and the
[RDS decision](superpowers/decisions/260710-rds-production-settings.md).

### Managed local app

When a non-production launch has no `DATABASE_URL`, `cmd/jobcron` starts or reuses the repository's
PostgreSQL 18 Compose service. It creates or reuses one fixed local owner and stores the database
cluster in a named Docker volume. The local HTTP surface uses that owner directly instead of
requiring login.

The app creates one protected local credential-encryption key on first use. PostgreSQL stores the
encrypted provider credential; the local key file stores only the master key used to decrypt it.

See the [local PostgreSQL guide](../deploy/local/README.md).

### Explicit local database

Setting `DATABASE_URL` outside production bypasses managed Compose startup. The target database
must already contain exactly one user. This mode is useful for development against an existing
PostgreSQL instance and for controlled migration verification.

### Read-only demo

Demo mode reuses the application and embedded frontend but rejects database mutations. Visitor
bookmark and hidden state stays in browser storage. An administrator token may authorize a demo
scrape, but ordinary visitors cannot scrape or rerate. The tracked demo deployment is
non-production and opens its uploaded SQLite snapshot only through the explicit
`--demo --db <path>` compatibility path; ordinary local startup does not use SQLite.

See the [demo deployment reference](../deploy/demo/README.md).

## Process and package boundaries

- `cmd/jobcron` loads configuration, resolves the database and, outside production, the fixed
  no-login user, opens storage, wires scrapers, installs the credential cipher, heals interrupted
  scoring where applicable, starts the scheduler, and serves HTTP.
- `internal/server` owns routes, authentication middleware, scrape and rerate orchestration,
  rendering, Server-Sent Events, budgets, and the in-process scheduler.
- `internal/scraper` defines the normalized posting contract and shared robots, pacing, and
  experience helpers. Source subpackages implement individual job-board adapters.
- `internal/scoring` applies deterministic profile rules and merges cached AI facts and deltas.
- `internal/ai` defines the provider contract, Anthropic client, prompts, response parsing,
  evidence gates, and AI version identity.
- `internal/storage` exposes one concrete repository and applies embedded schema migrations.
  PostgreSQL backs production and ordinary local modes. SQLite entry points exist only for the
  legacy importer, the tracked read-only demo, and compatibility tests.
- `internal/credential` encrypts per-user provider credentials and manages the protected local
  master key.
- `internal/auth` creates password hashes and opaque session tokens.
- `web` embeds templates, styles, scripts, fonts, and icons into the Go binary.

These are concrete package boundaries rather than service boundaries. They run in one process and
communicate through ordinary Go calls.

## HTTP and identity flow

Production requests follow this path:

```text
request -> authentication -> CSRF check for mutations -> handler -> storage -> template or JSON/SSE
```

Login creates a random bearer token for the browser and stores only its SHA-256 hash in the
database. Production cookies are `HttpOnly`, `Secure`, and `SameSite=Lax`. Login failures are
rate-limited by client address and normalized email. Forwarded client addresses are trusted only
when the request carries the configured proxy secret.

`GET /signup` and CSRF-protected `POST /signup` are public authentication routes. A configured
cohort code is compared through fixed-size SHA-256 digests in constant time. Valid requests
canonicalize the email, validate the 15-character password policy, create an Argon2id password
hash, atomically create the account plus its opaque initial session, and redirect to profile setup.
Signup has a separate IP-only rate-limit budget from login, so rotating submitted emails cannot
reset access-code guesses. Unauthenticated form size, limiter state, periodic eviction work, and
concurrent Argon2id hashing are bounded. Invalid codes, invalid account data, and duplicate
addresses share generic failure wording; an unset code fails closed without creating a user or
session.

Authenticated users manage their account at `GET /account`. Password changes require the current
password plus a policy-valid confirmed replacement. The mutation proceeds only while the exact
loaded password hash and unexpired submitting hashed session still belong to that user; the same
transaction writes the new Argon2id hash and revokes every other session. Self-service deletion
requires the current password and canonical email, then applies the same expected-credential and
unexpired-session guard before deleting the exact user row. A stale guard returns the generic
account-validation failure without mutation. Foreign-key cascades remove the user's private state
and provider credentials. The server then expires the browser session cookie and redirects to login.

Every authenticated handler resolves a `userID` before accessing profiles, saved-job state,
scores, AI usage, or credentials. Local mode supplies the fixed local owner's ID through the same
server methods. This keeps storage calls user-scoped while the broader account rollout remains
incomplete.

Sponsor-funded onboarding, password recovery, organizations, and per-user schedules are not
implemented. The
[multi-user expansion follow-up](superpowers/specs/260715-multi-user-account-expansion.md) records
that remaining product work.

## Scrape pipeline

A manual scrape uses a Server-Sent Events connection so the browser can display progress. The
actual work detaches from the request cancellation signal and runs with a bounded background
context. Closing the page therefore does not leave newly inserted postings unscored.

One scrape executes these steps:

1. Resolve the explicit user, profile, and optional AI runtime once.
2. Select profile-enabled scraper adapters.
3. For each source, check access policy, fetch listings, fetch required details, normalize fields,
   and upsert postings.
4. Isolate a failing source and continue with the remaining sources.
5. Sweep stale rows only for sources that completed successfully.
6. Mark cross-portal duplicates and retain one canonical posting.
7. Run or reuse global Stage 1A eligibility extraction for detailed postings.
8. Generate the active user's exact deterministic dealbreaker candidates.
9. Run or reuse user-scoped Stage 1B contextual validation within the shared paid-call budget.
10. Calculate deterministic scores from Stage 1A facts and conservatively merged Stage 1B
    verdicts.
11. Optionally run Stage 2 for the corrected eligible set and merge its cached deltas.
12. Finish the `scrape_runs` record with counts and any bounded error summary.

Scraper clients use shared request pacing and robots-policy helpers. The project prefers stable
HTTP or JSON endpoints and does not use browser automation for production scraping. See the
[source catalog](scraping/source-catalog.md) and the
[no-browser-driven-scraping decision](superpowers/decisions/260606-no-browser-driven-scraping.md).

## Scoring and AI

### Deterministic baseline

`internal/scoring` compares a normalized posting with the user's structured profile. Stack,
career, location, salary, and preference rules contribute explained line items. Hard keyword and
education dealbreakers exclude before any Stage 2 AI adjustment is merged. A career mismatch is a
separate reason; it becomes an exclusion only when the final score remains below `MinScore` after
Stage 2. Contextual validation may suppress an exact keyword hit only when its cited verdict is
`not_applicable`; it does not replace the deterministic candidate matcher.

This path requires no provider and remains the fallback for missing credentials, provider errors,
invalid model output, or exhausted AI budgets.

### Stage 1A: global posting facts

Stage 1A extracts career range, new-grad eligibility, education, and separate career and education
evidence from posting text. These facts describe the posting rather than a user, so the cache is
global and keyed by posting content and `EligibilityVersion`. Invalid or unavailable extraction
falls back to source fields and deterministic parsing.

### Stage 1B: user-scoped dealbreaker context

The deterministic matcher NFC-normalizes and lowercases text, then finds exact contiguous token
sequences from the user's dealbreaker list. Each match becomes a candidate whose stable identity
is the SHA-256 digest of that normalized token sequence.

Stage 1B batches only unresolved candidates and classifies each as `applies`, `not_applicable`, or
`uncertain`. Conclusive evidence must be a bounded verbatim quote from the posting and contain the
same candidate token sequence. The PostgreSQL cache key combines `user_id`, posting, posting
content hash, `DealbreakerVersion`, and normalized keyword hash, so neither users nor changed
inputs can share a verdict accidentally.

A keyword exclusion is suppressed only when every matched candidate is `not_applicable`.
`applies`, `uncertain`, missing cache entries, invalid evidence, unavailable credentials, provider
failures, and exhausted budgets all retain the deterministic exclusion. This conservative fallback
lets AI remove a supported false positive without silently weakening an unresolved hard rule.

Manual and opted-in scheduled scrapes may run Stage 1B before scoring. An explicit `AI 평가`
rerate also evaluates currently excluded candidates before rebuilding the eligible Stage 2 set.
Profile save and startup rescoring are provider-free: they reuse caches and mark missing validation
as pending instead of making surprise paid calls. Stage 1B and Stage 2 share the user's run budget
and call cap.

Scoring persists the exact decision in `ScoreResult.ExclusionReasons` inside
`scores.breakdown_json`. Rendering therefore explains the score that actually caused exclusion
without querying the validation cache or recalculating policy. The complete contract is in the
[Stage 1 contextual validation specification][stage1-context-spec].

### Stage 2: user fit

The second AI layer compares posting text with the user's free-text goals and dislikes. A
citation gate rejects unsupported adjustments. Accepted deltas, usage, and cache entries are
scoped by `user_id`, posting input, profile input, and AI version.

A rerate resolves one user's runtime and operates only on that user's visible rows. The scrape
pipeline may also run Stage 2 automatically for new postings. Per-call, per-run, daily, and monthly
limits bound paid usage; the usage ledger persists across restarts.

### Credential lifecycle

The profile form sends a newly entered provider key to the server. The server encrypts it with
AES-256-GCM before storing it in `user_ai_credentials`. Authenticated encryption binds the
ciphertext to its user, provider, and envelope version, so moving a row to another identity causes
decryption to fail.

The process decrypts a credential only while resolving that user's immutable AI runtime. The raw
key is not returned to the browser or written to logs. Production receives the master key from
configuration; managed local mode keeps it in a protected application-config file.

The completed storage and runtime design, migration contract, and remaining production rollout
gate are documented in the
[PostgreSQL convergence and per-user credential specification][postgres-credential-spec].

## Persistence and ownership

Normal application startup uses PostgreSQL only. `storage.OpenPostgres` checks connectivity and
applies pending embedded migrations transactionally before returning the repository.

The main ownership split is:

- Shared posting facts: postings, canonical-duplicate relationships, global Stage 1A extractions,
  and scrape-run history.
- User-owned state: profiles, deterministic scores, bookmarks, hidden or not-interested state,
  Stage 1B validations, Stage 2 scores, AI usage, and encrypted AI credentials.
- Authentication state: users and hashed login sessions.

Account email identity is stored as trimmed lowercase text. PostgreSQL enforces both that canonical
form and uniqueness, while storage exposes general create and lookup methods without manufacturing
application roles. The deployment-only owner bootstrap remains table-locked and refuses any
database that already contains an account.

Login creates a session only if the user's password hash still equals the exact hash just verified,
while holding the same PostgreSQL user-row lock used by password mutation. A concurrent
self-service password change or operator reset therefore cannot leave a session authenticated by
the old password. PostgreSQL self-service mutations lock the matching user before the matching
session; SQLite compatibility uses one conditional mutation statement. Both paths require the user
ID, expected password hash, and submitting hashed session to remain unexpired at one
transaction-time timestamp.
Self-service changes preserve only the submitting session; operator resets revoke all sessions.

Foreign keys remove dependent user state when its owner is deleted. Composite keys and repository
methods include `user_id` wherever two accounts must not share state. Account settings use these
same exact-user boundaries for password changes and deletion.

Legacy SQLite is not an ordinary writable runtime. The tracked read-only demo may open an uploaded
snapshot through `--demo --db`; all other application startup paths use PostgreSQL.
`cmd/jobcron-import` creates a verified snapshot, checks counts and collisions, and imports
preserved local data into an existing PostgreSQL owner. It inserts all postings before restoring
their self-referencing canonical-duplicate links, so a link may safely point to a posting with a
later ID.
`cmd/jobcron-user` bootstraps the owner and provides operator-side password reset or deletion when
the user cannot sign in. SQLite migrations remain embedded for importer compatibility, the
read-only demo, and tests.

See the [hosted-first storage decision][hosted-first-storage] and the
[local import procedure](../deploy/local/README.md#verified-sqlite-import).

## Rendering and browser behavior

The Go binary embeds all templates and static assets through `embed.FS`. Handlers load a
user-scoped view model, then render it with `html/template`, which escapes untrusted posting text
by default. Small JavaScript modules handle streaming progress, rerate state, bookmarks, source
filters, theme selection, and briefing-notification state.

The browser is not a second database for the writable app. Durable profiles, scores, and saved-job
state live in PostgreSQL so they follow the account across devices. Browser storage is reserved for
presentation preferences and the temporary read-only demo behavior.

Excluded daily and archive rows reuse one template partial that shows every persisted reason in
profile order. The panel includes a visible label, conservative confidence text, and cited evidence
when available. Keyword evidence is split into ordinary strings plus a marked token span; Go
templates escape every segment, and provider output never becomes `template.HTML`. The danger
styling keeps the full row at normal contrast and uses text and a warning symbol in addition to
color.

## Deployment boundaries

The prepared production deployment uses an immutable Linux arm64 container image. Caddy
terminates TLS and is the only public component. The application port and RDS database remain
private. Application state and encrypted AI credentials live in PostgreSQL; production mounts no
application-data or legacy-key volume.

Local development runs the same application and PostgreSQL storage contract. Compose manages only
the local database lifecycle; the application remains a normal host process unless the operator
explicitly chooses another setup.

Stable deployment choices are recorded in the
[production and naming decision](superpowers/decisions/260711-jobcron-production.md). Exact operator
steps belong in the [production guide](../deploy/production/README.md), not in this architecture
overview.

## Failure boundaries

- One source failure does not discard successful results from other sources.
- Stale-row sweeping runs only for sources that supplied a trustworthy fresh baseline.
- AI failure degrades to deterministic scoring rather than failing the scrape or page.
- Stage 1B failure retains the deterministic exclusion with an unverified status; it cannot abort
  scrape, profile save, startup, or provider-free rescoring.
- Missing or ambiguous scheduler ownership records a skipped run instead of selecting a user.
- Startup rescoring repairs postings left without scores after an interrupted prior run.
- Database migration failure prevents startup before the server accepts requests.

## Planned architecture changes

The following work is documented but is not part of the implemented architecture:

- [Multi-user account and scheduler expansion][multi-user-spec]

When this change is implemented, update this document to describe the resulting current state
and move completed implementation records through the documented lifecycle.

## Related documentation

- [Documentation index](README.md)
- [Project overview and local usage](../README.md)
- [Scraper source catalog](scraping/source-catalog.md)
- [Local PostgreSQL operations](../deploy/local/README.md)
- [Production deployment reference](../deploy/production/README.md)
- [Production human rollout guide](../deploy/production/HUMAN_DEPLOY_GUIDE.md)
- [Stable architectural decisions](superpowers/README.md#stable-decisions)

[postgres-credential-spec]:
  superpowers/specs/260714-postgresql-local-convergence-user-ai-credentials.md
[stage1-context-spec]:
  superpowers/archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence.md
[hosted-first-storage]:
  superpowers/decisions/260714-hosted-first-local-database-convergence.md
[multi-user-spec]:
  superpowers/specs/260715-multi-user-account-expansion.md
