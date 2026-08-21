# Terraform Slice 4 Replacement EC2 And Transient Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and privately verify a replacement production host that pulls one
approved immutable `linux/arm64` image, reaches private RDS through the
lower-privilege application role, keeps all runtime secrets in memory, and
produces independently recoverable database and sanitized-log archives without
changing public traffic.

**Architecture:** Extend the existing production Terraform root with one
Amazon Linux 2023 arm64 `t4g.micro` attached to Slice 3's no-ingress
`aws_security_group.origin`, Session Manager access, and an instance role
limited to the one runtime secret and write-only recovery objects. A
value-blind bootstrap installs Docker and tracked production assets; systemd
stops the stack before materializing a validated secret under `/run/jobcron`,
so Jobcron and Caddy fail closed. The trusted Mac performs registry-token
delivery, RDS administration, recovery copying, and private-path user
verification through Session Manager.

**Tech Stack:** Terraform `1.15.8`, AWS provider `6.33.0`, Amazon Linux 2023
arm64, EC2, Systems Manager, IAM, Secrets Manager, private RDS PostgreSQL,
encrypted S3, Docker Engine, Compose v2, systemd, GHCR, GitHub Actions, POSIX
shell, `jq`, Go contract tests, and macOS `zsh`.

## Global Constraints

- This plan implements the approved
  [foundation specification](../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md),
  [human-blocked launch contract](../specs/260726-terraform-first-production-launch-human-blocked-steps.md),
  and
  [Window 1 authorization contract](../specs/260728-pre-batch-1-window-1-authorization-contract.md).
- Slice 4 implementation may be prepared early, but no Slice 4 saved plan,
  apply, or private runtime operation starts until the Slice 3 completion
  checkpoint and exact integrated commit are recorded privately.
- The whole launch must remain at or below USD 100 recurring monthly cost and
  USD 200 aggregate one-time cost. These are aggregate ceilings, not Slice 4
  allowances.
- Every state-changing apply uses an exact saved plan that passed the
  address-and-action allow-list, no-destroy/no-replace check, aggregate cost
  gate, drift check, and independent review.
- Preserve the old EC2, old RDS, canonical network, inherited route
  relationships, reserved unattached EIP, and all rollback materials.
- Slice 4 must not create an EIP association, Cloudflare resource, DNS record,
  public ingress rule, port `22` rule, key pair, RDS replacement, secret
  version, or persistent secret file.
- Never publish credentials, account IDs, ARNs, resource IDs, addresses,
  endpoints, CIDRs, personal data, private image digests, plans, state, raw
  logs, or exact recovery locations.
- Terraform creates no secret version. Runtime and registry values enter only
  through the private controller paths defined below.
- The replacement instance role may use standard Session Manager permissions,
  `secretsmanager:GetSecretValue` for
  `aws_secretsmanager_secret.runtime.arn`, and write-only S3 access for
  `jobcron/*`; it may not read the RDS master secret, mutate the runtime secret,
  list/read/delete recovery objects, or administer RDS.
- The host has no EC2 key pair, no SSH, required IMDSv2, an encrypted 8 GiB
  `gp3` root volume, and no instance-metadata access from containers. It
  attaches only `aws_security_group.origin`, whose Slice 3 ingress is empty.
- Jobcron and Caddy remain stopped when the runtime secret is absent,
  unavailable, incomplete, or malformed.
- The host never builds an image. It pulls the approved private GHCR image by
  digest and retains the current and previous digests for rollback.
- Exact operational evidence stays below `.superpowers/sdd/`, which is ignored.
  Only sanitized durable conclusions may be tracked.
- Follow TDD: add a failing contract test, run it RED, make the smallest
  implementation, then run it GREEN before each commit.
- Do not update the roadmap or either documentation index in this task. Mayor
  integrates all Slice plan links centrally.

---

## Concepts The Implementer Must Know

- **Immutable digest:** a `sha256:` image identity that cannot move to different
  bytes, unlike a tag.
- **IMDSv2:** EC2 metadata access that requires a session token. A hop limit of
  `1` prevents the Docker network from reaching instance credentials.
- **Session Manager:** AWS-managed terminal and port forwarding over outbound
  HTTPS, replacing inbound SSH.
- **Value-blind:** commands may validate presence, type, count, or checksum but
  never print the underlying private value.
- **Fail closed:** failure to retrieve or validate a secret stops both
  application containers instead of reusing stale files or starting partially.
- **Saved plan:** the exact binary Terraform plan reviewed and later applied;
  regenerating it invalidates the prior review.
- **Write-only recovery prefix:** the host can upload new immutable objects but
  cannot list, read, overwrite, or delete existing recovery material.

## Exact File Map

Create:

- `.github/workflows/publish-production-image.yml` — manual private GHCR
  `linux/arm64` publisher using only `GITHUB_TOKEN` after a controller-created
  private package bootstrap.
- `scripts/check-production-image-workflow.sh` — static workflow policy gate.
- `scripts/check-production-image-workflow_test.sh` — mutation tests for the
  workflow gate.
- `deploy/production/jobcron-runtime.sh` — `prepare`, `pull`, `archive`, and
  `verify-local-state` host operations.
- `deploy/production/systemd/jobcron.service` — fail-closed Compose lifecycle.
- `deploy/production/systemd/jobcron-recovery.service` — one archive run.
- `deploy/production/systemd/jobcron-recovery.timer` — nightly archive trigger.
- `scripts/jobcron_runtime_test.go` — fake-AWS/Docker/PostgreSQL shell contract
  tests.
- `infra/terraform/production/compute.tf` — reviewed AL2023 AMI pin,
  origin-group EC2 attachment, instance profile, and least-privilege IAM.
- `infra/terraform/production/templates/replacement-host.sh.tftpl` —
  value-blind cloud-init bootstrap.
- `infra/terraform/production/tests/compute.tftest.hcl` — host, IAM, bootstrap,
  and no-cutover Terraform contracts.
- `scripts/check-terraform-slice-4-plan.sh` — saved-plan and aggregate-cost gate.
- `scripts/check-terraform-slice-4-plan_test.sh` — synthetic plan/cost mutation
  tests.
- `scripts/production-rds-role.sh` — lower-privilege role creation through a
  localhost-only SSM tunnel.
- `scripts/pull-production-recovery.sh` — trusted-Mac incremental download,
  manifest verification, and verified-object tagging.
- `scripts/production_private_ops_test.go` — value-blind RDS and recovery helper
  contract tests.

Modify:

- `deploy/production/compose.yaml` — consume `/run/jobcron/compose.env`, mount
  transient Origin CA files, disable container metadata access, and add bounded
  Docker log rotation.
- `deploy/production/Caddyfile` — use the transient Origin CA certificate/key
  and keep the application private.
- `deploy/production/compose_test.go` — lock transient mounts, fail-closed
  inputs, digest-only image identity, and log rotation.
- `deploy/production/.env.example` — remove instructions that imply a
  persistent production `.env`; retain synthetic local-render inputs only.
- `deploy/production/README.md` — replace stale SSH/manual-host instructions
  with the Slice 4 systemd and SSM flow.
- `deploy/production/HUMAN_DEPLOY_GUIDE.md` — replace SSH, persistent secret,
  Mac-built image, and public-start assumptions with the private Slice 4
  sequence.
- `infra/terraform/production/variables.tf` — add only non-secret replacement
  host inputs and exact Slice 3 interface validation.
- `infra/terraform/production/outputs.tf` — expose only the explicitly named
  sensitive `replacement_instance_id` operator selector.
- `.github/workflows/ci.yml` — run the new workflow, runtime, private-ops, and
  saved-plan checker tests.
- `scripts/check-terraform.sh` — include the production root tests added here.
- `docs/architecture.md` — update only after the implementation and private
  verification are complete.

Do not create a repository abstraction, deployment framework, custom daemon, or
second secret store. Existing shell, systemd, Compose, Terraform tests, and Go
test helpers cover the work.

## Private Controller Inputs And Evidence

All paths below are ignored and mode `0700` for directories and `0600` for
files:

```text
.superpowers/sdd/260728-terraform-slice-4/
├── controller.env
├── production.auto.tfvars.json
├── aggregate-cost.json
├── slice-3-checkpoint.json
├── slice-4.tfplan
├── slice-4-plan.json
├── slice-4-review.md
├── image.json
├── runtime-secret.json
├── registry-token
├── database-role.env
├── private-verification.md
└── recovery-verification.md
```

`controller.env` contains only controller selectors needed by AWS CLI commands.
`production.auto.tfvars.json` contains private Terraform input values.
`aggregate-cost.json` contains numeric whole-launch estimates, source names, and
the UTC time checked. `image.json` records the approved commit, private package
visibility result, `linux/arm64` platform, and immutable digest. No command in
this plan prints these files.

## Slice 3 Dependency Interface

The durable reviewed Mayor planning baseline is `0ed4540`, at
`docs/superpowers/plans/260728-terraform-slice-3-private-database-secret-containers-implementation.md`.
It defines these same-root Terraform addresses:

```text
aws_db_instance.production
aws_security_group.origin
aws_security_group.database
aws_vpc_security_group_ingress_rule.database_postgresql_from_origin
aws_secretsmanager_secret.runtime
aws_s3_bucket.recovery
aws_s3_bucket_lifecycle_configuration.recovery
```

Nothing in this planning dependency is pending. Runtime execution must use the
later exact Slice 3 completion checkpoint and integrated commit, and must stop
if that checkpoint differs from the reviewed interface below.

Slice 3 intentionally produces no Terraform output. Slice 4 consumes the
same-root resource attributes directly:

```text
aws_security_group.origin.id
aws_db_instance.production.endpoint
aws_secretsmanager_secret.runtime.arn
aws_s3_bucket.recovery.id
```

The Slice 3 checkpoint must prove private RDS, the empty runtime-secret
container, and the encrypted/versioned recovery bucket exist with no public
access, no secret version, the lifecycle config retains verified objects for at
least 14 days and unverified objects for 90 days, no destroy/replace action,
and no old-resource change. A missing address, renamed interface, stale
checkpoint, lifecycle mismatch, or non-clean post-apply plan stops Slice 4; do
not infer or substitute an address.

## Slice 4 Terraform Action Allow-List

The reviewed saved plan may contain exactly these five create actions:

```text
aws_iam_role.replacement_host                       create
aws_iam_role_policy_attachment.replacement_host_ssm create
aws_iam_role_policy.replacement_host_runtime        create
aws_iam_instance_profile.replacement_host           create
aws_instance.replacement_host                       create
```

Data-source reads do not appear in `resource_changes`. No update, delete,
replace, import, move, forget, or additional create is allowed. If provider
normalization introduces another action, stop and revise this versioned plan
before any apply. The plan may add only
`output_changes.replacement_instance_id` with `actions = ["create"]` and
`after_sensitive = true`; reject every other output change and every
non-sensitive output.

## Task 1: Publish One Private Immutable `linux/arm64` Image

**Files:**

- Create: `.github/workflows/publish-production-image.yml`
- Create: `scripts/check-production-image-workflow.sh`
- Create: `scripts/check-production-image-workflow_test.sh`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: a manually supplied full `release_sha` that exists in this
  repository, plus an existing unlinked private `jobcron` package with this
  repository granted Actions access.
- Produces: private `ghcr.io` package `jobcron` and a non-reusable tag formed
  from `sha-` plus the first 12 lowercase hexadecimal characters of
  `release_sha`. The workflow emits no digest; Task 10 resolves it privately
  and creates `image.json`.

- [x] **Step 1: Write RED mutation tests**

The shell test copies the workflow to a temporary directory and proves the
checker rejects each mutation:

```text
packages: write removed
contents permission broader than read
id-token permission added
pull_request or push trigger added
linux/arm64 changed
--push removed
build output not redirected
digest printed, logged, exported, or written to Actions summary
pre-push existing-private-package check removed or moved after a registry write
private-visibility check removed
existing immutable tag accepted for overwrite
personal token or repository secret used for publication
```

Run:

```sh
sh scripts/check-production-image-workflow_test.sh
```

Expected: FAIL because the checker does not exist.

- [x] **Step 2: Implement the smallest workflow**

The workflow must:

1. expose only `workflow_dispatch` with required string input `release_sha`;
2. set top-level permissions to `contents: read` and `packages: write`;
3. validate `release_sha` as 40 lowercase hex characters and confirm it is a
   repository commit;
4. check out that exact commit;
5. authenticate `ghcr.io` with `${{ github.actor }}` and
   `${{ secrets.GITHUB_TOKEN }}`;
6. query the owner package API without printing its response and fail unless
   the package already exists with visibility exactly `private`; this gate must
   run before manifest lookup, build, or push;
7. authenticate a manifest lookup and refuse to build if
   `sha-${release_sha:0:12}` already resolves, so an immutable commit tag can
   never be reused or overwritten;
8. build `deploy/production/Dockerfile` with Buildx for only `linux/arm64`;
9. push `ghcr.io/${GITHUB_REPOSITORY_OWNER,,}/jobcron:sha-${release_sha:0:12}`;
10. redirect all Buildx stdout/stderr and metadata output to a runner-temporary
   mode-`0600` file, print only a generic success/failure message, and remove
   the temporary file before job exit;
11. query the owner package API again without printing its response and fail unless
    visibility is exactly `private`; and
12. write only commit, platform, and visibility to the Actions summary.

Do not grant `id-token`, `actions`, `deployments`, `secrets`, or
`packages: delete`. Do not accept a caller-supplied image name or platform.
Workflow logs, outputs, artifacts, annotations, and summaries are public by
policy and must never contain the private image digest. After workflow success,
the controller authenticates privately, resolves the immutable commit tag to a
digest, verifies one `linux/arm64` manifest, and writes the digest only to the
ignored mode-`0600` `image.json`.

- [x] **Step 3: Make the checker GREEN**

Run:

```sh
sh scripts/check-production-image-workflow_test.sh
sh scripts/check-production-image-workflow.sh
```

Expected: all mutations rejected and the real workflow accepted.

- [x] **Step 4: Commit**

```sh
git add .github/workflows/ci.yml \
  .github/workflows/publish-production-image.yml \
  scripts/check-production-image-workflow.sh \
  scripts/check-production-image-workflow_test.sh
git commit -m "ci: add private arm64 image publication contract"
```

## Task 2: Make Compose Digest-Only And Runtime-Transient

**Files:**

- Modify: `deploy/production/compose.yaml`
- Modify: `deploy/production/Caddyfile`
- Modify: `deploy/production/compose_test.go`
- Modify: `deploy/production/.env.example`

**Interfaces:**

- Consumes: `/run/jobcron/compose.env`,
  `/run/jobcron/caddy/origin.crt`, and
  `/run/jobcron/caddy/origin.key`.
- Produces: private `app:7777`, Caddy TLS on container port `443`, no host
  publication in Slice 4, and local JSON-file log rotation.

- [x] **Step 1: Add RED Compose tests**

Add focused tests proving:

- `JOBCRON_IMAGE` must match
  `^ghcr\.io/[a-z0-9._-]+/jobcron@sha256:[a-f0-9]{64}$`;
- Compose has no `build`, mutable image tag, credential volume, app filesystem
  mount, host port, or metadata route;
- Caddy receives only read-only `/run/jobcron/caddy` mounts;
- the app publishes only `127.0.0.1:7777:7777` for the SSM browser tunnel and
  Caddy publishes only `127.0.0.1:8443:443` for private TLS checks;
- both services use `json-file` with `max-size: 10m` and `max-file: 3`;
- the app retains `--no-open --host 0.0.0.0 --port 7777`;
- the application values remain explicit and required; and
- Caddy uses the Origin CA files rather than automatic public issuance.

Run:

```sh
go test ./deploy/production -run 'TestProductionCompose' -count=1
```

Expected: FAIL on the current tag, persistent input, public-port, and Caddy
contracts.

- [x] **Step 2: Apply the minimal Compose/Caddy changes**

Keep the two existing services and volumes needed for Caddy state. Use an
internal Docker network for the app, bind the app only to
`127.0.0.1:7777:7777`, bind Caddy only to `127.0.0.1:8443:443`, add a read-only
bind from `/run/jobcron/caddy`, and configure explicit log rotation. Add
`AWS_EC2_METADATA_DISABLED=true` to both services. Keep the proxy-secret header
contract. No container port may bind `0.0.0.0` or `[::]` during Slice 4.

The `.env.example` remains synthetic test input and states that production
systemd uses `/run/jobcron/compose.env`; it must contain no command that copies
the example to persistent `.env`.

- [x] **Step 3: Run GREEN checks**

```sh
go test ./deploy/production -run 'TestProductionCompose' -count=1
docker compose -f deploy/production/compose.yaml \
  --env-file deploy/production/.env.example config --quiet
```

Expected: PASS with synthetic values and no private output.

- [x] **Step 4: Commit**

```sh
git add deploy/production/Caddyfile \
  deploy/production/compose.yaml \
  deploy/production/compose_test.go \
  deploy/production/.env.example
git commit -m "deploy: require transient digest-only production runtime"
```

## Task 3: Add One Value-Blind Host Runtime Helper

**Files:**

- Create: `deploy/production/jobcron-runtime.sh`
- Create: `scripts/jobcron_runtime_test.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: root-only `/etc/jobcron/runtime-secret-id`,
  `/run/jobcron/registry-token`, the tracked Compose/Caddy files, and standard
  AWS/Docker/PostgreSQL CLIs.
- Produces: `/run/jobcron/compose.env`, Origin CA files, one digest pull,
  immutable recovery objects, and presence-only verification.

- [x] **Step 1: Write RED fake-command tests**

Use temporary fake `aws`, `docker`, `pg_dump`, `sha256sum`, and `jq` commands.
Tests must prove:

- `prepare` rejects missing, malformed, null, empty, or extra-type fields;
- `prepare` reads a mode-`0600` root-only identifier file and rejects a missing,
  world-readable, empty, or multi-line identifier;
- `prepare` creates directories `0700`, files `0600`, uses `umask 077`, and
  atomically replaces a complete runtime;
- a failed second `prepare` removes transient outputs rather than reusing the
  prior secret;
- stdout/stderr never contains any synthetic secret value;
- `pull` reads the registry token from stdin/file without an argv value, uses a
  temporary `DOCKER_CONFIG` below `/run/jobcron`, pulls by digest, logs out,
  removes the credential directory and token, and never creates
  `$HOME/.docker/config.json`;
- `archive` writes custom-format `pg_dump`, sanitized Jobcron/Caddy logs, and
  SHA-256 manifests under immutable UTC keys;
- archive upload uses only `s3 cp` object writes and never list/get/delete; and
- `verify-local-state` reports booleans/counts only.

Run:

```sh
go test ./scripts -run 'TestJobcronRuntime' -count=1
```

Expected: FAIL because the runtime helper does not exist.

- [x] **Step 2: Implement four subcommands**

`prepare` fetches the one secret with `aws secretsmanager get-secret-value`,
reading its private identifier from `/etc/jobcron/runtime-secret-id`, extracts
the exact approved fields with `jq -e`, rejects wrong JSON types, and writes
through a new temporary directory before atomic rename. The identifier may
persist root-only because it is not a credential or secret value; the fetched
value may exist only below `/run/jobcron`. The helper never uses `set -x`,
`env`, `printenv`, or value-bearing diagnostics.

`pull` validates the digest shape and first checks whether that exact digest is
already present locally. A present digest needs no credential, which makes
reboot recovery independent of a persistent registry token. Otherwise it uses
`docker login ghcr.io --password-stdin`, pulls the digest, logs out, and removes
all token material even on failure.

`archive` sanitizes container logs before upload by retaining timestamp,
service, severity, and message while dropping request headers, cookies,
authorization data, and known secret-shaped fields. It writes the database dump
and logs before their manifests, then uploads manifests last.

`verify-local-state` checks modes, required files, forbidden persistent paths,
Docker log settings, current/previous digest count, and disk free bytes without
printing values.

- [x] **Step 3: Run GREEN and syntax checks**

```sh
sh -n deploy/production/jobcron-runtime.sh
go test ./scripts -run 'TestJobcronRuntime' -count=1
```

Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add .github/workflows/ci.yml \
  deploy/production/jobcron-runtime.sh \
  scripts/jobcron_runtime_test.go
git commit -m "deploy: add value-blind transient runtime helper"
```

## Task 4: Add Fail-Closed systemd Units

**Files:**

- Create: `deploy/production/systemd/jobcron.service`
- Create: `deploy/production/systemd/jobcron-recovery.service`
- Create: `deploy/production/systemd/jobcron-recovery.timer`
- Modify: `scripts/jobcron_runtime_test.go`

**Interfaces:**

- Consumes: `/opt/jobcron`, `/run/jobcron`, network-online, Docker, and the
  runtime helper.
- Produces: stopped-before-prepare application lifecycle and a nightly archive
  attempt.

- [x] **Step 1: Add RED unit tests**

Parse the units as text and prove:

- `jobcron.service` orders after network and Docker;
- its first pre-start action stops the existing Compose stack;
- secret preparation and digest pull occur only after that stop;
- Compose start cannot run after either preparation action fails;
- stop removes the stack and `/run/jobcron`;
- no unit logs environment or secret values;
- the recovery service uses the runtime helper only; and
- the timer is persistent with randomized delay and no overlap.

Run:

```sh
go test ./scripts -run 'TestJobcronSystemd' -count=1
```

Expected: FAIL because the units do not exist.

- [x] **Step 2: Implement the units**

Use `Type=oneshot` and `RemainAfterExit=yes` for the stack. The order is:

```text
docker compose down
jobcron-runtime.sh prepare
jobcron-runtime.sh pull
docker compose up -d --remove-orphans
```

`ExecStop` runs Compose down and removes `/run/jobcron`. Use restrictive
systemd sandboxing that still permits Docker socket access, `/run/jobcron`,
network, and the required AWS calls.

The recovery timer runs nightly, catches a sleeping/rebooted host with
`Persistent=true`, adds `RandomizedDelaySec=30m`, and uses the service's
single-instance systemd semantics.

- [x] **Step 3: Run GREEN checks**

```sh
go test ./scripts -run 'TestJobcron(Systemd|Runtime)' -count=1
```

Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add deploy/production/systemd scripts/jobcron_runtime_test.go
git commit -m "deploy: add fail-closed production systemd units"
```

## Task 5: Provision The Narrow Replacement Host

**Files:**

- Create: `infra/terraform/production/compute.tf`
- Create: `infra/terraform/production/templates/replacement-host.sh.tftpl`
- Create: `infra/terraform/production/tests/compute.tftest.hcl`
- Modify: `infra/terraform/production/variables.tf`
- Create or modify: `infra/terraform/production/outputs.tf`

**Interfaces:**

- Consumes: Slice 3 addresses, `canonical_network_config`, the chosen canonical
  public subnet key, and tracked deployment assets.
- Produces: the eight allow-listed Terraform resources and sensitive Session
  Manager selectors.

- [x] **Step 1: Write RED Terraform tests**

Use `mock_provider "aws"` and synthetic overrides. Assert:

- the host uses the validated `replacement_host_ami_id` supplied by an approved
  private controller path, never a mutable `latest` lookup;
- instance type is `t4g.micro`;
- root block device is encrypted `gp3`, exactly `8` GiB, and
  `delete_on_termination = true`;
- `key_name` is null and the instance attaches exactly
  `aws_security_group.origin.id`;
- Slice 4 declares no security group, egress rule, or database ingress rule;
- the existing
  `aws_vpc_security_group_ingress_rule.database_postgresql_from_origin`
  remains unchanged and continues to allow TCP `5432` from
  `aws_security_group.origin`;
- IMDSv2 is required with hop limit `1`;
- no reserved EIP or EIP association is referenced;
- the instance uses one canonical public subnet and may receive only an
  ephemeral public IPv4 address for outbound access;
- instance role trust is only EC2;
- attached managed policy is exactly
  `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`;
- inline policy actions/resources are exactly runtime-secret
  `GetSecretValue`, recovery-prefix `PutObject`, and
  `AbortMultipartUpload`;
- no RDS master-secret, S3 list/get/delete, secret mutation, wildcard action,
  or wildcard resource exists;
- user data contains no private value, creates no persistent `.env`, installs
  Docker/Compose v2/AWS CLI/`jq`/PostgreSQL client, copies tracked assets,
  enables the units, and leaves `jobcron.service` stopped;
- rendered raw user data is less than `16384` bytes; and
- the only output is `replacement_instance_id`, its value is
  `aws_instance.replacement_host.id`, and it is `sensitive = true`;
- no output exposes the database endpoint, bucket, secret identifier, ARN, or
  IP; and
- the rendered root-only identifier file contains only
  `aws_secretsmanager_secret.runtime.arn`, never a secret value.

Run:

```sh
terraform -chdir=infra/terraform/production test \
  -filter=tests/compute.tftest.hcl
```

Expected: FAIL because the compute resources do not exist.

- [x] **Step 2: Implement the minimum Terraform**

Use a required, validated `replacement_host_ami_id` input supplied by either
the mode-`0600` local controller packet or the protected `production` workflow
secret `TF_VAR_REPLACEMENT_HOST_AMI_ID`. A public AL2023 arm64 parameter may
inform a future operator-approved upgrade, but it must not participate in the
Terraform resource graph. Do not hide AMI changes with
`lifecycle.ignore_changes`.

Set:

```hcl
instance_type = "t4g.micro"
key_name      = null

metadata_options {
  http_endpoint               = "enabled"
  http_tokens                 = "required"
  http_put_response_hop_limit = 1
}

root_block_device {
  encrypted             = true
  volume_type           = "gp3"
  volume_size           = 8
  delete_on_termination = true
}
```

The inline policy has separate statements for the one runtime secret and
`"${aws_s3_bucket.recovery.arn}/jobcron/*"`. Do not grant bucket-level access.

The value-blind template installs packages, writes tracked assets from
base64-encoded Terraform template inputs, verifies checksums, creates
`/opt/jobcron` with root ownership, enables Docker, leaves the recovery timer
disabled pending its first verified manual run, and does not start Jobcron. Terraform renders
`aws_secretsmanager_secret.runtime.arn` into
`/etc/jobcron/runtime-secret-id`, owned by root with directory mode `0700` and
file mode `0600`. This persistent file contains a private **identifier**, which
locates the approved secret container but cannot authenticate or reveal its
contents. Secret **values** are fetched only at service start and exist only
below memory-backed `/run/jobcron`. User data contains no registry token,
runtime secret value, endpoint, image digest, or other private value.

The instance uses exactly:

```hcl
vpc_security_group_ids = [aws_security_group.origin.id]
```

Do not create a replacement security group or duplicate the existing database
ingress rule.

- [x] **Step 3: Run GREEN Terraform gates**

```sh
terraform -chdir=infra/terraform/production fmt -check -recursive
terraform -chdir=infra/terraform/production validate
terraform -chdir=infra/terraform/production test \
  -filter=tests/compute.tftest.hcl
```

Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add infra/terraform/production
git commit -m "infra: define the narrow replacement production host"
```

## Task 6: Add Trusted-Mac RDS And Recovery Operations

**Files:**

- Create: `scripts/production-rds-role.sh`
- Create: `scripts/pull-production-recovery.sh`
- Create: `scripts/production_private_ops_test.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: an active Identity Center session, localhost-only SSM tunnel,
  private `database-role.env`, and private recovery selectors.
- Produces: one lower-privilege application role, verified local archives, and
  `macbook-copy=verified` object tags.

- [x] **Step 1: Write RED fake-command tests**

Prove:

- the RDS helper requires `127.0.0.1` and `sslmode=verify-full`/`require`;
- master and application passwords enter `psql` without argv, stdout, or
  history exposure;
- SQL grants only connect, schema usage, current/future table DML, and current
  sequence usage needed by Jobcron;
- the helper cannot create a superuser, database, extension, replication role,
  or broad public grant;
- rerun is idempotent and rotates only the application password;
- the recovery helper downloads missing immutable objects only;
- manifests are verified before tagging;
- failed verification never tags or deletes;
- successful verification sets only `macbook-copy=verified`; and
- neither helper contains SSH, public RDS, secret echo, or server-side delete.

Run:

```sh
go test ./scripts -run 'TestProductionPrivateOps' -count=1
```

Expected: FAIL because the helpers do not exist.

- [x] **Step 2: Implement value-blind helpers**

The operator opens the SSM tunnel separately:

```sh
aws ssm start-session \
  --target "$REPLACEMENT_INSTANCE_ID" \
  --document-name AWS-StartPortForwardingSessionToRemoteHost \
  --parameters "$SSM_FORWARD_PARAMETERS"
```

`SSM_FORWARD_PARAMETERS` is loaded from `controller.env`; the command is never
captured in tracked evidence.

`production-rds-role.sh` reads master/application passwords silently, validates
the localhost-only URL, runs one transaction, and writes only a success boolean
to `database-role.env`. The application URL is assembled privately and stored
only in `runtime-secret.json`.

`pull-production-recovery.sh` uses short-lived Identity Center credentials,
copies missing objects to the private Mac directory, verifies SHA-256 manifests,
and tags verified objects. It does not delete local or remote data.

- [x] **Step 3: Run GREEN checks**

```sh
sh -n scripts/production-rds-role.sh
sh -n scripts/pull-production-recovery.sh
go test ./scripts -run 'TestProductionPrivateOps' -count=1
```

Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add .github/workflows/ci.yml \
  scripts/production-rds-role.sh \
  scripts/pull-production-recovery.sh \
  scripts/production_private_ops_test.go
git commit -m "ops: add private RDS and recovery verification helpers"
```

## Task 7: Enforce The Saved-Plan And Aggregate-Cost Contract

**Files:**

- Create: `scripts/check-terraform-slice-4-plan.sh`
- Create: `scripts/check-terraform-slice-4-plan_test.sh`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: `terraform show -json` output and private
  `aggregate-cost.json`.
- Produces: a value-blind pass/fail result for the exact Slice 4 controller
  gate.

- [x] **Step 1: Write RED synthetic mutations**

Start from one accepted synthetic plan with five resource creates and the one
approved sensitive output create. Prove rejection of:

- any address outside the five-entry resource allow-list;
- update, delete, replace, import, move, or forget;
- any security group, security-group rule, EIP association, Cloudflare, DNS,
  key pair, port `22`, RDS, secret version, or old-resource action;
- unknown action values;
- a missing, renamed, unexpected, or non-sensitive output change;
- an output change other than sensitive
  `output_changes.replacement_instance_id = ["create"]`;
- missing or stale Slice 3 checkpoint;
- aggregate recurring estimate above `100`;
- aggregate one-time estimate above `200`;
- cost evidence older than 24 hours;
- missing cost categories for AWS compute, public IPv4, database, storage,
  backup, registry, and Cloudflare;
- a malformed or sensitive Terraform output; and
- any plan diagnostic containing a private-value marker.

Run:

```sh
sh scripts/check-terraform-slice-4-plan_test.sh
```

Expected: FAIL because the checker does not exist.

- [x] **Step 2: Implement one `jq`-based checker**

Usage:

```text
scripts/check-terraform-slice-4-plan.sh PLAN_JSON COST_JSON SLICE3_CHECKPOINT
```

The checker prints only counts, category names, and `PASS`/`FAIL`. It validates
the exact five-resource allow-list, the one sensitive output change, the two
aggregate ceilings, required categories, 24-hour freshness, and a clean Slice
3 checkpoint including the recovery-lifecycle verdict. It never prints plan
fragments, output values, addresses containing resolved private selectors, or
cost-source URLs.

- [x] **Step 3: Run GREEN checks**

```sh
sh scripts/check-terraform-slice-4-plan_test.sh
```

Expected: all unsafe mutations rejected and the accepted fixture passes.

- [x] **Step 4: Commit**

```sh
git add .github/workflows/ci.yml \
  scripts/check-terraform-slice-4-plan.sh \
  scripts/check-terraform-slice-4-plan_test.sh
git commit -m "test: enforce the Terraform Slice 4 controller gate"
```

## Task 8: Replace Stale Production Operations Documentation

**Files:**

- Modify: `deploy/production/README.md`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`

**Interfaces:**

- Consumes: the implemented workflow, Terraform, systemd, runtime, RDS, and
  recovery commands from Tasks 1-7.
- Produces: one value-blind operator sequence consistent with this plan.

- [x] **Step 1: Add documentation contract assertions**

Extend the existing production documentation tests to reject:

```text
ssh -L
docker buildx build from the Mac
persistent production .env
docker compose up run directly by the operator
public RDS
EIP association
Cloudflare or DNS mutation
non-loopback host port publication before Window 2
```

Require references to Session Manager, `/run/jobcron`, digest pull, systemd,
private verification, recovery manifests, and stop conditions.

- [x] **Step 2: Rewrite only the stale sections**

Keep useful Compose and application explanations. Replace manual blank-host,
SSH, persistent-secret, and public-start steps with:

1. exact Slice 3 checkpoint;
2. immutable private image publication;
3. reviewed saved plan;
4. replacement host apply;
5. Session Manager verification;
6. localhost-only RDS tunnel and lower-privilege role;
7. runtime-secret population outside Terraform;
8. one-shot registry token delivery below `/run/jobcron`;
9. fail-closed systemd start;
10. private-path application verification;
11. database/log archive and trusted-Mac restore verification; and
12. stop before any EIP, Cloudflare, DNS, or public-traffic action.

- [x] **Step 3: Run documentation and publication checks**

```sh
go test ./scripts -run 'TestProduction(Compose|PrivateOps|Docs)' -count=1
git diff --check
```

Expected: PASS and no stale public-cutover instruction.

- [x] **Step 4: Commit**

```sh
git add deploy/production/README.md \
  deploy/production/HUMAN_DEPLOY_GUIDE.md \
  scripts
git commit -m "docs: align production operations with transient SSM runtime"
```

## Task 9: Run The Complete Pre-Cloud Gate

**Files:**

- Modify only if a gate exposes a defect in Tasks 1-8.

**Interfaces:**

- Consumes: exact implementation tip.
- Produces: clean, tested, publication-safe code ready for independent review.

- [x] **Step 1: Run all repository and Slice 4 checks**

```sh
sh scripts/check-production-image-workflow_test.sh
sh scripts/check-production-image-workflow.sh
sh scripts/check-terraform-slice-4-plan_test.sh
./scripts/check-terraform.sh
go test ./deploy/production -count=1
go test ./scripts -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./cmd/jobcron ./cmd/jobcron-import ./cmd/jobcron-user
test -z "$(gofmt -l .)"
git diff --check
```

Expected: every command succeeds.

- [x] **Step 2: Run publication safety**

Inspect the complete staged diff, then run configured Gitleaks over the exact
implementation range. Stop on any real secret, private identifier, personal
data, raw log, or unnecessary production detail. Do not suppress a finding
without proving it is a synthetic fixture.

- [x] **Step 3: Commit gate-only corrections**

```sh
git add -A
git commit -m "test: close Terraform Slice 4 verification gaps"
```

Skip this commit when no correction is required.

## Task 10: Publish The Candidate Image Privately

**Files:**

- Write only private `image.json` and operator evidence.

**Interfaces:**

- Consumes: exact clean implementation commit.
- Produces: verified private GHCR digest for `linux/arm64`.

- [ ] **Step 1: Confirm Slice 3, bootstrap the package, and verify credentials**

Verify the Slice 3 checkpoint is current, the repository workflow environment
is available, and the human-provided replacement-host `read:packages`
credential path exists. Stop before publication if the future host cannot pull
the private image.

Before the first candidate publication, create the unlinked `jobcron` package
from the private controller with a disposable bootstrap image and a classic PAT
used only for `write:packages`. Verify through the separate mode-`0600`
`read:packages` path that the package is private and unlinked. In GitHub package
settings, grant this repository **Manage Actions access** without using
**Connect repository**, then verify the package remains private. Do not put the
classic PAT in the workflow or retain its Docker credential directory.

- [ ] **Step 2: Dispatch the exact image workflow**

Dispatch `publish-production-image.yml` with the full implementation commit.
Verify the job proved the package was already private before any manifest
lookup, build, or push; used only `GITHUB_TOKEN`; kept the package private; and
published only `linux/arm64`. Verify all build output was redirected and no
digest appears in workflow logs, outputs, artifacts, annotations, or summary.
The workflow must refuse dispatch if the commit-derived tag already exists.

After success, authenticate from the trusted controller with the separate
private package-read path, resolve the immutable commit tag without printing
the response, verify exactly one `linux/arm64` manifest, and save the digest
only to private mode-`0600` `image.json`. Do not send the digest through chat,
mail, issue text, or any tracked/public evidence.

- [ ] **Step 3: Preserve rollback image evidence**

If a prior approved digest exists, retain it. Do not delete package versions or
reuse either tag. The new digest becomes `current`; the prior digest becomes
`previous`.

Stop on a mutable-only tag, public/internal package, multi-platform mismatch,
tag reuse, missing privately resolved digest, public digest disclosure, or
unexpected workflow permission.

## Task 11: Generate, Check, And Independently Review The Saved Plan

**Files:**

- Write only private controller artifacts.

**Interfaces:**

- Consumes: exact integrated Slice 3 commit, exact Slice 4 implementation tip,
  current credentials, private tfvars, and aggregate cost evidence.
- Produces: reviewed `slice-4.tfplan` authorized for one apply.

- [ ] **Step 1: Revalidate private inputs without printing them**

```sh
umask 077
test -s .superpowers/sdd/260728-terraform-slice-4/controller.env
test -s .superpowers/sdd/260728-terraform-slice-4/production.auto.tfvars.json
test -s .superpowers/sdd/260728-terraform-slice-4/aggregate-cost.json
test -s .superpowers/sdd/260728-terraform-slice-4/slice-3-checkpoint.json
aws sts get-caller-identity --query 'Account' --output text >/dev/null
```

Expected: all checks pass with no identifier printed.

- [ ] **Step 2: Create the saved plan and JSON privately**

```sh
terraform -chdir=infra/terraform/production init -input=false \
  -backend-config="$TF_BACKEND_CONFIG"
terraform -chdir=infra/terraform/production plan -input=false \
  -var-file="$TF_SLICE4_VAR_FILE" \
  -out="$TF_SLICE4_PLAN" \
  >"$TF_SLICE4_PLAN_LOG" 2>&1
terraform -chdir=infra/terraform/production show -json "$TF_SLICE4_PLAN" \
  >"$TF_SLICE4_PLAN_JSON"
```

All variables above are loaded from private `controller.env`. The plan/log paths
remain ignored and mode `0600`.

- [ ] **Step 3: Run controller gates**

```sh
scripts/check-terraform-slice-4-plan.sh \
  "$TF_SLICE4_PLAN_JSON" \
  "$TF_AGGREGATE_COST_JSON" \
  "$TF_SLICE3_CHECKPOINT_JSON"
```

Expected:

```text
resource_changes=5
output_changes=1
sensitive_outputs=1
destroy_or_replace=0
aggregate_cost=PASS
slice3_checkpoint=PASS
PASS
```

- [ ] **Step 4: Obtain independent review**

The reviewer receives the exact commit, saved-plan SHA-256, value-blind checker
output, aggregate-cost category summary, and this plan. The reviewer writes
`slice-4-review.md` with:

```text
Verdict: APPROVED
Commit: verified
Saved plan digest: verified
Address/action allow-list: exact
Destroy/replace: zero
Aggregate cost: within both ceilings
Old EC2/RDS/EIP/rollback materials: preserved
Private values in shared evidence: none
Recovery and stop conditions: executable
```

Any other verdict blocks apply. Regenerating the plan invalidates the review.

## Task 12: Apply Once And Prove The Host Boundary

**Files:**

- Write only private operational evidence.

**Interfaces:**

- Consumes: the exact independently approved binary plan.
- Produces: replacement host reachable through Session Manager and no public
  application traffic.

- [ ] **Step 1: Recheck the saved plan digest and apply**

Apply only:

```sh
terraform -chdir=infra/terraform/production apply -input=false \
  "$TF_SLICE4_PLAN"
```

Do not rerun `plan` between review and apply.

- [ ] **Step 2: Verify Terraform and AWS invariants value-blind**

Prove:

- Session Manager reports the instance online;
- no key pair and no inbound rule exist;
- port `22` is absent;
- the instance attaches only `aws_security_group.origin`;
- the origin group still has no ingress;
- the existing database PostgreSQL rule still references the origin group and
  is unchanged;
- root volume is encrypted `gp3`, 8 GiB;
- IMDSv2 is required;
- the instance role has only the approved policies;
- the host cannot read the RDS master secret;
- the host cannot list/read/delete recovery objects;
- the host can write only a new `jobcron/*` object;
- the reserved EIP remains unattached;
- old EC2 and old RDS remain unchanged; and
- `jobcron.service` is installed but stopped.

Record booleans and counts only.

- [ ] **Step 3: Run a no-change plan**

Generate a fresh plan after apply. It must contain zero changes. Any drift,
replacement, unexpected address, or old-resource action stops the sequence.

## Task 13: Create The App Role And Populate Secrets Privately

**Files:**

- Write only private `database-role.env`, `runtime-secret.json`, and registry
  token input.

**Interfaces:**

- Consumes: Session Manager host access and private RDS master credential on
  the trusted Mac.
- Produces: lower-privilege `DATABASE_URL`, complete runtime secret version,
  and one-shot host pull credential.

- [ ] **Step 1: Open the localhost-only SSM RDS tunnel**

Run the exact command from Task 6 in a dedicated trusted-Mac terminal. Confirm
the local listener is `127.0.0.1` and RDS remains private.

- [ ] **Step 2: Create or rotate the lower-privilege application role**

Run `scripts/production-rds-role.sh`. Verify its grants through catalog queries
without printing names or passwords. The instance must never receive the
master secret.

- [ ] **Step 3: Build and validate `runtime-secret.json` locally**

Include every required existing production value plus:

```text
approved immutable image digest
credential-encryption master key
lower-privilege TLS DATABASE_URL
cohort signup access code
Stage 1 sponsor user ID
proxy secret
Origin CA certificate
Origin CA private key
```

Validate required JSON keys, string types, non-empty values, certificate/key
shape, digest shape, and URL TLS mode without printing values.

- [ ] **Step 4: Put one secret version outside Terraform**

Use `aws secretsmanager put-secret-value` with
`fileb://.../runtime-secret.json`. Save only success metadata privately.
Terraform state and plans must remain unchanged.

- [ ] **Step 5: Deliver the separate registry credential**

Through Session Manager, create `/run/jobcron/registry-token` mode `0600` from
stdin. It must be a classic token scoped only `read:packages`, not
`GITHUB_TOKEN`, not part of the runtime secret, and not persisted after pull.

## Task 14: Start Privately And Verify Real User Behavior

**Files:**

- Write only private verification evidence.

**Interfaces:**

- Consumes: complete runtime secret, registry token, and private RDS role.
- Produces: privately running Jobcron/Caddy, recovery proof, and Slice 4 exit
  checkpoint.

- [ ] **Step 1: Prove fail-closed behavior first**

With an intentionally incomplete synthetic secret version:

1. start `jobcron.service`;
2. verify the old stack was stopped;
3. verify preparation failed;
4. verify neither Jobcron nor Caddy runs; and
5. verify `/run/jobcron` contains no partial secret file.

Restore the approved complete secret version privately.

- [ ] **Step 2: Start the approved digest**

Start `jobcron.service`. Verify:

- pull used the one-shot memory-backed Docker credential;
- the credential and Docker config were removed;
- no home-directory Docker credential exists;
- current and previous digests are retained;
- older unreferenced images are pruned only after success;
- both containers are healthy;
- the app connects to RDS with TLS and the lower-privilege role; and
- only the two loopback host ports exist and no public ingress exists.

- [ ] **Step 3: Walk the private user path**

Use Session Manager port forwarding from a trusted-Mac local port to replacement
host port `7777`. With the required browser workflow against that local HTTP
endpoint, walk:

1. login page render;
2. owner login;
3. dashboard render;
4. profile read and save;
5. archive navigation;
6. one scrape/re-rate action that is safe for the production cohort; and
7. logout and failed-session reuse.

Verify expected content and state, not only HTTP status. Record sanitized
outcomes in `private-verification.md`. This is private-path verification only;
do not attach the EIP or alter Cloudflare/DNS. Separately forward a trusted-Mac
local port to replacement host port `8443` and verify Caddy presents the
configured Origin CA certificate using the private CA verification material;
do not bypass certificate verification.

- [ ] **Step 4: Prove reboot recovery**

After the first successful private verification, enable `jobcron.service` and
reboot the replacement host. Verify `/run/jobcron` was cleared, systemd
recreated complete files with correct modes, no persistent `.env` or TLS key
exists, and the already-present approved digest starts again without requiring
or exposing a registry token.

- [ ] **Step 5: Prove database and log recovery**

Run one recovery service. From the trusted Mac:

1. pull missing dump, log, and manifest objects;
2. verify SHA-256 manifests;
3. tag verified objects;
4. restore the custom-format dump into a disposable database;
5. compare schema and bounded per-table row counts; and
6. verify sanitized logs contain no headers, cookies, tokens, secrets, or
   unnecessary personal data.

Confirm `aws_s3_bucket_lifecycle_configuration.recovery` is unchanged from the
Slice 3 checkpoint: objects tagged `macbook-copy=verified` are eligible for
expiry only after at least 14 days, while objects without that verified tag
remain for 90 days. Verify one tagged and one untagged synthetic timestamp
case value-blind before relying on the lifecycle. Do not enable Object Lock and
do not delete any recovery object in Slice 4.

- [ ] **Step 6: Enforce the disk ceiling**

After normal image/log pruning, record free bytes. Keep the encrypted root at
8 GiB unless free space is below 2 GiB. If below, stop and return to the human
with measured evidence; volume expansion is one-way and requires a new
versioned plan.

## Task 15: Close The Slice Without Public Cutover

**Files:**

- Modify: `docs/architecture.md`
- Move sanitized completed implementation knowledge only as directed by the
  documentation lifecycle.

**Interfaces:**

- Consumes: all private verification evidence and clean Terraform plans.
- Produces: durable architecture truth and a Slice 4 completion checkpoint for
  Slice 5.

- [ ] **Step 1: Run final no-change and publication gates**

Run the complete Task 9 suite again, then no-change plans for every affected
Terraform root. Inspect tracked documentation for private identifiers and run
Gitleaks over the exact final range.

- [ ] **Step 2: Update architecture truth**

Document only sanitized durable facts: replacement host class, SSM/no-SSH
boundary, transient runtime, fail-closed service, narrow IAM, private RDS,
digest-only images, and recovery flow. Do not include exact identifiers,
addresses, endpoints, costs, private paths, or evidence values.

- [ ] **Step 3: Record the Slice 4 exit checkpoint privately**

The checkpoint must say:

```text
implementation commit verified
private image and arm64 digest verified
saved plan independently approved and applied exactly once
post-apply plan clean
old EC2 and old RDS preserved
reserved EIP unattached
no Cloudflare, DNS, or public traffic change
SSM/no-SSH boundary verified
runtime fail-closed and reboot recovery verified
private user path verified
database restore and log sanitization verified
aggregate launch cost remains within both ceilings
rollback window remains open
```

- [ ] **Step 4: Commit durable closeout**

```sh
git add docs/architecture.md deploy/production infra/terraform/production \
  scripts .github/workflows
git commit -m "docs: record the verified Terraform Slice 4 architecture"
```

Do not delete the old EC2, old RDS, prior image, reserved EIP, recovery
materials, or private evidence. Do not begin EIP association, Cloudflare, DNS,
or public traffic work. Slice 5 may start only from this exact checkpoint.

## Stop Conditions

Stop immediately and return control to the human when any of these occurs:

- Slice 3 is incomplete, stale, ambiguous, or exposes a different interface.
- The approved image is not private, not `linux/arm64`, not digest-addressed, or
  cannot be pulled with a separate `read:packages` credential.
- The saved plan differs from the five exact resource creates or the one exact
  sensitive output create.
- Any destroy, replace, update, import, move, forget, EIP, Cloudflare, DNS,
  public ingress, port `22`, key-pair, RDS, or secret-version action appears.
- Aggregate reliable cost may exceed either approved whole-launch ceiling.
- Credentials expire, discovery is ambiguous, or the reviewed plan is
  regenerated.
- The instance can read the RDS master secret or read/delete recovery objects.
- A private value appears in Git, Terraform inputs/state/plan, user data,
  workflow output, chat, issue text, screenshot, or shared log.
- Missing/malformed secrets do not stop both containers.
- `/run/jobcron` permissions, credential cleanup, reboot recreation, private
  user path, archive manifests, or disposable restore verification fails.
- Normal pruning leaves less than 2 GiB free.
- Any action would close the rollback window or mutate public traffic.

## Rollback And Recovery Boundary

Before public cutover, rollback means stop the replacement stack, preserve its
evidence, and continue using the unchanged old resources. A failed host can be
recreated from Terraform, the approved current/previous image digests, the
runtime secret, RDS, S3 recovery objects, and trusted-Mac copy. Never delete or
recreate RDS as a host-recovery step. Any Terraform state problem stops work;
recover state before cloud mutation. The rollback window remains open until the
human explicitly closes it in a later authorized slice.
