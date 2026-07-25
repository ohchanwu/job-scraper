# PostgreSQL Account Mutation Clock-Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PostgreSQL password changes and self-deletion decide session
expiry using PostgreSQL's wall clock after acquiring the session-row lock.

**Architecture:** Preserve the existing user-then-session lock order and the
`lockPostgresAccountMutation` interface. After the session row is locked, query
`clock_timestamp()` from the same PostgreSQL transaction and compare
`expires_at` with that database timestamp in Go. Leave the SQLite compatibility
path unchanged because it stores and compares the application timestamp in one
process.

**Tech Stack:** Go, `database/sql`, PostgreSQL, existing storage integration
tests.

**Requirements source:** `jobs-89k` and the approved 2026-07-26 clock-source
design discussion.

## Global Constraints

- Reclassify `jobs-89k` from P1 to P3 before implementation; this is a
  correctness and test-reliability defect, not a new attacker capability in the
  current password-plus-session workflow.
- Keep the public `ChangePassword` and `DeleteSelf` signatures unchanged.
- Keep the PostgreSQL lock order unchanged: matching user row first, matching
  session row second.
- Sample PostgreSQL `clock_timestamp()` only after the session-row lock returns.
- Do not use PostgreSQL `now()` or `CURRENT_TIMESTAMP`; both are fixed at
  transaction start and can predate time spent waiting for a lock.
- Use the same sampled database timestamp for the expiry comparison and the
  password-change `updated_at` value.
- Leave the SQLite conditional mutation path unchanged.
- Add no clock interface, grace window, dependency, schema migration, API
  change, or UI change.
- The known failing full-race run is the regression's red evidence. The existing
  focused test can pass before the fix when scheduling delay exceeds clock skew;
  do not add a production clock-injection seam merely to manufacture a
  deterministic pre-fix failure.
- Require `JOBCRON_TEST_POSTGRES_URL` to be supplied through the execution
  environment for every PostgreSQL-backed test command. Never write its value
  into tracked documentation or logs.
- Commit locally at meaningful checkpoints. Never push, create an MR/PR, deploy,
  or open the user's default browser.
- Preserve `.beads/` and
  `docs/research/2026-07-25-new-job-platform-candidates.md`.

---

### Task 1: Use the PostgreSQL clock after the account-mutation locks

**Files:**

- Modify: `internal/storage/users.go:303-333`
- Test: `internal/storage/account_lifecycle_test.go:402-472`

**Interfaces:**

- Consumes:
  `lockPostgresAccountMutation(context.Context, *sql.Tx, int64, string, string) (bool, time.Time, error)`
- Produces: the same signature and caller contract; its returned `time.Time` is
  now sampled by PostgreSQL after both required row locks.

- [ ] **Step 1: Correct the issue priority and preserve the observed regression**

Run:

```bash
bd update jobs-89k --priority=3
test -n "${JOBCRON_TEST_POSTGRES_URL:-}" || {
  echo "JOBCRON_TEST_POSTGRES_URL is required" >&2
  exit 1
}
go test -race ./internal/storage \
  -run '^TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock$' \
  -count=100
```

Expected:

- The bead becomes P3.
- The focused test may pass before the fix because the bug needs PostgreSQL's
  clock to remain ahead until the Go comparison executes.
- Do not weaken or delete
  `TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock`; the
  previously captured full-race failure is the red evidence.

- [ ] **Step 2: Make the test explicitly prove PostgreSQL considers the locked session expired**

In
`TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock`, after
updating `expires_at` and before committing `gateTx`, add:

```go
var expiredByDatabase bool
if err := gateTx.QueryRowContext(ctx, `
SELECT expires_at <= clock_timestamp()
  FROM sessions
 WHERE user_id = $1
   AND session_token_hash = 'current-session'`, user.ID).Scan(&expiredByDatabase); err != nil {
	t.Fatalf("check database session expiry: %v", err)
}
if !expiredByDatabase {
	t.Fatal("database did not consider the locked session expired")
}
```

This assertion defines the expected authority: when PostgreSQL says the session
is expired, both account mutations must reject it.

- [ ] **Step 3: Run the focused test before the implementation edit**

Run:

```bash
go test -race ./internal/storage \
  -run '^TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock$' \
  -count=100
```

Expected: the new database-expiry assertion passes. The mutation assertion may
still pass or intermittently fail on the old implementation; record the result
without adding sleeps or a fake clock seam.

- [ ] **Step 4: Sample PostgreSQL's wall clock after the session lock**

In `lockPostgresAccountMutation`, keep both existing `FOR UPDATE` queries.
Replace:

```go
now := time.Now().UTC()
return expiresAt.After(now), now, nil
```

with:

```go
var mutationTime time.Time
if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
	return false, time.Time{}, err
}
return expiresAt.After(mutationTime), mutationTime, nil
```

Do not combine `clock_timestamp()` with the session `FOR UPDATE` query. The
separate query makes its ordering explicit: it runs only after the lock-acquiring
query has returned.

- [ ] **Step 5: Run the focused regression repeatedly**

Run:

```bash
gofmt -w internal/storage/users.go internal/storage/account_lifecycle_test.go
go test ./internal/storage \
  -run '^TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock$' \
  -count=100
go test -race ./internal/storage \
  -run '^TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock$' \
  -count=20
```

Expected: both subtests (`change_password` and `delete_self`) pass every
repetition with no race report.

- [ ] **Step 6: Verify neighboring account lifecycle behavior**

Run:

```bash
go test ./internal/storage \
  -run '^(TestChangePassword|TestDeleteSelf|TestPostgresAccountMutations)' \
  -count=1
go test -race ./internal/storage \
  -run '^(TestChangePassword|TestDeleteSelf|TestPostgresAccountMutations)' \
  -count=1
```

Expected: password replacement, session revocation, rollback, self-deletion,
concurrent mutation, and expiry cases all pass.

- [ ] **Step 7: Review the Task 1 diff**

Run:

```bash
git diff --check
git diff -- internal/storage/users.go internal/storage/account_lifecycle_test.go
rg -n 'lockPostgresAccountMutation|clock_timestamp|time\.Now' \
  internal/storage/users.go internal/storage/sessions.go \
  internal/storage/account_lifecycle_test.go
```

Confirm:

- `ChangePassword` and `DeleteSelf` still share the same helper.
- The lock order and helper signature are unchanged.
- PostgreSQL expiry no longer depends on `time.Now()`.
- SQLite still uses one application `now` value in its conditional statement.

- [ ] **Step 8: Commit the storage fix**

Run:

```bash
git add internal/storage/users.go internal/storage/account_lifecycle_test.go
git diff --cached --check
git diff --cached
git commit -m "fix(storage): use PostgreSQL clock for account expiry"
```

Expected: one focused implementation commit with no documentation or unrelated
files.

---

### Task 2: Document, fully verify, and archive the fix

**Files:**

- Modify: `docs/architecture.md:346-353`
- Modify:
  `docs/superpowers/plans/260726-postgresql-account-mutation-clock-source.md`
- Modify: `docs/superpowers/README.md`
- Move on completion:
  `docs/superpowers/plans/260726-postgresql-account-mutation-clock-source.md`
  to
  `docs/superpowers/archive/2026-07-26-postgresql-account-mutation-clock-source/260726-postgresql-account-mutation-clock-source.md`

**Interfaces:**

- Consumes: Task 1's unchanged storage API and database-owned
  `mutationTime`.
- Produces: current architecture documentation, completed plan evidence, and a
  closed `jobs-89k`.

- [ ] **Step 1: Update the architecture description**

Replace the account-mutation timing sentence around
`docs/architecture.md:349-353` with:

```markdown
PostgreSQL self-service mutations lock the matching user before the matching
session, then sample `clock_timestamp()` from PostgreSQL. The session-expiry
decision and password-change `updated_at` value therefore use one database
wall-clock timestamp sampled after lock acquisition. SQLite compatibility uses
one application timestamp in its conditional mutation statement. Both paths
require the user ID, expected password hash, and submitting hashed session to
remain unexpired at that path's single authoritative timestamp.
```

Keep the surrounding login-serialization and session-revocation documentation
unchanged.

- [ ] **Step 2: Run formatting, static analysis, builds, and the full suites**

Run:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go build ./cmd/jobcron ./cmd/jobcron-import ./cmd/jobcron-user
go test ./... -count=1
go test -race ./... -count=1
```

Expected: every command exits zero and the race suite reports no data race.

If the unrelated Jumpit pacing test flakes, reproduce it separately and report
the existing `jobs-e71`; do not modify scraper code in this workstream.

- [ ] **Step 3: Inspect the complete implementation range**

Record the Task 1 parent and inspect the range:

```bash
IMPLEMENTATION_BASE="$(git rev-parse HEAD^)"
git diff --check "$IMPLEMENTATION_BASE"..HEAD
git diff --stat "$IMPLEMENTATION_BASE"..HEAD
git diff --unified=10 "$IMPLEMENTATION_BASE"..HEAD
```

Confirm the range contains only:

- the PostgreSQL clock-source change;
- the strengthened account-mutation regression;
- the architecture update;
- this plan's completion and archive/index changes.

- [ ] **Step 4: Complete and archive the plan**

After every preceding gate passes:

1. Mark every checkbox in this plan complete.
2. Create
   `docs/superpowers/archive/2026-07-26-postgresql-account-mutation-clock-source/`.
3. Move this plan into that directory without renaming it.
4. In `docs/superpowers/README.md`, remove the active-plan link and add the
   archived plan under `Recently Archived`.
5. Confirm no stale active-plan references remain:

```bash
rg -n '260726-postgresql-account-mutation-clock-source' docs
```

Expected: tracked references point only to the archive path.

- [ ] **Step 5: Run documentation and publication-safety checks**

Run the repository's local Markdown link check if one exists. Otherwise verify
every relative link in the three changed Markdown files resolves locally.

Then run:

```bash
git add docs/architecture.md docs/superpowers/README.md
git add -A \
  docs/superpowers/plans/260726-postgresql-account-mutation-clock-source.md \
  docs/superpowers/archive/2026-07-26-postgresql-account-mutation-clock-source/
git diff --cached --check
git diff --cached
gitleaks git --staged --redact --no-banner
```

Manually confirm the staged documentation contains no credentials, personal
data, production identifiers, raw logs, or unnecessary local paths.

- [ ] **Step 6: Commit the documentation lifecycle update**

Run:

```bash
git commit -m "docs: record PostgreSQL account expiry clock"
```

Expected: one documentation-only commit following Task 1.

- [ ] **Step 7: Close the issue and report the local-only result**

Run:

```bash
IMPLEMENTATION_BASE="$(git rev-parse HEAD^^)"
bd close jobs-89k --reason \
  "PostgreSQL account mutations now sample clock_timestamp() after row-lock acquisition; targeted repetitions, full tests, and full race suite pass."
git status --short --branch
git log --oneline "$IMPLEMENTATION_BASE"..HEAD
```

Report:

- exact implementation and documentation commit SHAs;
- focused repetition counts;
- full test, race, vet, build, and Gitleaks results;
- `jobs-89k` closed at P3;
- preserved user-owned untracked files;
- no push, MR/PR, deploy, or browser work.
