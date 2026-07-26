# Terraform Slice 1: Identity, State Bootstrap, And CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish reproducible Terraform roots, protected remote state,
short-lived AWS access, and plan-only production automation without changing the
existing VPC, EC2, RDS, EIP, DNS, or application.

**Architecture:** A local bootstrap root creates the state bucket, GitHub OIDC
provider, and initially state-only automation roles. After an explicitly
approved local apply, bootstrap state migrates into the same protected bucket
using S3 native lock files. Production and edge remain separate roots; Slice 1
gives neither role permission to mutate future production resources.

**Tech Stack:** Terraform CLI 1.15.8, HashiCorp AWS provider 6.33.0, AWS CLI v2,
IAM Identity Center, Amazon S3, AWS IAM OIDC, GitHub Actions, Zsh, Bash, and
Gitleaks.

## Global Constraints

- This plan implements only Slice 1 from the
  [Terraform launch roadmap][roadmap].
- Do not execute infrastructure changes until the source-authority conflict in
  the roadmap is resolved.
- The AWS root user is never used for CLI, Terraform, or GitHub Actions.
- Use only the named `jobcron-admin` IAM Identity Center profile on the trusted
  Mac.
- Use `ap-northeast-2` as the workload Region. The IAM Identity Center directory
  Region may differ.
- Treat every tracked file and workflow log as public.
- Never track or print account IDs, ARNs, resource IDs, addresses, CIDRs,
  endpoints, credentials, certificate material, state bucket names, or private
  recovery locations.
- Store private Terraform values only in ignored `*.tfvars` and
  `*.backend.hcl` files or protected GitHub environment settings.
- Track `.terraform.lock.hcl`; ignore `.terraform/`, state, plans, variable
  values, backend values, and crash logs.
- Use three root configurations under `infra/terraform/`. Do not create a
  one-implementation module.
- Every Terraform apply uses an exact saved plan and a human approval
  checkpoint immediately before apply. Backend migration, which Terraform
  cannot encode in a plan, has its own approval and post-migration no-change
  proof.
- Use S3 native locking with `use_lockfile = true`; do not create a DynamoDB
  lock table.
- Pin Terraform, the AWS provider, and every third-party GitHub Action.
- Production automation is plan-only. It must contain no `terraform apply`.
- Do not create the scheduled edge apply workflow in this slice.
- Do not grant the edge role EC2, RDS, IAM, Secrets Manager, or general
  security-group permissions.
- Do not change the existing VPC, subnets, routes, security groups, EC2, RDS,
  EIP, DNS, registry, or application.
- Preserve the user's existing uncommitted specification edits and unrelated
  untracked files.
- Keep private run evidence only in
  `.superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md`;
  this path is ignored and must use mode `600`.
- Commit locally at meaningful checkpoints. Never push or create a pull request.

## Educational Model

**Terraform root:** a directory with its own configuration and state. The three
roots separate ownership so the scheduled edge automation can never acquire the
production root's authority.

**State:** Terraform's mapping between configuration and real cloud objects.
Losing or exposing it can cause destructive plans or leak metadata, so it is
versioned, encrypted, access-controlled, and recoverable.

**Native lock file:** an S3 object created while Terraform changes state.
Concurrent writers cannot both proceed. It replaces the older DynamoDB locking
pattern.

**OIDC:** GitHub presents a short-lived signed identity token to AWS. AWS issues
temporary role credentials only when the repository and protected environment
match the role's trust policy. No AWS access key is stored in GitHub.

**Saved plan:** an immutable file containing the exact actions Terraform
calculated. Applying that file prevents a second, different plan from being
silently calculated at approval time.

## Slice Completion Contract

Slice 1 is complete only when:

1. `jobcron-admin` verifies the expected account, SSO role, and workload Region
   without printing them;
2. all three roots pass format, initialization-without-backend, validation, and
   tests using the committed provider lock;
3. the bootstrap plan creates only the approved state and identity resources;
4. the state bucket is private, encrypted, versioned, TLS-only, and protected
   from Terraform destruction;
5. bootstrap state is remote, native locking works, and recovery from an older
   S3 object version has been rehearsed without publishing identifiers;
6. the production and edge trust boundaries match their protected environments;
7. both automation roles have state-only permissions in this slice;
8. static CI passes and production automation can plan but cannot apply; and
9. a clean production root plan contains no infrastructure changes.

---

### Task 0: Resolve The Architecture Authority Conflict

**Files:**

- Inspect:
  `docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md`
- Inspect:
  `docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md`
- Modify after human confirmation:
  `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
- Modify only after human confirmation: the conflicting warning in the
  Terraform architecture specification

**Interfaces:**

- Consumes: the human's decision that Terraform-first deployment remains active
- Produces: one unambiguous architecture authority for Tasks 1 through 9

- [ ] **Step 1: Show only the conflicting tracked-document status**

Run:

```bash
git diff -- \
  docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md \
  docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md
```

Expected: the active human spec names the Terraform spec as its authority while
the uncommitted warning calls that same Terraform spec deprecated.

- [ ] **Step 2: Explain the contradiction to the human**

Explain:

- the warning says not to execute the Terraform design;
- the current request asks to implement its six slices; and
- the warning's SSH and persistent-`.env` description contradicts the
  Session Manager and transient-runtime design in the same file.

Recommended decision: keep the Terraform design active and remove only the
contradictory warning.

- [ ] **Step 3: Stop for the human's authority decision**

Do not edit either specification or begin Task 1 until the human confirms which
document is authoritative.

- [ ] **Step 4: Verify the resolved authority**

If the human chooses the recommended resolution, remove only the contradictory
uncommitted warning and replace the roadmap's `Source-Authority Gate` section
with:

```markdown
## Source-Authority Gate

**Resolved:** The Terraform AWS foundation specification remains the
architecture authority. The Terraform-first human steps specification remains
the human checkpoint authority.
```

If the human chooses a different authority, stop and rewrite this plan before
execution.

Run:

```bash
rg -n 'Status:|WARNING: DEPRECATED|Infrastructure authority|must not be executed' \
  docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md \
  docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md
```

Expected when the recommended resolution is chosen:

- the Terraform design remains `Approved design, awaiting implementation
  planning`;
- the human spec still points to it; and
- no warning says the Terraform design itself must not be executed.

- [ ] **Step 5: Commit only the confirmed resolution**

```bash
git add \
  docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md
git add -p -- \
  docs/superpowers/specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
test -z "$(git diff --cached --name-only -- \
  docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md)"
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "docs: confirm Terraform launch authority"
```

Removing an uncommitted warning may restore the Terraform specification to
`HEAD`, leaving no hunk from that file to stage. That is expected. Stop if the
staged diff contains unrelated OF notes or user edits that the human did not
authorize for this commit.

### Task 1: Lock The Toolchain And Root Ownership

**Files:**

- Create: `.terraform-version`
- Modify: `.gitignore`
- Create: `infra/terraform/bootstrap/versions.tf`
- Create: `infra/terraform/production/versions.tf`
- Create: `infra/terraform/production/backend.tf`
- Create: `infra/terraform/edge/versions.tf`
- Create: `infra/terraform/edge/backend.tf`
- Create: `scripts/check-terraform.sh`

**Interfaces:**

- Consumes: Terraform CLI 1.15.8 installed on the trusted Mac
- Produces: three independently validated roots and the repository-wide
  `scripts/check-terraform.sh` verification entry point

- [ ] **Step 1: Write the pinned Terraform version**

Create `.terraform-version`:

```text
1.15.8
```

- [ ] **Step 2: Extend the Terraform ignore contract**

Keep the existing Terraform ignore rules and add:

```gitignore
*.backend.hcl
```

Do not ignore `.terraform.lock.hcl`.

- [ ] **Step 3: Create the root version contracts**

Create the same `versions.tf` in all three roots:

```hcl
terraform {
  required_version = "= 1.15.8"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.33.0"
    }
  }
}

provider "aws" {
  region = "ap-northeast-2"
}
```

Do not create `bootstrap/backend.tf` yet. Bootstrap must use local state for its
first apply.

- [ ] **Step 4: Declare partial remote backends for future roots**

Create `production/backend.tf`:

```hcl
terraform {
  backend "s3" {
    key          = "production/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }
}
```

Create `edge/backend.tf` with only the key changed:

```hcl
terraform {
  backend "s3" {
    key          = "edge/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }
}
```

Bucket and Region are supplied from ignored backend files or protected GitHub
environment settings.

- [ ] **Step 5: Write the minimal repository check**

Create executable `scripts/check-terraform.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

terraform -chdir="$repo_root/infra/terraform" fmt -check -recursive

for root in bootstrap production edge; do
  root_path="$repo_root/infra/terraform/$root"
  terraform -chdir="$root_path" init -backend=false -input=false
  terraform -chdir="$root_path" validate
  terraform -chdir="$root_path" test
done
```

- [ ] **Step 6: Generate and normalize the provider locks**

Run once in each root:

```bash
for root in bootstrap production edge; do
  terraform -chdir="infra/terraform/$root" providers lock \
    -platform=darwin_amd64 \
    -platform=linux_amd64
done
```

Expected: each root contains a tracked `.terraform.lock.hcl` selecting AWS
provider 6.33.0 with checksums for the trusted Mac and GitHub's Linux runner.

- [ ] **Step 7: Run the root checks**

```bash
chmod +x scripts/check-terraform.sh
bash -n scripts/check-terraform.sh
./scripts/check-terraform.sh
git check-ignore infra/terraform/bootstrap/example.tfplan
test "$(git check-ignore infra/terraform/bootstrap/.terraform.lock.hcl || true)" = ""
```

Expected: checks pass, the synthetic plan path is ignored, and provider lock
files are not ignored.

- [ ] **Step 8: Commit the root contract**

```bash
git add .terraform-version .gitignore scripts/check-terraform.sh infra/terraform
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "infra: establish Terraform root contracts"
```

### Task 2: Implement The Protected State Bucket

**Files:**

- Create: `infra/terraform/bootstrap/variables.tf`
- Create: `infra/terraform/bootstrap/state.tf`
- Create: `infra/terraform/bootstrap/outputs.tf`
- Create: `infra/terraform/bootstrap/terraform.tfvars.example`
- Create: `infra/terraform/bootstrap/tests/state.tftest.hcl`

**Interfaces:**

- Consumes: private `state_bucket_name` supplied in an ignored `.tfvars`
- Produces: `aws_s3_bucket.state` and its encryption, versioning,
  public-access, TLS, and destroy-protection contract

- [ ] **Step 1: Define the bootstrap inputs**

Create `variables.tf`:

```hcl
variable "state_bucket_name" {
  description = "Globally unique private S3 bucket used only for Terraform state."
  type        = string

  validation {
    condition     = length(var.state_bucket_name) >= 3
    error_message = "state_bucket_name must be a valid non-empty S3 bucket name."
  }
}

variable "github_repository" {
  description = "GitHub repository in owner/name form."
  type        = string
  default     = "ohchanwu/jobcron"

  validation {
    condition     = can(regex("^[^/]+/[^/]+$", var.github_repository))
    error_message = "github_repository must use owner/name form."
  }
}

variable "existing_github_oidc_provider_arn" {
  description = "Existing GitHub OIDC provider ARN to adopt, or null to create it."
  type        = string
  default     = null

  validation {
    condition = (
      var.existing_github_oidc_provider_arn == null ||
      endswith(
        var.existing_github_oidc_provider_arn,
        ":oidc-provider/token.actions.githubusercontent.com",
      )
    )
    error_message = "The existing provider must be GitHub's OIDC provider."
  }
}
```

- [ ] **Step 2: Provide a publication-safe input example**

Create `terraform.tfvars.example`:

```hcl
state_bucket_name                 = "replace-with-private-globally-unique-name"
github_repository                 = "ohchanwu/jobcron"
existing_github_oidc_provider_arn = null
```

The real file is `terraform.tfvars`, is ignored, and must never be printed or
committed.

- [ ] **Step 3: Define the bucket and safeguards**

Create `state.tf` with:

```hcl
resource "aws_s3_bucket" "state" {
  bucket = var.state_bucket_name

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket = aws_s3_bucket.state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id

  versioning_configuration {
    status = "Enabled"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }

  lifecycle {
    prevent_destroy = true
  }
}

data "aws_iam_policy_document" "state_bucket" {
  statement {
    sid    = "DenyInsecureTransport"
    effect = "Deny"

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.state.arn,
      "${aws_s3_bucket.state.arn}/*",
    ]

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "state" {
  bucket = aws_s3_bucket.state.id
  policy = data.aws_iam_policy_document.state_bucket.json
}
```

The bucket, versioning configuration, and encryption configuration are the
three destroy-protected state resources. The access block and TLS policy remain
reconcilable controls; replacing either does not delete stored state objects.

- [ ] **Step 4: Add sensitive operator outputs**

Create `outputs.tf`:

```hcl
output "state_bucket_name" {
  value     = aws_s3_bucket.state.id
  sensitive = true
}
```

`sensitive` suppresses ordinary CLI display but does not remove the value from
state. The bucket name must still be treated as private.

- [ ] **Step 5: Write the mocked state tests**

Create `tests/state.tftest.hcl`:

```hcl
mock_provider "aws" {}

variables {
  state_bucket_name = "jobcron-state-test-only"
}

run "state_contract" {
  command = plan

  assert {
    condition     = aws_s3_bucket_public_access_block.state.block_public_policy
    error_message = "State bucket must block public bucket policies."
  }

  assert {
    condition     = aws_s3_bucket_versioning.state.versioning_configuration[0].status == "Enabled"
    error_message = "State bucket versioning must be enabled."
  }

  assert {
    condition     = aws_s3_bucket_server_side_encryption_configuration.state.rule[0].apply_server_side_encryption_by_default[0].sse_algorithm == "AES256"
    error_message = "State bucket must enable default encryption."
  }
}
```

- [ ] **Step 6: Verify the state contract**

Append these lifecycle and TLS source-contract checks to
`scripts/check-terraform.sh`:

```bash
state_file="$repo_root/infra/terraform/bootstrap/state.tf"

test "$(grep -Fc 'prevent_destroy = true' "$state_file")" -eq 3
grep -Fq 'variable = "aws:SecureTransport"' "$state_file"
grep -Fq 'values   = ["false"]' "$state_file"
```

Then run:

```bash
./scripts/check-terraform.sh
git diff --check
```

Expected: all three roots validate, the bootstrap mock plan passes without AWS
credentials, all three state safeguards are destroy-protected, and the TLS-only
policy remains present.

- [ ] **Step 7: Commit the state bucket**

```bash
git add infra/terraform/bootstrap
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "infra: define protected Terraform state"
```

### Task 3: Implement GitHub OIDC Trust And State-Only Roles

**Files:**

- Create: `infra/terraform/bootstrap/identity.tf`
- Modify: `infra/terraform/bootstrap/outputs.tf`
- Modify: `scripts/check-terraform.sh`

**Interfaces:**

- Consumes: `var.github_repository` and `aws_s3_bucket.state`
- Produces: `aws_iam_openid_connect_provider.github`,
  `aws_iam_role.production`, and `aws_iam_role.edge`

- [ ] **Step 1: Declare GitHub's OIDC provider**

Create `identity.tf` beginning with:

```hcl
import {
  for_each = (
    var.existing_github_oidc_provider_arn == null
    ? {}
    : { github = var.existing_github_oidc_provider_arn }
  )

  to = aws_iam_openid_connect_provider.github
  id = each.value
}

resource "aws_iam_openid_connect_provider" "github" {
  url = "https://token.actions.githubusercontent.com"

  client_id_list = ["sts.amazonaws.com"]
}
```

The conditional import is part of the saved plan. It adopts the existing
provider when the private variable is set and creates one when it is `null`.
This avoids a duplicate without allowing an unreviewed standalone
`terraform import` state mutation.

Do not hard-code a certificate thumbprint. In AWS provider 6.33.0,
`thumbprint_list` is optional and computed, and AWS validates GitHub through its
trusted root CA library.

- [ ] **Step 2: Define the production trust document**

Add:

```hcl
data "aws_iam_policy_document" "production_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:environment:production"]
    }
  }
}

resource "aws_iam_role" "production" {
  name               = "JobcronTerraformProduction"
  assume_role_policy = data.aws_iam_policy_document.production_assume.json
}
```

- [ ] **Step 3: Define the edge trust document**

Repeat the production trust structure as `data.aws_iam_policy_document.edge_assume`
and `aws_iam_role.edge`, changing only:

```hcl
values = ["repo:${var.github_repository}:environment:edge"]
```

and:

```hcl
name = "JobcronTerraformEdge"
```

Because an environment appears in the OIDC subject, branch is not also encoded
in that subject. Task 8 restricts the `edge` environment to `main`.

- [ ] **Step 4: Give each role access only to its approved state keys**

Add:

```hcl
locals {
  production_state_keys = [
    "bootstrap/terraform.tfstate",
    "production/terraform.tfstate",
  ]
  production_lock_keys = [
    "bootstrap/terraform.tfstate.tflock",
    "production/terraform.tfstate.tflock",
  ]

  edge_state_keys = [
    "edge/terraform.tfstate",
  ]
  edge_lock_keys = [
    "edge/terraform.tfstate.tflock",
  ]
}

data "aws_iam_policy_document" "production_state" {
  statement {
    actions = [
      "s3:GetBucketLocation",
      "s3:ListBucket",
    ]
    resources = [aws_s3_bucket.state.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values = concat(
        local.production_state_keys,
        local.production_lock_keys,
      )
    }
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
    ]
    resources = [
      for key in concat(
        local.production_state_keys,
        local.production_lock_keys,
      ) :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }

  statement {
    actions = ["s3:DeleteObject"]
    resources = [
      for key in local.production_lock_keys :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }
}

resource "aws_iam_policy" "production_state" {
  name   = "JobcronTerraformProductionState"
  policy = data.aws_iam_policy_document.production_state.json
}

resource "aws_iam_role_policy_attachment" "production_state" {
  role       = aws_iam_role.production.name
  policy_arn = aws_iam_policy.production_state.arn
}

data "aws_iam_policy_document" "edge_state" {
  statement {
    actions = [
      "s3:GetBucketLocation",
      "s3:ListBucket",
    ]
    resources = [aws_s3_bucket.state.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values = concat(
        local.edge_state_keys,
        local.edge_lock_keys,
      )
    }
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
    ]
    resources = [
      for key in concat(
        local.edge_state_keys,
        local.edge_lock_keys,
      ) :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }

  statement {
    actions = ["s3:DeleteObject"]
    resources = [
      for key in local.edge_lock_keys :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }
}

resource "aws_iam_policy" "edge_state" {
  name   = "JobcronTerraformEdgeState"
  policy = data.aws_iam_policy_document.edge_state.json
}

resource "aws_iam_role_policy_attachment" "edge_state" {
  role       = aws_iam_role.edge.name
  policy_arn = aws_iam_policy.edge_state.arn
}
```

`DeleteObject` is limited to `.tflock` objects because Terraform deletes lock
files when an operation ends; the roles cannot delete state objects. Do not add
another policy or action in this slice.

- [ ] **Step 5: Add sensitive role outputs**

Append:

```hcl
output "production_role_arn" {
  value     = aws_iam_role.production.arn
  sensitive = true
}

output "edge_role_arn" {
  value     = aws_iam_role.edge.arn
  sensitive = true
}
```

- [ ] **Step 6: Test the exact OIDC subjects and permission ceiling**

Append these source-contract checks to `scripts/check-terraform.sh`:

```bash
identity_file="$repo_root/infra/terraform/bootstrap/identity.tf"

grep -Fq \
  'repo:${var.github_repository}:environment:production' \
  "$identity_file"
grep -Fq \
  'repo:${var.github_repository}:environment:edge' \
  "$identity_file"
grep -Fq '"sts.amazonaws.com"' "$identity_file"

for forbidden in '"ec2:' '"rds:' '"iam:' '"secretsmanager:'; do
  if grep -Fiq "$forbidden" "$identity_file"; then
    printf 'Slice 1 identity policy contains forbidden action: %s\n' \
      "$forbidden" >&2
    exit 1
  fi
done
```

This source check complements `terraform validate`: it prevents later edits
from silently widening the Slice 1 edge or production policy before a later
slice explicitly changes the contract.

- [ ] **Step 7: Run static and mocked verification**

```bash
./scripts/check-terraform.sh
git diff --check
```

Expected: both trust subjects and all state-only boundaries pass.

- [ ] **Step 8: Commit the identity boundary**

```bash
git add infra/terraform/bootstrap
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "infra: define Terraform OIDC boundaries"
```

### Task 4: Human Identity Center Configuration And Value-Blind Preflight

**Files:**

- Private only: `~/.aws/config`
- Private only:
  `.superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md`
- No tracked file changes

**Interfaces:**

- Consumes: the human's private Identity Center start or issuer URL, expected
  account ID, `JobcronAdministratorAccess` assignment, and FIDO2 authenticators
- Produces: a verified short-lived `jobcron-admin` CLI session

- [ ] **Step 1: Confirm the human prerequisites**

The human confirms privately:

- the account uses an organization instance of IAM Identity Center;
- the individual Identity Center user is assigned
  `JobcronAdministratorAccess`;
- the permission set currently attaches AWS-managed `AdministratorAccess` only
  for bootstrap;
- primary and recovery FIDO2 authenticators are registered; and
- root remains reserved for billing and account recovery.

- [ ] **Step 2: Configure the named profile without opening the default browser**

Run in the human's terminal:

```bash
aws configure sso --profile jobcron-admin --no-browser --use-device-code
```

Enter privately:

- SSO session name: `jobcron`
- private start URL or issuer URL
- the Identity Center directory Region
- registration scope: `sso:account:access`
- intended AWS account
- permission set: `JobcronAdministratorAccess`
- default client Region: `ap-northeast-2`
- output format: `json`
- profile name: `jobcron-admin`

The human opens the displayed verification URL themselves and completes MFA.

- [ ] **Step 3: Start the short-lived session**

```bash
aws sso login --profile jobcron-admin --no-browser --use-device-code
export AWS_PROFILE=jobcron-admin
```

Expected: `Successfully logged into Start URL` without an access key or secret
key being created. `AWS_PROFILE` makes the Terraform AWS provider use this
verified short-lived session instead of an ambient default profile.

- [ ] **Step 4: Verify identity without printing identifiers**

Run:

```zsh
jobcron_verify_aws_identity() {
  local expected_account_id actual_account_id caller_arn actual_region

  read -r -s 'expected_account_id?Expected AWS account ID: '
  printf '\n'
  actual_account_id="$(aws sts get-caller-identity \
    --profile jobcron-admin --query Account --output text)"
  caller_arn="$(aws sts get-caller-identity \
    --profile jobcron-admin --query Arn --output text)"
  actual_region="$(aws configure get region --profile jobcron-admin)"

  if [[ "$actual_account_id" == "$expected_account_id" &&
        "$caller_arn" == *':assumed-role/AWSReservedSSO_JobcronAdministratorAccess_'* &&
        "$actual_region" == 'ap-northeast-2' ]]; then
    printf 'AWS identity, role, and region verified\n'
    return 0
  fi

  printf 'AWS identity, role, or region mismatch\n' >&2
  return 1
}

jobcron_verify_aws_identity
JOBCRON_IDENTITY_RC=$?
unset -f jobcron_verify_aws_identity
if (( JOBCRON_IDENTITY_RC != 0 )); then
  unset JOBCRON_IDENTITY_RC
  false
fi
unset JOBCRON_IDENTITY_RC
```

Expected single success line:

```text
AWS identity, role, and region verified
```

Stop on any mismatch. Do not paste the raw `get-caller-identity` response into
chat or tracked logs; it contains the account ID, user ID, and ARN.

- [ ] **Step 5: Record private evidence**

Create the ignored log safely if it does not exist:

```bash
mkdir -p .superpowers/archive/2026-07-26-terraform-production-launch
umask 077
: >> .superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md
chmod 600 \
  .superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md
git check-ignore \
  .superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md
```

Record only in that private operator log:

- verification timestamp;
- profile name;
- pass/fail result; and
- the human's confirmation that the raw identifiers matched.

No Git commit is required.

### Task 5: Create And Review The Local Bootstrap Plan

**Files:**

- Private: `infra/terraform/bootstrap/terraform.tfvars`
- Ignored: `infra/terraform/bootstrap/slice1-bootstrap.tfplan`
- Private:
  `.superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md`
- No tracked file changes unless review finds an implementation defect

**Interfaces:**

- Consumes: verified `jobcron-admin`, reviewed HCL, and private bucket name
- Produces: one exact saved plan approved for bootstrap apply

- [ ] **Step 1: Create the ignored private values file**

```bash
cp infra/terraform/bootstrap/terraform.tfvars.example \
  infra/terraform/bootstrap/terraform.tfvars
chmod 600 infra/terraform/bootstrap/terraform.tfvars
```

Edit the bucket name privately. Confirm ignore status:

```bash
git check-ignore infra/terraform/bootstrap/terraform.tfvars
```

- [ ] **Step 2: Inventory the GitHub OIDC provider privately**

Run without printing an ARN:

```zsh
jobcron_inventory_github_oidc() {
  typeset -g JOBCRON_GITHUB_OIDC_COUNT=0
  typeset -g JOBCRON_GITHUB_OIDC_ARN=''
  local oidc_arn oidc_url

  while IFS= read -r oidc_arn; do
    [[ -z "$oidc_arn" ]] && continue
    oidc_url="$(
      aws iam get-open-id-connect-provider \
        --profile jobcron-admin \
        --open-id-connect-provider-arn "$oidc_arn" \
        --query Url \
        --output text
    )"
    if [[ "$oidc_url" == 'token.actions.githubusercontent.com' ]]; then
      JOBCRON_GITHUB_OIDC_COUNT=$((JOBCRON_GITHUB_OIDC_COUNT + 1))
      JOBCRON_GITHUB_OIDC_ARN="$oidc_arn"
    fi
  done < <(
    aws iam list-open-id-connect-providers \
      --profile jobcron-admin \
      --query 'OpenIDConnectProviderList[].Arn' \
      --output text |
      tr '\t' '\n'
  )

  if [[ "$JOBCRON_GITHUB_OIDC_COUNT" == "0" ]]; then
    printf 'No existing OIDC provider found\n'
    return 0
  fi
  if [[ "$JOBCRON_GITHUB_OIDC_COUNT" == "1" ]]; then
    printf 'One existing GitHub OIDC provider will be imported by the plan\n'
    return 0
  fi

  printf 'Multiple GitHub OIDC providers require private review\n' >&2
  return 1
}

jobcron_inventory_github_oidc
JOBCRON_OIDC_INVENTORY_RC=$?
unset -f jobcron_inventory_github_oidc
if (( JOBCRON_OIDC_INVENTORY_RC != 0 )); then
  unset JOBCRON_OIDC_INVENTORY_RC
  unset JOBCRON_GITHUB_OIDC_COUNT JOBCRON_GITHUB_OIDC_ARN
  false
fi
unset JOBCRON_OIDC_INVENTORY_RC
```

If one provider exists, privately edit `terraform.tfvars` so
`existing_github_oidc_provider_arn` contains the ARN value held in
`JOBCRON_GITHUB_OIDC_ARN`—not the shell variable's name—without printing it.
The import will then appear inside the saved plan for human review. If none
exists, leave the variable `null` so the saved plan creates the provider.

Clear the private shell values:

```zsh
unset JOBCRON_GITHUB_OIDC_COUNT JOBCRON_GITHUB_OIDC_ARN
```

Stop when more than one GitHub provider exists.

- [ ] **Step 3: Initialize local bootstrap state**

```bash
terraform -chdir=infra/terraform/bootstrap init -input=false
```

Expected: local backend initialization and the already committed provider lock.

- [ ] **Step 4: Create the exact saved plan without publishing it**

```bash
terraform -chdir=infra/terraform/bootstrap plan \
  -input=false \
  -out=slice1-bootstrap.tfplan
```

Do not redirect, upload, commit, or paste the plan. Review it in the human's
private terminal.

- [ ] **Step 5: Human plan review**

The human verifies that the plan contains only:

- one state bucket;
- its public-access block, versioning, AES256 default encryption, and TLS-only
  policy;
- GitHub's OIDC provider or its approved import;
- one production role and one edge role;
- state-key policies and attachments; and
- sensitive outputs.

Stop if the plan contains any replacement, destruction, existing network or
compute resource, broad edge permissions, access keys, or unexpected IAM trust.

- [ ] **Step 6: Record approval privately**

The human records the plan file SHA-256 and explicit approval:

```bash
shasum -a 256 infra/terraform/bootstrap/slice1-bootstrap.tfplan
```

The hash is private operational evidence and is not committed.

### Task 6: Apply Bootstrap And Migrate State

**Files:**

- Create after the local apply:
  `infra/terraform/bootstrap/backend.tf`
- Create:
  `infra/terraform/bootstrap/backend.example.hcl`
- Private: `infra/terraform/bootstrap/jobcron.backend.hcl`
- Private:
  `.superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md`

**Interfaces:**

- Consumes: the exact human-approved `slice1-bootstrap.tfplan`
- Produces: remote bootstrap state at `bootstrap/terraform.tfstate` with S3
  native locking

- [ ] **Step 1: Reconfirm authorization immediately before apply**

Human confirms:

- the saved plan hash still matches;
- the SSO session is active;
- no newer plan replaced the reviewed file; and
- applying the exact plan is authorized.

- [ ] **Step 2: Apply exactly the saved plan**

```bash
terraform -chdir=infra/terraform/bootstrap apply \
  -input=false \
  slice1-bootstrap.tfplan
```

Expected: only the reviewed bootstrap resources are created or imported.

Stop on partial failure. Do not rerun apply blindly; inspect state and create a
new plan.

- [ ] **Step 3: Create the partial backend contract**

Create `backend.tf`:

```hcl
terraform {
  backend "s3" {
    key          = "bootstrap/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }
}
```

Create `backend.example.hcl`:

```hcl
bucket = "replace-with-private-state-bucket"
region = "ap-northeast-2"
```

Create the real ignored `jobcron.backend.hcl`, set mode `600`, and verify
`git check-ignore` reports it.

- [ ] **Step 4: Migrate local state**

Immediately before migration, the human reconfirms the private destination
bucket and authorizes copying local bootstrap state into it. Then run:

```bash
terraform -chdir=infra/terraform/bootstrap init \
  -input=false \
  -migrate-state \
  -backend-config=jobcron.backend.hcl
```

The human answers `yes` only after confirming the destination bucket privately.

- [ ] **Step 5: Prove the migrated state is authoritative**

```bash
terraform -chdir=infra/terraform/bootstrap plan \
  -input=false \
  -detailed-exitcode \
  -out=slice1-post-migration.tfplan
```

Expected exit code: `0`, meaning no changes. Exit code `2` means drift; stop and
review privately. Exit code `1` is an error.

- [ ] **Step 6: Prove native locking**

In terminal A:

```bash
terraform -chdir=infra/terraform/bootstrap console
```

Keep the console prompt open. In terminal B:

```bash
terraform -chdir=infra/terraform/bootstrap plan \
  -input=false \
  -lock-timeout=0s \
  -out=slice1-lock-check.tfplan
```

Expected: terminal B reports that the state lock is already held and makes no
state change. Exit the console in terminal A with `Ctrl-D`, then confirm a normal
no-change plan succeeds. Do not use `-lock=false` and do not force-unlock an
active owner.

- [ ] **Step 7: Rehearse state-version recovery without changing production**

Create and privately review a refresh-only saved plan:

```bash
terraform -chdir=infra/terraform/bootstrap plan \
  -refresh-only \
  -input=false \
  -out=slice1-refresh.tfplan
```

The human confirms that the refresh-only plan contains no resource mutation and
explicitly approves applying that exact file. Then run:

```bash
terraform -chdir=infra/terraform/bootstrap apply \
  -input=false \
  slice1-refresh.tfplan
```

Capture, but do not print, the two newest state versions:

```zsh
jobcron_verify_state_recovery() {
  local state_bucket versions_json latest_version previous_version recovery_copy

  state_bucket="$(
    terraform -chdir=infra/terraform/bootstrap output -raw state_bucket_name
  )"
  versions_json="$(
    aws s3api list-object-versions \
      --profile jobcron-admin \
      --bucket "$state_bucket" \
      --prefix bootstrap/terraform.tfstate \
      --output json
  )"
  latest_version="$(
    jq -r '.Versions | map(select(.IsLatest == true))[0].VersionId' \
      <<<"$versions_json"
  )"
  previous_version="$(
    jq -r '.Versions | map(select(.IsLatest == false)) |
      sort_by(.LastModified) | reverse | .[0].VersionId' \
      <<<"$versions_json"
  )"

  if [[ -z "$latest_version" ||
        -z "$previous_version" ||
        "$previous_version" == 'null' ]]; then
    printf 'State version recovery evidence is incomplete\n' >&2
    return 1
  fi

  recovery_copy="$(mktemp)"
  if ! aws s3api get-object \
      --profile jobcron-admin \
      --bucket "$state_bucket" \
      --key bootstrap/terraform.tfstate \
      --version-id "$previous_version" \
      "$recovery_copy" >/dev/null; then
    rm -f "$recovery_copy"
    return 1
  fi

  if ! jq -e \
      '.version != null and .serial != null and .lineage != null' \
      "$recovery_copy" >/dev/null; then
    rm -f "$recovery_copy"
    return 1
  fi
  rm -f "$recovery_copy"
  printf 'Prior Terraform state version retrieved and parsed\n'
}

jobcron_verify_state_recovery
JOBCRON_RECOVERY_RC=$?
unset -f jobcron_verify_state_recovery
if (( JOBCRON_RECOVERY_RC != 0 )); then
  unset JOBCRON_RECOVERY_RC
  false
fi
unset JOBCRON_RECOVERY_RC
```

Expected: the earlier version can be retrieved and parsed as Terraform state
while live state remains unchanged.

- [ ] **Step 8: Commit the backend contract**

```bash
git add \
  infra/terraform/bootstrap/backend.tf \
  infra/terraform/bootstrap/backend.example.hcl
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "infra: migrate Terraform bootstrap state"
unset AWS_PROFILE
```

### Task 7: Add Static CI And Production Plan-Only Automation

**Files:**

- Create: `.github/workflows/terraform-check.yml`
- Create: `.github/workflows/terraform-production-plan.yml`
- Create: `infra/terraform/production/backend.example.hcl`
- Create: `infra/terraform/edge/backend.example.hcl`

**Interfaces:**

- Consumes: committed roots, provider locks, production role, and protected
  environment settings
- Produces: public-safe static checks and a manually dispatched production
  change detector with no apply path

- [ ] **Step 1: Pin the reviewed action commits**

Before implementation, use `git ls-remote` against each official repository to
confirm each major tag still resolves to the public Git commit pinned in the
workflow below. Any change requires review before updating the plan.

```bash
test "$(
  git ls-remote https://github.com/actions/checkout.git refs/tags/v6 |
    awk '{print $1}'
)" = "d23441a48e516b6c34aea4fa41551a30e30af803"

test "$(
  git ls-remote https://github.com/hashicorp/setup-terraform.git refs/tags/v4 |
    awk '{print $1}'
)" = "dfe3c3f87815947d99a8997f908cb6525fc44e9e"

test "$(
  git ls-remote https://github.com/aws-actions/configure-aws-credentials.git \
    'refs/tags/v5^{}' |
    awk '{print $1}'
)" = "61815dcd50bd041e203e49132bacad1fd04d2708"
```

No output means all three provenance checks passed.

- [ ] **Step 2: Create public-safe backend examples**

Create one example per future root:

```hcl
bucket = "replace-with-private-state-bucket"
region = "ap-northeast-2"
```

The production and edge keys remain tracked in their respective `backend.tf`
files.

- [ ] **Step 3: Create static Terraform CI**

Create `terraform-check.yml`:

```yaml
name: Terraform checks

on:
  pull_request:
  push:
    branches:
      - main

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - name: Check out repository
        uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803

      - name: Set up Terraform
        uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e
        with:
          terraform_version: 1.15.8
          terraform_wrapper: false

      - name: Check Terraform
        run: ./scripts/check-terraform.sh
```

It must not request an OIDC token or AWS credential.

- [ ] **Step 4: Create the manual production plan-only workflow**

Create `terraform-production-plan.yml`:

```yaml
name: Terraform production plan

on:
  workflow_dispatch:

permissions:
  id-token: write
  contents: read

jobs:
  plan:
    environment: production
    runs-on: ubuntu-latest
    env:
      AWS_REGION: ap-northeast-2
      TF_STATE_BUCKET: ${{ secrets.TF_STATE_BUCKET }}
    steps:
      - name: Check out repository
        uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803

      - name: Set up Terraform
        uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e
        with:
          terraform_version: 1.15.8
          terraform_wrapper: false

      - name: Configure short-lived AWS credentials
        uses: aws-actions/configure-aws-credentials@61815dcd50bd041e203e49132bacad1fd04d2708
        with:
          role-to-assume: ${{ secrets.AWS_ROLE_ARN }}
          aws-region: ${{ env.AWS_REGION }}
          mask-aws-account-id: true

      - name: Initialize production state
        run: |
          terraform -chdir=infra/terraform/production init \
            -input=false \
            -backend-config="bucket=${TF_STATE_BUCKET}" \
            -backend-config="region=${AWS_REGION}"

      - name: Detect production changes without publishing the plan
        shell: bash
        run: |
          set +e
          terraform -chdir=infra/terraform/production plan \
            -input=false \
            -detailed-exitcode \
            -out=production.tfplan \
            >"${RUNNER_TEMP}/production-plan.log" 2>&1
          plan_rc=$?
          set -e

          case "$plan_rc" in
            0)
              printf 'Terraform production plan: no changes\n'
              ;;
            2)
              printf 'Terraform production plan: changes detected; review locally\n'
              ;;
            *)
              printf 'Terraform production plan: failed; rerun locally\n' >&2
              exit 1
              ;;
          esac
```

Do not upload `production.tfplan` or the raw log.

On plan failure, instruct the operator to rerun locally for private diagnostics.
Do not print the raw error file because provider errors may contain identifiers.

- [ ] **Step 5: Add workflow contract checks**

Extend `scripts/check-terraform.sh` to fail unless:

```bash
grep -Fq 'id-token: write' \
  "$repo_root/.github/workflows/terraform-production-plan.yml"
grep -Fq 'mask-aws-account-id: true' \
  "$repo_root/.github/workflows/terraform-production-plan.yml"

if grep -Fq 'terraform apply' \
  "$repo_root/.github/workflows/terraform-production-plan.yml"; then
  printf 'production workflow must remain plan-only\n' >&2
  exit 1
fi

if grep -Eq 'uses: [^@]+@(v[0-9]+|main|master)$' \
  "$repo_root/.github/workflows/"terraform-*.yml; then
  printf 'Terraform workflows must pin actions by full commit SHA\n' >&2
  exit 1
fi
```

- [ ] **Step 6: Verify locally**

```bash
bash -n scripts/check-terraform.sh
./scripts/check-terraform.sh
git diff --check
```

Expected: static checks pass and the workflow contract rejects any apply command
or floating action tag.

- [ ] **Step 7: Commit the automation**

```bash
git add \
  .github/workflows/terraform-check.yml \
  .github/workflows/terraform-production-plan.yml \
  infra/terraform/production/backend.example.hcl \
  infra/terraform/edge/backend.example.hcl \
  scripts/check-terraform.sh
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "ci: add Terraform plan-only checks"
```

### Task 8: Human GitHub Environment And OIDC Verification

**Files:**

- External: GitHub `production` and `edge` environment settings
- Private:
  `.superpowers/archive/2026-07-26-terraform-production-launch/operator-log.md`
- No tracked file changes

**Interfaces:**

- Consumes: private role ARNs, state bucket name, protected environments, and
  the committed workflows
- Produces: verified short-lived OIDC sessions with no stored AWS access key

- [ ] **Step 1: Configure the production environment**

Human creates or reviews the `production` environment:

- required human approval before jobs run;
- deployment branches restricted to `main`;
- protected secret `AWS_ROLE_ARN`;
- protected secret `TF_STATE_BUCKET`; and
- no AWS access key ID or secret access key.

- [ ] **Step 2: Configure the edge environment**

Human creates or reviews the `edge` environment:

- deployment branches restricted to `main`;
- protected secret `AWS_ROLE_ARN`;
- protected secret `TF_STATE_BUCKET`; and
- no general production secrets.

The edge role remains state-only until Slice 5.

- [ ] **Step 3: Publish only when the human authorizes it**

The repository rule forbids autonomous pushes. The human reviews the local
commits and decides when to push them so GitHub can run the workflows.

- [ ] **Step 4: Run static CI**

Expected:

- all three roots initialize without backend access;
- format, validate, and Terraform tests pass; and
- no AWS credential is requested.

- [ ] **Step 5: Manually dispatch the production plan**

Human approves the protected environment run.

Expected public-safe result:

```text
Terraform production plan: no changes
```

or:

```text
Terraform production plan: changes detected; review locally
```

No plan body, ARN, account ID, bucket name, or state content appears in logs.

- [ ] **Step 6: Verify the permission ceiling**

Confirm through a reviewed IAM policy inspection that:

- production can use only bootstrap/production state objects in Slice 1;
- edge can use only edge state objects in Slice 1;
- neither role has long-lived credentials; and
- edge has no EC2, RDS, IAM, Secrets Manager, or security-group mutation action.

Record pass/fail privately.

### Task 9: Complete Slice 1 Documentation And Handoff

**Files:**

- Modify: `docs/architecture.md`
- Modify:
  `docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md`
- Modify:
  `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
- Modify: `docs/superpowers/README.md`
- Create:
  `docs/superpowers/archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-verification.md`

**Interfaces:**

- Consumes: all Slice 1 verification evidence
- Produces: sanitized architecture, completion evidence, and the Slice 2 planning
  handoff

- [ ] **Step 1: Update implemented architecture**

Document:

- the three root ownership boundaries;
- local bootstrap followed by remote S3 state;
- native lock files;
- Identity Center administrator access;
- OIDC production and edge trust boundaries;
- initially state-only automation roles; and
- production plan-only CI.

Do not describe future VPC/RDS/EC2 resources as implemented.

- [ ] **Step 2: Update the human checkpoint**

Mark Slice 1 human inputs/checkpoints complete only when their private evidence
exists. Preserve the user's completed signup-code and Anthropic-key OF notes.

- [ ] **Step 3: Write sanitized verification evidence**

Record only:

- Terraform and provider versions;
- commands run and exit statuses;
- resource categories, not identifiers;
- plan action counts;
- lock contention result;
- recovery rehearsal result;
- workflow names and safe result states; and
- the exact implementation commit SHA.

- [ ] **Step 4: Run the complete local gate**

```bash
./scripts/check-terraform.sh
git diff --check
gitleaks git --redact --no-banner --log-opts="-1"
```

Manually inspect the complete tracked diff for identifiers, credentials, private
topology, or raw plan output.

- [ ] **Step 5: Verify Slice 1 completion line by line**

Re-read the Slice Completion Contract at the top of this plan. Stop if any item
cannot be tied to fresh evidence.

- [ ] **Step 6: Archive Slice 1 and activate Slice 2 planning**

Move this completed plan and its verification into a dated archive directory.
Update the roadmap and Superpowers index so only the Terraform architecture,
human launch spec, roadmap, and next active slice remain in Active Work.

- [ ] **Step 7: Commit the completion record**

```bash
git add \
  docs/architecture.md \
  docs/superpowers/README.md \
  docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md \
  docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md \
  docs/superpowers/archive
git diff --cached --check
gitleaks git --staged --redact --no-banner
git commit -m "docs: complete Terraform infrastructure slice 1"
```

## Stop Conditions

Stop immediately when:

- the architecture authority remains contradictory;
- `jobcron-admin` resolves to the wrong account, role, or Region;
- a plan contains replacement, destruction, or an existing application
  resource;
- the OIDC provider would be duplicated;
- a raw identifier, state value, or plan body would enter tracked files or
  public logs;
- the edge role gains a non-state AWS action;
- the production workflow contains an apply path;
- state migration does not end in a no-change plan;
- lock contention does not block the second writer; or
- state version recovery cannot be demonstrated without touching live state.

## Rollback And Recovery

Before bootstrap apply, rollback is `git revert` of tracked implementation
commits; no AWS state exists.

After bootstrap apply but before state migration, keep local state protected.
Create a reviewed destroy plan only if the human abandons Terraform-first
deployment. Never delete resources manually and then discard state.

After state migration, S3 state is authoritative. Recover from a known object
version and reconcile with a reviewed no-change plan. Never reconstruct state
through a blind apply.

If OIDC trust is wrong, disable the affected GitHub environment, detach its
state policy, fix and review the trust document locally, then apply an exact
saved bootstrap plan through `jobcron-admin`.

If a workflow exposes an identifier or plan body, cancel the run, treat the log
as published, remove the output path, and assess whether any exposed value must
be rotated or renamed before rerunning.

## Authoritative References

- [Terraform S3 backend and native lock files](https://developer.hashicorp.com/terraform/language/backend/s3)
- [Terraform saved-plan workflow](https://developer.hashicorp.com/terraform/tutorials/cli/plan)
- [AWS CLI IAM Identity Center configuration](https://docs.aws.amazon.com/cli/latest/reference/configure/sso.html)
- [AWS CLI IAM Identity Center login](https://docs.aws.amazon.com/cli/latest/reference/sso/login.html)
- [AWS STS caller identity](https://docs.aws.amazon.com/cli/latest/reference/sts/get-caller-identity.html)
- [GitHub Actions OIDC for AWS](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws)
- [Pinning GitHub Actions to full commit SHAs](https://docs.github.com/en/actions/how-tos/administering-github-actions/managing-custom-actions)

[roadmap]: 260726-terraform-first-production-launch-roadmap.md
