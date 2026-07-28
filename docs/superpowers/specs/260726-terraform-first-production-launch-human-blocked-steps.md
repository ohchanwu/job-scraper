# Jobcron Terraform-First Production Launch: Human-Blocked Steps

**Status:** Active under the approved two-window launch authorization<br>
**Recorded:** 2026-07-26<br>
**Owner:** Human operator, assisted by agents where appropriate<br>
**Infrastructure authority:** [Terraform AWS foundation and Cloudflare ingress
automation][terraform-spec]
<br>
**Authorization decision:** [Two-window first-production-launch
authorization][two-window-decision]
<br>
**Remote checklist:** [Pre-Batch-1 human input checklist][pre-batch-1-checklist]

**Input contract:** [Pre-Batch-1 Window 1 authorization
contract][pre-batch-1-contract]

## Purpose

Define the decisions, private inputs, approval gates, and real-world verification
that only the human operator can supply for Jobcron's first production launch.
This document is not an executable command runbook and does not authorize an AWS,
Cloudflare, registry, DNS, database, or production change by itself.

Each of the six implementation slices in the Terraform specification requires
its own implementation plan, saved Terraform plan, independent review, and
verification evidence. Window 1 authorizes policy-compliant applies for Slices
2 through 5 without repeated human approval of each plan instance. Window 2
reserves the final EIP attachment, DNS, and public-traffic cutover for one
explicit human response. Those slice plans own exact commands. This document
owns the human-only inputs and checkpoints that those plans must not bypass.

## Supersession History

The [July 16 human-blocked launch specification][archived-spec] was never
implemented. Before the alpha deployment, the product sequence changed to
implement pre-alpha milestone 2 first. That work expanded the launch surface to
include multi-user accounts, cohort-gated signup, Anthropic/OpenAI/Gemini
bring-your-own-key support, sponsor-funded Stage 1 analysis, and contextual
dealbreaker provenance.

The later Terraform-first infrastructure decision also replaced the old
document's manual host assumptions. The archived specification therefore must
not be executed: it assumes SSH, an existing persistent EC2 `.env`, public port
80, Caddy-managed public certificates, and a manually prepared host.

The following execution artifacts share some of those stale assumptions and are
non-authoritative until a Terraform implementation slice updates and verifies
them:

- `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- `deploy/production/README.md`
- `deploy/production/compose.yaml`
- `docs/superpowers/plans/260715-postgresql-convergence-slice-5-first-production-deployment.md`

## Verified Planning State

As recorded in the Terraform infrastructure specification:

- the existing EC2 and RDS resources are disconnected and are not a functional
  production stack;
- bootstrap, production, and edge Terraform roots now use separate protected
  state keys; the bootstrap state is remote, native locking and version
  recovery are verified, the production OIDC role has state and narrow network
  read access, and the edge OIDC role remains state-only;
- static Terraform CI and the protected production plan-only workflow are
  verified; Terraform now owns the adopted canonical public network and one
  unattached reserved EIP, but no replacement host, private RDS tier, or
  automated Cloudflare prefix-list path exists yet;
- the old EC2, old VPC, and old RDS are retained rollback resources, not the
  target architecture;
- the replacement architecture uses a canonical VPC, private PostgreSQL RDS,
  a replacement EC2 host managed through Session Manager, transient runtime
  secrets below `/run/jobcron`, Caddy on origin port 443 only, and Cloudflare
  Full (strict); and
- infrastructure implementation must precede production cutover.

Every drift-prone cloud fact must be re-verified through authenticated tools
before planning or applying a change. Tracked documentation must contain no real
account identifiers, resource identifiers, addresses, endpoints, credentials,
certificates, or private recovery locations.

## Authority Boundary

An agent may prepare plans, inspect value-blind metadata, run approved checks,
and execute a Window 1 change without the human present when every controller
policy gate passes. An agent must not:

- invent or select the human's identity, account, billing, or recovery values;
- reveal, copy into Git, or echo undisclosed secret values;
- set or broaden the human-approved spending ceiling;
- choose the canonical production resources without an authenticated inventory;
- enable public traffic before the human authorizes cutover;
- delete rollback resources or close the rollback window; or
- treat a successful command as proof of a successful user experience.

## Global Hard Gates

These gates apply to every implementation slice:

1. No state-changing phase starts until the exact saved plan passes independent
   review and the applicable Window 1 controller policy gate or Window 2 human
   approval.
2. No real secret may enter Git, Terraform variables, plans, state, EC2 user
   data, issue text, chat, screenshots, or shared logs.
3. No cloud identifier or personal address may enter tracked documentation.
4. Jobcron and Caddy remain stopped while the runtime secret is absent,
   incomplete, malformed, or unavailable.
5. The existing EC2, VPC, RDS, and recovery materials remain intact until the
   explicit rollback-close checkpoint.
6. The EIP attachment and DNS cutover cannot start until the replacement stack
   passes private-path data, application, archive, and recovery checks.
7. Each user-facing claim must be verified through the same browser path a real
   user will use. HTTP status checks alone are insufficient.
8. Window 1 plans contain no destroy or replace action, use only the slice's
   explicit address-and-action allow-list, preserve the old EC2, old RDS,
   inherited network relationships, unattached reserved EIP, and rollback
   materials, and stay within the approved spending ceiling.
9. Live discovery is unambiguous and satisfies the documented deterministic
   selection contract; credentials are available and current.
10. Any ambiguity, drift, missing credential, unexpected action or address,
    policy broadening, spending violation, failed verification, uncertain
    recovery, destroy or replace action, or rollback-close need stops the
    sequence and returns control to the human.

## Human-Controlled Inputs

Prepare private values outside Git. This public file may record only a
value-blind completion state; the underlying values and evidence belong in the
access-controlled operator log.

### Window 1 Inputs And Authority

- [x] Authenticated `jobcron-admin` AWS CLI profile backed by IAM Identity
      Center and MFA
- [x] Expected AWS account, role, and region, checked without publishing their
      exact values
- [x] Human access to the intended Cloudflare account and zone, with later
      ability to authorize Origin CA and DNS work
- [x] OCI registry selection, image-visibility decision, workflow publication
      authority, and human recovery capability
- [ ] Least-privilege private replacement-host image-pull credential or
      equivalent approved pull mechanism
- [x] Maximum approved infrastructure spend
- [x] Access-controlled operator-log location
- [x] Private rollback decision owner and the condition that ends the rollback
      window

### Application And Data

- [ ] Approved release commit SHA
- [ ] Immutable `linux/arm64` image reference and digest
- [ ] Owner identity and password
- [x] Cohort signup access code for `JOBCRON_SIGNUP_ACCESS_CODE`
  - **OF** already exists
- [ ] Sponsor user ID for `JOBCRON_STAGE1_SPONSOR_USER_ID`
- [ ] Production session secret
- [ ] Credential-encryption master key, plus a separately stored recovery copy
- [ ] Immutable SQLite snapshot with its matching durable `-wal` file
- [x] Human-approved provider credentials for the minimal paid AI checks
  - **OF** use the existing Anthropic API key

### Infrastructure And Edge

- [ ] Mayor-prepared private inventory of the existing VPC, subnets, route
      tables, EIPs, EC2, RDS, security groups, DNS records, and rollback values
- [x] Deterministically selected canonical VPC and public-network candidate,
      plus confirmation that no existing EIP fills the reserved cutover role
- [ ] Slice 3 non-overlapping private subnet CIDRs that pass its controller
      policy gate
- [ ] Cloudflare Origin CA certificate and private key
- [ ] Private record of Origin CA expiry and a renewal reminder
- [ ] Private locations for Terraform recovery evidence, database archives,
      log archives, and verified MacBook copies

### Human Interaction Timing

Task 3's two security fixes are internal: independent agents review them and
execution continues without human approval of the fixes themselves. If AWS
reauthentication is required, the next expected human action is value-blind AWS
SSO device approval. Otherwise, the next expected human action is the
consolidated Window 1 input packet if human-controlled inputs remain missing.
After SSO approval and any required Window 1 inputs, Window 2 cutover is the
next planned approval. A stop condition is an exceptional human-facing
interruption.

## Authorization Gates By Terraform Slice

The slice order is load-bearing. Planning, implementation, and independent
review may be front-loaded, but no apply may run before its dependency passes
its exit checkpoint.

### Slice 1: Identity, State Bootstrap, And Terraform CI

Completed human actions and approvals:

- [x] Configure and authenticate `jobcron-admin` through IAM Identity Center.
- [x] Confirm the caller identity, expected role, and expected region without
      publishing exact values.
- [x] Review and approve the bootstrap resource names, access boundaries,
      encryption, versioning, and lock strategy.
- [x] Review and approve the exact local bootstrap plan before its apply.
- [x] Confirm state migrated to the protected S3 backend and test recovery from
      an object version.
- [x] Review the production and edge GitHub OIDC trust boundaries.
- [x] Confirm CI is plan-only for production and that no long-lived AWS access
      keys were added.

Exit evidence:

- authenticated Identity Center access works;
- remote state, native locking, encryption, versioning, and recovery work;
- the protected production workflow cannot apply without human approval; and
- the narrow edge role cannot mutate production compute, database, IAM, or
  secrets.

### Slice 2: Canonical VPC Adoption And EIP Reservation

Window 1 controller policy gates:

- [x] Authenticated inventory selects one canonical VPC and public-network
      candidate unambiguously and confirms that no existing EIP fills the
      reserved cutover role.
- [x] An independent reviewer approves the exact two-plan adoption packet: one
      narrow production-network read policy plus attachment and one
      production plan with eight imports and one unattached EIP creation.
- [x] Machine checks prove both plans use only the explicit allow-list, contain
      no destroy or replace action, remain within the approved spending ceiling,
      and preserve the old EC2, old RDS, routes, subnets, inherited main-route
      relationship, and rollback materials.

No private subnet CIDR choice is required in Slice 2. Exact CIDR selection and
policy validation occur in Slice 3 immediately before subnet creation.

A human decision is required only if inventory is ambiguous or another stop
condition fires.

Exit evidence:

- Terraform owns the eight approved existing network objects and one new,
  unattached EIP;
- the post-import plan is clean; and
- no production or rollback resource was replaced or deleted.

### Slice 3: Private Database Tier And Secret Containers

Window 1 controller policy gates:

- [ ] Independently review the plan for two private database subnets, database
      subnet group, security groups, new encrypted RDS instance, recovery
      bucket, and empty runtime-secret container.
- [ ] Confirm the plan uses RDS-managed master credentials and does not expose a
      secret version to Terraform.
- [ ] Apply only after machine checks prove deletion protection,
      `prevent_destroy`, backup retention, TLS requirements, and public-access
      settings, plus every global Window 1 policy requirement.
- [ ] Record the new RDS restore identifiers and recovery evidence privately.

Exit evidence:

- RDS is private, encrypted, backed up, deletion-protected, and reachable on
  port 5432 only from the application security group;
- Terraform cannot read or persist runtime or RDS master secret values; and
- the recovery bucket is private, encrypted, versioned, and protected.

### Slice 4: Replacement EC2, Transient Runtime, And Recovery

Window 1 controller policy gates and human-supplied inputs:

- [ ] Independently review the replacement-host, IAM, Session Manager, bootstrap,
      Caddy, and archive plan.
- [ ] Confirm the host has encrypted storage, no SSH key pair, no port 22
      ingress, and only the narrow permissions approved by the Terraform spec.
- [ ] Establish Session Manager access and an SSM port-forward from the trusted
      Mac to private RDS.
- [ ] Create the lower-privilege application database role without revealing
      the RDS master credential.
- [ ] Populate the runtime secret outside Terraform with every required
      application value, the immutable image reference, lower-privilege
      `DATABASE_URL`, cohort signup code, sponsor user ID, and Origin CA
      certificate and key.
- [ ] Confirm missing or malformed secret fields keep Jobcron and Caddy stopped.
- [ ] Confirm successful preparation writes sensitive material only below the
      memory-backed `/run/jobcron` path with restrictive permissions.
- [ ] Verify one database dump and one sanitized log archive reaches the
      recovery bucket, is copied to the trusted Mac, and passes its manifest
      check.

Exit evidence:

- Session Manager replaces SSH;
- the instance cannot read the RDS master secret or delete recovery archives;
- reboot recreates the transient runtime files without leaving a persistent
  `.env` or TLS private key;
- the approved image starts privately and connects to RDS with TLS through the
  lower-privilege role; and
- a pulled database dump restores into a disposable database with matching
  schema and row-count evidence.

### Slice 5: Cloudflare Prefix-List Automation

Window 1 controller policy gates:

- [ ] Independently review the fetched official Cloudflare IPv4 set, validation result,
      prefix-list quota, saved edge plan, and narrow edge-role permissions.
- [ ] Apply only when machine checks prove the plan can change the tagged prefix
      list and its single origin port 443 ingress rule.
- [ ] Confirm malformed, empty, implausible, duplicate, or oversized upstream
      data fails without changing AWS.
- [ ] Confirm a no-change scheduled run is a no-op.

Exit evidence:

- the origin security group accepts port 443 only through the managed
  Cloudflare prefix list;
- direct internet access to ports 22, 80, 7777, and 5432 is blocked; and
- an upstream fetch failure preserves the last valid prefix-list version.

### Slice 6: Data Bootstrap, Cutover, And Production Verification

#### Release And Data Preparation

- [ ] Re-run the repository's documented build, full test, race, lint,
      formatting, and production-mode checks on the exact release SHA.
- [ ] Run Gitleaks and manually inspect the publication diff.
- [ ] Build and publish the immutable `linux/arm64` image, inspect its manifest,
      and record its digest privately.
- [ ] Take the immutable SQLite snapshot and matching durable WAL file without
      opening the live source database in a way that changes it.
- [ ] Through the SSM port-forward, create the owner using the reviewed secure
      prompt flow.
- [ ] Create a pre-import RDS snapshot.
- [ ] Run the reviewed import dry run, review its source fingerprint and
      counts, apply it once, then confirm the required idempotence behavior.
- [ ] Record import and snapshot evidence privately.

#### Private-Path Verification Before Cutover

- [ ] Verify migrations, owner login, imported data, provider-credential
      storage, one paid provider call, scheduled scrape configuration, database
      dump, log archive, and restart behavior while public traffic remains off.
- [ ] Verify the current production configuration includes cohort signup and
      sponsor-funded Stage 1 without exposing either configured value.
- [ ] Confirm the replacement stack is healthy and the old resources remain
      available for rollback.

#### EIP, DNS, And Cloudflare Cutover

- [ ] Present one consolidated Window 2 packet containing the exact cutover
      scope, private-path verification result, rollback readiness, value-blind
      change summary, and stop conditions.
- [ ] Obtain the human's explicit approval of that exact packet before any EIP,
      DNS, or public-traffic change.
- [ ] Issue or confirm the Origin CA certificate covers the apex and `www`.
- [ ] Populate the runtime secret before Caddy starts.
- [ ] Record current DNS and EIP rollback values privately.
- [ ] Re-associate the EIP only after all private-path gates pass.
- [ ] Set the apex and `www` behavior, enable proxying, and select Full
      (strict).
- [ ] Keep origin port 80 closed. Defer HSTS until stable HTTPS and rollback
      behavior are proven.

#### Real Browser Acceptance

Using a real browser through the proxied public hostname:

- [ ] The apex and `www` behavior completes without a certificate error or
      redirect loop.
- [ ] Owner login works and the expected imported profile appears.
- [ ] Profile edits persist across logout, login, and container recreation.
- [ ] Job listings, score order, score details, bookmarks, hidden jobs, and
      canonical outbound job links show the expected content.
- [ ] Anthropic, OpenAI, and Gemini credential states remain isolated per user.
- [ ] A minimal paid AI re-rate succeeds with a human-approved provider key.
- [ ] Contextual dealbreaker evidence and blocker states render as specified.
- [ ] Cohort signup rejects an incorrect access code and accepts the approved
      code without revealing it.
- [ ] Sponsor-funded Stage 1 work is attributed to the configured sponsor
      without granting an application role.
- [ ] The scheduled scrape survives application restart and its next-run state
      is visible.
- [ ] No console error appears during the walked launch paths.

#### Durability And Closeout

- [ ] Recreate the application container and confirm PostgreSQL-backed state and
      encrypted provider credentials remain usable.
- [ ] Confirm automated RDS backups and point-in-time recovery are active.
- [ ] Confirm a fresh dump and log archive reach S3 and the MacBook copy verifies
      their manifests.
- [ ] Run a final no-change Terraform plan for each root.
- [ ] Store sanitized verification evidence in tracked documentation and exact
      operational evidence only in the private log.
- [ ] Keep the old resources until the human explicitly closes the rollback
      window.

## Failure And Rollback Contract

### Before EIP Or DNS Cutover

- Stop the sequence.
- Keep the old resources untouched.
- Destroy only newly created disposable resources, and only after reviewing a
  saved destroy plan.
- Repair the failed slice and start again from a clean saved plan.

### After EIP Cutover But Before New Production Writes

- Re-associate the EIP to the old instance.
- Disable Cloudflare proxying or restore the recorded edge state if required by
  the approved rollback procedure.
- Keep the new resources for diagnosis unless the human approves their removal.

### After New PostgreSQL Writes Begin

- Treat the new RDS database as authoritative.
- Roll back the application only to a schema-compatible immutable image.
- Use an approved snapshot or point-in-time recovery when data recovery is
  required.
- Never restore SQLite as a writable production database.

### Terraform State Failure

- Recover state from a known S3 object version.
- Reconcile state with read-only inspection and a reviewed plan.
- Never reconstruct missing state through a blind apply.

### EC2 Loss

- Recreate the host through Terraform.
- Restore runtime configuration from the approved Secrets Manager version.
- Restore data from RDS or the verified archive path as appropriate.
- Do not rebuild the server through undocumented manual SSH steps.

### Credential-Encryption Key Loss

- Restore the separately controlled recovery copy.
- If no valid recovery copy exists, require affected users to replace their
  provider credentials.
- Never rotate the key by silently making existing ciphertext unreadable.

## Definition Of Done

This human-blocked specification is complete only when:

1. all six Terraform slices have independently reviewed plans, verified applies,
   and recorded Window 1 policy or Window 2 human checkpoints;
2. the replacement stack satisfies the Terraform infrastructure acceptance
   criteria;
3. the exact release, import, paid-provider, browser, durability, archive, and
   recovery checks above pass;
4. the old EC2, VPC, and RDS are removed only after the human closes the
   rollback window;
5. `docs/architecture.md`, the production deployment guide, Compose/Caddy
   references, and the Superpowers index describe the deployed architecture;
6. no secret or private production identifier appears in tracked artifacts,
   Terraform state, plans, or shared logs; and
7. the human records the Window 2 go-live and rollback-close decisions
   privately.

## Out Of Scope

- Autoscaling, an Application Load Balancer, private application subnets,
  Multi-AZ RDS, origin IPv6, and HSTS
- Routine application releases after the first production launch
- Storing real operational values in tracked documentation
- Treating this checklist as authorization to apply or cut over

[archived-spec]: ../archive/2026-07-26-first-production-launch-human-blocked-steps/260716-first-production-launch-human-blocked-steps.md
[terraform-spec]: 260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
[pre-batch-1-checklist]: 260728-pre-batch-1-human-input-checklist.md
[pre-batch-1-contract]:
  260728-pre-batch-1-window-1-authorization-contract.md
[two-window-decision]:
  ../decisions/260727-two-window-first-production-launch-authorization.md
