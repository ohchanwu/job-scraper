# Jobcron First Production Launch: Human-Blocked Steps

> [!WARNING]
> **Status: Superseded; never implemented.**
>
> This July 16 launch sequence was never executed. Before the alpha deployment,
> the product plan changed to implement pre-alpha milestone 2 first, which made
> this document stale. It was superseded by the
> [Terraform-first production launch human-blocked steps][replacement-spec].
>
> The later Terraform-first infrastructure decision also invalidated this
> document's manual EC2, SSH, persistent `.env`, public-ingress, and certificate
> assumptions. This file is retained only as historical evidence. Do not execute
> its checklist or linked commands.

**Original status (2026-07-16):** Active; repository preparation was complete
and external rollout was awaiting human authorization<br>
**Owner:** Human operator, assisted by an agent where appropriate<br>
**Implementation source:** [PostgreSQL convergence specification][convergence-spec]<br>
**Execution source:** [Slice 5 first-production plan][slice-5-plan]<br>
**Exact operator commands:** [Production human deploy guide][deploy-guide]

## Current State

The repository-only production preparation is complete:

- PostgreSQL is the only writable application database.
- User AI credentials are encrypted per user in PostgreSQL.
- Production Compose requires an immutable image, `DATABASE_URL`,
  `SESSION_SECRET`, and `JOBCRON_CREDENTIAL_ENCRYPTION_KEY`.
- Production has no app filesystem credential mount or migration container.
- The verified importer runs from a clean checkout of the approved release
  commit on the operator-controlled Mac. Verify its full commit SHA and clean
  worktree before running the reviewed CLI through a localhost-only SSH tunnel.
- Light and dark dashboard screenshots have been refreshed with score sorting,
  applied AI deltas, and visible AI-delta details.

The EC2 application host is still a blank deployment target. It has an existing
`.env`, but Docker and the application stack are not installed. Preserve the
existing values for:

```dotenv
DATABASE_URL=<existing-private-value>
SESSION_SECRET=<existing-private-value>
JOBCRON_ENV=production
JOBCRON_HOST=0.0.0.0
JOBCRON_PORT=7777
JOBCRON_NO_OPEN=1
JOBCRON_DAILY_SCRAPE_TIME=05:00
```

`JOBCRON_IMAGE` and `JOBCRON_CREDENTIAL_ENCRYPTION_KEY` are present in the
existing `.env` with placeholder values. Before first startup, replace those
placeholders with the approved immutable image tag and the generated production
master key; placeholder presence is not deployment readiness.

The convergence specification remains active until the production import,
authenticated browser verification, paid AI check, container-recreation check,
and final security gate pass. Archive it only after those requirements are
complete.

## Why The Remaining Work Is Human-Blocked

- Publishing the release image requires registry credentials and changes
  external registry state.
- Installing software and deploying on EC2 requires cloud access.
- RDS snapshot creation and import require authority over production data.
- Owner identity, owner password, and the production master key are private
  human-controlled values.
- The paid Anthropic verification consumes the user's credential and billing.
- DNS changes and go-live affect the public service.
- Closing the rollback window permits removal of retained recovery materials.

An agent may guide or execute these steps while the human is present and has
authorized the external changes, but the agent must not invent identities,
handle undisclosed secrets, approve spending, or close the rollback window.

## Secret And Publication Rules

- Never place a real API key, password, database URL, session secret, credential
  master key, RDS endpoint, EC2 address, owner email, snapshot identifier, or
  local recovery path in Git, chat, issues, PRs, or shared logs.
- Enter private values only in the approved secret store, AWS console, or a
  private terminal. Clear temporary shell variables after use.
- Keep RDS private. Operator access uses a tunnel bound to
  `127.0.0.1:15432`; do not expose PostgreSQL publicly.
- Do not copy the SQLite source to EC2.
- Do not print rendered Compose output because it contains expanded secrets.
- Keep exact deployment evidence in an access-controlled operator log.

## Private Inputs To Prepare

- [ ] Approved release commit SHA
- [ ] Docker-compatible OCI registry repository plus the credentials needed to
      push the image from the trusted Mac and pull it from EC2
- [x] EC2 host identity and SSH key
- [x] Existing EC2 `.env`, with placeholders still requiring replacement
- [ ] Owner email and a newly chosen owner password; the reviewed
      `jobcron-user create-owner` CLI is ready and reads the password from its
      secure prompt rather than a flag or shell history
- [x] Base64-encoded credential master key that decodes to exactly 32 bytes
- [ ] Immutable SQLite snapshot and durable `-wal` file
- [ ] AWS permission to create a manual pre-import RDS snapshot, providing a
      restore point if import validation fails before production use
- [ ] Access-controlled operator-log location for exact resource identifiers,
      snapshot ID, command outcomes, and rollback evidence that must not enter
      this public repository
- [x] Anthropic API key and approval for one minimal paid verification

## Human Execution Checklist

Follow the exact sequence and commands in the
[production human deploy guide](../../../../deploy/production/HUMAN_DEPLOY_GUIDE.md).
This checklist records the approval gates and observable outcomes without
duplicating secret-bearing commands.

### 1. Approve The Candidate And Publish The Image

- [ ] The candidate worktree is clean and its full commit SHA is approved.
- [ ] All required Go, PostgreSQL, race, build, release, Compose, and formatting
      gates pass on that exact commit.
- [ ] The publication review and Gitleaks scan find no new secret or private
      operational data.
- [ ] The human approves publishing the image to the intended registry.
- [ ] The pushed immutable tag uses the candidate commit and the inspected
      manifest contains `linux/arm64`.
- [ ] The image digest is recorded privately.

Stop if the worktree is dirty, PostgreSQL coverage is skipped, a gate fails, or
the published image cannot be tied to the approved commit.

### 2. Verify AWS And DNS, Then Prepare EC2

- [x] The expected EC2 instance and private PostgreSQL 18 RDS instance are
      healthy.
- [ ] RDS public access is disabled and PostgreSQL ingress is limited to the EC2
      security group.
- [ ] EC2 inbound rules expose only the approved SSH source plus public HTTP and
      HTTPS.
- [ ] RDS automated-backup retention and EC2 storage encryption are confirmed.
- [ ] Current DNS and previous rollback values are recorded privately.
- [ ] The apex record and `www` alias are ready for the first Caddy certificate
      issuance.
- [ ] Docker Engine and Docker Compose v2 are installed on EC2.
- [ ] The exact approved deployment files are present on EC2 and the checkout is
      clean at the release commit.

Do not start the app yet. Owner creation and import happen first through the
private tunnel.

### 3. Preserve The Existing Environment And Validate Compose

- [ ] The existing `.env` is backed up with owner-only permissions.
- [ ] Its existing database URL, session secret, environment, host, port,
      no-open, and daily-scrape values are unchanged.
- [ ] `JOBCRON_IMAGE` is set to the immutable image from step 1.
- [x] `JOBCRON_CREDENTIAL_ENCRYPTION_KEY` is generated on a trusted machine,
      decodes to exactly 32 bytes, and has a separate secure backup.
- [ ] The production Compose render passes the value-blind verifier using a
      private temporary file.
- [ ] No app container has been pulled or started before owner/import
      verification.

The credential master key is separate from RDS. Losing it makes stored
ciphertext unreadable, so its protected backup is part of production recovery.

### 4. Create The Owner, Snapshot RDS, And Import

- [ ] A localhost-only SSH tunnel from the trusted Mac to private RDS is open.
- [ ] The tunneled database URL, owner email, and production master key exist
      only in the current private shell.
- [ ] `jobcron-user create-owner` creates exactly one owner through its secure
      password prompt.
- [ ] An approved query confirms the production user count is exactly one.
- [ ] A manual pre-import RDS snapshot reaches `available`; its identifier is
      recorded privately.
- [ ] The legacy local app is stopped and the retained SQLite source is
      immutable during import.
- [ ] The default importer dry-run reports the expected fingerprint, eight data
      categories, credential provider count, and zero unapproved collisions.
- [ ] The human approves those dry-run results.
- [ ] The identical importer command with `--apply` succeeds atomically.
- [ ] A repeated dry-run reports `already imported` and verifies the imported
      state.
- [ ] The owner's password hash remains unchanged and no plaintext credential
      is present in PostgreSQL.
- [ ] Private shell variables are cleared and the SSH tunnel is closed after
      verification.

Retain the SQLite snapshot, durable `-wal`, master-key backup, and RDS snapshot
through the rollback window. This launch does not use the importer's optional
legacy `--ai-keys` source; the prepared Anthropic key is entered through the
production application after import for the paid verification.

### 5. Start Production And Walk The Real User Path

- [ ] EC2 pulls and starts the exact immutable image without building locally.
- [ ] The app and Caddy start without migration, credential, or secret-bearing
      log errors.
- [ ] HTTP redirects to HTTPS, `www` redirects to the canonical host, and both
      hostnames have valid certificates.
- [ ] Unauthenticated access reaches login rather than private data.
- [ ] The owner can sign in and sees the imported profile values.
- [ ] A known rule score, AI score with delta details, bookmark, hidden job, and
      AI usage state match the retained source verification.
- [ ] The profile UI shows the credential as configured without exposing it.
- [ ] A real job link reaches the intended posting, not a generic page or
      unrelated redirect.
- [ ] The human approves one minimal paid AI rerating, and the resulting delta
      and user-scoped usage update are visible.
- [ ] Recreating only the app container preserves the profile, scores,
      bookmarks, hidden state, masked credential state, and AI functionality.
- [ ] The scheduled scrape configuration is verified; any manual scrape is run
      only with explicit network and provider-cost approval.
- [ ] The walked production pages have no unexpected browser-console errors.

Use the gstack `/browse` workflow for web verification. Do not open or take over
the user's normal browser.

### 6. Run The Final Completion And Security Gate

- [ ] Two synthetic users pass the isolated two-session browser journey against
      a disposable PostgreSQL database, not production.
- [ ] All automated gates pass again on the deployed commit.
- [ ] The complete tracked diff passes manual publication review.
- [ ] Gitleaks reports no new actionable finding.
- [ ] Sanitized evidence records the deployed commit, image digest, RDS
      snapshot, import verification, browser journeys, paid check, and
      container-recreation result without private values.
- [ ] The Slice 5 plan and convergence specification satisfy their Definitions
      of Done and move to the tracked archive with both documentation indexes
      updated.
- [ ] The human explicitly closes the rollback window.

## Rollback Boundaries

### Before Import Commit

Fix the cause and repeat dry-run. No production application has started, so no
PostgreSQL restore is required.

### After Import But Before New Application Writes

Restore the approved pre-import RDS snapshot to a safe replacement instance,
update the private database URL, and repeat the verified import sequence. Do not
improvise destructive cleanup SQL.

### After PostgreSQL-Only Writes Begin

Keep PostgreSQL authoritative. Roll back to a schema-compatible immutable image,
or restore an approved RDS snapshot and replay explicitly approved changes.
Never return the writable application to SQLite.

### EC2 Loss

Recreate the host from the approved image and deployment files, reconnect it to
RDS, and restore the separately protected credential master key. If the master
key is unavailable, affected users must replace their stored provider
credentials.

### DNS Rollback

Restore the privately recorded previous DNS values. Keep the current EC2 and RDS
state until the rollback has been verified through the real browser path.

## Human Definition Of Done

The launch is complete only when every applicable item above is verified,
skipped items have a private rationale, rollback evidence exists, no secret has
entered a public artifact, the authenticated production path passes, and the
human explicitly closes the rollback window.

[convergence-spec]:
  ../../specs/260714-postgresql-local-convergence-user-ai-credentials.md
[slice-5-plan]:
  ../../plans/260715-postgresql-convergence-slice-5-first-production-deployment.md
[deploy-guide]: ../../../../deploy/production/HUMAN_DEPLOY_GUIDE.md
[replacement-spec]:
  ../../specs/260726-terraform-first-production-launch-human-blocked-steps.md
