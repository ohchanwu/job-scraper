# AWS production deploy: jobcron.app

## Terraform launch status

Terraform now owns the adopted canonical public-network foundation and one
unattached reserved EIP. The existing EC2 and RDS resources are unchanged.
The remaining host, database, edge, and cutover instructions below are
pre-launch guidance; do not execute them until their later Terraform slices
replace the stale manual assumptions.

This directory defines the first production deployment to a blank EC2 host. No
app or Docker stack is currently deployed. Caddy is the only public entry point,
the app uses private AWS RDS PostgreSQL, and the EC2 `.env` already holds the
configured database URL, session secret, environment, host, port, no-open
setting, and daily scrape time.

For the first rollout, preserve those existing values and add only
`JOBCRON_IMAGE`, `JOBCRON_CREDENTIAL_ENCRYPTION_KEY`, `JOBCRON_PROXY_SECRET`,
`JOBCRON_SIGNUP_ACCESS_CODE=<cohort-access-code>`, and
`JOBCRON_STAGE1_SPONSOR_USER_ID=<sponsor-user-id>`. The sponsor ID assigns
global Stage 1A billing; it is not authorization or an application role. Validate Compose before starting anything. From the
trusted Mac, open the localhost-only tunnel,
silently read private values only into the current shell, and export
`JOBCRON_ENV=production` so import fails closed, run `create-owner`, create the
pre-import RDS snapshot, dry-run `jobcron-import`, review the fingerprint, eight
category counts, provider count, and collisions, then rerun with `--apply`.
Start the app only after verification. See `HUMAN_DEPLOY_GUIDE.md` for the exact
sequence, private temporary-file handling, and phase-specific rollback boundary.

Production has no app filesystem mount, `jobcron_config` volume, legacy
credential volume, or migration container. Encrypted BYOK credentials live in
PostgreSQL. A retained local `ai_keys.json` may be used only as an optional
legacy import source from the trusted Mac; it is not stored on EC2.

## Files

- `compose.yaml` pulls the approved immutable image, reaches RDS through
  `DATABASE_URL`, and retains only Caddy's standard volumes.
- `Caddyfile` redirects `www.jobcron.app` to `jobcron.app` and proxies the
  canonical host to the private app container.
- `.env.example` documents required values with placeholders only.
- `Dockerfile` builds the arm64 image on the trusted build machine.
- `HUMAN_DEPLOY_GUIDE.md` defines the blank-host rollout, private-RDS import,
  first app start, verification, retention, and rollback sequence.

## Validate the compose file locally

Use synthetic values only:

```sh
cd deploy/production
JOBCRON_IMAGE=example/jobcron:sha-000000000000 \
DATABASE_URL='postgres://example:example@db.example.invalid:5432/example?sslmode=require' \
SESSION_SECRET=synthetic-session-secret \
JOBCRON_CREDENTIAL_ENCRYPTION_KEY='MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=' \
JOBCRON_PROXY_SECRET='synthetic-proxy-secret' \
JOBCRON_SIGNUP_ACCESS_CODE='synthetic-cohort-code' \
JOBCRON_STAGE1_SPONSOR_USER_ID='1' \
JOBCRON_DAILY_SCRAPE_TIME='06:15' \
docker compose config
```

The rendered config must expose only Caddy on ports `80` and `443`; keep the app
private on `7777`; include the database, session, credential-key, production,
no-open, scheduler, and daily-time settings; and contain no app filesystem or
legacy credential volume. It must not include demo mode, an admin token, a
Worknet key, or a publicly supplied trusted-proxy header. Caddy and the app must
receive the same proxy secret so only Caddy can supply the client address used
by authentication rate limits.

Compose passes the preserved `JOBCRON_DAILY_SCRAPE_TIME` value into the app;
omitting it uses the safe `05:00` default.

Until a human closes the rollback window, retain the original SQLite snapshot,
optional legacy key file, master-key backup, and pre-import RDS snapshot. Before
import commit, fix and rerun dry-run. After import but before new writes, restore
the snapshot. After new writes, keep PostgreSQL authoritative and roll back code
or PostgreSQL; never return to SQLite.
