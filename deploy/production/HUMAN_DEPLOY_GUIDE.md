# Human deploy guide for jobcron.app production

## Terraform launch status

Terraform now owns the adopted canonical public-network foundation and one
unattached reserved EIP. The existing EC2 and RDS resources are unchanged.
Do not execute the remaining rollout steps yet: later Terraform slices must
replace their stale manual-host, SSH, certificate, and cutover assumptions.

This is the first deployment of the production jobcron app to a blank AWS EC2
host. No app or Docker stack is currently deployed. The operator builds the
approved image on a Mac, keeps AWS RDS PostgreSQL private, and lets Caddy manage
HTTPS after the verified import is complete.

The existing EC2 `.env` already contains the configured database URL, session
secret, environment, host, port, no-open setting, and daily scrape time. Preserve
those values. This rollout adds only the immutable image reference and the
production credential master key before the first app start.

Production has no legacy credential volume, `jobcron_config` volume, or
migration container. The production app stores encrypted BYOK credentials in
PostgreSQL. A retained local `ai_keys.json` is an optional legacy import source
on the trusted Mac only; do not copy it to EC2.

Do not put real secrets, host addresses, database identifiers, account identity,
source paths, personal data, or captured logs in Git, issues, or chat. Keep exact
deployment evidence in the access-controlled operator log.

## 1. Preserve the rollback materials

Before changing the host or database, retain these materials through the human-
approved rollback window:

- the original SQLite snapshot and its durable `-wal` file;
- the optional legacy key file used as the local import source;
- a separate backup of the production credential master key; and
- the pre-import RDS snapshot created later in this guide.

Keep these materials outside Git and do not move the SQLite database or legacy
key file to EC2. Stop the legacy local app before import and leave the retained
source unchanged through verification.

## 2. Build and publish the approved immutable image

From the approved release checkout on the operator's Mac:

```sh
git status --short
git rev-parse HEAD

RELEASE_TAG="sha-$(git rev-parse --short=12 HEAD)"
IMAGE="<registry-user>/jobcron:$RELEASE_TAG"

docker login
docker buildx build \
  --platform linux/arm64 \
  -f deploy/production/Dockerfile \
  -t "$IMAGE" \
  --push .
docker buildx imagetools inspect "$IMAGE"
```

Stop if the checkout is dirty or the inspected image does not match the approved
commit and architecture. Record the digest only in the access-controlled
operator log.

## 3. Confirm the blank EC2 host, private RDS, and DNS

Confirm that no app container or Docker stack is running on EC2. Keep RDS public
access disabled. Its security group must allow PostgreSQL `5432` only from the
EC2 instance security group, and the configured URL must require TLS.

Cloudflare proxy remains off for the first pass. Confirm that the apex A record
points to the EC2 host and that `www` is a CNAME to the apex. Use placeholders in
shared documentation; record exact addresses only in the operator log.

## 4. Install Docker and place the approved deployment files

On the expected Amazon Linux 2023 host:

```sh
sudo dnf install -y docker
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
```

Reconnect once, install the Compose v2 plugin for the host architecture, then
verify `docker version` and `docker compose version`. Obtain the exact approved
commit under the production application directory without rewriting history:

```sh
cd <production-repository-directory>
git rev-parse HEAD
git status --short
```

The commit must equal the approved release commit and the tree must be clean.
Authenticate to the image registry on EC2 only if the registry requires it.

## 5. Preserve `.env` and add only the missing entries

Back up the existing EC2 `.env` with owner-only permissions. Do not replace its
database URL, session secret, environment, host, port, no-open setting, or daily
scrape time (`JOBCRON_DAILY_SCRAPE_TIME`). Add only:

```dotenv
JOBCRON_IMAGE=<registry-user>/jobcron:sha-<12-character-commit>
JOBCRON_CREDENTIAL_ENCRYPTION_KEY=<base64-32-byte-master-key>
JOBCRON_PROXY_SECRET=<random-shared-proxy-secret>
JOBCRON_SIGNUP_ACCESS_CODE=<cohort-access-code>
JOBCRON_STAGE1_SPONSOR_USER_ID=<sponsor-user-id>
```

Generate the credential master key on a trusted machine, keep a separate secure
backup, and validate that it decodes to exactly 32 bytes without printing it.
Generate the proxy secret independently; it is shared only by Caddy and the app
so authentication throttles use the real client address. Keep demo mode, the
legacy admin token, and the Worknet key unset for this first pass.
The signup code gates the initial cohort. The sponsor user ID assigns global
Stage 1A billing only; it grants no authorization and creates no application role.
Email ownership is not verified, and forgotten-password recovery remains
operator-assisted until the public-signup follow-up is implemented.

Validate Compose before starting anything:

```sh
cd <production-compose-directory>
(
  set -eu
  umask 077
  rendered_compose="$(
    mktemp "${TMPDIR:-/tmp}/jobcron-production-compose.XXXXXX" 2>/dev/null
  )" || {
    printf '%s\n' 'production Compose validation failed: private render file' >&2
    exit 1
  }
  trap 'rm -f "$rendered_compose" >/dev/null 2>&1 || :' EXIT
  trap 'exit 1' HUP INT TERM
  if ! docker compose --env-file .env config \
    2>/dev/null > "$rendered_compose"; then
    printf '%s\n' 'production Compose validation failed: render' >&2
    exit 1
  fi
  if ! sh ../../scripts/inspect-production-compose-render.sh \
    "$rendered_compose"; then
    exit 1
  fi
)
```

The private temporary file contains rendered secrets, so the subshell creates it
with owner-only permissions and removes it on success, failure, or interruption.
The portable inspector uses core `grep -E -q` and handles its statuses
explicitly: `0` rejects a legacy match, `1` passes, and any status above `1`
fails with a fixed value-blind inspection error. Tool diagnostics are
suppressed so caller-controlled temporary paths and rendered values cannot leak
through a failure message.

Do not pull or start the app yet. Owner creation and import happen from the Mac
through the private-RDS tunnel before the first app start. `jobcron-user` opens
PostgreSQL and applies the embedded migrations, so no migration container or
early app start is needed.

## 6. Open the localhost-only tunnel from the Mac

In a dedicated terminal on the trusted Mac, open the tunnel and leave it running
through owner creation, import, and verification:

```sh
ssh -o ExitOnForwardFailure=yes -N \
  -L 127.0.0.1:15432:<private-rds-host>:5432 \
  <ec2-user>@<ec2-host>
```

Never make RDS public. In a second terminal at the trusted source checkout, use
the default macOS `zsh` to read the tunneled URL and the same protected
production master key added to the EC2 `.env` without echoing either value. Read
the owner email without placing it in command history, then export all values
only in the current shell:

```zsh
read -r -s 'TUNNELED_DATABASE_URL?Tunneled PostgreSQL URL: '
printf '\n'
export TUNNELED_DATABASE_URL

read -r 'OWNER_EMAIL?Owner email: '
export OWNER_EMAIL

read -r -s 'JOBCRON_CREDENTIAL_ENCRYPTION_KEY?Production credential master key: '
printf '\n'
export JOBCRON_CREDENTIAL_ENCRYPTION_KEY

export JOBCRON_ENV=production
```

Validate the decoded key length without printing the key. Do not put these
values in a shell profile, command transcript, or tracked file. Production mode
makes the importer fail closed if the master key is missing or invalid instead
of falling back to a local development key.

## 7. Create exactly one owner before import

Let `jobcron-user` prompt securely for the new password:

```sh
go run ./cmd/jobcron-user create-owner \
  --database-url "$TUNNELED_DATABASE_URL" \
  --email "$OWNER_EMAIL"
```

The command opens PostgreSQL and applies the embedded migrations. After success,
confirm through an approved query that the user count is exactly one. Do not run
`create-owner` again.

For a later password reset, target the exact account address and supply the new
password only through the command-specific environment variable:

```zsh
read -r 'USER_EMAIL?User email: '
export USER_EMAIL
read -r -s 'JOBCRON_USER_PASSWORD?Replacement password: '
printf '\n'
export JOBCRON_USER_PASSWORD

go run ./cmd/jobcron-user reset-password \
  --database-url "$TUNNELED_DATABASE_URL" \
  --email "$USER_EMAIL"

unset JOBCRON_USER_PASSWORD USER_EMAIL
```

The reset revokes every session for that account. To permanently delete one
account and its private state, require the operator to repeat the same address:

```zsh
read -r 'USER_EMAIL?User email: '
export USER_EMAIL

go run ./cmd/jobcron-user delete-user \
  --database-url "$TUNNELED_DATABASE_URL" \
  --email "$USER_EMAIL" \
  --confirm-email "$USER_EMAIL"

unset USER_EMAIL
```

Deletion cascades the selected account's private profile, saved-job, score, AI,
credential, import, and session state. Shared postings, global extraction facts,
and scrape-run history remain.

## 8. Create the pre-import RDS snapshot

Create a manual pre-import RDS snapshot through the approved AWS operator
surface and wait until its status is `available`. Record its identifier only in
the access-controlled operator log. Do not begin the importer dry-run before the
snapshot is available.

## 9. Dry-run, review, and apply the import

Stop the legacy local app and confirm it has exited. Invoke the importer from the
trusted Mac without `--apply`; dry-run is the default:

```sh
go run ./cmd/jobcron-import \
  --sqlite '<immutable-sqlite-snapshot>' \
  --postgres "$TUNNELED_DATABASE_URL" \
  --owner-email "$OWNER_EMAIL" \
  --ai-keys '<optional-legacy-key-file>'
```

Omit `--ai-keys` when no legacy key file was approved. Before any write, review:

- the immutable source fingerprint;
- all eight category counts: profile, postings, rule scores, bookmarks, hidden
  jobs, AI extractions, AI scores, and AI usage;
- the credential provider count; and
- every collision, with zero unapproved collisions.

Stop on any mismatch. Fix the cause and rerun the same dry-run; do not apply a
result that has not been explicitly approved.

After approval, rerun the identical importer command with `--apply`:

```sh
go run ./cmd/jobcron-import \
  --sqlite '<immutable-sqlite-snapshot>' \
  --postgres "$TUNNELED_DATABASE_URL" \
  --owner-email "$OWNER_EMAIL" \
  --ai-keys '<optional-legacy-key-file>' \
  --apply
```

The importer preserves the newly created owner's password hash. It does not
import sessions or passwords. It may import an approved legacy credential by
re-encrypting it for PostgreSQL with the production master key; it never makes
the legacy key file part of the EC2 deployment.

Rerun the command once more without `--apply`. Expect `already imported` plus
successful verification. Confirm that the owner password hash did not change
and that PostgreSQL contains no plaintext credential.

After verification, clear the values from the trusted shell:

```sh
unset TUNNELED_DATABASE_URL OWNER_EMAIL JOBCRON_CREDENTIAL_ENCRYPTION_KEY \
  JOBCRON_ENV
```

Then stop the dedicated SSH tunnel. Keep it open until verification finishes;
do not leave it running afterward.

## 10. Pull and start production for the first time

Only after owner creation and verified import, run on EC2:

```sh
cd <production-compose-directory>
docker compose --env-file .env pull
docker compose --env-file .env up -d
docker compose --env-file .env ps
docker compose --env-file .env logs --no-color --tail=200 app caddy
```

Do not run `docker compose build`, `docker compose up --build`, or a migration
container on EC2. Expected behavior:

- Caddy alone publishes ports `80` and `443`.
- The app is reachable only inside the Docker network as `app:7777`.
- The app starts against the already imported PostgreSQL database.
- Caddy obtains and stores HTTPS certificates in its own standard volumes.
- No app filesystem or legacy credential volume is mounted.

## 11. Walk the production checks

Use the approved headless browser workflow to verify the real user path:

- HTTPS works with a valid certificate and HTTP redirects to HTTPS.
- `www` redirects to the canonical apex host.
- The owner can sign in with the newly created password.
- Access-code signup creates a separate cohort account without claiming to verify email ownership.
- The migrated profile, one known rule score, one known AI score, one bookmark,
  and one hidden job match the approved source evidence.
- Saving, replacing, and deleting synthetic Anthropic, OpenAI, and Gemini credentials stays
  isolated to the signed-in account through the PostgreSQL-backed credential path.
- Password change, logout/login, and self-service account deletion preserve session and cascade
  boundaries; operator assistance remains the recovery path.
- The daily scrape time matches the preserved EC2 `.env` value, and one global collection is
  followed by sequential per-user analysis.

Keep exact URLs, identities, screenshots, and logs out of tracked documentation.

## 12. Roll back according to the deployment phase

- **Before import commit:** fix the cause and rerun the dry-run. No PostgreSQL
  import rollback is needed because dry-run does not write.
- **After import but before new writes:** restore the pre-import RDS snapshot,
  then investigate and repeat the approved owner/import sequence.
- **After the app accepts new writes:** keep PostgreSQL authoritative. Roll back
  application code or restore PostgreSQL as appropriate; never return to
  SQLite.

Keep the original SQLite snapshot, legacy key file, production master-key
backup, and pre-import RDS snapshot until the human explicitly closes the
rollback window.
