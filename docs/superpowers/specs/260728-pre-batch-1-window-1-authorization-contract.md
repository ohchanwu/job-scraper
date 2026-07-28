# Pre-Batch-1 Window 1 Authorization Contract

**Status:** Active; all six human decision groups are approved

**Recorded:** 2026-07-28

**Human input evidence:** [Pre-Batch-1 human input checklist][checklist]

**Standing authority:** [Two-window first-production-launch
authorization][two-window]

**Execution order:** [Terraform-first production launch roadmap][roadmap]

## Purpose

Convert the public-safe answers in the pre-Batch-1 checklist into an exact
execution contract for Mayor/Gas Town. This document defines what the answers
authorize, what they do not authorize, which conditional human actions may
remain during Batch 1, and when execution must stop.

Tracked documentation is public. This contract records only decision classes,
limits, and completion states. Credentials, cloud identifiers, personal data,
private topology, recovery locations, and production profile contents remain
outside Git.

## Batch 1 Scope

In this contract, Batch 1 is the bounded Window 1 execution after Slice 2:

1. Slice 3 private database tier and empty secret containers;
2. Slice 4 replacement EC2, Session Manager, transient runtime, private
   recovery paths, and private-path application verification; and
3. Slice 5 Cloudflare prefix-list automation inside AWS.

The immutable `linux/arm64` image may be built and published during Batch 1
because Slice 4 cannot verify the replacement runtime without it. Publication
is preparatory work, not public cutover authority.

Each state-changing apply remains limited to the exact resource-address and
action allow-list in its independently reviewed slice plan. This contract does
not authorize an unplanned resource merely because it fits under the spending
ceiling.

This contract deliberately does not duplicate slice allow-lists or discovery
selectors. The controller must consume the versioned Slice 3, 4, or 5 plan that
defines them. A missing or stale slice plan is a stop condition, not permission
to infer an allow-list.

## Verified Human Decisions

### 1. Spending Ceiling

The human approved:

- a maximum of USD 100 in recurring monthly cost; and
- a maximum of USD 200 in aggregate one-time launch cost.

These are whole-launch ceilings, not per-slice allowances. Cost review must
include the first-production AWS, public IPv4, database, compute, storage,
backup, registry, and Cloudflare resources. A plan must stop if its reliable
estimate plus already approved launch resources may exceed either ceiling.

The ceilings do not override a slice allow-list, the no-destroy rule, or any
security gate.

### 2. Rollback Ownership And Close Condition

The rollback decision owner is the human operator.

The rollback window remains open for at least seven consecutive days after
public cutover. It may close only after all of these checks pass:

- login and signup gating;
- scrape and AI evaluation;
- daily briefing and archive/history;
- backup and restore rehearsal;
- monitoring; and
- real-browser checks.

No unresolved Critical or P1 incident may remain. Mayor may preserve and test
rollback resources, but may not delete them or declare the rollback window
closed.

### 3. Container Registry

The selected registry is GitHub Container Registry at `ghcr.io`. The image must
be private and deployed by immutable digest rather than by a mutable tag alone.

The authentication contract has two separate paths:

1. A GitHub Actions workflow publishes the repository's image with its built-in
   `GITHUB_TOKEN` and only the required `packages: write` permission.
2. The replacement host pulls the private image through a separate,
   least-privilege private credential path. A workflow-scoped `GITHUB_TOKEN`
   must not be treated as a reusable host credential.

GitHub's current container-registry documentation says a new package defaults
to private, but the workflow must still verify the actual package visibility
after first publication. If the replacement-host pull path is absent, Mayor
must request one private human action. The preferred fallback is a classic
personal access token with `read:packages` only, stored through the approved
Slice 4 private bootstrap path. The pull must use a memory-backed, one-shot
Docker credential directory below `/run/jobcron`, with directory mode `0700`
and file mode `0600`. The token must enter `docker login` through standard
input, the image must be pulled by digest, and the temporary credential
directory must be removed immediately afterward. Verification must prove that
no persistent home-directory Docker credential file was created. The token is
not part of the application runtime-secret payload. No registry token may
enter Git, Terraform, workflow logs, command history, chat, or tracked
evidence.

### 4. Cloudflare Access And Timing

The human confirmed access to the intended Cloudflare account and DNS zone and
can authorize Origin CA and DNS work when asked.

Batch 1 does not require a Cloudflare API token. Slice 5 consumes Cloudflare's
publicly published IPv4 ranges and changes only its narrow AWS prefix-list and
origin-ingress allow-list. If a Slice 5 plan unexpectedly requires a
Cloudflare account mutation, execution must stop because the plan has crossed
its approved boundary.

Origin CA issuance, DNS changes, proxying, and public traffic belong to Window
2. They require the later consolidated cutover packet and a new explicit human
approval.

### 5. Credential-Recovery Copy

The selected recovery destination class is a human-controlled password
manager. It is independent of the deployment host, and the human confirmed
that they can retrieve it during a recovery exercise.

Mayor may generate the credential-encryption key privately, but Batch 1 may not
claim recovery readiness until a separate copy exists in the approved password
manager and a value-blind retrieval confirmation is recorded. If no
agent-accessible password-manager write path exists, Mayor must write the value
once to an ignored local handoff file with mode `0600`, communicate only that
file's private location, and ask the human to copy it into the password
manager. After the human confirms retrieval, Mayor removes the temporary
handoff file and records only pass/fail evidence. The value must never pass
through chat or a tracked file.

### 6. Production Profile And Private Password Path

The ignored local production profile exists at:

```text
.superpowers/profile/jobcron-profile.md
```

Its contents were not inspected for this public contract. The human confirmed
that the profile was verified locally, any required correction was confined to
that ignored file, and the production password will be supplied or generated
through a private path.

This completes the final human-input gate. No profile value, owner identity,
password, API key, signup code, or sponsor identifier may be copied into the
tracked checklist, this contract, Terraform input, logs, issues, or chat.

## Mayor/Gas Town Batch 1 Authority

Slice 2 has passed its exit checkpoint and all six human decision groups are
complete. Mayor/Gas Town may therefore prepare and execute Batch 1 without
per-plan human approval when every saved plan:

- passes independent review;
- contains no destroy or replace action;
- contains only its slice's explicit addresses and actions;
- stays inside both aggregate spending ceilings;
- preserves the old EC2, old RDS, inherited network relationships, unattached
  reserved EIP, and rollback materials;
- follows an unambiguous deterministic discovery result;
- uses current short-lived credentials;
- exposes no private value; and
- passes the slice's verification and recovery checks.

For this gate:

- independent review means a reviewer other than the plan implementer approves
  the exact saved-plan digest and controller report;
- current credentials means the `jobcron-admin` SSO session passes the
  repository's value-blind expected-account, role, and region check immediately
  before planning and applying; and
- cost evidence records the pricing source and date, resource quantity,
  recurring upper bound, one-time upper bound, and cumulative launch total
  without publishing a resource identifier.

Any cost whose safe upper bound cannot be calculated counts against the
ceiling at its documented worst-case bound. If no defensible bound exists, the
plan stops.

Regenerating a plan reruns these gates. It does not create a new human approval
requirement by itself.

Mayor/Gas Town must bundle any private execution-time needs into one
pre-execution handoff when they are knowable in advance. Expected conditional
human actions are limited to:

- value-blind AWS SSO approval if the short-lived session expires;
- supplying or authorizing a least-privilege private GHCR pull credential if
  the host pull path is absent; and
- placing the recovery copy in the selected password manager when no safe
  agent-controlled path exists.

## Actions Not Authorized

This contract does not authorize:

- attaching or re-associating the reserved EIP;
- changing DNS, Cloudflare proxying, Origin CA state, or public traffic;
- opening direct internet access to SSH, HTTP, the application port, or
  PostgreSQL;
- deleting or replacing the old EC2, old RDS, rollback data, or recovery
  materials;
- closing the rollback window;
- exceeding either spending ceiling;
- publishing the image publicly;
- broadening IAM, registry, or secret access beyond the reviewed plan; or
- storing a real secret or private identifier in tracked or shared output.

Window 2 remains a separate one-response human gate for the exact consolidated
cutover packet.

## Stop Conditions

Execution stops and returns to the human if:

1. the production profile is missing, no longer current, or cannot be consumed
   without exposing a private value;
2. a plan has an unexpected address, create, update, destroy, or replacement;
3. the aggregate cost may exceed either approved ceiling;
4. live discovery is ambiguous or differs from the reviewed assumptions;
5. a required credential is missing, expired, or broader than intended;
6. a private GHCR image cannot be pulled without exposing a credential;
7. a Cloudflare account mutation appears before Window 2;
8. the recovery copy cannot be created or retrieved safely;
9. private data appears in a plan, state summary, log, chat, screenshot, or
   tracked file;
10. private-path application, database, archive, or restore verification fails;
    or
11. any action would weaken the rollback path.

## Acceptance Criteria

This contract remains active only while:

1. all six approved decision groups above match the checklist;
2. the production profile remains current and its password path remains
   private;
3. Slice 2's protected production plan and exit evidence pass;
4. every Batch 1 implementation plan cites this contract and defines an exact
   action allow-list;
5. cost evidence evaluates the aggregate USD 100 monthly and USD 200 one-time
   ceilings;
6. the image workflow uses an immutable digest and separates workflow publish
   authority from private-host pull authority;
7. Slice 5 requires no Cloudflare account credential or mutation;
8. recovery-copy completion is recorded without revealing its location or
   value; and
9. Window 2 actions remain blocked pending the consolidated human approval.

## Rollback

Before Window 2, rollback for any Batch 1 failure is to stop the replacement
stack's Jobcron and Caddy services through the Slice 4 Session Manager runbook,
preserve evidence, and leave the old production resources and unattached
reserved EIP unchanged. Rollback does not run `terraform destroy`, detach or
attach an EIP, change DNS, or delete a database or recovery object. Revert
tracked code only after preserving any state-recovery instructions required by
the independently reviewed plan.

After public cutover, the separate Window 2 rollback packet controls EIP and
DNS restoration. This contract never authorizes deletion of the prior stack.

## Out Of Scope

- The exact private profile contents or credentials
- The exact resource inventory, identifiers, addresses, or endpoints
- Slice-specific Terraform design and implementation details
- The Window 2 cutover packet and its approval
- Rollback-resource deletion or rollback-window closure

## References

- [GitHub Docs: Working with the Container registry][github-container]
  (verified 2026-07-28)
- [Terraform-first production launch human-blocked steps][human-steps]

[checklist]: 260728-pre-batch-1-human-input-checklist.md
[github-container]:
  https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry
[human-steps]:
  260726-terraform-first-production-launch-human-blocked-steps.md
[roadmap]:
  ../plans/260726-terraform-first-production-launch-roadmap.md
[two-window]:
  ../decisions/260727-two-window-first-production-launch-authorization.md
