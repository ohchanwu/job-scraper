# Jobcron Production Custody P1 Repairs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task by task.

**Goal:** Close the two P1 custody failures from Witness verdict `hq-eil2` for exact commit `6acbb0ac70b5d75a1dcf7cb538b080a748683b43` without permitting production deployment before an exact successor review passes.

**Architecture:** PostgreSQL migration startup will resolve the target schema from the live database session and explicitly pin that schema for every ledger and DDL operation. The reviewed Jobcron builder will authenticate the complete GOROOT file tree rather than only executable tool binaries. Both fixes extend existing code paths and test helpers; no new service or abstraction is needed.

**Tech Stack:** Go, `database/sql`, pgx, POSIX shell, Git, PostgreSQL integration tests.

**Spec:** [Terraform-first production launch human-blocked steps](../specs/260726-terraform-first-production-launch-human-blocked-steps.md), with the exact acceptance blockers recorded by Witness in durable verdict `hq-eil2` on thread `thread-7d7bb04fd73e`.

**Global Constraints:** Keep production stopped. Do not mutate AWS, Cloudflare, the production host, production PostgreSQL, or private credentials. Do not push. Commit locally only after Tier B verification, staged secret scanning, and publication review. Rebind Guzzle only after Witness approves the exact successor SHA.

## Task 1: Make PostgreSQL schema custody fail closed

**Files:**

- Modify: `internal/storage/store_test.go`
- Modify: `internal/storage/store.go`

1. Add a PostgreSQL integration regression that creates two schemas, configures the role/database session search path independently of the URL, and verifies that both `schema_migrations` and every application table land in the same live-session-selected schema.
2. Run the focused regression and capture the expected failure against `6acbb0a`.
3. Replace URL-derived ledger selection with a live-session schema lookup.
4. Quote the resolved schema as a PostgreSQL identifier and explicitly pin it in every migration transaction before ledger or embedded migration SQL runs.
5. Use the same resolved schema-qualified ledger for read-only verification and legacy backfill.
6. Run the focused storage unit and PostgreSQL integration tests, including pending, tampered, unknown, legacy, DML-only, and advisory-lock failure paths.

## Task 2: Authenticate the complete reviewed Go toolchain

**Files:**

- Modify: `scripts/build_reviewed_jobcron_user_test.go`
- Modify: `scripts/build-reviewed-jobcron-user.sh`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- Modify: `docs/architecture.md`

1. Add a regression that constructs a controlled Go toolchain, records its approved digest, mutates a non-executable GOROOT input, and requires the reviewed builder to reject it before build execution.
2. Run the focused regression and capture the expected failure against `6acbb0a`.
3. Change both the operator digest command and builder verification to hash every regular file under the resolved GOROOT in deterministic relative-path order.
4. Update the test digest helper and operator documentation to the same algorithm.
5. Document that the reviewed build authenticates the complete GOROOT file tree.
6. Run the focused builder suite, including ambient settings, replacement refs, attributes, invalid inputs, and non-executable mutation.

## Task 3: Verify, commit, and request exact rereview

**Files:**

- Review all files changed since `6acbb0a`
- Update this plan and `docs/superpowers/README.md` when archiving completed work

1. Format the Go changes and run the repository's documented build, full tests, linter/vet, race tests, PostgreSQL integration suite, and relevant shell checks.
2. Manually exercise both adversarial failure paths: split search-path custody and a non-executable GOROOT mutation.
3. Review `git diff 6acbb0a...HEAD` plus the working-tree diff for scope and accidental data.
4. Stage the intended changes, run Gitleaks over the staged diff, and manually inspect the staged publication surface for credentials and production identifiers.
5. Commit the successor locally with a descriptive message; do not push.
6. Send one durable rereview request on canonical thread `thread-7d7bb04fd73e`, naming the exact successor SHA, sole parent, tree, clean status, and verification evidence.
7. Keep Guzzle and production stopped unless Witness returns a durable exact-SHA approval. If approved, rebind Guzzle's operational task to that exact SHA; if not, implement the next successor.

## Task 4: Close exact `ef258f6` adversarial follow-up findings

**Files:**

- Modify: `internal/storage/store.go`
- Modify: `internal/storage/store_test.go`
- Modify: `scripts/build-reviewed-jobcron-user.sh`
- Modify: `scripts/build_reviewed_jobcron_user_test.go`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- Modify: `docs/architecture.md`

1. Add red regressions for the production `pg_catalog, public` search-path order, an unreadable GOROOT file that makes per-file hashing fail, and a `bin/go` symlink to an external executable.
2. Select the first non-system schema from the live effective search path, keep the ledger qualified, and retain the transaction-local application-schema plus `pg_catalog` pin.
3. Materialize the GOROOT path list and manifest in owner-only temporary files and require every POSIX-shell stage to succeed explicitly.
4. Reject symlinks and other non-directory, non-regular entries anywhere in the selected GOROOT before hashing or execution.
5. Repeat the focused adversarial checks and all Task 3 gates, commit one local successor, and request rereview on the same canonical thread.
