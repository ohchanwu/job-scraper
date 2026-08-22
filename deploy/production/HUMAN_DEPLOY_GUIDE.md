# Human deploy guide for jobcron.app production

This is a private Slice 4 replacement-host sequence. It does not authorize a
public cutover. Use only the approved commit, private controller artifacts, and
short-lived operator credentials. Never put secrets, identifiers, addresses,
personal data, screenshots, or raw logs in Git, issues, chat, or shared command
output.

All host access uses AWS Systems Manager Session Manager. RDS remains private,
the replacement host has no key pair or inbound rule, and runtime values exist
on the host only below `/run/jobcron`. Stop immediately on any mismatch; never
infer or substitute a selector.

## 1. Verify the exact Slice 3 checkpoint

Confirm the private checkpoint is current and reports:

- the exact reviewed Slice 3 commit and resource interface;
- private RDS and its unchanged origin-group PostgreSQL rule;
- an empty runtime-secret container with zero versions;
- an encrypted, versioned, public-blocked recovery bucket;
- the reviewed recovery lifecycle: verified off-cloud copies expire after 14
  days, all current versions after 90 days, and the resulting noncurrent data
  version one day later;
- the origin security group is the only resource tagged
  `jobcron:edge-target = origin-security-group`, while the canonical VPC stays
  untagged and is derived from that group's `vpc_id`;
- the reviewed normalization-only state action completed with 0 added,
  0 changed, and 0 destroyed;
- the final refresh inspection contained exactly one update-only AWS-managed
  recovery-observation field, zero resource changes, and zero output changes;
  this is accepted irreducible observation drift and must not be reapplied; and
- zero destroy, replace, or old-resource actions.

Stop if any field, address, lifecycle rule, or freshness check differs. Preserve
the old host, old database, reserved EIP, prior image, and recovery materials.

## 2. Publish the immutable private image

From the exact clean implementation commit, dispatch
`.github/workflows/publish-production-image.yml`. The workflow must use only
`GITHUB_TOKEN`, publish one private `linux/arm64` package, and record the
approved commit and immutable digest in the private `image.json` evidence.

Verify package visibility, platform, commit label, and digest without copying
private package metadata into shared output. The host must consume
`ghcr.io/<owner>/jobcron@sha256:<digest>`, never a mutable tag.

## 3. Generate and review the saved plan

Before loading private inputs, render Compose with `.env.example` and inspect
the private temporary file fail-closed:

```sh
cd deploy/production
umask 077
rendered_compose="$(mktemp)"
trap 'rm -f "$rendered_compose"' EXIT HUP INT TERM
docker compose --env-file .env.example config \
  2>/dev/null >"$rendered_compose"
sh ../../scripts/inspect-production-compose-render.sh \
  "$rendered_compose"
rm -f "$rendered_compose"
trap - EXIT HUP INT TERM
cd ../..
```

Stop if rendering or inspection fails. The temporary file contains synthetic
values only and is removed on success, failure, or interruption.

Load the ignored `controller.env` and private Terraform inputs without printing
them. Create `slice-4.tfplan`, its JSON form, and the aggregate cost evidence in
the protected controller directory. Run:

```sh
scripts/check-terraform-slice-4-plan.sh \
  "$TF_SLICE4_PLAN_JSON" \
  "$TF_AGGREGATE_COST_JSON" \
  "$TF_SLICE3_CHECKPOINT_JSON"
```

Require the exact five creates, one sensitive output create, no other action,
fresh aggregate cost within both ceilings, and a passing Slice 3 checkpoint.
An independent reviewer must approve the exact commit and saved-plan digest.
Regenerating the plan invalidates that review.

If an independently approved recovery must replace an already-created host,
do not reuse the create-plan verdict and do not infer that updating `user_data`
will replay cloud-init. Render the exact evaluated bootstrap privately:

```sh
umask 077
render_json="$(mktemp)"
trap 'rm -f "$render_json" "$TF_SLICE4_RENDERED_USER_DATA"' EXIT HUP INT TERM
terraform -chdir=infra/terraform/production console >"$render_json" <<'EOF'
jsonencode(local.replacement_user_data)
EOF
jq -ejr 'fromjson | if type == "string" and length > 0 then . else error("invalid") end' \
  "$render_json" >"$TF_SLICE4_RENDERED_USER_DATA"
chmod 0600 "$TF_SLICE4_RENDERED_USER_DATA"
rm -f "$render_json"
```

Create the saved plan with the explicit
`-replace=aws_instance.replacement_host` option, render its JSON without
printing it, then run the same checker with the private rendered bootstrap as
the fourth argument:

```sh
scripts/check-terraform-slice-4-plan.sh \
  "$TF_SLICE4_PLAN_JSON" \
  "$TF_AGGREGATE_COST_JSON" \
  "$TF_SLICE3_CHECKPOINT_JSON" \
  "$TF_SLICE4_RENDERED_USER_DATA"
```

This mode requires exactly one destroy-then-create action for the replacement
instance with explicit replace-by-request provenance, every other protected
resource as a no-op, the sensitive instance-ID output transition, unchanged
AMI, instance type, subnet, instance profile, and security-group binding,
keyless public-IP behavior before and after, IMDSv2, the encrypted 8 GiB root
volume, and the exact tracked bootstrap asset digests. It rejects extra
actions, diagnostics, unknown recovery or security controls, stale assets, or
a rendered bootstrap that is not a regular mode-`0600` file. Remove the
rendered file after the exact saved-plan digest receives independent approval;
regenerating either artifact invalidates that approval.

## 4. Apply only the reviewed replacement-host plan

Recheck the saved-plan digest, then apply the binary plan exactly once:

```sh
terraform -chdir=infra/terraform/production apply -input=false \
  "$TF_SLICE4_PLAN"
```

Verify value-blind that Session Manager sees the host; port `22`, a key pair,
and inbound rules are absent; IMDSv2 and the encrypted 8 GiB root volume are
enforced; IAM is limited to runtime-secret read and new recovery-object writes;
the reserved EIP is unattached; old resources are unchanged; and
`jobcron.service` is installed but stopped. A fresh Terraform plan must then be
clean.

## 5. Use Session Manager for host and RDS access

Load the private instance selector and forwarding parameters, then open the
localhost-only RDS tunnel in a dedicated trusted-Mac terminal:

```sh
aws ssm start-session \
  --target "$REPLACEMENT_INSTANCE_ID" \
  --document-name AWS-StartPortForwardingSessionToRemoteHost \
  --parameters "$SSM_FORWARD_PARAMETERS"
```

Confirm the listener is bound to `127.0.0.1` and RDS is still private. Use
separate Session Manager sessions for host operations and later private
verification. Do not capture the commands or resolved parameters in tracked
evidence.

## 6. Apply schema migrations through the private tunnel

Before the first runtime start, bind the migration binary to the full exact SHA
approved by independent review. Refuse a different HEAD, any tracked or
untracked worktree change, or any stash:

```sh
(
set -eu
umask 077
PATH=/usr/bin:/bin
export PATH
export JOBCRON_REVIEWED_SHA='<approved-40-hex-commit>'
export JOBCRON_PREVIOUS_IMAGE_COMMIT='<commit from the private deployed image.json>'
export JOBCRON_GO_BINARY='<approved-absolute-go-binary>'
export JOBCRON_GO_TOOLCHAIN_DIGEST='<approved-64-hex-complete-goroot-manifest-digest>'
export GIT_NO_REPLACE_OBJECTS=1
printf '%s\n' "$JOBCRON_REVIEWED_SHA" | grep -Eq '^[0-9a-f]{40}$'
printf '%s\n' "$JOBCRON_PREVIOUS_IMAGE_COMMIT" | grep -Eq '^[0-9a-f]{40}$'
printf '%s\n' "$JOBCRON_GO_TOOLCHAIN_DIGEST" | grep -Eq '^[0-9a-f]{64}$'
case $JOBCRON_GO_BINARY in /*) ;; *) exit 1 ;; esac
test -x "$JOBCRON_GO_BINARY"
go_dir=$(CDPATH= cd -P "${JOBCRON_GO_BINARY%/*}" && pwd)
go_root=$(CDPATH= cd -P "$go_dir/.." && pwd)
toolchain_manifest_dir=$(mktemp -d)
toolchain_unsupported="$toolchain_manifest_dir/unsupported"
toolchain_paths="$toolchain_manifest_dir/paths"
toolchain_sorted_paths="$toolchain_manifest_dir/sorted-paths"
toolchain_manifest="$toolchain_manifest_dir/manifest"
cleanup_toolchain_manifest() {
  rm -f -- "$toolchain_unsupported" "$toolchain_paths" \
    "$toolchain_sorted_paths" "$toolchain_manifest"
  rmdir "$toolchain_manifest_dir"
}
trap cleanup_toolchain_manifest EXIT
trap 'exit 1' HUP INT TERM
(cd "$go_root" && find . ! -type d ! -type f -print0 >"$toolchain_unsupported")
test ! -s "$toolchain_unsupported"
(cd "$go_root" && find . -type f -print0 >"$toolchain_paths")
test -s "$toolchain_paths"
LC_ALL=C sort -z <"$toolchain_paths" >"$toolchain_sorted_paths"
(cd "$go_root" && xargs -0 shasum -a 256 <"$toolchain_sorted_paths" >"$toolchain_manifest")
computed_go_toolchain_digest=$(shasum -a 256 "$toolchain_manifest")
computed_go_toolchain_digest=${computed_go_toolchain_digest%% *}
test "$computed_go_toolchain_digest" = "$JOBCRON_GO_TOOLCHAIN_DIGEST"
cleanup_toolchain_manifest
trap - EXIT HUP INT TERM
run_git() {
  env -i PATH=/usr/bin:/bin HOME=/var/empty GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null GIT_NO_REPLACE_OBJECTS=1 \
    /usr/bin/git -c core.hooksPath=/dev/null -c core.attributesFile=/dev/null \
      -c core.excludesFile=/dev/null "$@"
}
test "$(run_git rev-parse HEAD)" = "$JOBCRON_REVIEWED_SHA"
test -z "$(run_git status --porcelain=v1 --untracked-files=all)"
test -z "$(run_git stash list)"
run_git cat-file -e "$JOBCRON_PREVIOUS_IMAGE_COMMIT^{commit}"
previous_migration_tree=$(run_git rev-parse \
  "$JOBCRON_PREVIOUS_IMAGE_COMMIT:internal/storage/postgres_migrations")
reviewed_migration_tree=$(run_git rev-parse \
  "$JOBCRON_REVIEWED_SHA:internal/storage/postgres_migrations")
test "$previous_migration_tree" = "$reviewed_migration_tree"
migration_dir=$(mktemp -d)
migration_bin="$migration_dir/jobcron-user-$JOBCRON_REVIEWED_SHA"
migration_builder="$migration_dir/build-reviewed-jobcron-user"
cleanup_migration_binary() {
  rm -f -- "$migration_bin" "$migration_builder"
  rmdir "$migration_dir"
}
trap cleanup_migration_binary EXIT
trap 'exit 1' HUP INT TERM
repo_root=$(run_git rev-parse --show-toplevel)
run_git show "${JOBCRON_REVIEWED_SHA}:scripts/build-reviewed-jobcron-user.sh" >"$migration_builder"
chmod 500 "$migration_builder"
"$migration_builder" "$repo_root" "$JOBCRON_REVIEWED_SHA" "$migration_bin" "$JOBCRON_GO_BINARY" "$JOBCRON_GO_TOOLCHAIN_DIGEST"
test "$(run_git rev-parse HEAD)" = "$JOBCRON_REVIEWED_SHA"
test -z "$(run_git status --porcelain=v1 --untracked-files=all)"
test -z "$(run_git stash list)"
unset JOBCRON_DATABASE_PASSWORD
"$migration_bin" migrate \
  --database-url "$JOBCRON_MASTER_DATABASE_URL" \
  --backfill-legacy-migration-tree "$previous_migration_tree"
)
```

The builder itself comes from the reviewed commit. It disables Git replacement
objects, clones the reviewed object into a private repository that has no local
attributes or configuration, and then removes the clone metadata before the
build. The Go command is an explicitly selected absolute local binary and runs
under an environment allowlist with `GOTOOLCHAIN=local`, `CGO_ENABLED=0`, private
caches, module checksum verification, and `-mod=readonly`. Local Git metadata,
ignored files, overlays, ambient build controls, and concurrent worktree edits
cannot enter the privileged binary. The builder rejects symlinks and other
non-regular GOROOT entries, checks every manifest stage explicitly, and verifies
a SHA-256 manifest of every regular file in the selected Go distribution before
allowing the toolchain to run; executable-only or lone-`go` hashes are insufficient.

The previous image commit comes from the private immutable-image evidence, not
from a mutable branch or tag. Exact migration-tree object equality proves that
the version-only production ledger was created by the same migration bytes now
embedded in the reviewed binary. Only after that audit may the explicit
backfill argument add each filename and pinned SHA-256 digest; the binary also
requires that audited tree object to equal its pinned migration tree. Without
the argument, a legacy ledger fails closed; a tree, filename, or digest mismatch
always fails closed.

The URL must contain the master username but no password, use exactly the
`127.0.0.1` Session Manager tunnel, and set only `sslmode=require`.
`verify-full` cannot verify the RDS certificate against a loopback hostname.
For this production run, leave `JOBCRON_DATABASE_PASSWORD` unset and enter the
AWS-managed master password only through the silent stdin prompt. The command's
environment source exists for controlled automation and tests, not this manual
path. It emits only `database_migrations_ready=true`; the master credential
never reaches the host or a command argument. The operation has a two-minute
total deadline and serializes concurrent migrators with a PostgreSQL advisory
lock.

## 7. Create the lower-privilege database role

Set the helper's private inputs to the localhost-only master URL, private RDS
endpoint, application role name, owner-only `database-role.env`, and
owner-only `runtime-secret.json`. Then run:

```sh
scripts/production-rds-role.sh
```

Enter the master and application passwords only through the helper's silent
stdin prompts. It grants connect, schema usage, application-table DML, sequence
usage, and read-only access to `schema_migrations`; the runtime role cannot
forge the migration ledger or create a database, superuser, extension, or
replication role. Existing elevated role attributes are removed, while any role
membership or ownership of the production database, public schema, or public
relations makes the transaction fail closed. Direct and public ledger writes
are revoked, and effective `SELECT`/`INSERT`/`UPDATE`/`DELETE` privileges are
checked before readiness. Verify the catalog grants without printing names or
passwords. The helper stores the lower-privilege TLS `DATABASE_URL` only in the
private runtime JSON and emits only `database_role_ready=true`. Run this helper
after every operator migration so newly created tables and sequences receive
the runtime grants and ledger writes are revoked again.

If the approved legacy SQLite import is still required, keep the source and
optional key file on the trusted Mac. Through the same tunnel, create exactly
one owner, dry-run `jobcron-import`, review the immutable fingerprint, all
category counts and collisions, then repeat the identical approved command
with `--apply`. Never send the RDS master credential or legacy files to the
host.

## 8. Populate the runtime secret outside Terraform

Complete `runtime-secret.json` locally with exactly:

- the lower-privilege TLS database URL;
- session, credential-encryption, proxy, and cohort values;
- the Stage 1 sponsor user ID;
- the approved immutable image digest; and
- the Origin CA certificate and private key.

Validate exact keys, non-empty string values, digest shape, TLS mode, and
certificate/key shape without printing content. Add one Secrets Manager version
using the private file input. Terraform must create no secret version, and its
state and plan must remain unchanged.

Through a Session Manager host session, write the separate classic
`read:packages` token from stdin to `/run/jobcron/registry-token` with mode
`0600`. It must not be `GITHUB_TOKEN`, part of the runtime secret, or retained
after the image pull.

## 9. Prove the systemd runtime fails closed, then start

First select an intentionally incomplete synthetic secret version and start
`jobcron.service`. Confirm the prior stack is stopped, preparation fails,
neither container runs, and `/run/jobcron` contains no partial secret output.
Restore the approved complete secret version.

Start the service again through systemd. It must run `prepare`, digest pull, and
Compose in that order. Confirm:

- the registry token and temporary Docker configuration are removed;
- no home-directory Docker credential exists;
- the current and previous digests remain available;
- both containers are healthy with bounded JSON log rotation;
- the app uses TLS and the lower-privilege role; and
- host publication is limited to loopback ports `7777` and `8443`.

Run `/opt/jobcron/jobcron-runtime.sh verify-local-state` and record only its
value-blind booleans and counts.

## 10. Complete private verification

Forward trusted-Mac ports through Session Manager to host loopback ports `7777`
and `8443`. With the required headless browser workflow, walk the login page,
owner login, dashboard, profile read/save, archive, one cohort-safe scrape or
re-rate, logout, and failed-session reuse. Verify expected content and state,
not only an HTTP status.

Separately verify Caddy's Origin CA certificate with the private CA material;
do not bypass certificate verification. Record sanitized results in the private
`private-verification.md`. This is private verification only.

## 11. Prove reboot recovery

After private verification succeeds, enable `jobcron.service` and reboot the
replacement host. Confirm the memory-backed runtime directory was cleared,
systemd recreated complete files with modes `0700` and `0600`, no secret or TLS
key persisted elsewhere, and the already-present approved digest starts without
another registry token.

Any incomplete secret, wrong mode, failed pull, unhealthy container, public
listener, or failed user-path check is a stop condition.

## 12. Verify recovery manifests and restore

Run `jobcron-recovery.service` once and enable its timer only after that run
succeeds. The service uploads a custom-format database dump, sanitized Jobcron
and Caddy logs, and one SHA-256 recovery manifest for each artifact.
It accepts only the generated TLS RDS URL, passes a password-free URL as
`pg_dump`'s database argument, and supplies the decoded password only through
the child environment. Confirm the process arguments and sanitized evidence do
not contain either the encoded or decoded database password.

On the trusted Mac, set the private bucket, timestamped prefix, and owner-only
destination, then run:

```sh
scripts/pull-production-recovery.sh
```

The helper copies missing objects, validates the exact six-object set, verifies
all manifests, and only then applies `macbook-copy=verified`. Restore the dump
into a disposable database, compare schema and bounded table counts, and inspect
the logs for headers, cookies, tokens, secrets, and unnecessary personal data.
Verify the reviewed tagged and untagged retention cases without deleting any
object.

Record only sanitized outcomes in `recovery-verification.md`. Failed download,
manifest, tagging, restore, row-count, or sanitization checks are stop
conditions.

## 13. Stop before public cutover

Run a final no-change Terraform plan and confirm the old host, old database,
reserved EIP, prior image, and recovery materials remain available. Confirm no
Cloudflare, DNS, public ingress, or public traffic change occurred.

Do not associate the reserved EIP, change edge configuration, or accept public
traffic. Keep the rollback window open. Slice 5 may begin only from the exact
private checkpoint after every stop condition above is clear.
