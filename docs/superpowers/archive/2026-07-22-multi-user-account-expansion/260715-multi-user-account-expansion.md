# Multi-User Account Expansion

- **Date:** 2026-07-15
- **Last updated:** 2026-07-24
- **Status:** Completed and archived; implementation shipped
- **Depends on:**
  [`260714-postgresql-local-convergence-user-ai-credentials.md`](../../specs/260714-postgresql-local-convergence-user-ai-credentials.md)
- **Related:**
  [`260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md`](../../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md)

## Purpose

Prepare Jobcron for its first multi-user demo cohort while the overseer studies
AWS, IAM, Terraform, and the production deployment flow. Application work may
proceed locally against PostgreSQL; AWS resource changes and production cutover
remain separately reviewed, human-gated work.

The first users are the overseer's SSAFY classmates. They receive self-service
account creation behind one shared cohort access code. Removing that gate and
opening signup to the general public is a later security and operations milestone.

## Approval-Time Pre-Implementation Baseline

At approval time, the PostgreSQL convergence dependency had completed its
repository-owned work. The remaining dependency work was the external production
rollout, not an application prerequisite for this milestone.

The repository then already provided:

- PostgreSQL as the only writable application database;
- user-scoped profiles, saved-job state, AI scores, usage, and credentials;
- explicit user identity for AI runtime and credential resolution;
- two-user isolation coverage;
- Argon2id password hashing, hashed session tokens, CSRF protection, secure
  production cookies, and login throttling;
- encrypted, per-user Anthropic credential storage; and
- foreign-key cascades for private user-owned state.

The database could safely hold multiple users, but two owner-era entry points
still prevented the app from operating that way. The `jobcron-user create-owner`
command refused to create a second account, and the daily scheduler refused to
run when more than one user existed. The completed milestone removed both
restrictions.

## Locked Product Decisions

1. Accounts are independent. There are no organizations, teams, application
   roles, or social-login providers.
2. The cohort gets self-service email-and-password signup protected by one
   server-side access code. The shared code proves only that a registrant knows
   the value; it does not prove that they are a particular classmate. Signup is
   not yet open to the general public.
3. Passwords reuse the existing Argon2id implementation. Cohort users can change
   their password while signed in. Recovery for a forgotten password and email
   verification are deferred; the operator handles cohort recovery through the
   user-management CLI.
4. AI access is bring-your-own-key. The UI supports Anthropic, OpenAI, and
   Gemini. The overseer's account sponsors global Stage-1 cache population;
   a separate Jobcron-funded service credential is out of scope.
5. The server performs one global scrape every day at 05:00 KST. It populates
   missing global Stage-1 extractions through one explicitly configured sponsor
   account, then scores the shared postings separately for each user.
6. Per-user scoring runs sequentially at first. Concurrency or a job queue is
   added only if measured run duration requires it.
7. A missing, invalid, or exhausted AI credential affects only its assigned
   work. Sponsor failure skips uncached Stage-1 AI extraction; another user's
   failure affects only that user's analysis. Deterministic scoring continues.
8. Deleting an account removes its sessions, profile, saved-job state, AI
   scores, usage, contextual validations, import records, and credentials.
   Global postings, public `ai_extractions`, and global scrape history remain.
9. Cohort users can permanently delete their own account after re-authentication
   and explicit confirmation. Operator CLI commands remain available for support.

## Milestone Scope

### Account creation

- Add a general `Store.CreateUser` Go storage method that can create more than
  one account. Keep the existing `jobcron-user create-owner` CLI command for
  creating the first production account before web signup is enabled; this
  one-time setup is the “owner bootstrap.”
- Canonicalize email identity consistently at creation, login, reset, and
  deletion. Store and compare the trimmed lowercase address.
- Keep the database uniqueness constraint as the final duplicate-account guard.
- Add signup pages using the existing templates, CSRF protection, Argon2id
  hashing, session creation, and secure cookie settings.
- Require password confirmation, a minimum of 15 characters, and support long
  passphrases without composition rules.
- Return an enumeration-safe response for duplicate email addresses rather than
  revealing whether an account already exists.
- Rate-limit signup attempts by client address and normalized email.
- Read the cohort gate from `JOBCRON_SIGNUP_ACCESS_CODE`. Never store it in the
  repository, database, URL, logs, or rendered page. Signup fails closed when it
  is unset, and comparisons are constant-time.
- Sign the user in after successful account creation and direct them to profile
  setup.
- Do not add application roles. Operator-only actions remain CLI operations.

### Cohort account lifecycle

- Keep the existing server-side session design. Jobcron does not use JWTs: the
  browser receives an opaque random cookie, while PostgreSQL stores only its
  hash. Deleting session rows invalidates those cookies immediately.
- Let an authenticated user change their password after confirming the current
  password, then revoke their other sessions.
- Let an authenticated user permanently delete their account after confirming
  the current password and accepting an explicit destructive confirmation.
- Generalize the existing password-reset CLI to select exactly one user by
  normalized email.
- Add an operator CLI command that deletes exactly one user after explicit
  confirmation.
- Revoke all sessions as part of account deletion through the existing foreign
  key cascade, then expire the browser cookie.
- If the configured Stage-1 sponsor account is deleted, future cache population
  fails closed until the operator configures another sponsor. Never substitute
  the first or next database user automatically.
- Verify that deleting a user removes every private row but preserves global
  posting facts and scrape history.
- State clearly in the signup UI that the cohort release does not verify email
  ownership and that forgotten-password recovery requires contacting the operator.

### AI providers and credentials

- Keep the existing `ai.Provider` interface as Jobcron's application boundary for
  Anthropic, OpenAI, and Gemini. Do not expose provider-specific types above each
  provider adapter.
- Use the smallest provider client that preserves Jobcron's custom HTTP transport
  and egress pinning, explicit pacing and retry behavior, usage accounting,
  stable error mapping, and deterministic offline tests. Start with the existing
  dependency-free HTTP chassis. An official provider SDK is allowed when a short
  implementation spike proves that it removes more adapter and maintenance code
  than it adds without weakening those properties.
- Give each provider a small static model allowlist and one inexpensive default.
  Keep volatile model identifiers in the code and tests rather than locking them
  into this specification.
- Let each user store at most one encrypted key per provider and select one active
  provider and model for scoring.
- Add authenticated create, replace, and delete-key actions for each supported
  provider, with confirmation before deletion.
- Keep blank key input as “preserve the existing key” for the selected provider.
- Keep create, replace, read, and delete operations scoped to the authenticated
  user's selected provider row.
- Keep Anthropic's existing Messages API adapter unchanged except where the
  shared three-provider UI or tests require it.
- Use one canonical Stage-1 semantic contract and output schema across all
  providers. Provider adapters may differ in wire format or structured-output
  features, but they must not redefine the extracted facts.
- Restore OpenAI through the existing HTTP chassis unless the SDK spike meets the
  criteria above. OpenAI already fits the current request-and-response shape, so
  an SDK must earn its dependency cost rather than being selected by default.
- For Gemini, compare direct Gemini REST, Google's official SDK, and the documented
  OpenAI-compatible endpoint during implementation planning. Select the smallest
  option that meets the same transport, pacing, accounting, error, and testability
  requirements. Do not mandate the compatibility endpoint merely to make the
  provider adapters look alike.
- Add offline HTTP contract tests for all three providers and opt-in live contract
  tests that run only when the corresponding test API key is present.
- Correct the profile copy that still describes a local `0600` key file. In the
  hosted app, the credential is encrypted before storage in PostgreSQL.
- Do not add providers beyond these three, dynamic provider plugins, or remote
  model discovery.
- Do not add LangChain for this milestone. Jobcron currently performs fixed,
  structured extraction and scoring calls rather than agent, tool-use, retrieval,
  or multi-step orchestration workflows. Reconsider a workflow framework only if
  those capabilities become real requirements and the existing provider boundary
  starts accumulating orchestration code.

### Delivery order

- Complete the multi-user core first: signup and account lifecycle, server-side
  sessions, user isolation, and the global scheduled-work flow using the existing
  Anthropic adapter.
- Implement OpenAI and Gemini as a separate provider-expansion slice after that
  core is independently verified. They do not block the first cohort launch. Hide
  an unavailable provider option rather than exposing a control that cannot work.
- Keep the slices independently reviewable so provider transport choices cannot
  delay or destabilize the account and scheduler changes.

Implementation references:

- [OpenAI Chat Completions API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
- [Gemini OpenAI compatibility](https://ai.google.dev/gemini-api/docs/openai)
- [LangChain overview](https://docs.langchain.com/oss/python/langchain/overview)
- [LangChainGo community project](https://github.com/tmc/langchaingo)

### Scheduled work

- Align the application default and production configuration on 05:00 KST.
- Replace `SoleOwnerUserID` in scheduled work. Multiple users must no longer make
  the scheduled run ambiguous or cause it to be skipped.
- Fetch, normalize, deduplicate, and upsert shared postings once without using a
  user's paid AI credential.
- Read the Stage-1 sponsor from `JOBCRON_STAGE1_SPONSOR_USER_ID`. Resolve exactly
  that account's selected provider, model, encrypted credential, and usage budget.
  The variable is an explicit billing assignment, not an application role.
- Populate missing global Stage-1 extractions before per-user scoring. Charge
  every paid Stage-1 call only to the sponsor's usage ledger.
- Apply the same payer rule to scheduled scrapes, interactive rerates, backfills,
  and retries. The user who happens to trigger a Stage-1 miss never becomes its
  payer.
- If the sponsor ID is absent, unknown, deleted, unconfigured, invalid, or over
  budget, record a bounded failure and continue with existing cache hits and the
  deterministic fallback. Never borrow another user's credential.
- Enumerate current users and perform profile-dependent scoring sequentially.
  Users without a saved profile are skipped until they finish setup.
- Resolve each user's selected provider, AI runtime, credential, and usage budget
  independently for contextual validation and Stage-2 scoring. Those paid calls
  occur only inside that user's pass and are charged only to that user's ledger.
- Keep `ai_extractions` global and identify one reusable extraction by
  `(posting_id, content_hash, extraction_contract_version)`. `content_hash`
  represents the normalized posting input. The contract version represents the
  Stage-1 fact definitions, output schema, and validation rules.
- Keep provider, model, transport, and response settings out of Stage-1 cache
  identity. Prompt wording changes also reuse the cache when they preserve the
  same contract. A semantic, schema, or validation change must bump the contract
  version and produce a miss.
- Reuse the existing `ai_version` storage field for the extraction contract
  version; do not add a replacement cache table or column merely to rename it.
- On a cache hit, reuse the public posting facts without debiting any account. On
  a miss, call the sponsor's selected provider with the sponsor's key, validate
  the structured response, debit only the sponsor, and upsert the extraction.
- Do not cache provider errors, malformed structured output, or deterministic
  fallback results. A failed paid extraction must not poison later users' cache
  lookups.
- Record a bounded per-user failure summary without logging credentials or
  allowing one user's error to abort the remaining users.
- Keep the existing single global scrape lock. Replace it only if production
  measurements show that it prevents required work.

### Isolation

- Every authenticated profile, bookmark, hidden-job, rerate, rescore, cache,
  usage, and credential path continues to receive an explicit session `userID`.
- Users cannot read, overwrite, prune, or debit one another's state.
- `ai_extractions` remains global only while it contains public posting facts and
  no profile, credential, or account data.
- The sponsor designation grants no permissions and changes no authorization
  checks. It identifies only which account pays for global Stage-1 cache misses.
- The 05:00 workflow must resolve the configured sponsor explicitly. It must not
  infer sponsorship from account creation order or silently spend another user's
  API key.

## Truly Public Signup Follow-Up

The cohort access code is a deliberate boundary, not sufficient protection for
open internet signup. The following work is required before removing it.

### Prove email ownership and recovery

- Add transactional email delivery and verify each address with a signed,
  single-use, expiring token before activating the account.
- Add resend limits, generic responses, and safe token replacement so these
  endpoints cannot enumerate accounts or generate unbounded email.
- Add self-service password reset using the same single-use token properties.
- Configure the sending domain, SPF, DKIM, and DMARC, and handle delivery
  failures, bounces, and provider complaints.

### Resist automated abuse

- Remove the cohort code only after signup and recovery endpoints have both
  per-address and per-network throttling.
- Add a privacy-preserving bot challenge such as Cloudflare Turnstile when
  signup is opened publicly.
- Bound expensive work per account. A new user cannot trigger unlimited scraper,
  scoring, AI, email, or storage consumption.
- Add an operator mechanism to disable an abusive account without introducing
  application roles or a web administration console.

### Complete public support and policy

- Publish clear privacy, retention, deletion, and acceptable-use terms before
  collecting accounts from the general public.
- Provide a support path for lost access, disputed email ownership, and deletion
  requests without exposing whether an unrelated account exists.

### Harden public operations

- Monitor signup, verification, recovery, throttling, delivery failures, and
  account deletion without logging passwords, tokens, API keys, or unnecessary
  personal data.
- Exercise enumeration, replay, expired-token, duplicate-email, rate-limit,
  session-revocation, and deletion failure paths.
- Store email-provider, Turnstile, and signup secrets in the production secret
  store and inject them through the reviewed Terraform and runtime configuration.
- Grant the application only the IAM permissions required to read its runtime
  secrets and use the selected email service.
- Verify the complete public journey in a real browser: signup, email
  verification, login, password reset, credential management, and account
  deletion.

Public signup is ready only when an unknown internet user can complete that
journey safely without operator intervention and the operator can contain abuse
without editing the database manually.

## Verification Requirements

- Run the complete Go test suite and the PostgreSQL integration suite.
- Add storage tests for normalized email uniqueness, concurrent duplicate
  creation, password reset targeting, and account-deletion cascades.
- Verify that signup with a missing, incorrect, or unconfigured cohort access code
  creates neither an account nor a session. Also test signup throttling,
  enumeration-safe duplicate handling, CSRF, auto-login, and unauthenticated access.
- Verify the 05:00 scheduler calculation, then invoke the scheduled workflow
  directly in tests rather than waiting for the clock. With at least two users,
  assert one shared fetch, sponsor-funded Stage-1 population, separate user
  scoring, one broken user key that does not stop another user, and one missing
  profile that is skipped.
- Verify sponsor failure behavior: missing, unknown, deleted, unconfigured,
  invalid, and exhausted sponsors do not debit any other user and do not stop the
  deterministic workflow.
- Verify that scheduled scrapes, interactive rerates, backfills, and retries all
  charge Stage-1 misses only to the configured sponsor.
- Verify cache identity and payer behavior: repeated requests for the same
  posting, content hash, and extraction contract cause one sponsor-paid call and
  subsequent global hits. Changing provider, model, transport, or non-semantic
  prompt wording remains a hit; changing posting content or the extraction
  contract causes a miss. Provider or validation failure writes no extraction.
- Add offline provider contract tests for Anthropic, OpenAI, and Gemini, including
  authentication, request and response shape, usage accounting, model validation,
  API errors, and host pinning.
- Verify in a real browser that two accounts cannot see or change one another's
  profile, saved jobs, hidden jobs, scores, usage, or AI credential.
- Walk signup, login, profile setup, provider switching, key replacement, key
  deletion, password change, account deletion, logout, and failure paths on
  desktop and mobile viewports with no console errors.
- Update `docs/architecture.md`, deployment documentation, and the active
  Terraform specification with the final runtime-secret interface.

## Non-Goals

- Organizations, teams, invitations tied to individual email addresses, or
  application roles.
- Social login, multi-factor authentication, or public password recovery in the
  cohort milestone.
- Providers beyond Anthropic, OpenAI, and Gemini; dynamic provider plugins;
  remote model discovery; a separate Jobcron-funded service credential; or
  shared funded contextual and Stage-2 usage.
- LangChain or another general-purpose agent, retrieval, or workflow framework.
- Per-user scraping schedules, duplicate source fetches, concurrent fan-out,
  queues, workers, or distributed scheduling.
- Applying Terraform, modifying live AWS resources, changing DNS, or performing
  the production cutover as part of this application milestone.

## Deployment And Learning Gate

The application milestone may be implemented and verified locally before the
overseer finishes the AWS and IaC study path. It must not silently broaden into a
production infrastructure change.

Before production deployment, the overseer reviews and can explain:

- the Terraform plan and which AWS resources it creates, changes, or destroys;
- the IAM principal and least-privilege permissions used for planning, applying,
  deployment, and runtime secret access;
- Terraform state storage, locking, backup, and recovery;
- how `JOBCRON_STAGE1_SPONSOR_USER_ID` reaches the container as reviewed runtime
  configuration, and how `JOBCRON_SIGNUP_ACCESS_CODE` and existing secrets reach
  it without entering Git or Terraform state as plaintext;
- the database migration, rollback, credential-rotation, and account bootstrap
  procedures; and
- the expected recurring AWS and third-party costs.

Only then does the separate production deployment plan proceed.
