# Terraform Slice 2: Canonical VPC Adoption And EIP Reservation

**Status:** Implemented and verified

**Recorded:** 2026-07-27

**Architecture authority:** [Terraform AWS foundation and Cloudflare ingress
automation][terraform-spec]

**Human authority:** [Terraform-first production launch human-blocked
steps][human-spec]

**Roadmap:** [Terraform-first production launch roadmap][roadmap]

**Implementation plan:** [Terraform Slice 2 implementation plan][implementation-plan]

**Authorization decision:** [Two-window first-production-launch
authorization][two-window-decision]

**Revision approval:** On 2026-07-28, the human approved replacing four
nonexistent explicit subnet-association imports with read-only verification of
the inherited main-route relationship and replacing the nonexistent EIP import
with creation of one unattached EIP.

That approval is Slice 2's spending ceiling: at most one standard VPC-domain
EIP and no other billable production create. It does not establish the broader
Window 1 maximum infrastructure spend required before Batch 1.

## Decision Summary

Slice 2 adopts the existing RDS VPC and its shared public networking into the
production Terraform state. It also creates one new, unattached Elastic IPv4
allocation reserved for the later rollback/cutover path. It does not move,
replace, recreate, or reconfigure any live workload.

Mayor/Gas Town owns discovery, private identifier handling, Terraform
implementation, plan review, apply execution, verification, and evidence.
Under Window 1, an unambiguous candidate and a compliant two-plan packet may
proceed without another human response.

The packet contains:

- a bootstrap plan that grants the protected production workflow only the EC2
  read calls needed to refresh the adopted objects through one separate policy
  and attachment; and
- a production plan that imports the approved existing network objects and
  creates only one unattached EIP.

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

### Adopt Existing Objects

The selected candidate must resolve to exactly:

- one VPC containing the current RDS instance;
- one internet gateway attached to that VPC;
- four existing public subnets in that VPC;
- one public route table shared by those four subnets;
- no explicit route-table associations for those four subnets, which therefore
  inherit that VPC's main route table; and
- the existing IPv4 default route through that internet gateway.

These are eight imports: one VPC, one internet gateway, four subnets, one route
table, and one default route.

### Create

Create exactly one VPC-domain EIP with no association argument or separate EIP
association resource. The allocation remains unattached in Slice 2 and is the
only approved production create action.

### Do Not Touch

Other than the exact adopt/create lists above, Slice 2 must not import, create,
update, replace, destroy, detach, or reassociate:

- the old EC2 instance or its VPC;
- the current RDS instance, subnet group, or security groups;
- any stale security group;
- any private subnet;
- any NAT gateway;
- any DNS or Cloudflare object;
- any EIP association;
- any additional route or route target;
- any subnet CIDR or subnet attribute;
- any application, container, database row, secret, certificate, or backup; or
- the narrow edge role or edge Terraform state.

Tag changes are deferred. The production plan is limited to eight imports and
one unattached EIP creation.

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
3. none of the four subnets has an explicit route-table association, so all
   four inherit the same VPC main route table;
4. that route table's IPv4 default route targets the chosen attached internet
   gateway;
5. no existing EIP is the rollback/cutover allocation, and the production plan
   proposes one new unattached VPC-domain EIP;
6. at least two Availability Zones have enough unused, non-overlapping address
   space for Slice 3's private database subnets; and
7. the chosen existing objects are not already managed by another Terraform
   state.

If exactly one candidate satisfies every relationship, Mayor selects it
automatically under Window 1 after independent review. If more than one
candidate remains plausible, the controller stops and presents the human a
concise comparison without identifiers. If an EIP appears before the saved
plan is created, or no candidate satisfies every relationship, Slice 2 stops
instead of weakening the contract or allocating a duplicate.

## Controller Policy Gates

### Gate 1: Deterministic Resource Selection

The controller may accept the logical candidate only when authenticated
inventory selects exactly one VPC and its enumerated public-network dependency
set, verifies the inherited main-route relationship, and confirms that no
existing EIP satisfies the reserved rollback/cutover role. An independent
reviewer must reproduce the result. Passing this gate authorizes Mayor to store
the candidate's durable, non-credential network configuration in the protected
`production` GitHub environment secret; this does not mutate AWS
infrastructure.

The human does not choose the two private subnet CIDRs in this slice. Mayor only
proves that sufficient non-overlapping capacity exists. Exact CIDR selection
and policy validation belong to Slice 3, immediately before subnet creation.

### Gate 2: Exact Adoption Packet

Mayor supplies a value-blind summary and cryptographic digest for each saved
plan. An independent reviewer must first confirm the raw private plans satisfy
this specification.

The controller may apply both exact saved plans in dependency order only when
the summary and machine checks prove:

- bootstrap: one narrow production-network read policy and one attachment, with
  no existing-policy or trust change and no update, replace, or delete action;
- production: exactly eight imports, one addition, zero changes, and zero
  destroys;
- the only production create action is `aws_eip.origin`;
- no route-table association, route, subnet, EC2, or RDS create, update, or
  delete action;
- old EC2 and old RDS relationship fingerprints unchanged; and
- the plans are saved locally, unpublished, and bound to the current remote
  state serials.

Authorization applies only to those exact plan files and digests. Any
regenerated plan requires a new summary, machine checks, and independent
review, but not another human response when every Window 1 policy gate passes.

### Stop And Return To The Human

A human decision is required if:

- inventory is genuinely ambiguous;
- the plan proposes an unexpected action, address, live-resource mutation,
  destroy, or replace action;
- a partial import leaves state recovery uncertain;
- another Terraform state already owns a candidate object; or
- implementation would need scope beyond the approved resource bundle;
- credentials are missing or stale;
- the plan exceeds the approved spending ceiling; or
- any other Window 1 policy or verification gate fails.

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
aws_eip.origin
```

Logical keys remain stable and contain no AWS identifiers.

The allow-list contains nine resource addresses. Eight are import-only: one
VPC, one internet gateway, four subnets, one route table, and one default route.
The ninth is create-only: one unattached EIP.

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

Every adopted resource and the created EIP has a bound `prevent_destroy`
lifecycle rule. The production root must not declare a subnet route-table
association, main-route-table association, EIP association resource, or EIP
association argument in this slice.

Route management must use one dedicated `aws_route` resource for the existing
IPv4 default route. It must not mix inline route blocks with standalone route
resources.

The inherited relationship remains outside Terraform ownership. Immediately
before each local saved plan and exact-plan apply, the controller must resolve
each subnet's effective route table from read-only AWS inventory: explicit
association first, otherwise the VPC main route table. The gate fails unless
all four subnets still have no explicit association and inherit the adopted
route table. Provider refresh and the same check run again after apply.

## Production Workflow Read Boundary

The protected production workflow remains plan-only. Slice 2 may add only the
minimum read actions needed by the pinned AWS provider to refresh the adopted
objects:

```text
ec2:DescribeAddresses
ec2:DescribeAddressesAttribute
ec2:DescribeAvailabilityZones
ec2:DescribeInternetGateways
ec2:DescribeNetworkAcls
ec2:DescribeRouteTables
ec2:DescribeSecurityGroups
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

All imports and applies run locally with `jobcron-admin` after the applicable
controller policy gate passes.

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

The saved production plan must report exactly eight imports and:

```text
1 to add, 0 to change, 0 to destroy
```

Machine review with `terraform show -json` must prove:

- every planned address is in the exact allow-list above;
- each of the eight existing network addresses carries import metadata;
- `aws_eip.origin` has no import metadata and its only action is `create`;
- no other action list contains `create`, `update`, or `delete`;
- no subnet or EIP association resource appears;
- no unknown extra resource appears; and
- no sensitive value is written to a tracked or published sink.

After independent review and all Window 1 policy checks pass, Mayor applies the
two exact saved plans in dependency order without another human response. The
bootstrap plan runs first. If it fails or no longer matches state, the
production plan does not run.

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

If an import or EIP creation fails:

1. do not rerun or reconstruct state blindly;
2. compare the production state with the exact import allow-list;
3. inspect EIPs read-only and do not allocate a second address if the first
   create reached AWS but not state;
4. verify live AWS relationships remain unchanged;
5. continue only if the remaining actions can be represented by a newly saved
   plan that passes independent review and every Window 1 policy gate; and
6. otherwise request human approval for an exact state-only recovery.

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
- inherited-route assertion and no-association-resource tests
- exact production-role read-policy ceiling tests
- workflow mutation tests rejecting apply/import/state/destroy commands,
  symbolic action refs, unmasked account IDs, and broadened IAM actions
- plan-JSON allow-list checker for both saved plans
- Gitleaks plus manual publication review

### Private Live Verification

- pre-apply and post-apply resource relationship fingerprints match;
- all four public subnets still inherit the same main route table;
- exactly one Terraform-owned EIP exists and remains unattached;
- the old EC2 instance and current RDS instance are unchanged;
- subnet CIDRs, attributes, and route targets match;
- the post-apply bootstrap plan is clean;
- the post-import local production plan is clean;
- the protected GitHub production plan reports `no changes`; and
- state-version recovery remains available for both affected roots.

## Acceptance Criteria

Slice 2 is complete only when:

1. authenticated inventory and independent review selected one candidate
   bundle unambiguously;
2. an independent reviewer approved both exact saved plans;
3. the exact two-plan packet passed every Window 1 controller policy gate;
4. the production role has only the required read additions;
5. all eight adopted network addresses and the one created EIP address are
   present in production state;
6. the post-import local and protected GitHub plans are clean;
7. the EIP remains unattached, the public subnets still inherit the same main
   route table, and routes, subnets, old EC2, and current RDS are unchanged;
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
5. independent whole-slice review and the controller policy packet;
6. exact-plan applies, post-import cleanup, and live verification; and
7. architecture, operator-guide, roadmap, and archive updates.

Expected engineering effort is approximately one half-day for inventory and
candidate proof, one day for TDD implementation and review, and one half-day for
the policy-gated apply and verification checkpoint. Normal human effort is zero
unless inventory is ambiguous or another stop condition fires.

## Out Of Scope

- Choosing or creating Slice 3 private subnet CIDRs
- Creating or modifying RDS, EC2, IAM instance roles, runtime secrets, or
  security groups
- Adding VPC or origin discovery tags
- Attaching or reassociating the EIP
- Cloudflare, DNS, Caddy, application, or database changes
- Importing rollback resources that are intentionally outside final Terraform
  ownership
- Scheduled or GitHub-driven production applies

[human-spec]:
  ../../specs/260726-terraform-first-production-launch-human-blocked-steps.md
[implementation-plan]:
  260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md
[roadmap]:
  ../../plans/260726-terraform-first-production-launch-roadmap.md
[terraform-spec]:
  ../../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
[two-window-decision]:
  ../../decisions/260727-two-window-first-production-launch-authorization.md
