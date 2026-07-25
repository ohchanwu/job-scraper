# Alpha Pre-Launch Fixes

**Status:** Ready for implementation<br>
**Verified:** 2026-07-13 against the working tree based on `241f499`<br>
**Human handoff:** [Terraform-first production launch human-blocked steps](../../specs/260726-terraform-first-production-launch-human-blocked-steps.md)

## Context

Five repository-owned gaps should be fixed before the human production launch:

1. The production Compose stack does not persist the app configuration directory,
   so recreating the app container would delete the saved Anthropic API key.
2. The owner bootstrap guide assumes a source checkout with direct RDS access,
   but production RDS is private, meaning it does not accept connections from the
   public internet. The production image also contains only the `jobcron` server,
   not the `jobcron-user` or `jobcron-import` operator commands.
3. Removing a bookmark while viewing `/bookmarks` updates the stored state but
   leaves the card on screen until the user reloads.
4. The root README files show the 전체 공고 page in 날짜순 mode, even though
   점수순 better communicates Jobcron's job-ranking value.
5. The root README files still present the localhost binary as the normal user
   journey instead of making the upcoming hosted app primary and local operation
   an advanced contributor/self-hosting path.

This file contains work an implementation agent can complete and verify inside
the repository. AWS access, Docker Hub publication, production secrets, owner
credentials, data import approval, and go-live checks are intentionally separated
into the human handoff linked above.

## Goals

- Persist `ai_keys.json` across normal app-container replacement without storing
  it in PostgreSQL, the image, Git, or `.env`.
- Make the documented owner creation and optional SQLite import path executable
  when RDS remains private.
- Remove a successfully unbookmarked card from the signed-in `/bookmarks` page
  without a reload, using the existing posting-card exit motion.
- Keep the signed-in bookmark count, empty state, source filter, and search state
  synchronized after a card leaves the page.
- Make both README screenshots show the 전체 공고 page with 점수순 active.
- Present `jobcron.app` as the primary full-product path once launched, keep the
  live demo as the immediate evaluation path, and retain local operation under
  an advanced-use section.
- Leave `demo.jobcron.app` and its browser-local bookmark behavior unchanged.

## Settled Overseer Feedback

- **No demo changes.** Do not edit `deploy/demo/`, do not change the
  `jobcronDemoBookmarks` localStorage contract, and do not add smooth removal to
  the demo bookmarks page. Shared JavaScript may change only behind a signed-in
  `/bookmarks` condition, with existing demo tests proving no regression.
- Human-only external actions do not belong in the agent execution path. They are
  specified separately in `260713-alpha-launch-human-blocked-steps.md`.
- **Hosted first, local advanced.** Preserve release binaries, source builds,
  SQLite, and the isolated writable preview for launch, but move them out of the
  primary README journey. PostgreSQL convergence for local runs is the first
  post-launch persistence task, as recorded in
  `../decisions/260714-hosted-first-local-database-convergence.md`.

## Current State

| Surface                                                    | Verified behavior                                                                                                               | Gap                                                                                                                                                     |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `deploy/production/compose.yaml:8-46`                      | Runs the app and Caddy; only Caddy has named volumes.                                                                           | The app's `/root/.config/jobcron` directory is ephemeral.                                                                                               |
| `internal/ai/keys.go:12-86`                                | Saves `ai_keys.json` under `os.UserConfigDir()` with mode `0600` and atomic replacement.                                        | Inside the root-run Alpine container, the file resolves below `/root/.config/jobcron`, which is not mounted.                                            |
| `deploy/production/Dockerfile:5-19`                        | Builds and copies only `/usr/local/bin/jobcron`.                                                                                | Operator commands are unavailable inside the production image. This is intentional, but the guide does not provide a working alternative.               |
| `deploy/production/HUMAN_DEPLOY_GUIDE.md:172-183`          | Tells the operator to run `jobcron-user` from a source checkout with network access to RDS.                                     | A laptop cannot directly reach private RDS, and no SSH tunnel workflow is documented.                                                                   |
| `cmd/jobcron-user/main.go:32-89`                           | Creates an owner from a PostgreSQL URL and securely prompted or environment-provided password.                                  | The command needs a reachable database endpoint and must run after migrations.                                                                          |
| `cmd/jobcron-import/main.go:36-125`                        | Imports profile, postings, scores, bookmarks, hidden state, AI caches, and AI usage in one transaction.                         | The production guide omits import sequencing, dry-run verification, and the requirement to use the same owner email. It does not import `ai_keys.json`. |
| `web/bookmark.js:59-100`                                   | Optimistically flips the bookmark icon, calls `/api/bookmark/{id}`, reconciles to the JSON response, and rolls back on failure. | A successful `bookmarked: false` response does not remove the card on signed-in `/bookmarks`.                                                           |
| `web/not-interested.js:70-83` and `web/styles.css:765-770` | Adds `.posting.removing`, waits for the 0.22-second opacity transition, and removes after a 260 ms fallback.                    | The signed-in bookmark flow does not reuse this behavior.                                                                                               |
| `web/bookmarks.html:71-116`                                | Renders a live empty state only when the initial server response has no postings.                                               | The signed-in page cannot reveal its empty state after JavaScript removes the last card.                                                                |
| `web/source-filter.js:43-53`                               | Caches initial card nodes for source and text filtering.                                                                        | Removed nodes can produce a false non-empty filter result unless disconnected nodes are ignored.                                                        |
| `README.md:21-24` and `README.ko.md:21-24`                 | Use `dashboard.png` and `dashboard-dark.png` for theme-aware screenshots.                                                       | The captured page has 날짜순 active, and the alternative text does not name the sort.                                                                   |
| Root README introduction, install, preview, and build sections | Present a local single binary and localhost installation as the main usable product path while `jobcron.app` is still coming soon. | The launch-facing hierarchy should make the hosted app primary without removing advanced local operation.                                               |
| `web/archive.html:55-57`                                   | Provides working 날짜순 and 점수순 links. `/?sort=score` activates the flat descending-score view.                              | No application behavior change is needed.                                                                                                               |

## Root Causes

### Missing key durability

The production design moved user data to PostgreSQL but kept BYOK credentials in
a local `0600` file by design. The Compose stack added persistence for Caddy but
never mounted the app's configuration directory, so container filesystem
lifecycle and credential lifecycle are incorrectly coupled.

### Unexecutable bootstrap boundary

The deployment guide describes the operator command but not the network path to
private RDS. Shipping only the server binary is a sound production-image choice,
yet it means the helper commands must run from a trusted source checkout through
an explicit localhost SSH tunnel via EC2.

### Stale bookmarks UI

The bookmark client was written as a cross-page icon toggle, so its successful
response handler reconciles only the button. The signed-in page also assumes its
empty state is decided only during server rendering.

### Misrepresentative screenshots

The screenshots were captured while the archive's default 날짜순 mode was active.

### Competing public entry points

The READMEs accurately document local operation, but their opening and install
hierarchy makes localhost look like the product's long-term default. That is no
longer the intended launch path: the hosted app should serve ordinary users,
while local binaries, source builds, and previews remain available for advanced
use.

## Execution Boundary

| Agent-owned in this spec                                                                    | Human-owned in the handoff spec                                                                                                                                         |
| ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Compose volume, deploy documentation, signed-in bookmark behavior, tests, and README hierarchy/assets | AWS state, security groups, DNS, Docker Hub push, real `.env`, secrets, owner identity, import approval, API-key entry, production verification, and rollback execution |

The implementation agent must stop before mutating AWS, Docker Hub, DNS, RDS, or
the production host. No real credential may enter a tracked file, test fixture,
tool output, commit, or chat message.

## Proposed Change

### P0-A: Persist the production app configuration directory

Update `deploy/production/compose.yaml` so the app service mounts a stable,
explicitly named Docker volume:

```yaml
services:
  app:
    volumes:
      - jobcron_config:/root/.config/jobcron

volumes:
  jobcron_config:
    name: jobcron_config
  caddy_data:
  caddy_config:
```

Implementation requirements:

1. Use the container path `/root/.config/jobcron`, which contains
   `ai_keys.json` under the current root-run Alpine image.
2. Give the volume the explicit engine name `jobcron_config` so it is stable if
   the Compose project directory or project name changes.
3. Do not mount the repository, a host home directory, or the key file itself.
   A directory-level named volume preserves atomic rename behavior.
4. Keep API keys out of PostgreSQL, `.env`, Compose environment variables, image
   layers, logs, fixtures, and Git.
5. Document that `docker compose down` preserves the volume but
   `docker compose down -v` deletes it and must not be used during routine deploys.
6. Document recovery truthfully: the named volume survives container replacement
   on the same EC2 host, but host loss still requires secure backup or key
   re-entry.
7. Prove persistence with a non-secret sentinel file across
   `docker compose up -d --force-recreate app`; never use a real or fake API key
   as the test value.

### P0-B: Document a runnable private-RDS owner and import path

Keep the production image minimal. Do not add `jobcron-user` or `jobcron-import`
to it. Update the production guide to use a trusted local source checkout and a
localhost-only SSH tunnel through EC2.

The guide must specify this order:

1. Start the production app once so PostgreSQL migrations finish.
2. Open a separate terminal with a localhost-bound tunnel:

   ```sh
   ssh -o ExitOnForwardFailure=yes -N \
     -L 127.0.0.1:15432:<rds-endpoint>:5432 \
     ec2-user@<ec2-public-host>
   ```

3. In the trusted local checkout, define a tunneled PostgreSQL URL whose host is
   `127.0.0.1:15432` and whose query includes `sslmode=require`. The real value
   stays in the operator's shell or password manager, never in documentation.
4. Create the owner first with `jobcron-user`. Let the command prompt for the
   password instead of putting it in shell history:

   ```sh
   go run ./cmd/jobcron-user create-owner \
     --database-url "$TUNNELED_DATABASE_URL" \
     --email "$OWNER_EMAIL"
   ```

5. If the human approves migration, run `jobcron-import` first with `--dry-run`,
   then without it, using the same owner email:

   ```sh
   go run ./cmd/jobcron-import \
     --sqlite '<recovered-sqlite-path>' \
     --postgres "$TUNNELED_DATABASE_URL" \
     --owner-email "$OWNER_EMAIL" \
     --dry-run

   go run ./cmd/jobcron-import \
     --sqlite '<recovered-sqlite-path>' \
     --postgres "$TUNNELED_DATABASE_URL" \
     --owner-email "$OWNER_EMAIL"
   ```

6. Explain why owner-first and same-email are required. The importer preserves an
   existing owner's password hash on email conflict, but a different email would
   attach imported user-scoped state to the wrong account.
7. State exactly what is imported: profile, postings, scores, bookmarks,
   not-interested state, AI extractions, AI scores, and AI usage. State exactly
   what is not imported: `ai_keys.json`, sessions, and production secrets.
8. Tell the operator to close the SSH tunnel after bootstrap and to enter the
   Anthropic key through the signed-in UI only after the durable volume exists.
9. Replace hard-coded assumptions about an already-published image tag with an
   immutable release-tag placeholder that the human checklist supplies.

### P1: Make signed-in unbookmarking finish on `/bookmarks`

Use the existing `.posting.removing` transition and 260 ms hard-removal fallback.
Do not add another animation style and do not change demo behavior.

```text
Signed-in /bookmarks unbookmark click
                    |
       DELETE returns {"bookmarked": false}
                    |
          add .posting.removing
                    |
         transitionend or 260 ms
                    |
             remove card node
                    |
                    v
  update count, live empty state, and active filters

Request failure or final bookmarked=true
                    |
                    v
     keep card and reconcile bookmark button
```

Implementation requirements:

1. Add a small `fadeRemove` helper to `web/bookmark.js` matching
   `web/not-interested.js:70-83` and reusing `.posting.removing`.
2. Invoke it only when all three conditions hold: the request succeeded, the
   final `bookmarked` value is `false`, and `location.pathname` is `/bookmarks`.
3. Leave the existing demo branch at `web/bookmark.js:67-72` behaviorally
   unchanged. Do not add a demo removal transition.
4. After live removal, update `저장된 공고` from remaining connected cards. When
   the last card leaves, hide the empty list and reveal the existing signed-in
   empty-state copy.
5. Render a hidden live empty-state target when the initial signed-in response has
   postings. Do not change the demo empty-state branch or copy.
6. Dispatch a generic posting-list change event after live removal. Reapply the
   active source and text filters, ignoring disconnected card nodes. If other
   bookmarks remain but none match the active filter, show the existing
   filter-specific message rather than the page-level no-bookmarks state.
7. A failed request must keep the card, restore the original icon state, and
   leave count and empty-state UI unchanged.
8. Do not reload or navigate the page to obtain the updated state.

### P2: Make the READMEs hosted-first and replace screenshots with 점수순 captures

Reorganize both root READMEs around the same product hierarchy:

1. `demo.jobcron.app` is the live read-only evaluation path.
2. `jobcron.app` is the upcoming primary full-product path; do not claim that it
   is already public.
3. Normal usage instructions describe the shared product UI.
4. Release binaries, first-run notes, the writable localhost preview,
   build-from-source instructions, and local PostgreSQL development live under
   an `Advanced local use` section and its Korean equivalent.
5. Do not remove a local command or change SQLite behavior in this launch task.

Capture the signed-in 전체 공고 page at `/?sort=score` in both themes and replace:

- `docs/assets/screenshots/dashboard.png`
- `docs/assets/screenshots/dashboard-dark.png`

Capture requirements:

- Use a 1440 x 900 viewport and keep both files as 1440 x 900 PNG images.
- Make `점수순` visibly active and ensure kept postings descend by score.
- Use the 전체 source pill, clear text search, and keep the low-score section in
  its normal collapsed state.
- Capture light mode into `dashboard.png` and dark mode into
  `dashboard-dark.png`.
- Use public-safe job data. Do not expose an API key, owner email, profile data,
  session value, database address, or other private configuration.
- Keep the existing theme-aware `<picture>` behavior. Update alternative text in
  English for `README.md` and Korean for `README.ko.md` to name score ordering.

## Dependency Graph

```text
P0-A durable app volume ───────────────┐
                                      ├─> human launch checklist can begin
P0-B private-RDS bootstrap runbook ────┘

P1 signed-in bookmark lifecycle ──> automated tests ──> signed-in browser QA

P2 hosted-first hierarchy ──> score-sorted page ──> light capture ─┬─> rendered README check
                                                 └─> dark capture ──┘

All agent verification ──> publication-safety review ──> implementation handoff
```

P0-A and P0-B are hard launch gates because proceeding without them can lose the
API key or strand owner/data setup behind the private network boundary. P1 and P2
are user-facing launch polish and can proceed independently. Capture screenshots
last so the documentation represents the verified release candidate.

## Acceptance Criteria

### Durable configuration

1. Rendered production Compose config mounts the explicitly named
   `jobcron_config` volume at `/root/.config/jobcron` on the app service.
2. Caddy's existing volumes remain unchanged and no host bind mount is added for
   app configuration.
3. A non-secret sentinel created in the app config volume remains present after
   app-container force recreation.
4. `docker compose down` followed by `up -d` preserves the volume; the guide warns
   that `down -v` is destructive.
5. No credential value appears in Compose, `.env.example`, tests, image history,
   documentation, staged diff, or logs captured for verification.

### Private-RDS bootstrap and import

6. The production guide contains a localhost-bound SSH tunnel command and does
   not tell operators to make RDS public.
7. The guide starts the app for migrations before owner creation or import.
8. Owner creation uses the local trusted checkout, secure password prompt, and
   tunneled PostgreSQL URL.
9. Optional import includes dry-run and real commands with exact current flags:
   `--sqlite`, `--postgres`, and `--owner-email`.
10. The guide requires owner creation before import and the same owner email for
    both commands.
11. The guide lists all imported tables/state and explicitly says
    `ai_keys.json`, sessions, and production secrets are not imported.
12. The production image remains server-only; it does not gain operator binaries,
    shells beyond the existing base image, or source code.

### Signed-in bookmarks

13. On signed-in `/bookmarks`, a successful response with
    `{"bookmarked": false}` adds `.removing` and removes the clicked card after
    `transitionend` or the 260 ms fallback, without reload.
14. A failed request leaves the card in the DOM, restores the original icon, and
    leaves count and empty state unchanged.
15. A successful response whose final state is `bookmarked: true` leaves the card
    in the DOM.
16. Removing one of two bookmarks changes the signed-in header count from 2 to 1
    after the card leaves the DOM.
17. Removing the final bookmark changes the count to 0, hides the empty list, and
    reveals the signed-in empty-state message.
18. With a source pill or text search active, removing the final matching card
    updates the filter empty message; disconnected nodes cannot keep a false
    non-empty state.
19. Unbookmarking outside signed-in `/bookmarks` does not remove a card through
    the bookmark script.
20. The demo localStorage branch, demo deployment files, and visible behavior at
    `demo.jobcron.app` remain unchanged.

### README assets and full regression

21. Both dashboard assets are 1440 x 900 PNGs showing 점수순 active with visible
    kept postings ordered from highest score to lowest.
22. Both READMEs retain theme-aware `<picture>` markup and localized alternative
    text that identifies score ordering.
23. No screenshot contains credentials, private account data, or production
    infrastructure details.
24. Existing 관심 없음 removal, bookmark API, per-user storage, CSRF handling,
    production auth, Caddy ingress, and demo read-only behavior remain unchanged.
25. All checks in the testing plan pass before commit.
26. Both READMEs present `jobcron.app` as the upcoming primary full-product path,
    `demo.jobcron.app` as the live read-only evaluation path, and local
    install/build/preview workflows as advanced use without removing them.

## Testing Plan

| Layer                | What                                                                                                                                                                                                                               |          Minimum cases |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------: |
| Compose static       | Render with non-secret dummy environment values; assert one app config mount, explicit volume name, unchanged Caddy volumes, and no forbidden demo/admin/proxy/worknet variables.                                                  |                      5 |
| Compose lifecycle    | Build/load the production image, create a non-secret sentinel in `jobcron_config`, force-recreate app, and assert the sentinel remains.                                                                                            |                      1 |
| Operator commands    | Run existing `jobcron-user` and `jobcron-import` tests, including owner-password preservation, dry-run counts, transaction rollback, and imported state.                                                                           |         Existing suite |
| Runbook review       | Execute every non-secret command form against local test infrastructure or `--dry-run`; validate exact flags and ordering.                                                                                                         |                      6 |
| JavaScript lifecycle | Add `web/testdata/bookmark-lifecycle.test.js` covering signed-in success, HTTP failure rollback, contradictory final state, non-bookmarks routes, count, last-card empty state, active-filter recomputation, and timeout fallback. |                      8 |
| Go wrapper           | Add `web/bookmark_test.go` following `web/ai_rerate_test.go` so the lifecycle harness runs under `go test ./...`.                                                                                                                  |                      1 |
| Server/template      | Extend `internal/server/bookmarks_test.go` to prove a non-empty signed-in page includes a hidden live empty-state target without changing demo markup.                                                                             |                      2 |
| Browser user path    | With `/browse`, unbookmark the first and last signed-in cards, verify count and empty states without reload, refresh to confirm persistence, and exercise source and text filters.                                                 |                5 flows |
| Demo regression      | Walk `demo.jobcron.app` bookmark behavior without editing it and run the existing demo tests.                                                                                                                                      |      Existing behavior |
| Visual QA            | Walk every signed-in app page on desktop and mobile in light and dark themes with no console errors, then inspect both README images.                                                                                              | 2 viewports x 2 themes |
| Project regression   | Run `gofmt -l .`, `go vet ./...`, `go test ./...`, `go build ./cmd/jobcron ./cmd/jobcron-user ./cmd/jobcron-import`, direct Node lifecycle tests, and production Compose validation.                                               |                    All |
| Publication safety   | Inspect the complete staged diff, run Gitleaks and the public-repo redaction scan, and manually inspect both PNGs.                                                                                                                 |               4 checks |

Browser verification must follow the real user path. HTTP-only checks cannot prove
animation, count/empty-state behavior, private-RDS operator usability, or image
content.

## Rollback Plan

- Revert the Compose and production-doc changes together if the volume or runbook
  fails verification. Do not delete `jobcron_config`; preserving it is safer than
  cleanup because it may contain a paid API credential.
- Revert bookmark JavaScript, template, filter, and lifecycle tests together. The
  server API and stored bookmarks are unchanged, so no data rollback is needed.
- Restore the previous PNG assets and alternative text if a screenshot is wrong.
- The implementation in this spec does not mutate production, so AWS, DNS, RDS,
  and live image rollback belong to the human handoff.

## Effort Estimate

| Work                                        | Human estimate | Codex + gstack estimate |
| ------------------------------------------- | -------------: | ----------------------: |
| Durable app volume and Compose verification |    1-1.5 hours |           20-30 minutes |
| Private-RDS owner/import runbook            |    1.5-2 hours |           25-40 minutes |
| Signed-in bookmark lifecycle and tests      |      3-4 hours |           50-75 minutes |
| Browser QA and README captures              |    1.5-2 hours |           30-45 minutes |
| Final regression and publication review     |    1-1.5 hours |           20-30 minutes |
| **Total**                                   | **8-11 hours** |     **145-220 minutes** |

## Files Reference

| File                                                                | Change                                                                                                                                      |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `deploy/production/compose.yaml:8-46`                               | Add the explicit `jobcron_config` named volume and app mount.                                                                               |
| `deploy/production/HUMAN_DEPLOY_GUIDE.md:16-205`                    | Document immutable tag input, volume lifecycle, private-RDS tunnel, owner-first bootstrap, optional import, key re-entry, and safe handoff. |
| `deploy/production/README.md:1-42`                                  | Summarize the durable config and operator path; keep validation expectations aligned.                                                       |
| `deploy/production/.env.example:1-15`                               | Replace stale concrete tag assumptions with a safe immutable-tag placeholder if needed.                                                     |
| `internal/ai/keys.go:12-86`                                         | Reference only. Preserve the existing path, atomic write, and `0600` behavior.                                                              |
| `cmd/jobcron-user/main.go:32-89`                                    | Reference only. Preserve secure prompt and owner semantics.                                                                                 |
| `cmd/jobcron-import/main.go:36-177`                                 | Reference only. Preserve transactional import and password-hash behavior.                                                                   |
| `deploy/production/Dockerfile:5-19`                                 | Do not add operator binaries; verify the image remains server-only.                                                                         |
| `web/bookmark.js:59-107`                                            | Add signed-in `/bookmarks` removal and live page-state updates without changing the demo branch.                                            |
| `web/bookmarks.html:71-116`                                         | Keep a revealable signed-in empty-state target without changing demo copy or behavior.                                                      |
| `web/source-filter.js:43-53,89-121`                                 | Reapply filters after live list changes and ignore disconnected cards.                                                                      |
| `web/styles.css:765-770`                                            | Reuse the existing transition; change only if verification exposes a shared bug.                                                            |
| `web/not-interested.js:70-83`                                       | Reference only. Do not change 관심 없음 behavior.                                                                                           |
| `web/testdata/bookmark-lifecycle.test.js`                           | Add signed-in JavaScript lifecycle coverage.                                                                                                |
| `web/bookmark_test.go`                                              | Run the JavaScript harness from the Go suite.                                                                                               |
| `internal/server/bookmarks_test.go`                                 | Verify the live empty-state target and preserve existing API/page coverage.                                                                 |
| `docs/assets/screenshots/dashboard.png`                             | Replace with the light score-sorted capture.                                                                                                |
| `docs/assets/screenshots/dashboard-dark.png`                        | Replace with the dark score-sorted capture.                                                                                                 |
| `README.md:21-24`                                                   | Update English alternative text.                                                                                                            |
| `README.ko.md:21-24`                                                | Update Korean alternative text.                                                                                                             |
| `docs/superpowers/specs/260713-alpha-launch-human-blocked-steps.md` | Hand off credentials and external-state actions without embedding their values.                                                             |

## What's Working Well: Do Not Touch

- `ai_keys.json` remains a local `0600` file with atomic replacement. It must not
  move into PostgreSQL or an environment variable.
- The production image remains a minimal server image. Operator tools run from a
  trusted checkout through a tunnel.
- RDS remains private; never open PostgreSQL `5432` to the public internet for
  convenience.
- Caddy remains the only public ingress. `JOBCRON_PROXY_SECRET`,
  `JOBCRON_DEMO`, `JOBCRON_ADMIN_TOKEN`, and `JOBCRON_WORKNET_KEY` remain unset
  for the first production pass.
- The bookmark API, optimistic icon update, disabled-button guard, CSRF header,
  and failure rollback are correct.
- The 관심 없음 flow is the motion reference and stays unchanged.
- The 전체 공고 sort implementation and remembered sort cookie are correct.
- `deploy/demo/` and `demo.jobcron.app` are not implementation targets.

## Out of Scope

- Executing any human-blocked step, deploying `jobcron.app`, or changing external
  infrastructure from this implementation task.
- Editing or redeploying `demo.jobcron.app`.
- Making RDS public or adding operator binaries to the production image.
- Importing API keys, sessions, passwords, or production secrets from SQLite.
- Changing the default 전체 공고 sort from 날짜순 to 점수순.
- Visually redesigning the README, changing screenshot dimensions, or adding
  screenshots beyond the two existing theme variants.
- Removing local binaries, source builds, SQLite, or the writable preview before
  launch. PostgreSQL convergence is the first post-launch persistence task, not
  part of this implementation.
- Changing bookmark storage, API contract, authentication, or database schema.
- Removing muted cards from `/bookmarks`; muted bookmarked postings remain there.
- Adding Cloudflare proxying, Worknet, a proxy secret, Multi-AZ, or a new
  deployment platform.

## Definition of Done

The work is complete when all 26 acceptance criteria pass, the two repository
deployment blockers are fixed, Overseer's no-demo direction is verified, the
signed-in bookmark flow and hosted-first rendered READMEs pass browser review, publication
safety checks are clean, and the human can follow the separate handoff without
needing an implementation decision from the agent.
