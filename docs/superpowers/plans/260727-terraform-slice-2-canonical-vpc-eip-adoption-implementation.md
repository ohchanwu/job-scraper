# Terraform Slice 2 Canonical VPC And EIP Adoption Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Status:** Approved for execution under Window 1 controller policy gates

**Goal:** Adopt the existing RDS VPC, its four-subnet public network, and the
existing Elastic IPv4 allocation into the production Terraform state without
changing live AWS infrastructure.

**Architecture:** The bootstrap root adds a separate read-only EC2 policy to
the protected production GitHub role. The production root declares 13 adopted
resource addresses backed by one durable private network input. Temporary
import blocks bind exact private IDs only during the imports-only plan and are
removed after adoption.

**Tech Stack:** Terraform 1.15.8, AWS provider 6.33.0, AWS CLI v2 with IAM
Identity Center, Bash, `jq`, GitHub Actions OIDC, and GitHub CLI.

**Specification:** [Terraform Slice 2 canonical VPC and EIP adoption
specification][slice-2-spec]

**Authorization decision:** [Two-window first-production-launch
authorization][two-window-decision]

## Global Constraints

- Start every implementation task from the exact current Mayor baseline. When
  the baseline is not on GitHub, fetch it from
  `${HOME}/gt/jobscraper/mayor/rig`; do not push it.
- Repository instructions prohibit `git push`, PR creation, `gt done`, and
  merge-queue submission. Commit locally at each reviewed checkpoint.
- Use `PATH=/opt/homebrew/bin:$PATH` for native ARM64 Terraform on this Mac.
- Use only short-lived `AWS_PROFILE=jobcron-admin` credentials for local AWS
  work. A login renewal may require the human to approve an SSO page.
- Treat tracked files and workflow logs as public. Never track or print account
  IDs, ARNs, resource IDs, IP addresses, CIDRs, endpoints, names from private
  inventory, plan bodies, state, or secret values.
- Exact private inventory, inputs, plans, digests, and recovery evidence belong
  only under this plan's ignored `.superpowers/sdd/` workspace or ignored
  `*.tfvars.json`, `*.tfplan`, and `*.backend.hcl` files.
- No AWS mutation is allowed before independent review and the exact saved-plan
  controller policy gate.
- Candidate selection must be deterministic and unambiguous. Passing the
  selection policy gate authorizes only value-blind creation of
  `TF_VAR_CANONICAL_NETWORK_CONFIG` in GitHub's protected `production`
  environment.
- A state-changing apply may proceed without another human response only when
  the saved plan passes every Window 1 policy in the authorization decision.
- Plan A may create only
  `aws_iam_policy.production_network_read` and
  `aws_iam_role_policy_attachment.production_network_read`.
- Plan B must show exactly 13 imports, `0 to add`, `0 to change`, and
  `0 to destroy`.
- Do not import or modify the old EC2 instance, old EC2 VPC, current RDS,
  security groups, DNS, Cloudflare, EIP association, subnet attributes, routes,
  or tags.
- The production GitHub workflow remains plan-only and publishes no plan body.
- Use TDD for every tracked behavior change. Record RED and GREEN commands in
  the task report.
- Before every documentation commit, run Gitleaks, the public-repository
  redactor, and a manual staged-diff publication review.

## Dependency Graph

```text
Task 1 bootstrap read boundary ─┐
Task 2 production declarations ├─> Task 3 CI and plan gates
                               └─> Task 4 inventory -> selection policy gate
                                                        |
                                                        v
                                  Task 5 bind inputs and save plans
                                                        |
                                                        v
                              independent review -> apply policy gate
                                                        |
                                                        v
                                  Task 6 apply, clean, verify
                                                        |
                                                        v
                                  Task 7 docs and archive
```

Tasks 1 and 2 are logically independent, but implement and review them
sequentially so each commit has one owner and one exact review range.

## Controller Setup

Resolve this plan's ignored workspace before Task 1:

```bash
PLAN_FILE=docs/superpowers/plans/260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md
SLICE_BASE="$(git rev-parse HEAD)"
SUBAGENT_DRIVEN_SKILL_DIR="${HOME}/.codex/plugins/cache/claude-plugins-official/superpowers/6.2.0/skills/subagent-driven-development"
SDD_WORKSPACE="$(
  "$SUBAGENT_DRIVEN_SKILL_DIR/scripts/sdd-workspace" \
    "$PLAN_FILE"
)"
export PLAN_FILE SLICE_BASE SUBAGENT_DRIVEN_SKILL_DIR SDD_WORKSPACE
```

Create `progress.md` there with the exact plan path on its first line. Every
worker writes its full report into that workspace and returns only status,
commit SHAs, test summary, and concerns.

---

### Task 1: Add The Production Network Read Boundary

**Files:**

- Modify: `infra/terraform/bootstrap/identity.tf`
- Modify: `infra/terraform/bootstrap/tests/identity.tftest.hcl`
- Modify: `scripts/check-terraform.sh`
- Modify: `scripts/check-terraform-workflows_test.sh`

**Interfaces:**

- Consumes: existing `aws_iam_role.production`
- Produces:
  `aws_iam_policy.production_network_read` and
  `aws_iam_role_policy_attachment.production_network_read`
- Does not change: `data.aws_iam_policy_document.production_state`,
  `aws_iam_policy.production_state`, any trust policy, or any edge resource

- [x] **Step 1: Write the failing Terraform policy test**

Add an `identity_contract` assertion that requires one policy-document
statement with `resources == ["*"]` and exactly this set:

```hcl
toset([
  "ec2:DescribeAddresses",
  "ec2:DescribeAvailabilityZones",
  "ec2:DescribeInternetGateways",
  "ec2:DescribeRouteTables",
  "ec2:DescribeSubnetAttribute",
  "ec2:DescribeSubnets",
  "ec2:DescribeTags",
  "ec2:DescribeVpcAttribute",
  "ec2:DescribeVpcs",
])
```

Add `override_data` for
`data.aws_iam_policy_document.production_network_read` using a harmless
test-only document, following the existing state-policy overrides.

- [x] **Step 2: Write failing source-contract mutations**

Update the expected IAM-action multiset in `scripts/check-terraform.sh` to
include each approved EC2 action exactly once. Remove only the blanket
`"ec2:"` ban; keep the RDS, IAM, and Secrets Manager bans.

Add mutations in `scripts/check-terraform-workflows_test.sh` that change
`"ec2:DescribeVpcs"` first to `"ec2:Describe*"` and then to
`"ec2:CreateVpc"`. Both must be rejected with:

```text
Slice 2 network read policy actions differ from the approved ceiling.
```

- [x] **Step 3: Run RED verification**

Run:

```bash
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/bootstrap test \
  -filter=tests/identity.tftest.hcl
./scripts/check-terraform-workflows_test.sh
```

Expected: failure because the policy data source and resources do not exist.

- [x] **Step 4: Implement the minimal separate policy**

Append to `identity.tf`:

```hcl
data "aws_iam_policy_document" "production_network_read" {
  statement {
    actions = [
      "ec2:DescribeAddresses",
      "ec2:DescribeAvailabilityZones",
      "ec2:DescribeInternetGateways",
      "ec2:DescribeRouteTables",
      "ec2:DescribeSubnetAttribute",
      "ec2:DescribeSubnets",
      "ec2:DescribeTags",
      "ec2:DescribeVpcAttribute",
      "ec2:DescribeVpcs",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "production_network_read" {
  name   = "JobcronTerraformProductionNetworkRead"
  policy = data.aws_iam_policy_document.production_network_read.json
}

resource "aws_iam_role_policy_attachment" "production_network_read" {
  role       = aws_iam_role.production.name
  policy_arn = aws_iam_policy.production_network_read.arn
}
```

Do not add a second statement or a wildcard action.

- [x] **Step 5: Run GREEN verification**

Run:

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
```

Expected: all Terraform and mutation tests pass.

- [x] **Step 6: Review and commit**

Confirm the diff contains two new IAM resources, no existing policy changes,
no trust changes, and no edge changes.

```bash
git diff --check
git add infra/terraform/bootstrap/identity.tf \
  infra/terraform/bootstrap/tests/identity.tftest.hcl \
  scripts/check-terraform.sh \
  scripts/check-terraform-workflows_test.sh
git commit -m "infra: add production network read boundary"
```

---

### Task 2: Declare The Adopted Production Network

**Files:**

- Create: `infra/terraform/production/variables.tf`
- Create: `infra/terraform/production/network.tf`
- Create: `infra/terraform/production/tests/network.tftest.hcl`
- Modify: `scripts/check-terraform.sh`
- Modify: `scripts/check-terraform-workflows_test.sh`

**Interfaces:**

- Consumes: `var.canonical_network_config`
- Produces the 13 stable addresses defined in the specification
- Produces no output containing private values

- [x] **Step 1: Write the failing production-root test**

Create `tests/network.tftest.hcl` with `mock_provider "aws" {}` and one
`command = plan` run. Supply this non-production fixture:

```hcl
variables {
  canonical_network_config = {
    vpc = {
      cidr_block                          = "10.255.0.0/24"
      enable_dns_hostnames                = true
      enable_dns_support                  = true
      enable_network_address_usage_metrics = false
      instance_tenancy                    = "default"
    }
    public_subnets = {
      public_a = {
        availability_zone                              = "example-1a"
        cidr_block                                     = "10.255.0.0/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_b = {
        availability_zone                              = "example-1b"
        cidr_block                                     = "10.255.0.16/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_c = {
        availability_zone                              = "example-1c"
        cidr_block                                     = "10.255.0.32/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_d = {
        availability_zone                              = "example-1d"
        cidr_block                                     = "10.255.0.48/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
    }
  }
}
```

Assert:

- `length(aws_subnet.public) == 4`;
- the keys are exactly `public_a` through `public_d`;
- the route destination is `0.0.0.0/0`;
- the EIP domain is `vpc`; and
- every association points to `aws_route_table.public.id`.

- [x] **Step 2: Add failing static lifecycle checks**

Change `require_resource_prevent_destroy` to accept
`resource_type`, `resource_name`, and `source_file`; pass `state.tf` to the
existing calls. Then add calls using `network.tf` for:

```text
aws_vpc.canonical
aws_internet_gateway.canonical
aws_subnet.public
aws_route_table.public
aws_route.public_ipv4_default
aws_route_table_association.public
aws_eip.origin
```

Add source checks rejecting an inline `route {` block in
`aws_route_table.public` and any EIP association field or
`aws_eip_association` resource.

Extend the mutation fixture to copy `network.tf`. Add mutations that rename the
`aws_eip.origin` resource, insert an inline route block, and append an
`aws_eip_association` resource. Require the checker to reject each for its
specific contract message.

- [x] **Step 3: Run RED verification**

Run:

```bash
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/production test
```

Expected: failure because the variable and network resources do not exist.

- [x] **Step 4: Add the durable private input schema**

Create `variables.tf`:

```hcl
variable "canonical_network_config" {
  description = "Private configuration of the adopted canonical network."
  sensitive   = true

  type = object({
    vpc = object({
      cidr_block                           = string
      enable_dns_hostnames                 = bool
      enable_dns_support                   = bool
      enable_network_address_usage_metrics = bool
      instance_tenancy                     = string
    })
    public_subnets = map(object({
      availability_zone                              = string
      cidr_block                                     = string
      assign_ipv6_address_on_creation                = bool
      enable_dns64                                   = bool
      enable_resource_name_dns_a_record_on_launch    = bool
      enable_resource_name_dns_aaaa_record_on_launch = bool
      map_public_ip_on_launch                        = bool
      private_dns_hostname_type_on_launch            = string
    }))
  })

  validation {
    condition = (
      toset(keys(var.canonical_network_config.public_subnets)) ==
      toset(["public_a", "public_b", "public_c", "public_d"])
    )
    error_message = "Canonical public subnet keys must be public_a through public_d."
  }
}
```

- [x] **Step 5: Add the minimal network resources**

Create `network.tf` with the exact stable resource names from the
specification. Use `for_each` for subnets and associations, reference resource
IDs internally, omit `tags`, and bind this lifecycle block to every resource:

```hcl
lifecycle {
  prevent_destroy = true
}
```

Use one standalone route:

```hcl
resource "aws_route" "public_ipv4_default" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}
```

Declare `aws_eip.origin` with only `domain = "vpc"` and the lifecycle guard.
Do not declare an association.

- [x] **Step 6: Run GREEN and negative-input verification**

Run:

```bash
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/production test
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
```

Add a second test run with only three subnet keys and assert Terraform rejects
it with the exact validation message.

- [x] **Step 7: Review and commit**

```bash
git diff --check
git add infra/terraform/production/variables.tf \
  infra/terraform/production/network.tf \
  infra/terraform/production/tests/network.tftest.hcl \
  scripts/check-terraform.sh
git commit -m "infra: declare canonical production network"
```

---

### Task 3: Enforce Private Workflow Inputs And Saved-Plan Contracts

**Files:**

- Modify: `.github/workflows/terraform-production-plan.yml`
- Create: `scripts/check-terraform-plan.sh`
- Create: `scripts/check-terraform-plan_test.sh`
- Modify: `scripts/check-terraform.sh`
- Modify: `scripts/check-terraform-workflows_test.sh`

**Interfaces:**

- Workflow consumes secret `TF_VAR_CANONICAL_NETWORK_CONFIG` as environment
  variable `TF_VAR_canonical_network_config`
- `scripts/check-terraform-plan.sh bootstrap PLAN_JSON` accepts only the two
  approved creates
- `scripts/check-terraform-plan.sh adoption PLAN_JSON` accepts only the 13
  approved imports

- [ ] **Step 1: Write failing workflow mutations**

Require this exact workflow mapping once:

```yaml
TF_VAR_canonical_network_config: ${{ secrets.TF_VAR_CANONICAL_NETWORK_CONFIG }}
```

Add mutations that:

1. remove the mapping; and
2. append `printf '%s\n' "$TF_VAR_canonical_network_config"` to a run block.

Both must fail with:

```text
production workflow must map but never print private network config
```

- [ ] **Step 2: Write failing plan-checker tests**

Create `check-terraform-plan_test.sh`. Generate minimal temporary JSON
fixtures and require:

- valid bootstrap: the two exact addresses, both actions `["create"]`;
- bootstrap rejects an update, delete, or third address;
- valid adoption: all 13 exact addresses, actions `["no-op"]`, and non-null
  `.change.importing`;
- adoption rejects a missing import, an extra address, missing import metadata,
  or any `create`, `update`, or `delete` action.

Each rejection must assert the checker emits only a generic contract error,
never a resource ID or plan value.

- [ ] **Step 3: Run RED verification**

Run:

```bash
./scripts/check-terraform-workflows_test.sh
./scripts/check-terraform-plan_test.sh
```

Expected: failure because the workflow mapping and checker do not exist.

- [ ] **Step 4: Implement the plan checker**

Use Bash plus `jq`. Filter plan JSON to resource changes whose actions are not
`["no-op"]` for bootstrap, and to changes with import metadata for adoption.
Compare sorted addresses to hard-coded public resource addresses only.

On success print exactly one of:

```text
bootstrap plan contract verified
adoption plan contract verified
```

On any failure print only:

```text
Terraform saved plan violates the Slice 2 contract
```

Exit non-zero without printing the rejected JSON.

- [ ] **Step 5: Implement the workflow guard**

Add the exact secret mapping under the existing job `env:` block. In
`check-terraform.sh`, require exactly one occurrence of
`TF_VAR_canonical_network_config` and exactly one occurrence of
`TF_VAR_CANONICAL_NETWORK_CONFIG`.

Run `check-terraform-plan_test.sh` beside the existing workflow mutation test
when `CHECK_TERRAFORM_FIXTURE_MODE` is not enabled.

- [ ] **Step 6: Run GREEN verification**

```bash
./scripts/check-terraform-plan_test.sh
./scripts/check-terraform-workflows_test.sh
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
```

- [ ] **Step 7: Review and commit**

Confirm the production workflow still contains only `init` and `plan`
Terraform commands.

```bash
git add .github/workflows/terraform-production-plan.yml \
  scripts/check-terraform-plan.sh \
  scripts/check-terraform-plan_test.sh \
  scripts/check-terraform.sh \
  scripts/check-terraform-workflows_test.sh
git commit -m "ci: enforce Terraform adoption plan contracts"
```

---

### Task 4: Build The Private Candidate Packet

**Files:**

- Create privately:
  `.superpowers/sdd/260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation/inventory/`
- Create privately:
  `candidate-private.json`
- Create privately:
  `candidate-summary.md`
- Create privately:
  `task-4-report.md`
- Tracked files: none

**Interfaces:**

- Consumes: authenticated read-only AWS inventory
- Produces: one logical candidate bundle and selection policy packet

- [ ] **Step 1: Reauthenticate value-blind**

```bash
aws sso login --profile jobcron-admin --no-browser
aws sts get-caller-identity \
  --profile jobcron-admin \
  --query 'length(Account)' \
  --output text >/dev/null
test "$(aws configure get region --profile jobcron-admin)" = "ap-northeast-2"
```

The human approves only the SSO page. Do not print the account or role.

- [ ] **Step 2: Capture the private inventory**

Write full JSON responses under the ignored inventory directory for:

```text
aws rds describe-db-instances
aws ec2 describe-instances
aws ec2 describe-vpcs
aws ec2 describe-subnets
aws ec2 describe-route-tables
aws ec2 describe-internet-gateways
aws ec2 describe-addresses
aws ec2 describe-network-interfaces
aws ec2 describe-security-groups
```

Use `--profile jobcron-admin --region ap-northeast-2 --output json` on every
call. Do not send the files through mail or paste them into a report.

- [ ] **Step 3: Derive the candidate privately**

Select the VPC containing the current RDS instance. Resolve each subnet's
effective route table: explicit association first, otherwise the main route
table. Sort the four public subnets by Availability Zone and assign logical
keys `public_a` through `public_d`.

The candidate must have:

- one attached internet gateway;
- exactly four public subnets;
- one shared public route table;
- one `0.0.0.0/0` route to that gateway;
- one intended existing EIP whose current association is retained;
- no ownership in another Terraform state; and
- unused, non-overlapping capacity in at least two Availability Zones.

If any condition is false or multiple EIPs remain plausible, mark the packet
`AMBIGUOUS` and stop at the selection policy gate.

- [ ] **Step 4: Write the two packet forms**

`candidate-private.json` contains exact IDs, CIDRs, attributes, EIP
association fingerprint, current EC2/RDS fingerprints, and capacity evidence.
Use this exact top-level shape:

```json
{
  "canonical_network_config": {
    "vpc": {},
    "public_subnets": {}
  },
  "canonical_import_ids": {
    "vpc": "",
    "internet_gateway": "",
    "public_subnets": {},
    "public_route_table": "",
    "public_ipv4_default": "",
    "public_associations": {},
    "eip": ""
  },
  "fingerprints": {
    "eip_association": "",
    "old_ec2": "",
    "current_rds": "",
    "network_relationships": ""
  },
  "capacity_verified": true,
  "other_terraform_ownership_detected": false
}
```

The empty strings and objects above document keys only; populate the private
file from inventory.

`candidate-summary.md` contains only:

```text
Candidate label: Candidate A
RDS VPC relationship: verified
Public subnet count: 4
Public route-table count: 1
Internet gateway count: 1
EIP candidate count: 1
EIP association retained: yes
Two-AZ private-subnet capacity: verified
Other Terraform ownership: not detected
Recommendation: approve | ambiguous
```

Do not include identifiers or topology values.

- [ ] **Step 5: Independently review the packet**

The reviewer reads the private JSON locally, reruns the relationship queries,
and records one of `APPROVED` or `AMBIGUOUS` in `task-4-report.md`.

- [ ] **Step 6: Enforce the selection policy gate**

If the reviewer reproduces exactly one candidate, select Candidate A
automatically and continue. If the verdict is `AMBIGUOUS`, stop and present
only `candidate-summary.md` and the review verdict to the human.

No GitHub secret or AWS resource changes occur before this gate passes.

---

### Task 5: Bind The Policy-Compliant Candidate And Create Exact Saved Plans

**Files:**

- Modify: `infra/terraform/production/variables.tf`
- Create temporarily: `infra/terraform/production/imports.tf`
- Create privately:
  `infra/terraform/production/canonical-network.auto.tfvars.json`
- Create privately:
  `infra/terraform/production/adoption-imports.auto.tfvars.json`
- Create privately:
  `infra/terraform/production/jobcron.backend.hcl`
- Create privately in the SDD workspace:
  `slice2-bootstrap-read.tfplan`
- Create privately in the SDD workspace:
  `slice2-adoption.tfplan`
- Create privately in the SDD workspace:
  `controller-policy-summary.md`

**Interfaces:**

- Consumes: policy-compliant `candidate-private.json`
- Produces: two exact, digested saved plans and controller policy packet

- [ ] **Step 1: Add the transient import schema and blocks**

Add sensitive variable `canonical_import_ids` with this shape:

```hcl
object({
  vpc                    = string
  internet_gateway       = string
  public_subnets         = map(string)
  public_route_table     = string
  public_ipv4_default    = string
  public_associations    = map(string)
  eip                    = string
})
```

Create 13 import blocks that map these values to the exact stable addresses.
The four subnet and four association maps must use only `public_a` through
`public_d`.

- [ ] **Step 2: Build private Terraform inputs**

Generate the durable network configuration from the policy-compliant inventory,
including the attributes declared in Task 2. Generate the transient import ID
object separately. Confirm Git ignores both:

```bash
candidate_private="$SDD_WORKSPACE/inventory/candidate-private.json"
jq '{canonical_network_config: .canonical_network_config}' \
  "$candidate_private" \
  >infra/terraform/production/canonical-network.auto.tfvars.json
jq '{canonical_import_ids: .canonical_import_ids}' \
  "$candidate_private" \
  >infra/terraform/production/adoption-imports.auto.tfvars.json
cp infra/terraform/bootstrap/jobcron.backend.hcl \
  infra/terraform/production/jobcron.backend.hcl
git check-ignore \
  infra/terraform/production/canonical-network.auto.tfvars.json \
  infra/terraform/production/adoption-imports.auto.tfvars.json \
  infra/terraform/production/jobcron.backend.hcl
```

- [ ] **Step 3: Set the protected GitHub environment secret**

After the selection policy gate passes, pipe the compact durable JSON without
echoing it:

```bash
jq -c '.canonical_network_config' \
  "$SDD_WORKSPACE/inventory/candidate-private.json" |
  gh secret set TF_VAR_CANONICAL_NETWORK_CONFIG \
    --env production \
    --repo ohchanwu/jobcron
```

Verify only that the secret name exists:

```bash
gh secret list --env production --repo ohchanwu/jobcron |
  awk '$1 == "TF_VAR_CANONICAL_NETWORK_CONFIG" { found = 1 } END { exit !found }'
```

- [ ] **Step 4: Initialize both protected backends**

Use the existing private bootstrap backend configuration. Create the production
backend configuration from the same private state-bucket source without
printing it.

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap init \
  -input=false -reconfigure \
  -backend-config=jobcron.backend.hcl
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production init \
  -input=false -reconfigure \
  -backend-config=jobcron.backend.hcl
```

- [ ] **Step 5: Commit the safe temporary configuration**

```bash
git add infra/terraform/production/variables.tf \
  infra/terraform/production/imports.tf
git commit -m "infra: bind canonical network imports"
```

Confirm `git status --short` does not list any private input, plan, JSON, or
backend file.

- [ ] **Step 6: Save Plan A**

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap plan \
  -input=false -out="$SDD_WORKSPACE/slice2-bootstrap-read.tfplan"
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/bootstrap show -json \
  "$SDD_WORKSPACE/slice2-bootstrap-read.tfplan" \
  >"$SDD_WORKSPACE/slice2-bootstrap-read.json"
./scripts/check-terraform-plan.sh \
  bootstrap "$SDD_WORKSPACE/slice2-bootstrap-read.json"
```

Expected: exactly two creates and no other action.

- [ ] **Step 7: Save Plan B**

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production plan \
  -input=false -out="$SDD_WORKSPACE/slice2-adoption.tfplan"
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/production show -json \
  "$SDD_WORKSPACE/slice2-adoption.tfplan" \
  >"$SDD_WORKSPACE/slice2-adoption.json"
./scripts/check-terraform-plan.sh \
  adoption "$SDD_WORKSPACE/slice2-adoption.json"
```

Expected: exactly 13 imports and `0 add, 0 change, 0 destroy`. Any attribute
drift blocks the apply policy gate; adjust tracked configuration to the
observed selected state, rerun tests, and generate a new plan.

- [ ] **Step 8: Bind plans to evidence**

Privately record:

- SHA-256 digest of each plan file;
- SHA-256 digest of each plan JSON;
- current state lineage/serial fingerprints for both roots;
- pre-plan EIP association fingerprint;
- old EC2 and current RDS fingerprints; and
- current S3 state object version identifiers.

Hash private values before placing them in the controller summary.

- [ ] **Step 9: Independent whole-packet review**

The reviewer checks both raw plans, both JSON contracts, the private inventory,
all fingerprints, the exact tracked diff, and the no-publication boundary.
Verdict must be `APPROVED` before the apply policy gate.

- [ ] **Step 10: Enforce the apply policy gate**

Record the value-blind summary:

```text
Plan A: 2 creates, 0 updates, 0 replacements, 0 destroys
Plan B: 13 imports, 0 additions, 0 changes, 0 destroys
Existing policies/trust/edge changes: none
Route/subnet/EIP association/EC2/RDS changes: none
Plan digests: recorded
State serial bindings: recorded
Independent review: APPROVED
```

Apply only those exact saved plans without another human response when machine
checks and independent review prove every Window 1 policy. Stop and return to
the human on ambiguity, drift, missing credentials, an unexpected action or
address, policy broadening, a spending-limit violation, failed verification,
uncertain recovery, or any destroy or replace action.

---

### Task 6: Apply, Remove Import Scaffolding, And Prove No Change

**Files:**

- Delete: `infra/terraform/production/imports.tf`
- Modify: `infra/terraform/production/variables.tf`
- Modify: production tests and static guards only if import-only declarations
  require removal from their expected source contract
- Create privately: apply and verification evidence under the SDD workspace

**Interfaces:**

- Consumes: exact policy-compliant plan files and digests
- Produces: adopted remote state and clean local/remote production plans

- [ ] **Step 1: Reverify exact plan identity**

Recompute both plan digests and compare them byte-for-byte with the reviewed
controller packet. Confirm state lineage/serial fingerprints still match. If
either differs, stop and regenerate the packet.

- [ ] **Step 2: Apply Plan A only**

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap apply \
  -input=false "$SDD_WORKSPACE/slice2-bootstrap-read.tfplan"
```

Immediately run a new bootstrap plan. It must be clean before Plan B runs.

- [ ] **Step 3: Apply Plan B only**

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production apply \
  -input=false "$SDD_WORKSPACE/slice2-adoption.tfplan"
```

Expected: `13 imported, 0 added, 0 changed, 0 destroyed`.

- [ ] **Step 4: Verify live relationships before cleanup**

Rerun the Task 4 inventory. Require matching pre/post fingerprints for:

- EIP association;
- old EC2 and current RDS;
- VPC, subnet, route table, associations, default route, and internet gateway;
  and
- all CIDRs and adopted attributes.

If any AWS relationship changed, stop and report. Do not run `state rm`.

- [ ] **Step 5: Remove import-only configuration**

Delete `imports.tf` and remove only `canonical_import_ids` from `variables.tf`.
Keep `canonical_network_config` and the protected GitHub secret.

```bash
PATH=/opt/homebrew/bin:$PATH terraform \
  -chdir=infra/terraform/production fmt
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production plan \
  -input=false -detailed-exitcode
```

Expected exit: `0`, no changes.

- [ ] **Step 6: Run the protected GitHub plan**

After the commits are published by the human-controlled path, dispatch
`terraform-production-plan.yml`, approve the `production` environment, and
require:

```text
Terraform production plan: no changes
```

The Node.js 20 deprecation annotation must also be absent.

- [ ] **Step 7: Run full verification and commit cleanup**

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
git add infra/terraform/production
git commit -m "infra: complete canonical network adoption"
```

Do not commit ignored private evidence.

---

### Task 7: Publish Sanitized Architecture And Completion Evidence

**Files:**

- Modify: `docs/architecture.md`
- Modify: `deploy/production/README.md`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- Modify:
  `docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md`
- Modify: `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
- Modify: `docs/superpowers/README.md`
- Create:
  `docs/superpowers/archive/2026-07-27-terraform-slice-2/260727-terraform-slice-2-verification.md`
- Move into the same archive:
  `260727-terraform-slice-2-canonical-vpc-eip-adoption.md`
- Move into the same archive:
  `260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md`

**Interfaces:**

- Consumes: sanitized Task 6 verification
- Produces: completed Slice 2 archive and activates Slice 3 planning

- [ ] **Step 1: Update maintained deployment truth**

State that Terraform now owns the canonical VPC, internet gateway, four public
subnets, shared public route table/default route/associations, and EIP
allocation. State explicitly that the EIP association, old EC2, and current RDS
remain unchanged and outside this slice's mutation scope.

- [ ] **Step 2: Mark the two controller policy gates complete**

Check only the deterministic candidate and exact-plan policy items. Do not mark
Slice 3 CIDR selection complete.

- [ ] **Step 3: Write sanitized verification**

Record:

- commit range;
- test commands and pass counts;
- Plan A and Plan B action counts;
- import count;
- local and protected workflow no-change results;
- relationship-fingerprint equality;
- state recovery availability; and
- confirmation that no private value was published.

Use digests only if they cannot reveal a private identifier. Never publish
state object versions, plan hashes linked to private artifacts, IDs, CIDRs,
addresses, endpoints, or raw logs.

- [ ] **Step 4: Archive and activate Slice 3**

Move the completed spec and plan out of active directories, update
`docs/superpowers/README.md`, and set the roadmap status to Slice 2 complete and
Slice 3 ready for specification/planning. Update relative links inside both
archived documents so the archived plan links to its sibling specification and
the archived specification links to its sibling plan or the active roadmap;
run a local-file existence check for every changed reference link.

- [ ] **Step 5: Run publication gates**

```bash
git diff --check
"${HOME}/.agents/skills/gstack/bin/gstack-redact" \
  --from-file docs/superpowers/archive/2026-07-27-terraform-slice-2/260727-terraform-slice-2-verification.md \
  --repo-visibility public --json
git add docs deploy
gitleaks git --staged --redact --no-banner
git diff --cached
```

Manually inspect the complete staged diff for private topology or identifiers.

- [ ] **Step 6: Commit**

```bash
git commit -m "docs: complete Terraform infrastructure slice 2"
```

## Final Verification

Before completion:

```bash
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
git diff --check
gitleaks git --log-opts="${SLICE_BASE}..HEAD" --redact --no-banner
git status --short --branch
```

An independent final reviewer must verify the full slice range against the
specification. Slice 2 is not complete until both local and protected GitHub
production plans are clean and the 13 imported addresses are present in the
protected production state.

[slice-2-spec]:
  ../specs/260727-terraform-slice-2-canonical-vpc-eip-adoption.md
[two-window-decision]:
  ../decisions/260727-two-window-first-production-launch-authorization.md
