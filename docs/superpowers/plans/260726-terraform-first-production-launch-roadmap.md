# Terraform-First Production Launch Implementation Roadmap

**Status:** Active; Slice 1 is complete and the Slice 2 specification awaits
human review

**Recorded:** 2026-07-26

**Architecture authority:** [Terraform AWS foundation and Cloudflare ingress
automation][terraform-spec]

**Human authority:** [Terraform-first production launch human-blocked
steps][human-spec]

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
7. an explicit human approval immediately before each state change;
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

Later slices may be researched while an earlier slice is being reviewed, but no
state-changing task may run before its dependency passes its exit checkpoint.

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

### Slice 2: Canonical VPC And EIP Adoption

**Specification:** [Slice 2 adoption specification][slice-2-spec]

**Status:** Specification ready for human review; implementation planning
follows approval

Will inventory the existing network privately, select the canonical VPC, and
adopt only the approved VPC, public networking, and EIP without replacement or
destruction. Mayor/Gas Town performs the inventory and execution; the human
normally supplies only candidate approval and exact-plan approval.

### Slice 3: Private Database Tier And Secret Containers

**Status:** Plan only after the adoption plan is clean

Will create private database subnets, security groups, encrypted PostgreSQL RDS,
the recovery bucket, and an empty runtime-secret container without putting a
secret version in Terraform.

### Slice 4: Replacement EC2 And Transient Runtime

**Status:** Plan only after private RDS passes its checkpoint

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

Write the next slice plan only when the current slice is close enough to its
exit checkpoint that live resource names, interfaces, and failure evidence are
known. This avoids a six-slice command book that goes stale before use.

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
[slice-1-plan]:
  ../archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-identity-state-bootstrap-ci.md
[slice-1-verification]:
  ../archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-verification.md
[slice-2-spec]:
  ../specs/260727-terraform-slice-2-canonical-vpc-eip-adoption.md
[terraform-spec]:
  ../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
