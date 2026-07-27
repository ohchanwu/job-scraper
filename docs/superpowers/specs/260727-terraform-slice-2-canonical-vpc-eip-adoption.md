# Terraform Slice 2: Canonical VPC And EIP Adoption

**Status:** Approved for implementation; Human Gate 1 pending

**Recorded:** 2026-07-27

**Architecture authority:** [Terraform AWS foundation and Cloudflare ingress
automation][terraform-spec]

**Human authority:** [Terraform-first production launch human-blocked
steps][human-spec]

**Roadmap:** [Terraform-first production launch roadmap][roadmap]

**Implementation plan:** [Terraform Slice 2 implementation plan][implementation-plan]

## Decision Summary

Slice 2 adopts the existing RDS VPC, its shared public networking, and the
existing Elastic IPv4 allocation into the production Terraform state. It does
not move, replace, recreate, or reconfigure any live workload.

Mayor/Gas Town owns discovery, private identifier handling, Terraform
implementation, plan review, apply execution, verification, and evidence.
The human overseer has two normal interactions:

1. approve Mayor's recommended canonical VPC and EIP candidate; and
2. approve one exact two-plan adoption packet after independent review.

The packet contains:

- a bootstrap plan that grants the protected production workflow only the EC2
  read calls needed to refresh the adopted objects through one separate policy
  and attachment; and
- a production plan that imports only the approved network and EIP objects.

No normal Slice 2 step requires the human to navigate AWS, run a command, copy
an identifier, choose subnet CIDRs, inspect raw JSON, or edit a file.

## Why This Slice Exists

The replacement EC2 host and private RDS tier must share one canonical VPC.
Terraform cannot safely create those later resources until it owns the existing
network objects they depend on. Adoption first makes that ownership explicit
while preserving the old EC2 and RDS rollback path.

An import changes Terraform state: it teaches Terraform that an existing AWS
object corresponds to a resource address. A correct import does not recreate or
modify that AWS object.

## Verified Current State

As of 2026-07-27:

- Slice 1 established protected remote state, native S3 locking, state-version
  recovery, IAM Identity Center administration, GitHub OIDC roles, static
  Terraform checks, and a protected production plan-only workflow.
- `infra/terraform/production/` contains only provider and backend
  configuration. It owns no live AWS resources yet.
- The production GitHub role can access only its state and lock objects. It has
  no EC2 mutation permission.
- The existing RDS VPC is the architecture-selected canonical VPC.
- Exact AWS identifiers, CIDRs, addresses, plans, state, and inventory evidence
  are private operational data and must not enter Git or workflow logs.

This state can drift. The implementation plan must rerun authenticated,
read-only inventory immediately before candidate selection and immediately
before creating the saved adoption plan.

## Scope

### Adopt

The approved candidate must resolve to exactly:

- one VPC containing the current RDS instance;
- one internet gateway attached to that VPC;
- four existing public subnets in that VPC;
- one public route table shared by those four subnets;
- the route-table associations for those four subnets;
- the existing IPv4 default route through that internet gateway; and
- one existing Elastic IPv4 allocation used by the rollback/cutover path.

### Do Not Touch

Slice 2 must not import, create, update, replace, destroy, detach, or reassociate:

- the old EC2 instance or its VPC;
- the current RDS instance, subnet group, or security groups;
- any stale security group;
- any private subnet;
- any NAT gateway;
- any DNS or Cloudflare object;
- any EIP association;
- any route, route target, subnet CIDR, or subnet attribute;
- any application, container, database row, secret, certificate, or backup; or
- the narrow edge role or edge Terraform state.

Tag changes are deferred. The adoption plan is imports-only.

## Private Inventory Contract

Mayor runs the inventory with the short-lived `jobcron-admin` IAM Identity
Center profile. Read-only AWS calls may inspect VPCs, RDS, EC2, subnets, route
tables, route-table associations, internet gateways, EIPs, network interfaces,
security groups, Availability Zones, and relevant tags.

Exact output goes only to the ignored current-run directory under
`.superpowers/sdd/`. The tracked repository may contain only:

- resource counts;
- yes/no relationship checks;
- logical candidate labels such as `Candidate A`;
- sanitized rejection reasons; and
- the final pass/fail verification summary.

The inventory must establish all of these relationships before recommending a
candidate:

1. the chosen VPC contains the current RDS instance;
2. all four chosen public subnets belong to that VPC;
3. all four subnets use the same public route table;
4. that route table's IPv4 default route targets the chosen attached internet
   gateway;
5. the chosen EIP is the existing rollback/cutover allocation;
6. the EIP's association target will remain unchanged in Slice 2;
7. at least two Availability Zones have enough unused, non-overlapping address
   space for Slice 3's private database subnets; and
8. the chosen objects are not already managed by another Terraform state.

Mayor presents one recommendation with a short explanation. If more than one
candidate remains plausible, the human receives a concise comparison without
identifiers and selects one. If no candidate satisfies every relationship,
Slice 2 stops instead of weakening the contract.

## Human-Only Authority

### Normal Gate 1: Resource Selection

The human approves or rejects the recommended logical candidate. Approval
covers the VPC, its enumerated public-network dependency set, and the EIP as one
bundle. It also authorizes Mayor to store that candidate's durable,
non-credential network configuration in the protected `production` GitHub
environment secret; this does not mutate AWS infrastructure.

The human does not choose the two private subnet CIDRs in this slice. Mayor only
proves that sufficient non-overlapping capacity exists. Exact CIDR selection
and approval belong to Slice 3, immediately before subnet creation.

### Normal Gate 2: Exact Adoption Packet

Mayor supplies a value-blind summary and cryptographic digest for each saved
plan. An independent reviewer must first confirm the raw private plans satisfy
this specification.

The human approves both exact saved plans in one response only when the summary
states:

- bootstrap: one narrow production-network read policy and one attachment, with
  no existing-policy or trust change and no update, replace, or delete action;
- production: exactly 13 imports, zero additions, zero changes, and zero
  destroys;
- no route, subnet, EIP association, EC2, or RDS action;
- old EC2 and old RDS relationship fingerprints unchanged; and
- the plans are saved locally, unpublished, and bound to the current remote
  state serials.

Approval applies only to those exact plan files and digests. Any regenerated
plan requires a new summary and approval.

### Conditional Human Gate

A further human decision is required only if:

- inventory is genuinely ambiguous;
- the plan proposes any live-resource mutation;
- a partial import leaves state recovery uncertain;
- another Terraform state already owns a candidate object; or
- implementation would need scope beyond the approved resource bundle.

## Terraform Ownership Contract

The production root uses these stable tracked addresses:

```text
aws_vpc.canonical
aws_internet_gateway.canonical
aws_subnet.public["public_a"]
aws_subnet.public["public_b"]
aws_subnet.public["public_c"]
aws_subnet.public["public_d"]
aws_route_table.public
aws_route.public_ipv4_default
aws_route_table_association.public["public_a"]
aws_route_table_association.public["public_b"]
aws_route_table_association.public["public_c"]
aws_route_table_association.public["public_d"]
aws_eip.origin
```

Logical keys remain stable and contain no AWS identifiers.

The allow-list contains 13 resource addresses: one VPC, one internet gateway,
four subnets, one route table, one default route, four associations, and one
EIP.

Tracked variable schemas separate two private inputs:

- a durable canonical-network configuration containing required VPC and subnet
  CIDRs, Availability Zones, and adopted attributes; and
- transient import identifiers used only by the temporary import blocks.

Both values live in ignored local `*.auto.tfvars.json` files during adoption.
After adoption, Mayor removes the transient import identifiers and import
blocks. The durable network configuration remains in an ignored local input and
in the protected `production` GitHub environment secret
`TF_VAR_CANONICAL_NETWORK_CONFIG`. Mayor creates or updates that secret
value-blind from the approved private inventory; the human does not copy its
contents.

The workflow maps the protected secret to Terraform's
`TF_VAR_canonical_network_config` environment variable. It must never print the
value. No resource identifier, CIDR, address, or Availability Zone is hard-coded
in tracked HCL.

Every adopted resource has a bound `prevent_destroy` lifecycle rule. The
production root must not declare an EIP association resource or an EIP
association argument in this slice.

Route management must use one dedicated `aws_route` resource for the existing
IPv4 default route. It must not mix inline route blocks with standalone route
resources.

## Production Workflow Read Boundary

The protected production workflow remains plan-only. Slice 2 may add only the
minimum read actions needed by the pinned AWS provider to refresh the adopted
objects:

```text
ec2:DescribeAddresses
ec2:DescribeAvailabilityZones
ec2:DescribeInternetGateways
ec2:DescribeRouteTables
ec2:DescribeSubnetAttribute
ec2:DescribeSubnets
ec2:DescribeTags
ec2:DescribeVpcAttribute
ec2:DescribeVpcs
```

The implementation plan must confirm this list against the pinned provider's
actual refresh behavior. A missing read call may be added only with evidence
and a corresponding exact-policy test. `ec2:Describe*`, write actions, wildcard
resources for non-Describe actions, trust-policy changes, and edge-role changes
are forbidden.

The workflow continues to:

- use OIDC short-lived credentials;
- mask the AWS account ID;
- redirect the private plan body away from logs;
- print only `no changes`, `changes detected`, or `failed`;
- expose no apply, import, state, destroy, or force-unlock command; and
- require the protected `production` environment.

Static checks must require the private network input mapping and reject any
workflow command that prints it.

All imports and applies run locally with `jobcron-admin` after human approval.

## Plan And Apply Contract

### Plan A: Bootstrap Read Policy

The saved bootstrap plan may add only
`aws_iam_policy.production_network_read` and
`aws_iam_role_policy_attachment.production_network_read`. Keeping network reads
separate from state access preserves the existing policy boundary. The plan
must show:

- two additions;
- zero in-place updates;
- zero replacements;
- zero destroys;
- no existing policy change;
- no OIDC trust change;
- no edge-role change; and
- no bootstrap state-bucket change.

### Plan B: Production Adoption

The saved production plan must report exactly 13 imports and:

```text
0 to add, 0 to change, 0 to destroy
```

Machine review with `terraform show -json` must prove:

- every planned address is in the exact allow-list above;
- every allowed address carries import metadata;
- no action list contains `create`, `update`, or `delete`;
- no unknown extra resource appears; and
- no sensitive value is written to a tracked or published sink.

Mayor applies the two exact saved plans in dependency order. The bootstrap plan
runs first. If it fails or no longer matches state, the production plan does
not run.

After import, temporary import blocks and import-only private inputs are removed
from the working configuration. The durable private network configuration
remains available locally and in the protected GitHub environment. A new
production plan must be clean. The protected GitHub production workflow must
also complete successfully with `no changes`.

## Failure And Recovery

Before either apply, Mayor records the current protected state object versions
privately.

If Plan A fails, stop and inspect the bootstrap state and IAM policy. Do not run
Plan B.

If an import fails:

1. do not rerun or reconstruct state blindly;
2. compare the production state with the exact import allow-list;
3. verify live AWS relationships remain unchanged;
4. continue only if the remaining imports can be represented by a newly saved,
   reviewed, and approved plan; and
5. otherwise request human approval for an exact state-only recovery.

Removing an imported address from Terraform state does not delete the AWS
object, but `terraform state rm` is still a destructive state operation. It is
not pre-authorized by this specification.

No recovery path may destroy or modify the adopted AWS resources. State is
recovered from the protected S3 object versions if its integrity cannot be
proven.

## Verification

### Automated

- `terraform fmt -check -recursive`
- `terraform init -backend=false`, `validate`, and `test` for all roots
- exact resource-address and `prevent_destroy` contract tests
- route-ownership and no-EIP-association tests
- exact production-role read-policy ceiling tests
- workflow mutation tests rejecting apply/import/state/destroy commands,
  symbolic action refs, unmasked account IDs, and broadened IAM actions
- plan-JSON allow-list checker for both saved plans
- Gitleaks plus manual publication review

### Private Live Verification

- pre-apply and post-apply resource relationship fingerprints match;
- the EIP association target is unchanged;
- the old EC2 instance and current RDS instance are unchanged;
- subnet CIDRs, attributes, route-table associations, and route targets match;
- the post-apply bootstrap plan is clean;
- the post-import local production plan is clean;
- the protected GitHub production plan reports `no changes`; and
- state-version recovery remains available for both affected roots.

## Acceptance Criteria

Slice 2 is complete only when:

1. the human approved the recommended candidate bundle;
2. an independent reviewer approved both exact saved plans;
3. the human approved the exact two-plan packet;
4. the production role has only the required read additions;
5. all 13 expected network and EIP addresses are present in production state;
6. the post-import local and protected GitHub plans are clean;
7. the EIP association, routes, subnets, old EC2, and current RDS are unchanged;
8. no private identifier or plan body entered Git or workflow logs;
9. recovery evidence exists privately; and
10. implementation documentation is updated and this specification is archived
    with a sanitized verification report.

## Implementation Shape

The implementation plan should split work into independently reviewed units:

1. authenticated private inventory and candidate recommendation;
2. Terraform contract tests and network resource configuration;
3. narrow production-role read-policy change;
4. temporary import inputs and saved-plan JSON gates;
5. independent whole-slice review and the human adoption packet;
6. exact-plan applies, post-import cleanup, and live verification; and
7. architecture, operator-guide, roadmap, and archive updates.

Expected engineering effort is approximately one half-day for inventory and
candidate proof, one day for TDD implementation and review, and one half-day for
the approval/apply/verification checkpoint. Normal human effort is two concise
approval responses.

## Out Of Scope

- Choosing or creating Slice 3 private subnet CIDRs
- Creating or modifying RDS, EC2, IAM instance roles, runtime secrets, or
  security groups
- Adding VPC or origin discovery tags
- Reassociating the EIP
- Cloudflare, DNS, Caddy, application, or database changes
- Importing rollback resources that are intentionally outside final Terraform
  ownership
- Scheduled or GitHub-driven production applies

[human-spec]:
  260726-terraform-first-production-launch-human-blocked-steps.md
[implementation-plan]:
  ../plans/260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md
[roadmap]:
  ../plans/260726-terraform-first-production-launch-roadmap.md
[terraform-spec]:
  260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
