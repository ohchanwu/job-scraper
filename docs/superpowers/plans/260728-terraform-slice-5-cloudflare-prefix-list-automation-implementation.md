# Terraform Slice 5 Cloudflare Prefix-List Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a validated Cloudflare public IPv4 prefix list and exactly one
TCP 443 origin-security-group rule, then keep the list current through one
narrow daily/manual edge workflow.

**Architecture:** A Python standard-library boundary normalizes and validates
the official Cloudflare IPv4 response before Terraform sees it. The existing
`edge` root discovers the unique Slice 3 origin security group by its semantic
tag, owns one customer-managed prefix list and one ingress rule, and exposes no
output. The bootstrap root grants the existing edge OIDC role only the EC2
reads and tagged writes required by those two resources.

**Tech Stack:** Terraform `1.15.8`, AWS provider `6.33.0`, Python 3 standard
library, Bash, and pinned GitHub Actions.

**Authoritative planning baseline:** Mayor `main` commit `ec1e206`.

## Global Constraints

- This plan implements the approved
  [foundation specification][foundation],
  [human-blocked launch contract][human-steps], and
  [Window 1 authorization contract][window-1].
- The approved [Slice 3 plan][slice-3] and current code define the discovery
  interface. The origin security group carries exactly
  `jobcron:edge-target = origin-security-group`; do not add a VPC tag or copy a
  VPC or security-group ID into Git.
- The approved [Slice 4 plan][slice-4] remains plan-only at this baseline.
  Slice 5 code may be prepared, but no bootstrap or edge saved plan, apply, or
  scheduled mutation may start until the integrated Slice 4 completion
  checkpoint proves the private replacement stack is healthy and attached to
  `aws_security_group.origin`.
- Every Batch 1 state-changing apply uses the exact saved plan whose SHA-256
  digest and private controller report were approved by a reviewer other than
  the implementer.
- The whole launch must remain at or below USD 100 recurring monthly and
  USD 200 aggregate one-time cost. These are aggregate ceilings, not a Slice 5
  allowance.
- Fetch only `https://www.cloudflare.com/ips-v4`. Do not use a mirror, cached
  repository copy, Cloudflare API token, account credential, or account API.
- Accept exactly 10 through 20 unique, strict IPv4 CIDRs. Reject an empty set,
  IPv6, host-bit CIDRs, duplicates, more than 20 entries, the default route,
  loopback, link-local, multicast, and RFC 1918 space before Terraform planning.
- Keep `max_entries = 20`. Crossing that ceiling stops automation for an
  explicit quota and design review.
- Create one regional customer-managed prefix list and exactly one origin
  security-group ingress rule. The rule is TCP 443 and references the prefix
  list. Do not add IPv6, port 80, direct CIDR ingress, or a second rule.
- Do not create or mutate Cloudflare resources, Origin CA, DNS, proxy state,
  public traffic, EIP associations, production-root resources, EC2, RDS,
  Secrets Manager, or general IAM.
- Do not grant `ec2:DeleteManagedPrefixList`,
  `ec2:RevokeSecurityGroupIngress`, `ec2:DeleteSecurityGroup`,
  `ec2:CreateSecurityGroup`, or wildcard EC2 write permissions.
- Every tracked file, workflow log, chat message, and report is public. Never
  publish account IDs, ARNs, resource IDs, IP addresses other than the official
  public Cloudflare set, private plan/state data, saved-plan digests, backend
  names, exact costs tied to private resources, credentials, or recovery paths.
- Store fetched input, generated tfvars, saved plans, plan JSON, state binding,
  digests, cost evidence, quota evidence, and reviewer packets only below the
  ignored `.superpowers/sdd/260728-terraform-slice-5/` workspace.
- A changed input, code commit, state serial, live discovery result, cost
  packet, credential session, or saved plan invalidates the prior review.
- Apply only a saved-plan filename. Never run `terraform apply` without that
  filename and never use `-auto-approve` in a local Batch 1 controller step.
- Preserve the last valid prefix-list version on fetch, validation, planning,
  review, or apply failure. A failed run must stop before mutation.
- Preserve the old EC2, old RDS, canonical network, inherited routes,
  unattached EIP, and every rollback artifact.
- Use existing Terraform roots and repository checks. Do not add a module,
  deployment framework, Cloudflare provider, new package, or generic policy
  engine.

---

## Operational Outcome

After the initial independently reviewed Batch 1 apply:

1. the edge state owns `aws_ec2_managed_prefix_list.cloudflare_ipv4`;
2. the prefix list has `address_family = "IPv4"` and `max_entries = 20`;
3. its entries equal the validated official Cloudflare IPv4 set;
4. the edge state owns
   `aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare`;
5. the rule targets the uniquely tagged Slice 3 origin security group and
   permits only TCP 443 from the prefix list;
6. the production root owns no edge ingress rule;
7. direct internet access to ports 22, 80, 7777, and 5432 remains absent;
8. a daily/manual workflow refreshes only the prefix-list entries; and
9. unchanged or invalid upstream data causes no AWS mutation.

`max_entries` is the prefix list's quota weight when referenced by a security
group. Fixing it at 20 leaves small Cloudflare growth room without silently
consuming an unbounded security-group quota.

## Exact File Map

Create:

- `scripts/normalize-cloudflare-ipv4.py` — value-blind strict parser that writes
  sorted Terraform tfvars JSON.
- `scripts/normalize-cloudflare-ipv4_test.py` — standard-library subprocess
  tests for every acceptance and rejection class.
- `infra/terraform/edge/variables.tf` — typed 10-20-entry input boundary.
- `infra/terraform/edge/cloudflare.tf` — unique origin discovery, prefix list,
  and one TCP 443 rule.
- `infra/terraform/edge/tests/cloudflare.tftest.hcl` — mocked edge-root contract
  tests.
- `scripts/check-terraform-slice-5-plan.py` — exact bootstrap, initial edge,
  and refresh saved-plan plus aggregate-cost gate.
- `scripts/check-terraform-slice-5-plan_test.py` — synthetic mutation tests for
  all three modes.
- `.github/workflows/terraform-edge-prefix-list.yml` — pinned daily/manual
  post-checkpoint refresh workflow.

Modify:

- `infra/terraform/bootstrap/identity.tf` — add the narrow tagged edge EC2
  policy and attachment.
- `infra/terraform/bootstrap/tests/identity.tftest.hcl` — lock the exact actions,
  resources, tag conditions, attachment, and forbidden permissions.
- `scripts/check-terraform.sh` — run the two Python tests and enforce static
  Slice 5 resource, lifecycle, tag, and workflow boundaries.
- `scripts/check-terraform-workflows_test.sh` — add mutations for the new
  workflow.
- `docs/architecture.md` — document the final edge ownership and failure
  boundary only after private verification passes.
- `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
  — record the implementation checkpoint after it exists.

Do not create an edge output, Cloudflare provider configuration, copied
resource-ID variable, or production-root ingress resource.

## Private Controller Workspace

Create with directory mode `0700` and file mode `0600`:

```text
.superpowers/sdd/260728-terraform-slice-5/
├── controller.env
├── aggregate-cost.json
├── slice-4-checkpoint.json
├── cloudflare-ips-v4.txt
├── cloudflare.auto.tfvars.json
├── quota.json
├── bootstrap.tfplan
├── bootstrap-plan.json
├── bootstrap-plan.log
├── bootstrap-controller.md
├── bootstrap-review.md
├── edge.tfplan
├── edge-plan.json
├── edge-plan.log
├── edge-controller.md
├── edge-review.md
├── post-apply-plan.log
├── no-change-run.md
└── failure-preservation.md
```

`controller.env` contains only private selectors required for value-blind AWS
checks. `aggregate-cost.json` uses the same whole-launch schema and category
coverage as Slice 4. `slice-4-checkpoint.json` records the integrated commit,
state binding, private replacement-host health result, origin-group attachment,
rollback preservation, and zero public-cutover actions. No command below prints
these files.

## Dependency Interface

Slice 5 consumes the Slice 3 semantic tag:

```hcl
tags = {
  "jobcron:edge-target" = "origin-security-group"
}
```

The edge root discovers that group with:

```hcl
data "aws_security_group" "origin" {
  tags = {
    "jobcron:edge-target" = "origin-security-group"
  }
}
```

The AWS provider must fail discovery when the selector yields zero or multiple
groups. Do not fall back to a name, copied ID, first result, or VPC-wide search.
The data source exposes `id` and `vpc_id`; the rule consumes only `id`.

Before any Slice 5 state change, the Slice 4 checkpoint must prove:

- the exact integrated Slice 4 commit is current;
- `aws_instance.replacement_host` exists and uses
  `aws_security_group.origin`;
- the replacement host and private runtime checks pass;
- the origin group has no public ingress;
- the old EC2, old RDS, unattached EIP, and rollback material remain;
- no EIP, DNS, Cloudflare, Origin CA, proxy, or public-traffic change occurred;
  and
- the current production and bootstrap plans are clean outside the approved
  Slice 5 changes.

## Exact State-Change Allow-Lists

### Bootstrap edge-IAM saved plan

Mode: `slice5-bootstrap`.

Exactly these addresses may have `actions = ["create"]`:

```text
aws_iam_policy.edge_prefix_list
aws_iam_role_policy_attachment.edge_prefix_list
```

Every existing bootstrap address must be `["no-op"]`. No update, delete,
replace, import, move, forget, or output change is allowed.

### Initial edge saved plan

Mode: `slice5-edge-create`.

Exactly these addresses may have `actions = ["create"]`:

```text
aws_ec2_managed_prefix_list.cloudflare_ipv4
aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare
```

No other address or output change is allowed. The checker must verify the
prefix-list entries exactly equal the normalized tfvars set and the ingress
rule's known plan fields equal TCP 443 plus the exact tags. The computed
`prefix_list_id` may be unknown before creation; every other security-relevant
field must be known.

### Scheduled/manual edge refresh saved plan

Mode: `slice5-edge-refresh`.

When the official set changes, only
`aws_ec2_managed_prefix_list.cloudflare_ipv4` may have `actions = ["update"]`.
The ingress rule must be `["no-op"]`; no output changes are allowed.

When the official set is unchanged, `terraform plan -detailed-exitcode` must
return `0` and the workflow exits before `terraform show`, the plan checker, or
`terraform apply`.

Every mode rejects Cloudflare-provider resources, DNS, EC2 instances, RDS,
Secrets Manager, production-root addresses, EIP association, port 80, direct
CIDR ingress, IPv6, destroy, replace, import, move, forget, unknown actions, and
private-value diagnostics.

## Narrow Edge IAM Ceiling

Add `data.aws_iam_policy_document.edge_prefix_list` and attach its customer
managed policy only to `aws_iam_role.edge`.

The Describe-only read statement uses `Resource = "*"` and exactly:

```text
ec2:DescribeManagedPrefixLists
ec2:DescribeSecurityGroups
ec2:DescribeSecurityGroupRules
ec2:DescribeTags
```

Put `ec2:GetManagedPrefixListEntries` in a separate read statement scoped to:

```text
arn:aws:ec2:ap-northeast-2:${data.aws_caller_identity.current.account_id}:prefix-list/*
```

That statement requires the resource-tag condition
`aws:ResourceTag/jobcron:edge-source = cloudflare-ipv4`.

The write statements permit exactly:

```text
ec2:AuthorizeSecurityGroupIngress
ec2:CreateManagedPrefixList
ec2:CreateTags
ec2:ModifyManagedPrefixList
```

Apply these conditions:

- `CreateManagedPrefixList` requires request tag
  `jobcron:edge-source = cloudflare-ipv4` and exact `aws:TagKeys`.
- `ModifyManagedPrefixList` targets only a prefix-list ARN carrying
  `jobcron:edge-source = cloudflare-ipv4`.
- `AuthorizeSecurityGroupIngress` targets only a security-group ARN carrying
  `jobcron:edge-target = origin-security-group`, and its rule request tags must
  equal `jobcron:edge-rule = origin-https-from-cloudflare`.
- `CreateTags` is limited to prefix-list and security-group-rule ARNs, the two
  approved tag keys, and the corresponding EC2 create actions.

The policy has `lifecycle.prevent_destroy = true`. Tests must reject every
additional AWS action, `"ec2:*"`, `"*"`, unconditioned write statement, wrong
tag, additional tag key, attachment to the production role, and any IAM, RDS,
Secrets Manager, Route 53, Cloudflare, instance, EIP, or general
security-group-administration permission.

## Task 1: Normalize And Validate The Official IPv4 Response

**Files:**

- Create: `scripts/normalize-cloudflare-ipv4.py`
- Create: `scripts/normalize-cloudflare-ipv4_test.py`

**Interfaces:**

- Command:
  `python3 scripts/normalize-cloudflare-ipv4.py INPUT OUTPUT`
- `INPUT` is the raw response body fetched from the one approved URL.
- `OUTPUT` is JSON with exactly one key:
  `{"cloudflare_ipv4_cidrs": ["CIDR", ...]}`.
- Success prints exactly `Cloudflare IPv4 set verified`.
- Failure prints exactly `Cloudflare IPv4 set rejected` to stderr and returns
  nonzero. It never prints a rejected line or file content.

- [x] **Step 1: Write RED parser tests**

Use `unittest`, `tempfile`, `json`, and `subprocess`. Start with ten strict,
distinct documentation-only IPv4 prefixes in shuffled order and assert the
output is numerically sorted and contains only `cloudflare_ipv4_cidrs`.

Add one failing subprocess case for each:

```text
empty input
9 entries
21 entries
exact duplicate
duplicate after whitespace normalization
IPv6
host bits set
malformed CIDR
0.0.0.0/0
127.0.0.0/8
169.254.0.0/16
224.0.0.0/4
10.0.0.0/8
172.16.0.0/12
192.168.0.0/16
unreadable input
unwritable output
```

For each rejection assert nonzero status, empty stdout, the one generic stderr
line, and no partial output file.

Run:

```sh
python3 scripts/normalize-cloudflare-ipv4_test.py
```

Expected: FAIL because the command does not exist.

- [x] **Step 2: Implement the minimal standard-library parser**

Use `ipaddress.ip_network(line.strip(), strict=True)`. Ignore blank lines, but
reject the final empty set. Reject non-IPv4 networks and any network that
equals `0.0.0.0/0` or overlaps these fixed boundaries:

```python
FORBIDDEN = tuple(map(ipaddress.ip_network, (
    "127.0.0.0/8",
    "169.254.0.0/16",
    "224.0.0.0/4",
    "10.0.0.0/8",
    "172.16.0.0/12",
    "192.168.0.0/16",
)))
```

Keep `0.0.0.0/0` out of `FORBIDDEN`; test it by equality so the default route
does not make every public network appear forbidden. Track canonical
`with_prefixlen` strings in a set and reject a repeated value. Require
`10 <= len(networks) <= 20`. Sort by integer network address and prefix length.
Write JSON to a sibling temporary file, `fsync`, and `os.replace` it so a failed
run cannot leave a partial valid-looking output.

- [x] **Step 3: Run the parser tests**

```sh
python3 scripts/normalize-cloudflare-ipv4_test.py
```

Expected: PASS with no network access.

- [x] **Step 4: Commit**

```sh
git add scripts/normalize-cloudflare-ipv4.py \
  scripts/normalize-cloudflare-ipv4_test.py
git commit -m "feat: validate Cloudflare public IPv4 input"
```

## Task 2: Declare The Two Edge Resources

**Files:**

- Create: `infra/terraform/edge/variables.tf`
- Create: `infra/terraform/edge/cloudflare.tf`
- Create: `infra/terraform/edge/tests/cloudflare.tftest.hcl`

**Interfaces:**

- Consumes `var.cloudflare_ipv4_cidrs` as `list(string)`.
- Discovers `data.aws_security_group.origin` through the fixed Slice 3 tag.
- Produces only:
  `aws_ec2_managed_prefix_list.cloudflare_ipv4` and
  `aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare`.
- Produces no Terraform output.

- [ ] **Step 1: Write RED Terraform tests**

Use `mock_provider "aws" {}` and mock the origin data source with
documentation-only IDs. Supply ten documentation-only IPv4 prefixes.

Assert:

- the variable accepts exactly 10 through 20 distinct strings;
- `data.aws_security_group.origin.tags` equals the one Slice 3 selector;
- the managed prefix list has `address_family = "IPv4"`,
  `max_entries = 20`, the exact input entries, and tag
  `jobcron:edge-source = cloudflare-ipv4`;
- the ingress rule uses `data.aws_security_group.origin.id`;
- it uses `aws_ec2_managed_prefix_list.cloudflare_ipv4.id`;
- `ip_protocol = "tcp"`, `from_port = 443`, and `to_port = 443`;
- its entire tag map equals
  `jobcron:edge-rule = origin-https-from-cloudflare`; and
- the configuration has no output.

Add variable-failure runs for 9 entries, 21 entries, and duplicates.

Run:

```sh
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/edge test
```

Expected: FAIL because the variable and resources do not exist.

- [ ] **Step 2: Add the input boundary**

Declare the list with validation:

```hcl
validation {
  condition = (
    length(var.cloudflare_ipv4_cidrs) >= 10 &&
    length(var.cloudflare_ipv4_cidrs) <= 20 &&
    length(distinct(var.cloudflare_ipv4_cidrs)) ==
    length(var.cloudflare_ipv4_cidrs)
  )
  error_message = "Cloudflare IPv4 CIDRs must contain 10 through 20 unique entries."
}
```

The Python boundary owns semantic CIDR validation; Terraform repeats count and
uniqueness to fail closed if a caller bypasses the script.

- [ ] **Step 3: Add the exact edge HCL**

Use the dependency-interface data source. Configure:

```hcl
resource "aws_ec2_managed_prefix_list" "cloudflare_ipv4" {
  name           = "jobcron-cloudflare-ipv4"
  address_family = "IPv4"
  max_entries    = 20

  dynamic "entry" {
    for_each = var.cloudflare_ipv4_cidrs
    content {
      cidr = entry.value
    }
  }

  tags = {
    "jobcron:edge-source" = "cloudflare-ipv4"
  }

  lifecycle {
    prevent_destroy = true
  }
}
```

Add the one `aws_vpc_security_group_ingress_rule` with the fields and exact tag
from Step 1 plus `lifecycle.prevent_destroy = true`. Do not add descriptions
that can churn or carry private identifiers.

- [ ] **Step 4: Run edge tests and formatting**

```sh
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform fmt -check -recursive
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/edge test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add infra/terraform/edge
git commit -m "feat: declare the Cloudflare edge allow-list"
```

## Task 3: Grant The Existing Edge Role Only The Required AWS Actions

**Files:**

- Modify: `infra/terraform/bootstrap/identity.tf`
- Modify: `infra/terraform/bootstrap/tests/identity.tftest.hcl`
- Modify: `scripts/check-terraform.sh`
- Modify: `scripts/check-terraform-workflows_test.sh`

**Interfaces:**

- Produces:
  `aws_iam_policy.edge_prefix_list` and
  `aws_iam_role_policy_attachment.edge_prefix_list`.
- Attaches only to `aws_iam_role.edge`.
- Consumes no private resource ID in tracked HCL.

- [ ] **Step 1: Write RED IAM assertions and static mutations**

In Terraform tests, compare the policy document's statement action sets,
resource patterns, tag conditions, and attachment to the exact ceiling above.
Assert that the wildcard-resource statement contains only the four Describe
actions, and that `GetManagedPrefixListEntries` is isolated in its own
regional, account-scoped prefix-list statement with the required
`jobcron:edge-source = cloudflare-ipv4` resource-tag condition.

In the static check fixture path, add a rejection for:

```text
ec2:*
GetManagedPrefixListEntries in a Resource = "*" statement
GetManagedPrefixListEntries grouped with the four Describe actions
GetManagedPrefixListEntries without the prefix-list ARN scope
GetManagedPrefixListEntries without the required resource-tag condition
GetManagedPrefixListEntries with the wrong resource-tag value
CreateSecurityGroup
DeleteManagedPrefixList
RevokeSecurityGroupIngress
DeleteTags
unconditioned CreateManagedPrefixList
unconditioned ModifyManagedPrefixList
unconditioned AuthorizeSecurityGroupIngress
additional tag key
wrong semantic tag value
attachment to the production role
missing prevent_destroy
```

Run:

```sh
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap test \
  -filter=tests/identity.tftest.hcl
CHECK_TERRAFORM_FIXTURE_MODE=1 ./scripts/check-terraform.sh
./scripts/check-terraform-workflows_test.sh
```

Expected: FAIL because the policy is absent.

- [ ] **Step 2: Add the narrow policy**

Use `aws_iam_policy_document`; do not hand-build JSON. Add exactly
`data "aws_caller_identity" "current" {}` and build the regional EC2 ARN
patterns with the fixed `aws` partition, approved `ap-northeast-2` region, and
`data.aws_caller_identity.current.account_id`. The account ID remains in state
and generated policy JSON, never in tracked HCL or logs.

Keep the exact read and write action sets from **Narrow Edge IAM Ceiling**,
including the separate resource-scoped `GetManagedPrefixListEntries`
statement. Use request-tag and resource-tag conditions so a similarly named
untagged resource is outside the write boundary.

- [ ] **Step 3: Bind destroy protection and the edge-only attachment**

Add:

```hcl
lifecycle {
  prevent_destroy = true
}
```

to `aws_iam_policy.edge_prefix_list`. Attach it only to
`aws_iam_role.edge.name`.

- [ ] **Step 4: Run IAM and static checks**

```sh
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap test \
  -filter=tests/identity.tftest.hcl
CHECK_TERRAFORM_FIXTURE_MODE=1 ./scripts/check-terraform.sh
./scripts/check-terraform-workflows_test.sh
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add infra/terraform/bootstrap scripts/check-terraform.sh \
  scripts/check-terraform-workflows_test.sh
git commit -m "feat: grant narrow edge prefix-list access"
```

## Task 4: Enforce The Three Saved-Plan Modes And Aggregate Cost

**Files:**

- Create: `scripts/check-terraform-slice-5-plan.py`
- Create: `scripts/check-terraform-slice-5-plan_test.py`
- Modify: `scripts/check-terraform.sh`

**Interfaces:**

```text
python3 scripts/check-terraform-slice-5-plan.py \
  MODE PLAN_JSON COST_JSON SLICE4_CHECKPOINT [CIDR_TFVARS_JSON]
```

Success output is exactly one of:

```text
Slice 5 bootstrap plan contract verified
Slice 5 edge create plan contract verified
Slice 5 edge refresh plan contract verified
```

Every failure prints only:

```text
Terraform saved plan violates the Slice 5 contract
```

- [ ] **Step 1: Write RED accepted fixtures**

Use Python standard-library JSON and temporary files. Build one accepted
fixture per mode from the exact allow-lists above. The cost fixture must:

- have an RFC 3339 `checked_at` no older than 24 hours;
- contain source name and source date per category;
- include AWS compute, public IPv4, database, storage, backup, registry, and
  Cloudflare;
- keep aggregate recurring cost `<= 100`; and
- keep aggregate one-time cost `<= 200`.

The Slice 4 checkpoint fixture must record an exact integrated commit, current
state binding, replacement-host health, origin-group attachment, rollback
preservation, and zero public-cutover actions.

- [ ] **Step 2: Add one mutation per rejection class**

Reject:

```text
unknown mode
malformed or unreadable input
missing, extra, or duplicate allow-listed address
create/update/delete/replace/import/move/forget outside the exact mode
unknown action
any output change
private-value marker or plan diagnostic
stale or incomplete Slice 4 checkpoint
cost evidence older than 24 hours
missing cost category or source date
aggregate recurring cost above 100
aggregate one-time cost above 200
CIDR tfvars missing or not identical to the planned entries
entry count below 10 or above 20
duplicate, IPv6, forbidden, or max_entries other than 20
ingress protocol or port other than TCP 443
direct CIDR ingress or a second ingress rule
wrong or additional tag
Cloudflare-provider, DNS, production, EC2, RDS, Secrets Manager, EIP, or IAM
  address outside the bootstrap mode
```

Run:

```sh
python3 scripts/check-terraform-slice-5-plan_test.py
```

Expected: FAIL because the checker does not exist.

- [ ] **Step 3: Implement the minimal JSON checker**

Parse into dictionaries, reject unknown keys where the schema is controlled,
compare sorted `(address, actions)` tuples, and never include an input value in
an exception or error. For edge modes, compare the planned prefix-list CIDRs as
a set to the generated tfvars list and separately require the tfvars list to be
sorted and semantically valid.

The refresh mode accepts exactly one `["update"]` for the prefix list and one
`["no-op"]` for the ingress rule. The no-change case is handled by Terraform
exit code `0` before this command.

- [ ] **Step 4: Wire the complete infrastructure gate**

Add both Python test commands to `scripts/check-terraform.sh`. Keep the
fixture-mode recursion guard. The existing
`.github/workflows/terraform-check.yml` continues to call the one
`./scripts/check-terraform.sh` entry point, so no workflow edit is needed.

- [ ] **Step 5: Run tests**

```sh
python3 scripts/check-terraform-slice-5-plan_test.py
./scripts/check-terraform.sh
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add scripts/check-terraform-slice-5-plan.py \
  scripts/check-terraform-slice-5-plan_test.py \
  scripts/check-terraform.sh
git commit -m "test: enforce Terraform Slice 5 plan contracts"
```

## Task 5: Add The Pinned Daily And Manual Edge Workflow

**Files:**

- Create: `.github/workflows/terraform-edge-prefix-list.yml`
- Modify: `scripts/check-terraform-workflows_test.sh`
- Modify: `scripts/check-terraform.sh`

**Interfaces:**

- Triggers: cron `17 18 * * *` (03:17 Asia/Seoul) and `workflow_dispatch`.
- Environment: protected `edge`.
- Protected values: `AWS_ROLE_ARN`, `TF_STATE_BUCKET`, and
  `TF_AGGREGATE_COST_JSON`.
- Repository variable: `EDGE_AUTOMATION_ENABLED`, set to `"true"` only after
  the initial reviewed apply and no-change checkpoint.
- Concurrency group: `terraform-edge-prefix-list`, with
  `cancel-in-progress: false`.

- [ ] **Step 1: Write RED workflow mutations**

Require:

- `schedule` and `workflow_dispatch`;
- one literal official URL;
- `EDGE_AUTOMATION_ENABLED == 'true'`;
- `environment: edge`;
- `id-token: write` and `contents: read`, with no other permission;
- full commit SHAs for checkout, Terraform setup, and AWS credential actions;
- account-ID masking;
- the exact concurrency group and no cancellation;
- raw response, tfvars, logs, plan, plan JSON, and cost JSON only below
  `RUNNER_TEMP`;
- `plan -detailed-exitcode -out=...`;
- exit code `0` returns without apply;
- exit code `2` runs the refresh checker before apply;
- one apply command naming the saved plan;
- no artifact upload, cache, `env`, `printenv`, plan-body print, unsaved apply,
  production root, Cloudflare credential, or Cloudflare action.

Mutate each requirement once and assert the generic policy error.

Run:

```sh
./scripts/check-terraform-workflows_test.sh
```

Expected: FAIL because the workflow is absent.

- [ ] **Step 2: Implement fail-before-mutation ordering**

The workflow order is exact:

```text
checkout
setup Terraform
configure short-lived edge credentials
fetch official ips-v4 to RUNNER_TEMP
normalize to RUNNER_TEMP tfvars JSON
write protected aggregate-cost JSON to RUNNER_TEMP without echoing
initialize only infra/terraform/edge
terraform plan -detailed-exitcode -out=edge.tfplan with -var-file
exit cleanly on 0
terraform show -json to RUNNER_TEMP on 2
run slice5-edge-refresh checker
terraform apply -input=false edge.tfplan
```

Use `curl --fail --silent --show-error --proto '=https' --tlsv1.2`, a bounded
timeout, no redirect following, and an output file. Do not pipe the response
through logs.

- [ ] **Step 3: Pin the action versions**

Reuse the repository's reviewed full SHAs:

```yaml
actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803
hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e
aws-actions/configure-aws-credentials@e6de054238d6b7531b4efff3b6587d9aade6a06c
```

- [ ] **Step 4: Run workflow and infrastructure gates**

```sh
./scripts/check-terraform-workflows_test.sh
./scripts/check-terraform.sh
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add .github/workflows/terraform-edge-prefix-list.yml \
  scripts/check-terraform-workflows_test.sh scripts/check-terraform.sh
git commit -m "ci: automate validated Cloudflare edge refresh"
```

## Task 6: Run The Complete Pre-Cloud And Publication Gate

**Files:**

- Modify only if a gate exposes a Slice 5 defect.

**Interfaces:**

- Consumes the exact implementation tip.
- Produces a clean, publication-safe candidate for independent code review.

- [ ] **Step 1: Run all repository gates**

```sh
python3 scripts/normalize-cloudflare-ipv4_test.py
python3 scripts/check-terraform-slice-5-plan_test.py
./scripts/check-terraform-workflows_test.sh
./scripts/check-terraform.sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./cmd/jobcron ./cmd/jobcron-import ./cmd/jobcron-user
test -z "$(gofmt -l .)"
git diff --check
```

Expected: every command passes.

- [ ] **Step 2: Review the complete implementation diff**

Compare against the integrated Slice 4 baseline. Confirm the diff contains only
the exact file map, no dependency, no Cloudflare provider, no production-root
mutation, no private ID, and no state-changing workflow outside the edge root.

- [ ] **Step 3: Run publication security**

Inspect the complete staged diff, run the configured Gitleaks scanner over the
exact implementation range, and manually reject credentials, private
identifiers, personal data, raw logs, plan bodies, state, backend values, and
unnecessary production topology. Verify every apparent secret fixture is
synthetic before allowing it.

- [ ] **Step 4: Commit gate-only corrections**

```sh
git add -A
git commit -m "test: close Terraform Slice 5 verification gaps"
```

Skip this commit when no correction is needed.

## Task 7: Prepare The Private Slice 5 Controller

**Files:**

- Create only below `.superpowers/sdd/260728-terraform-slice-5/`.
- Tracked files: none.

**Interfaces:**

- Consumes current SSO, exact integrated code, Slice 4 checkpoint, live
  discovery, current quota, and whole-launch cost evidence.
- Produces no AWS mutation.

- [ ] **Step 1: Create the private workspace safely**

```sh
umask 077
mkdir -p .superpowers/sdd/260728-terraform-slice-5
chmod 0700 .superpowers/sdd/260728-terraform-slice-5
```

Verify the directory is ignored with `git check-ignore`.

- [ ] **Step 2: Revalidate current authority and dependencies**

Run the repository's value-blind expected-account, role, and region check with
`AWS_PROFILE=jobcron-admin`. Verify the exact Slice 4 integrated commit and
private checkpoint. Stop on expired credentials, missing checkpoint, unhealthy
private replacement runtime, changed origin tag, public ingress, or weakened
rollback.

- [ ] **Step 3: Fetch and validate without AWS mutation**

Fetch the approved URL to `cloudflare-ips-v4.txt`, then run:

```sh
python3 scripts/normalize-cloudflare-ipv4.py \
  .superpowers/sdd/260728-terraform-slice-5/cloudflare-ips-v4.txt \
  .superpowers/sdd/260728-terraform-slice-5/cloudflare.auto.tfvars.json
```

Expected safe output:

```text
Cloudflare IPv4 set verified
```

Record source URL, retrieval time, entry count, and set digest privately. Do not
print the set or digest.

- [ ] **Step 4: Verify deterministic discovery and quota**

Use read-only AWS CLI calls to prove exactly one origin group carries the Slice
3 tag, record its VPC relationship privately, count current ingress quota use,
and prove `max_entries = 20` plus the one rule stays within the current quota.
Store raw JSON in `quota.json`; print only a value-blind PASS/FAIL summary.

- [ ] **Step 5: Refresh aggregate cost evidence**

Update all required categories, source dates, quantities, recurring upper
bound, one-time upper bound, and cumulative totals. Count unknown costs at their
documented worst-case bound. Stop if either ceiling may be exceeded.

- [ ] **Step 6: Persist the preparation checkpoint**

Record findings and exact private packet paths on the task bead without private
values. Do not send raw evidence or digests through mail or chat.

## Task 8: Create, Review, And Apply The Bootstrap IAM Plan

**Files:**

- Create privately: `bootstrap.tfplan`, `bootstrap-plan.json`,
  `bootstrap-plan.log`, `bootstrap-controller.md`, and `bootstrap-review.md`.
- Tracked files: none.

**Interfaces:**

- Produces only the two `slice5-bootstrap` creates.

- [ ] **Step 1: Initialize the protected bootstrap backend**

Use the existing private partial backend configuration and
`terraform init -reconfigure`. Do not print backend values.

- [ ] **Step 2: Save and check the exact plan**

```sh
terraform -chdir=infra/terraform/bootstrap plan \
  -input=false \
  -out="$PWD/.superpowers/sdd/260728-terraform-slice-5/bootstrap.tfplan" \
  >"$PWD/.superpowers/sdd/260728-terraform-slice-5/bootstrap-plan.log" 2>&1
terraform -chdir=infra/terraform/bootstrap show -json \
  "$PWD/.superpowers/sdd/260728-terraform-slice-5/bootstrap.tfplan" \
  >"$PWD/.superpowers/sdd/260728-terraform-slice-5/bootstrap-plan.json"
python3 scripts/check-terraform-slice-5-plan.py \
  slice5-bootstrap \
  .superpowers/sdd/260728-terraform-slice-5/bootstrap-plan.json \
  .superpowers/sdd/260728-terraform-slice-5/aggregate-cost.json \
  .superpowers/sdd/260728-terraform-slice-5/slice-4-checkpoint.json
```

Expected safe output:

```text
Slice 5 bootstrap plan contract verified
```

- [ ] **Step 3: Build the private controller report**

Record code commit, state serial and lineage, current credential result, two
create addresses, zero update/delete/replace/import/output changes, exact
policy action and tag-condition summary, aggregate cost result, Slice 4
checkpoint, plan digest, and recovery action.

- [ ] **Step 4: Obtain independent exact-digest review**

A reviewer other than the implementer inspects the raw saved plan, JSON,
tracked diff, state binding, IAM policy, cost packet, checkpoint, checker
output, and controller report. The reviewer writes `APPROVED` or `REJECTED` and
the exact digest to `bootstrap-review.md`.

Do not transmit the digest or raw packet through chat or mail.

- [ ] **Step 5: Recheck bindings and apply the saved file**

Immediately before apply, prove code commit, state serial, credential check,
plan digest, cost packet, checkpoint, and reviewer verdict still match. Then:

```sh
terraform -chdir=infra/terraform/bootstrap apply \
  -input=false \
  "$PWD/.superpowers/sdd/260728-terraform-slice-5/bootstrap.tfplan"
```

Stop on any mismatch. After apply, run a refresh-only plan and prove only the
two new IAM addresses exist and the bootstrap root is clean.

## Task 9: Create, Review, And Apply The Initial Edge Plan

**Files:**

- Create privately: `edge.tfplan`, `edge-plan.json`, `edge-plan.log`,
  `edge-controller.md`, and `edge-review.md`.
- Tracked files: none.

**Interfaces:**

- Produces only the two `slice5-edge-create` creates.

- [ ] **Step 1: Initialize the protected edge backend**

Use the existing private partial backend file. Re-run the fetch, validation,
credential, unique-discovery, quota, cost, Slice 4 checkpoint, and bootstrap
post-apply checks before planning.

- [ ] **Step 2: Save and check the exact edge plan**

```sh
terraform -chdir=infra/terraform/edge plan \
  -input=false \
  -var-file="$PWD/.superpowers/sdd/260728-terraform-slice-5/cloudflare.auto.tfvars.json" \
  -out="$PWD/.superpowers/sdd/260728-terraform-slice-5/edge.tfplan" \
  >"$PWD/.superpowers/sdd/260728-terraform-slice-5/edge-plan.log" 2>&1
terraform -chdir=infra/terraform/edge show -json \
  "$PWD/.superpowers/sdd/260728-terraform-slice-5/edge.tfplan" \
  >"$PWD/.superpowers/sdd/260728-terraform-slice-5/edge-plan.json"
python3 scripts/check-terraform-slice-5-plan.py \
  slice5-edge-create \
  .superpowers/sdd/260728-terraform-slice-5/edge-plan.json \
  .superpowers/sdd/260728-terraform-slice-5/aggregate-cost.json \
  .superpowers/sdd/260728-terraform-slice-5/slice-4-checkpoint.json \
  .superpowers/sdd/260728-terraform-slice-5/cloudflare.auto.tfvars.json
```

Expected safe output:

```text
Slice 5 edge create plan contract verified
```

- [ ] **Step 3: Build the private edge controller report**

Record code and state bindings, official-source retrieval result, normalized
entry count and private digest, quota result, exact two-create summary, zero
other actions, aggregate cost result, IAM checkpoint, plan digest, rollback
preservation, and failure procedure.

- [ ] **Step 4: Obtain independent exact-digest review**

The independent reviewer inspects the raw fetch, normalized set, validator
result, quota, saved plan, plan JSON, checker output, IAM policy, cost packet,
Slice 4 checkpoint, state binding, and tracked diff. The reviewer records only
`APPROVED` or `REJECTED` plus the exact digest in `edge-review.md`.

- [ ] **Step 5: Apply only the reviewed edge plan**

Recheck every binding and verdict immediately before:

```sh
terraform -chdir=infra/terraform/edge apply \
  -input=false \
  "$PWD/.superpowers/sdd/260728-terraform-slice-5/edge.tfplan"
```

The apply may create only the prefix list and one ingress rule.

- [ ] **Step 6: Prove clean convergence**

Regenerate a plan with the same tfvars. Expected:

```text
No changes. Your infrastructure matches the configuration.
```

Record the result privately. Do not enable scheduled automation yet.

## Task 10: Verify Failure Preservation And Enable Post-Checkpoint Automation

**Files:**

- Modify after verification:
  `docs/architecture.md`
- Modify after verification:
  `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
- Create private evidence below the Slice 5 SDD workspace.

**Interfaces:**

- Produces the Slice 5 exit checkpoint and enables the already-merged workflow.

- [ ] **Step 1: Prove invalid upstream data stops before AWS**

Run the workflow commands locally with fixtures for empty, malformed, duplicate,
9-entry, 21-entry, IPv6, default, loopback, link-local, multicast, and each
RFC 1918 class. Prove each fails before `terraform init`, plan, or apply and
that the live prefix-list version and ingress rule remain unchanged.

- [ ] **Step 2: Prove fetch failure preserves the last valid version**

Use a controlled failing fetch fixture without changing the tracked official
URL. Record the pre/post prefix-list version and rule identity privately.
Expected value-blind result:

```text
Fetch failure preservation: PASS
```

- [ ] **Step 3: Prove one refresh fixture and one no-change run**

Use only synthetic plan JSON to prove `slice5-edge-refresh` accepts one
prefix-list update and rejects an ingress update. Then run the real workflow
manually with unchanged official data and prove exit code `0` skips apply.

- [ ] **Step 4: Verify the live security boundary**

Use read-only AWS inspection to prove:

```text
Managed prefix list: one, IPv4, max entries 20, official validated set
Origin ingress: exactly one prefix-list rule, TCP 443
Direct internet ports 22/80/7777/5432: absent
Production-root Slice 5 changes: none
Cloudflare/DNS/Origin CA/EIP/public-traffic changes: none
Old EC2/current RDS/rollback materials: preserved
```

Keep identifiers and raw results private.

- [ ] **Step 5: Enable the protected workflow variable**

Only after Steps 1-4 pass, set `EDGE_AUTOMATION_ENABLED` to `"true"` in the
protected `edge` environment. This enables daily and manual post-checkpoint
refreshes. Do not add a repository default that bypasses the gate.

- [ ] **Step 6: Update durable documentation**

Document only the stable ownership and failure boundary in
`docs/architecture.md`. Mark Slice 5 complete in the roadmap with the exact
integrated implementation baseline and a link to sanitized verification. Do
not publish private evidence, plan digests, identifiers, CIDRs, or workflow
secret names beyond the generic interface already documented.

- [ ] **Step 7: Run final gates and publication review**

```sh
./scripts/check-terraform.sh
go test ./... -count=1
go vet ./...
go build ./cmd/jobcron ./cmd/jobcron-import ./cmd/jobcron-user
test -z "$(gofmt -l .)"
git diff --check
```

Run Gitleaks and manual publication review over the complete final range.

- [ ] **Step 8: Commit the verified documentation**

```sh
git add docs/architecture.md \
  docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md
git commit -m "docs: record Terraform Slice 5 verification"
```

## Stop Conditions

Stop before mutation if any of these occurs:

- Slice 4 is not integrated, healthy, or attached to the origin group.
- The origin selector yields zero or multiple groups.
- The official fetch fails or redirects outside the approved HTTPS endpoint.
- Validation rejects the response or its count leaves 10-20.
- The current prefix-list quota cannot safely carry `max_entries = 20`.
- A plan contains an address or action outside its exact mode.
- An initial plan lacks an independent exact-digest approval.
- A plan, state, log, screenshot, chat, or tracked file exposes private data.
- Credentials are expired, wrong-account, wrong-role, wrong-region, or broader
  than the reviewed edge ceiling.
- Aggregate cost evidence is stale, incomplete, or may cross either ceiling.
- Any EIP, Cloudflare account, Origin CA, DNS, proxy, public-traffic,
  production-root, destroy, or replacement action appears.
- The old EC2, old RDS, unattached EIP, or rollback path changes.
- The post-apply plan is not clean or the no-change workflow invokes apply.

## Recovery

- Before any apply, delete only ignored local candidate artifacts and regenerate
  from the current code, state, credentials, source data, and cost packet.
- If the bootstrap apply fails, inspect the exact saved plan and private logs;
  do not broaden the policy or retry an unreviewed plan.
- If the edge apply fails, leave the existing prefix-list version active,
  inspect the saved plan and state, and retry only after a new checked plan and
  independent review.
- If a scheduled refresh fails, keep automation enabled only when the last valid
  version remains active and the failure is understood. Otherwise set the
  protected enable variable to false; do not delete the prefix list or ingress
  rule.
- Never recover by applying blindly, reconstructing state, deleting the prefix
  list, opening direct CIDR ingress, adding port 80, or mutating Cloudflare.
- Recover Terraform state from protected S3 object versions under the existing
  bootstrap procedure.

[foundation]:
  ../specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
[human-steps]:
  ../specs/260726-terraform-first-production-launch-human-blocked-steps.md
[window-1]:
  ../specs/260728-pre-batch-1-window-1-authorization-contract.md
[slice-3]:
  260728-terraform-slice-3-private-database-secret-containers-implementation.md
[slice-4]:
  260728-terraform-slice-4-replacement-ec2-transient-runtime-implementation.md
