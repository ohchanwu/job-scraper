# AWS production deploy: jobcron.app

## Slice 4 status

This directory now implements the replacement-host runtime, but running it is a
separate human-authorized operation. The operator must begin from the exact
Slice 3 checkpoint, publish the approved private `linux/arm64` image through the
repository workflow, and record its immutable digest in private evidence.
Terraform may apply only the independently reviewed saved plan accepted by
`scripts/check-terraform-slice-4-plan.sh`.

The replacement host has no key pair or inbound rule. All host and database
access uses AWS Systems Manager Session Manager. RDS stays private, and the app
uses a lower-privilege database role created through a localhost-only tunnel.
The reserved EIP remains unattached, and the operator stops before any
Cloudflare, DNS, or public-traffic action.

## Runtime contract

Production secrets do not live in the repository, Terraform state, or a host
environment file. `jobcron.service` retrieves the approved Secrets Manager
version into `/run/jobcron`, validates the exact key set, pulls only the
approved digest, and starts Compose through systemd. The one-shot
`read:packages` registry token is separate from the runtime secret and is
deleted with its temporary Docker configuration after the pull.

The runtime is fail-closed: incomplete secrets, unexpected values, bad file
modes, or a failed pull leave both containers stopped and no partial runtime
files. On reboot, the memory-backed `/run/jobcron` directory is empty and
systemd recreates it from the approved secret. `verify-local-state` reports
value-blind booleans and counts for file modes, log rotation, digest retention,
Docker credentials, and free disk space.

## Files

- `compose.yaml` consumes `/run/jobcron/compose.env`, pulls the approved
  immutable image, reaches private RDS through `DATABASE_URL`, and retains only
  Caddy's standard volumes.
- `Caddyfile` uses transient Origin CA material from `/run/jobcron/caddy`,
  redirects `www.jobcron.app`, and keeps app access private.
- `.env.example` contains synthetic local-render inputs only.
- `Dockerfile` builds the release image used by the private publication
  workflow.
- `jobcron-runtime.sh` implements `prepare`, `pull`, `archive`, and
  `verify-local-state`.
- `systemd/` contains the fail-closed app service and nightly recovery units.
- `HUMAN_DEPLOY_GUIDE.md` defines the exact-plan, Session Manager, private
  verification, recovery manifest, reboot, and stop-condition sequence.

## Validate Compose locally

Use synthetic values only:

```sh
cd deploy/production
JOBCRON_IMAGE='ghcr.io/example/jobcron@sha256:0000000000000000000000000000000000000000000000000000000000000000' \
DATABASE_URL='postgres://example:example@db.example.invalid:5432/example?sslmode=require' \
SESSION_SECRET=synthetic-session-secret \
JOBCRON_CREDENTIAL_ENCRYPTION_KEY='MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=' \
JOBCRON_PROXY_SECRET='synthetic-proxy-secret' \
JOBCRON_SIGNUP_ACCESS_CODE='synthetic-cohort-code' \
JOBCRON_STAGE1_SPONSOR_USER_ID='1' \
docker compose config
```

The rendered config must publish only loopback host ports `7777` and `8443`;
include the database, session, credential-key, production, no-open, scheduler,
and signup settings; and contain no app filesystem or legacy credential volume.
It must not include demo mode, an admin token, a Worknet key, or a
caller-supplied trusted-proxy header. Caddy and the app receive the same proxy
secret so only Caddy can supply the client address used by authentication rate
limits.

## Private operations and recovery

Private verification uses Session Manager port forwarding to the loopback app
and Caddy ports. The operator checks real user behavior, the Origin CA
certificate, lower-privilege RDS access, reboot recovery, and the absence of
public ingress before recording sanitized evidence.

`jobcron-recovery.service` creates a custom-format database dump, sanitized
container logs, and SHA-256 recovery manifests. The trusted Mac runs
`scripts/pull-production-recovery.sh` to copy missing objects, verify every
manifest, and apply only the `macbook-copy=verified` tag. Restore verification
uses a disposable database and bounded schema and row-count comparisons.

Stop conditions include any checkpoint mismatch, unapproved saved-plan action,
private value in shared output, public ingress, incomplete runtime secret,
failed private user path, failed recovery verification, or less than 2 GiB free
after normal pruning. Keep the old host, old database, reserved EIP, prior
image, and recovery materials throughout the rollback window.
