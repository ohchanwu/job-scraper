# Architecture

Jobcron is a single Go web application that scrapes Korean job boards, stores normalized
postings in PostgreSQL, scores them for an authenticated or fixed local user, and serves an
embedded HTML interface.
Optional AI calls extract global eligibility facts, validate user-specific dealbreaker hits in
context, and enrich score explanations. The deterministic scoring path remains complete when AI
is disabled or unavailable.

This document describes the implemented architecture as of 2026-07-28. Approved future work is
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
   |-- deterministic scoring -> optional Anthropic, OpenAI, or Gemini calls
   `-- storage repositories -> PostgreSQL
```

The production process is intentionally small. There is no separate frontend server, worker
queue, scheduler service, or AI service. Scrapes and rerates run inside the application process,
and a process-local singleflight lock prevents overlapping operations. Each bounded operation key
retains a one-token gate: release hands ownership to one queued context-aware waiter, and fail-fast
callers cannot barge ahead of that handoff.

For user-facing behavior and local startup, start with the [project README](../README.md).

## Runtime modes

### Production

Production requires an explicit `DATABASE_URL`, a session secret, and a credential-encryption
master key. Cookie-session authentication and CSRF protection cover the writable HTTP surface.
The prepared deployment makes Caddy the only public listener and proxies requests to the private
application container backed by private Amazon RDS. The first production rollout is still pending.

Production startup opens PostgreSQL without resolving a sole owner. Authenticated requests resolve
their user from the session, so both empty and multi-user databases can start successfully.
Startup cache-only recovery visits each profiled user and isolates failures.
Production and demo modes are mutually exclusive; configuration loading rejects their combination
whether demo mode comes from the environment or the command line.

`JOBCRON_SIGNUP_ACCESS_CODE` and `JOBCRON_STAGE1_SPONSOR_USER_ID` are optional. A configured
sponsor ID must be a positive base-10 integer. Startup loads both settings and wires them into the
server, while the production Compose file passes them through without defaults. The access code
opens cohort signup; leaving it unset keeps the signup page visible but closes account creation.
Global Stage 1A cache misses use only the configured sponsor's provider and usage ledger.
The sponsor ID assigns Stage 1A billing; it grants no authorization and creates no application
role. Missing, invalid, deleted, unconfigured, or exhausted sponsorship produces a bounded,
value-blind warning and deterministic fallback without charging another user.

The daily scheduler is enabled by configuration and runs inside the application process at
`JOBCRON_DAILY_SCRAPE_TIME` in Asia/Seoul (`05:00` by default). It collects all registered sources
once, then analyzes profiled users sequentially in ascending ID order while holding the shared
scrape/re-rate gate. One user's credential or scoring failure is summarized and does not abort
later users.

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
- `internal/ai` defines the provider contract, the Anthropic Messages adapter, one shared
  OpenAI-compatible Chat Completions adapter for OpenAI and Gemini, prompts, response parsing,
  evidence gates, AI version identity, and the server-owned provider/model registry used by the
  profile form.
- `internal/storage` exposes one concrete repository, a read-only PostgreSQL schema-version gate,
  and an explicit operator migration path.
  Both paths validate one canonical embedded migration manifest and reject malformed files,
  duplicate versions, changed pinned SQL, pending versions, database-ahead versions, and stored
  filename or SHA-256 mismatches as applicable. A one-time version-only ledger conversion requires
  an exact audited migration-tree object that matches the binary's pin. The production role
  can read `schema_migrations` but cannot insert, update, or delete its rows.
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

The cohort gate does not verify email ownership. Forgotten-password recovery is operator-assisted;
email verification, public recovery, and open signup remain follow-up work.

Authenticated users manage their account at `GET /account`. Password changes require the current
password plus a policy-valid confirmed replacement. Login and account password verification plus
signup and replacement hashing share bounded password-work capacity and finish before any database
transaction. Missing-email login attempts verify against a fixed valid dummy Argon2id hash through
the same capacity path, so saturation does not reveal whether an account exists. Password
changes and self-deletion share dedicated account-mutation attempt windows: an absolute client-IP
budget bounds all re-authentication work, while a client-IP-and-user failure budget resets after a
correct current password. Either rejection returns the same generic `429` before taking password
work capacity. The
mutation proceeds only while the exact loaded password hash and unexpired submitting hashed session
still belong to that user; the same transaction writes the new Argon2id hash and revokes every other
session. Self-service deletion requires the current password and canonical email, then waits for the
shared scrape/re-rate operation gate before applying the same expected-credential and
unexpired-session guard. That ordering lets detached paid work finish its provider calls and usage
debit before private rows are cascaded. Cancellation while waiting leaves the account unchanged. A
stale guard returns the generic account-validation failure without mutation. The server then expires
the browser session cookie and redirects to login.

Every authenticated handler resolves a `userID` before accessing profiles, saved-job state,
scores, AI usage, or credentials. Local mode supplies the fixed local owner's ID through the same
server methods. This keeps storage calls user-scoped across the implemented multi-user cohort.

Email verification, public password recovery, organizations, and per-user schedules are not
implemented. The
[multi-user expansion follow-up](superpowers/archive/2026-07-22-multi-user-account-expansion/260715-multi-user-account-expansion.md) records
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

The scheduled path separates this into one global collection phase (sources, details, upserts,
sweeping, deduplication, and sponsor-funded Stage 1A) followed by sequential per-user analysis
(Stage 1B, deterministic scoring, and Stage 2). Interactive scrapes use the same phases for one
authenticated user and only that profile's enabled sources.

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
global and keyed by posting content and `ExtractionContractVersion`. Invalid or unavailable extraction
falls back to source fields and deterministic parsing. Cache hits require no sponsor. Cache misses
use only the configured sponsor's runtime, token budget, call cap, and `ai_usage`; an unavailable or
exhausted sponsor never falls through to the triggering or analyzed user's credentials.

### Stage 1B: user-scoped dealbreaker context

Candidate detection is deterministic and unchanged. The matcher NFC-normalizes and lowercases
text, then finds exact contiguous token sequences from the user's dealbreaker list over
`title + company + description`. Each match becomes a candidate whose stable identity is the
SHA-256 digest of that normalized token sequence.

Each candidate then carries one canonical server-owned match: the evidence quote, the field it
came from (`structured_tag`, `title`, `company`, `description`, or `combined_fields`), and the tag
category when a structured tag supplied it. The server picks the most specific field first, uses
the shortest matching description line, and bounds evidence to 240 code points around the matched
span. A structured tag qualifies only when the tag name itself matches the phrase and that name
also appears in the description, so provenance never claims a field the posting text does not
support. Saved dealbreaker lines are capped at 240 code points so bounded evidence can always
contain the complete phrase. This match is immutable provider input: the detector owns the proof
and the model owns only the verdict.

Stage 1B alone sends the complete normalized posting, because a dealbreaker occurrence can sit
past the 12,000-rune model-input cap and judging an occurrence the model never saw is worse than
leaving it unresolved. Stage 1A and Stage 2 keep the capped input. All three key content on the
same full-text hash, so widening Stage 1B's text does not change cache identity.

Stage 1B batches only unresolved candidates and returns, per candidate, a verdict (`applies`,
`not_applicable`, `uncertain`), one reason code compatible with that verdict, and optional
grounded reason text. `applies` admits `requirement`, `responsibility`, and `expected_condition`;
`not_applicable` admits `benefit_or_eligibility`, `explicitly_negated`, and
`incidental_or_metadata`; `uncertain` admits only `insufficient_context`. A row is discarded when
its candidate is unknown or duplicated, its server match is malformed, its verdict and reason code
are incompatible, or an `uncertain` row carries any reason text. Reason text that is overlong or
absent from the posting is dropped while the verdict survives, and rows decode independently so
one malformed row never voids its valid siblings. The PostgreSQL cache key combines `user_id`,
posting, posting content hash, `DealbreakerVersion`, and normalized keyword hash, so neither users
nor changed inputs can share a verdict accidentally.

A keyword exclusion is suppressed only when every matched candidate is `not_applicable`.
`applies`, `uncertain`, missing cache entries, invalid rows, unavailable credentials, provider
failures, and exhausted budgets all retain the deterministic exclusion. This conservative fallback
lets AI remove a supported false positive without silently weakening an unresolved hard rule.

Manual and opted-in scheduled scrapes may run Stage 1B before scoring. An explicit `AI 평가`
press evaluates Stage 1B candidates across every stored posting, with the selected surface's rows
first and remaining rows in stable storage order; a wrongly excluded posting is invisible on the
surface that would otherwise scope the pass, so scoping it there would keep it hidden forever.
Stage 1A extraction and Stage 2 scoring stay limited to the selected surface. The shared per-call
cap and token budget still bound paid work, so several presses may be needed to drain a large
backlog. Profile save and startup rescoring are provider-free: they reuse caches and mark missing
validation as pending instead of making surprise paid calls. Stage 1B and Stage 2 share the user's
run budget and call cap.

Persistence follows the same split. `DealbreakerPromptVersion` is `2`, and migration `0019` stores
`match_json`, `reason_code`, and `reason_evidence` on `ai_dealbreaker_validations` while dropping
the version-1 `evidence` column. There is no down migration and no version-1 backfill; the earlier
binary is intentionally unsupported after the migration. A returned row is stored against the
candidate's own server match, never against anything the provider echoed back.

Each rerate records contextual postings pending before and after the run plus attempted, accepted,
and unresolved checks. A successful provider response that produces no citation-gated validations
ends as `no_progress`, not as a successful evaluation. The existing terminal rerate tracker carries
the concise blocker message across the automatic browser reload. The UI reports pending contextual
validation separately from stale Stage 2 scores while keeping one unique-posting count on the
button.

Scoring persists the exact decision in `ScoreResult.ExclusionReasons` inside
`scores.breakdown_json`. Each keyword reason carries the server match's evidence, source, and
category alongside the phrase and confidence, so rendering explains the score that actually caused
exclusion without querying the validation cache or recalculating policy. The complete contract is
in the [dealbreaker match-provenance contract][dealbreaker-provenance-spec]; it supersedes the
earlier [Stage 1 contextual validation specification][stage1-context-spec].

### Stage 2: user fit

The second AI layer compares posting text with the user's free-text goals and dislikes. A
citation gate rejects unsupported adjustments. Accepted deltas, usage, and cache entries are
scoped by `user_id`, posting input, profile input, and AI version.

A rerate resolves one user's runtime and operates only on that user's visible rows. The scrape
pipeline may also run Stage 2 automatically for new postings. Per-call, per-run, daily, and monthly
limits bound paid usage; the usage ledger persists across restarts.

### Credential lifecycle

The profile form supports Anthropic, OpenAI, and Gemini and sends a newly entered provider key to
the server. The server encrypts it with AES-256-GCM before storing one row per
`(user_id, provider)` in `user_ai_credentials`. Authenticated encryption binds the ciphertext to
its user, provider, and envelope version, so moving a row to another identity causes decryption to
fail. Switching providers with a blank key preserves every saved row; explicit deletion removes
only the authenticated user's selected-provider row and leaves the profile selection intact.

The process decrypts a credential only while resolving that user's immutable AI runtime. The raw
key is not returned to the browser or written to logs. Production receives the master key from
configuration; managed local mode keeps it in a protected application-config file.

The completed storage and runtime design, migration contract, and remaining production rollout
gate are documented in the
[PostgreSQL convergence and per-user credential specification][postgres-credential-spec].

## Persistence and ownership

Normal application startup uses PostgreSQL only. `storage.OpenPostgres` checks connectivity and
verifies every embedded migration version without issuing DDL. Production operators use
`storage.OpenPostgresMigrating` through `jobcron-user migrate` with the RDS master role over a
localhost-only Session Manager tunnel before starting a new runtime. They then refresh grants for
the lower-privilege application role. The master credential stays on the trusted controller; the
host receives only the DML-capable runtime URL. Normal startup fails closed when an operator
migration was missed rather than granting schema-creation privileges to the runtime role.
The operator command bounds connection and lock waits with one two-minute context. Each migration
transaction takes the same PostgreSQL advisory lock and rechecks its recorded version after the
lock, preventing concurrent operators from applying one version twice while preserving
per-version commits and idempotent recovery. Each ledger row persists the migration version,
filename, and pinned content digest. Runtime startup verifies all three without DDL; the operator
path refuses an old version-only ledger unless the separate immutable-image audit established that
its migration tree exactly equals the reviewed tree and explicitly authorizes the transactional
backfill.
The production guide disables replacement objects before extracting the reviewed builder, then
clones the exact commit into a private repository with no controller-local attributes or config.
The build removes Git metadata and runs an explicitly selected local Go binary under an environment
allowlist, fixed local toolchain and CGO policies, private caches, module checksum verification, and
read-only module mode. It rejects symlinked or non-regular GOROOT entries, checks every manifest
stage explicitly, and authenticates every regular file before using the toolchain. The production
role helper rejects the master identity, pins both role and
database-specific search paths, converges privileges deny-first, refuses forward or reverse
memberships and non-public ownership/authority, revokes direct and public migration-ledger writes
including TRUNCATE, and checks effective database/schema/table/sequence/routine privileges before
emitting readiness. Migration startup selects the first non-system schema from the live effective
PostgreSQL search path, uses that explicitly qualified schema for the ledger, and pins every
migration transaction to the same application schema before executing embedded DDL.

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
session, then sample `clock_timestamp()` from PostgreSQL. The session-expiry decision and
password-change `updated_at` value therefore use one database wall-clock timestamp sampled after
lock acquisition. SQLite compatibility uses one application timestamp in its conditional mutation
statement. Both paths require the user ID, expected password hash, and submitting hashed session to
remain unexpired at that path's single authoritative timestamp.
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
profile order. The panel includes a visible label, conservative confidence text, and the quoted
server match when available, followed by a short Korean source label such as `복지 태그`, `제목`,
`회사`, or `공고 본문`. That label reads only the stored server match, never provider output, and
an unrecognized source falls back to the calm generic `공고 정보` rather than leaking a raw enum
into the briefing. Keyword evidence is split into ordinary strings plus a marked token span; Go
templates escape every segment, and provider output never becomes `template.HTML`. The danger
styling keeps the full row at normal contrast and uses text and a warning symbol in addition to
color. Match provenance reuses this existing surface; it adds no new explanation interface, CSS, or
JavaScript.

## Deployment boundaries

The prepared production deployment uses an immutable Linux arm64 container image. Caddy
terminates TLS and is the only public component. The application port and RDS database remain
private. Application state and encrypted AI credentials live in PostgreSQL; production mounts no
application-data or legacy-key volume.

Local development runs the same application and PostgreSQL storage contract. Compose manages only
the local database lifecycle; the application remains a normal host process unless the operator
explicitly chooses another setup.

Terraform has three ownership roots. `bootstrap` owns the protected S3 state bucket, GitHub's OIDC
provider, and the automation roles. `production` owns the canonical VPC, internet gateway, four
public subnets, their shared main route table and default route, and one reserved Elastic IP
allocation. The subnets continue to inherit the VPC main route table; Terraform asserts that
relationship during controlled operations but does not own redundant route-table association
resources. The reserved EIP remains unattached.

The `production` root also owns two private database subnets and their VPC-local-only route table,
an origin security group with no ingress, a database security group that accepts PostgreSQL only
from that origin group, an encrypted private PostgreSQL RDS instance with managed master
credentials, an empty runtime-secret container, and a protected recovery bucket. The runtime
secret has no value until Slice 4 creates its first version outside Terraform. Recovery objects
tagged as verified after an off-cloud copy expire after 14 days; all current versions expire after
90 days, and both rules permanently expire the resulting noncurrent data version one day later.

The `production` root also owns one Amazon Linux 2023 arm64 replacement host managed only through
Session Manager. Its AMI is a validated private controller input pinned to the reviewed image, so
an upstream `latest` pointer cannot silently schedule host replacement. Changing the pin is an
explicit replacement operation that requires its own reviewed plan. Local controllers supply the
pin from a mode-`0600` packet; the manual GitHub plan workflow receives the same input from its
protected `production` environment secret `TF_VAR_REPLACEMENT_HOST_AMI_ID` without printing it.
Bootstrap installs only Docker, `jq`, and the PostgreSQL client from the pinned Amazon Linux
repositories, requires the AMI-provided `curl` and AWS CLI v2, and installs the exact arm64 Docker
Compose plugin from Docker's release assets only after verifying its pinned SHA-256. Bootstrap
always replaces any pre-existing plugin with that verified artifact before executing Compose. A
private `DOCKER_CONFIG` keeps user-level plugins out of both bootstrap and runtime resolution. Runtime assets
are written only after those tool gates pass.
Systemd materializes secret values only below `/run/jobcron`. Post-stop cleanup removes generated
runtime material but preserves an unconsumed one-shot registry token across preflight failures;
the pull path consumes and removes that token on both success and failure. Bootstrap leaves the
recovery timer disabled until one manually verified recovery run succeeds.
Recovery accepts only the generated TLS RDS connection shape. It gives `pg_dump` a password-free
connection URI and carries the decoded database password only in the child environment, keeping
credentials out of process arguments and recovery evidence.

The origin security group carries the public semantic discovery tag
`jobcron:edge-target = origin-security-group`. The adopted canonical VPC remains untagged because
Window 1 forbids updating it; Slice 5 derives the VPC from the tagged security group's `vpc_id`.
`edge` owns future Cloudflare ingress automation. The old EC2 instance and prior RDS instance
remain unchanged rollback resources outside this ownership slice.

The bootstrap root was first applied with local state and then migrated into the protected S3
backend. All roots use separate state keys and native S3 lock files. Bucket versioning supports
recovery from an earlier state-object version, and that retrieval path has been rehearsed without
replacing live state. Human administration uses the `jobcron-admin` IAM Identity Center profile
rather than long-lived access keys.

The bootstrap root exposes separate production and edge roles whose trust policies require the
matching GitHub environment. These roles can read and write only their approved state and lock-file
keys; `DeleteObject` is limited to lock files. They intentionally have no application-deployment
permissions.

Static Terraform CI runs without AWS credentials. A separate manually dispatched production
workflow uses the protected `production` environment and short-lived OIDC credentials only to
initialize state and detect plan changes; it has no apply or plan-publication path.

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
- An unavailable sponsor degrades global Stage 1A misses without selecting or charging another
  user; one user's analysis failure does not stop later users.
- Startup rescoring repairs postings left without scores after an interrupted prior run.
- Database migration failure prevents startup before the server accepts requests.

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
[dealbreaker-provenance-spec]:
  superpowers/archive/2026-07-25-contextual-dealbreaker-match-provenance/260725-contextual-dealbreaker-match-provenance-contract.md
[stage1-context-spec]:
  superpowers/archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence.md
[hosted-first-storage]:
  superpowers/decisions/260714-hosted-first-local-database-convergence.md
