# Terraform-First Production Launch Implementation Roadmap

**Status:** Active; Slices 1 through 3 are complete and Slice 4 is active under
the approved two-window authorization

**Recorded:** 2026-07-26

**Architecture authority:** [Terraform AWS foundation and Cloudflare ingress
automation][terraform-spec]

**Human authority:** [Terraform-first production launch human-blocked
steps][human-spec]

**Authorization decision:** [Two-window first-production-launch
authorization][two-window-decision]

## Goal

Implement Jobcron's first production infrastructure in six independently
reviewable slices while teaching the human operator what each change does, what
they are approving, how failure appears, and how to recover.

The tracked plans are the durable source of execution order and safety gates.
Codex/Mayor supplies drift-sensitive commands, explains live output, and helps
diagnose failures during execution. A CLI session must never be the only place
where a required approval, stop condition, or recovery procedure exists.

## Teaching Contract For Every Slice

Every slice plan must include:

1. the user-visible or operational outcome;
2. a plain-language explanation of each new cloud or Terraform concept;
3. exact tracked files and private input locations;
4. agent actions versus human-only actions;
5. value-blind commands that do not print identifiers or secrets;
6. expected safe output;
7. the Window 1 controller policy gate or Window 2 human approval that
   authorizes each state change;
8. stop conditions and common failure symptoms;
9. rollback or recovery steps; and
10. evidence required before the next slice starts.

Exact account IDs, ARNs, resource IDs, addresses, CIDRs, endpoints, credentials,
certificate material, state bucket names, and recovery locations remain outside
tracked documentation.

## Slice Dependency Graph

```text
1. Identity, state bootstrap, and Terraform CI
   |
2. Canonical VPC and EIP adoption
   |
3. Private database tier and secret containers
   |
4. Replacement EC2, Session Manager, transient runtime,
   Caddy, and recovery archives
   |
5. Cloudflare prefix-list automation
   |
6. Data bootstrap, EIP/DNS cutover, verification,
   and documentation
```

Planning, implementation, and independent review may be front-loaded across
later slices. State-changing applies remain strictly ordered: no slice may
apply before its dependency passes its exit checkpoint.

Slices 2 through 5 may apply under Window 1 only when the saved plan passes the
machine-checkable policy and independent-review constraints in the
[authorization decision][two-window-decision]. Slice 6's final EIP attachment,
DNS, and public-traffic cutover remains Window 2 and requires one explicit
human response to the consolidated cutover packet.

## Slice Plans

### Slice 1: Identity, State Bootstrap, And Terraform CI

**Plan:** [Slice 1 implementation plan][slice-1-plan]

**Verification:** [Slice 1 verification][slice-1-verification]

**Status:** Complete at implementation baseline `fa4cd818129f`

Delivers:

- verified IAM Identity Center access through `jobcron-admin`;
- three small Terraform roots with one state key each;
- a private encrypted and versioned S3 state bucket with native lock files;
- GitHub's AWS OIDC provider;
- protected production and edge roles with only the permissions needed in this
  slice;
- static Terraform CI; and
- a manually dispatched production plan-only workflow.

### Slice 2: Canonical VPC Adoption And EIP Reservation

**Specification:** [Archived Slice 2 adoption specification][slice-2-spec]

**Implementation plan:** [Archived Slice 2 implementation plan][slice-2-plan]

**Verification:** [Slice 2 verification][slice-2-verification]

**Status:** Complete at implementation baseline `19865f9db16b`

Terraform owns the eight approved public-network objects and one unattached
reserved EIP. Local and protected production plans are clean, and existing
production and rollback resources were not replaced or deleted.

### Slice 3: Private Database Tier And Secret Containers

**Implementation plan:** [Archived Slice 3 implementation plan][slice-3-plan]

**Verification:** [Slice 3 verification][slice-3-verification]

**Status:** Complete at implementation baseline `0a25905`

Terraform owns the private database subnets and VPC-local route table,
security-group-only PostgreSQL path, encrypted private RDS instance, empty
runtime-secret container, and protected recovery bucket. The prior production
resources remain unchanged rollback resources.

### Slice 4: Replacement EC2 And Transient Runtime

**Plan:** [Slice 4 implementation plan][slice-4-plan]

**Status:** Active from the verified Slice 3 checkpoint

Will create the encrypted replacement host, Session Manager access, transient
runtime preparation, Caddy origin TLS, and off-host dump/log recovery.

### Slice 5: Cloudflare Prefix-List Automation

**Status:** Plan only after the private replacement stack is healthy

Will add the validated Cloudflare IPv4 prefix list, the single origin port 443
rule, and the narrow scheduled edge workflow. This is the first slice allowed
to give the edge role its limited mutation permissions.

### Slice 6: Data Bootstrap, Cutover, And Verification

**Status:** Plan only after edge automation fails safely and passes no-op checks

Will publish the immutable image, import data through SSM, verify private paths,
perform the separately approved EIP and Cloudflare cutover, walk the real
browser journeys, and close the rollback window only after durability checks.

## Plan-Authoring Cadence

Plans and independent reviews may be prepared ahead of the apply sequence.
Revalidate drift-sensitive inventory and regenerate saved plans immediately
before each policy-gated apply so front-loading does not turn into stale
authority.

When a slice finishes:

- archive its completed plan and sanitized verification evidence together;
- update this roadmap with the completion commit and the next active plan;
- update `docs/architecture.md` when implemented architecture changes; and
- keep exact operational evidence in the private operator log.

## Roadmap Completion

This roadmap is complete when all six slice plans have passed, the proxied
production application passes the active human launch specification, stale
manual deployment assumptions are archived, and the human explicitly closes
the rollback window.

[human-spec]:
  ../specs/260726-terraform-first-production-launch-human-blocked-steps.md
[two-window-decision]:
  ../decisions/260727-two-window-first-production-launch-authorization.md
[slice-1-plan]:
  ../archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-identity-state-bootstrap-ci.md
[slice-1-verification]:
  ../archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-verification.md
[slice-2-spec]:
  ../archive/2026-07-27-terraform-slice-2/260727-terraform-slice-2-canonical-vpc-eip-adoption.md
[slice-2-plan]:
  ../archive/2026-07-27-terraform-slice-2/260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md
[slice-2-verification]:
  ../archive/2026-07-27-terraform-slice-2/260727-terraform-slice-2-verification.md
[slice-3-plan]:
  ../archive/2026-07-28-terraform-slice-3/260728-terraform-slice-3-private-database-secret-containers-implementation.md
[slice-3-verification]:
  ../archive/2026-07-28-terraform-slice-3/260728-terraform-slice-3-verification.md
[slice-4-plan]:
  260728-terraform-slice-4-replacement-ec2-transient-runtime-implementation.md
[terraform-spec]:
  ../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
