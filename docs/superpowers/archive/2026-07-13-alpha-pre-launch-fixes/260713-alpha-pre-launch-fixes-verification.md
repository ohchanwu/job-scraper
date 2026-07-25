# Alpha Pre-Launch Fixes Verification

**Status:** Agent-owned implementation verified; human launch steps remain<br>
**Verified commit:** `77be565`

## Delivered

- Durable same-host BYOK key volume
- Private-RDS owner and optional import runbook
- Signed-in in-place bookmark removal and synchronized page state
- Hosted-first English and Korean README journeys with score-sorted images

## Automated Verification

- `test -z "$(gofmt -l .)"`, `go vet ./...`, and the full `go test ./...`
  suite passed.
- PostgreSQL operator-package tests passed for `cmd/jobcron-user` and
  `cmd/jobcron-import` against the disposable local Compose database.
- `go build` passed for `jobcron`, `jobcron-user`, and `jobcron-import`.
- Both browser-lifecycle Node suites passed; the new bookmark suite reported
  10/10 passing cases, including timeout, HTTP failure, filter, search, and demo
  branches.
- Production Compose rendered successfully with sentinel values and its static
  assertions confirmed one application config mount with unchanged Caddy
  mounts.
- A disposable production-style container lifecycle confirmed the named config
  volume survives force recreation and down/up while using only sentinel
  credentials. The temporary container, volume, and network were removed.

## Browser Verification

- `/`, `/briefing`, `/bookmarks`, `/hidden`, and `/profile` were walked at
  1440x900 and 390x844 in both light and dark themes.
- Primary navigation, source filtering, text search, theme switching, a real
  posting destination, bookmark toggling/removal, the adjacent hide flow, and
  AI-provider controls were exercised through the rendered UI.
- Signed-in bookmark removal persisted across reloads and produced the correct
  source-filter, text-search, and final page-level empty states.
- All 20 page/theme/viewport combinations had no horizontal overflow or console
  errors. Representative screenshots were inspected for clipping, hierarchy,
  and legibility.
- `https://demo.jobcron.app` remained read-only: its bookmark state used
  `jobScraperDemoBookmarks`, disappeared immediately on `/bookmarks`, added no
  `.removing` transition, and issued no bookmark mutation request.
- Local review preview: `http://127.0.0.1:17780`

## Publication Safety

- The cumulative implementation and staged documentation diffs were reviewed;
  no `deploy/demo` file or prohibited production surface changed.
- Gitleaks passed on the staged documentation diff with redaction enabled.
- Manual semantic review found no credentials, private endpoints, owner
  identity, recovery paths, or unnecessary production identifiers.
- Both public PNGs and their localized alternative text were inspected; the
  images show the same score-sorted briefing in light and dark themes.

## Remaining Human Boundary

The [active human-blocked launch checklist](../../specs/260726-terraform-first-production-launch-human-blocked-steps.md)
owns AWS, DNS, Docker Hub, production secrets, owner identity, import approval,
API-key entry, go-live, and rollback. None of those actions were executed.
