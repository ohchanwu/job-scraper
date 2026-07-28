# Terraform Slice 3 Private Database And Secret Containers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the private PostgreSQL tier, an empty runtime-secret container,
and a protected recovery bucket without changing public traffic or exposing a
private value.

**Architecture:** Extend the adopted canonical VPC with two `/24` database
subnets in distinct Availability Zones, a route table containing only its
implicit VPC-local route, and an application-to-database security-group edge on
TCP 5432. A creation-only production saved plan then creates PostgreSQL 18.4,
an empty Secrets Manager container, and an encrypted/versioned/TLS-only recovery
bucket; a separate bootstrap saved plan grants the protected GitHub workflow
read-only refresh access to those resource types.

**Tech Stack:** Terraform 1.15.8, AWS provider 6.33.0, Bash, `jq`, Python 3
standard-library `ipaddress`, AWS CLI, GitHub Actions OIDC.

## Global Constraints

- Start from the exact Mayor baseline `950a523`.
- The governing authority is the
  [Pre-Batch-1 Window 1 authorization contract][window-1-contract]. This plan
  does not expand that authority.
- Slice 2 must remain at its exit checkpoint before any Slice 3 state change.
- Use `PATH=/opt/homebrew/bin:$PATH` and short-lived
  `AWS_PROFILE=jobcron-admin` credentials for local AWS work.
- Treat tracked files, chat, mail, screenshots, and workflow logs as public.
  Never publish account IDs, ARNs, resource IDs, IP addresses, CIDRs, endpoints,
  resource names, plan bodies, state, secret values, or private artifact
  digests.
- Store inventory, Terraform inputs, saved plans, plan JSON, digests, pricing
  evidence, state-version evidence, and reviewer packets only below the ignored
  `.superpowers/sdd/260728-terraform-slice-3-private-database-secret-containers-implementation/`
  workspace or in ignored `*.tfvars.json`, `*.tfplan`, and `*.backend.hcl`
  files.
- The production GitHub workflow remains plan-only. Do not grant it apply,
  create, update, delete, pass-role, or secret-value permissions.
- Use RDS-managed master credentials. Terraform must declare no
  `aws_secretsmanager_secret_version` and must never read the managed password.
- Do not create a NAT gateway, NAT instance, network interface, EC2 instance,
  EIP, EIP association, internet route, Cloudflare resource, DNS resource, or
  public ingress rule.
- Do not alter the adopted VPC, internet gateway, four public subnets, public
  route table/default route, unattached EIP, old EC2, or current RDS.
- Every saved plan must contain zero update, delete, and replacement actions.
- Regenerate both saved plans after any input, code, state, inventory, or
  credential change. A regenerated digest requires a new independent review.
- Apply only the independently reviewed saved-plan files. Never apply an
  unsaved plan or rerun `terraform apply` without the reviewed filename.
- The whole-launch ceilings are USD 100 recurring per month and USD 200
  aggregate one-time cost. Unknown cost uses its documented worst-case bound;
  no defensible bound is a stop condition.
- Use TDD for every tracked behavior change. Each tracked task records its RED
  and GREEN commands before its commit.
- Before every documentation commit, run Gitleaks, the public-repository
  redactor, link checks, and a manual staged-diff publication review.
- Do not push, create a PR/MR, submit the merge queue, or run `gt done` while
  executing this plan unless the human gives a newer explicit instruction.

## Operational Outcome

After this slice:

- the replacement database has private network placement ready before any
  application host is replaced;
- PostgreSQL accepts TCP 5432 only from the future origin/application security
  group;
- PostgreSQL requires TLS and has encryption, seven-day backups, deletion
  protection, and Terraform `prevent_destroy`;
- RDS owns the master password in its own managed secret;
- Terraform owns one empty application runtime-secret container but no value;
- Terraform owns one private, encrypted, versioned, TLS-only recovery bucket;
  verified current versions expire after 14 days, every current version expires
  after 90 days, and the resulting noncurrent data version expires one day
  later; and
- the old EC2, old RDS, unattached reserved EIP, DNS, and Cloudflare remain
  unchanged for rollback.

Plain-language concepts:

- A **DB subnet group** tells RDS which private subnets it may use.
- A **parameter group** is database-engine configuration; `rds.force_ssl = 1`
  rejects non-TLS PostgreSQL sessions.
- A **source security-group rule** trusts workloads carrying another security
  group instead of trusting an IP address.
- An **RDS-managed master password** is generated and rotated by RDS in a
  service-managed secret; Terraform sees only metadata, never the password.
- A **saved plan** is an immutable Terraform action file. Its SHA-256 digest
  binds review to the exact bytes later applied.

## File Map

- Create `scripts/select-terraform-slice3-cidrs.py`: deterministically select
  two non-overlapping `/24` networks and two distinct enabled AZs without
  printing them.
- Create `scripts/select-terraform-slice3-cidrs_test.py`: standard-library
  unit checks for selection, ambiguity, capacity, and private-output behavior.
- Modify `infra/terraform/bootstrap/identity.tf`: add one refresh-only Slice 3
  policy and one attachment.
- Modify `infra/terraform/bootstrap/tests/identity.tftest.hcl`: lock the exact
  read-only IAM ceiling.
- Modify `infra/terraform/production/variables.tf`: add one sensitive
  `private_database_config` object.
- Create `infra/terraform/production/database.tf`: private subnets, local-only
  routing, security groups, DB subnet/parameter groups, and RDS.
- Create `infra/terraform/production/secrets.tf`: empty runtime-secret
  container only.
- Create `infra/terraform/production/recovery.tf`: protected recovery bucket
  and its security and retention controls.
- Create `infra/terraform/production/tests/database.tftest.hcl`: network,
  security-group, PostgreSQL, TLS, backup, and destruction-protection tests.
- Create `infra/terraform/production/tests/secrets.tftest.hcl`: secret
  container and no-version tests.
- Create `infra/terraform/production/tests/recovery.tftest.hcl`: bucket
  encryption, versioning, public-block, TLS-only, and lifecycle tests.
- Modify `scripts/check-terraform-plan.sh`: add exact `slice3-bootstrap` and
  `slice3-production` saved-plan contracts.
- Modify `scripts/check-terraform-plan_test.sh`: add positive and adversarial
  fixtures for both contracts.
- Modify `scripts/check-terraform.sh`: require the Slice 3 resources and reject
  forbidden resources, secret versions, routes, broad IAM, and unsafe RDS/S3
  settings.
- Modify `scripts/check-terraform-workflows_test.sh`: lock the private input
  mapping and non-printing behavior.
- Modify `.github/workflows/terraform-production-plan.yml`: map the protected
  private configuration secret without publishing a plan.
- At slice closeout, modify `docs/architecture.md`,
  `deploy/production/README.md`, `deploy/production/HUMAN_DEPLOY_GUIDE.md`,
  `docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md`,
  `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`,
  and `docs/superpowers/README.md`.
- At slice closeout, archive this plan with a sanitized verification record in
  `docs/superpowers/archive/2026-07-28-terraform-slice-3/`.

## Dependency Graph

```text
Task 1 deterministic private selection
  |
  +--> Task 2 refresh-only bootstrap IAM
  |
  +--> Task 3 private DB network and PostgreSQL
  |      |
  |      +--> Task 4 empty secret and recovery bucket
  |                 |
  +-----------------+--> Task 5 exact plan/workflow policy
                             |
                             v
                    Task 6 private candidate and cost packet
                             |
                             v
                    Task 7 exact saved plans and review
                             |
                             v
                    Task 8 apply and recovery verification
                             |
                             v
                    Task 9 sanitized closeout
```

The tracked implementation tasks may be prepared before live discovery.
Tasks 6 through 8 are sequential and drift-sensitive.

## Exact State-Change Allow-Lists

### Bootstrap saved plan

Mode: `slice3-bootstrap`.

Exactly these addresses must have `actions = ["create"]`:

```text
aws_iam_policy.production_slice3_read
aws_iam_role_policy_attachment.production_slice3_read
```

Every other resource change must be `["no-op"]`. The plan must contain no
import metadata.

### Production saved plan

Mode: `slice3-production`.

Exactly these addresses must have `actions = ["create"]`:

```text
aws_subnet.database["database_a"]
aws_subnet.database["database_b"]
aws_route_table.database
aws_route_table_association.database["database_a"]
aws_route_table_association.database["database_b"]
aws_security_group.origin
aws_security_group.database
aws_vpc_security_group_ingress_rule.database_postgresql_from_origin
aws_db_subnet_group.production
aws_db_parameter_group.production
aws_db_instance.production
aws_secretsmanager_secret.runtime
aws_s3_bucket.recovery
aws_s3_bucket_public_access_block.recovery
aws_s3_bucket_versioning.recovery
aws_s3_bucket_server_side_encryption_configuration.recovery
aws_s3_bucket_policy.recovery
aws_s3_bucket_lifecycle_configuration.recovery
```

Every adopted Slice 2 address must be `["no-op"]`. No address may carry import
metadata. The checker rejects all other addresses, including every
`aws_route`, `aws_nat_gateway`, `aws_instance`, `aws_eip`,
`aws_eip_association`, `aws_secretsmanager_secret_version`, Cloudflare, and DNS
address.

The fixed non-private semantic tag on `aws_security_group.origin` is:

```hcl
tags = {
  "jobcron:edge-target" = "origin-security-group"
}
```

This plan deliberately narrows the foundation specification's older
"tagged VPC and origin security group" wording to the origin security group
only. Window 1 forbids updating the adopted canonical VPC. Slice 5 discovers
the unique origin security group by the exact tag above and derives the
canonical VPC from that group's `vpc_id`, so it needs no copied resource ID and
no VPC tag mutation.

## Controller Setup

Before Task 1, create the ignored workspace without placing a private value on
stdout:

```bash
export PATH=/opt/homebrew/bin:$PATH
export AWS_PROFILE=jobcron-admin
export AWS_REGION=ap-northeast-2
export SDD_WORKSPACE="$PWD/.superpowers/sdd/260728-terraform-slice-3-private-database-secret-containers-implementation"
mkdir -p "$SDD_WORKSPACE"/{inventory,plans,evidence,review,cost}
git check-ignore "$SDD_WORKSPACE"
```

Expected safe output: the ignored workspace path only.

### Task 1: Deterministic Private Subnet Selection

**Files:**

- Create: `scripts/select-terraform-slice3-cidrs.py`
- Create: `scripts/select-terraform-slice3-cidrs_test.py`

**Interfaces:**

- Consumes:
  `select-terraform-slice3-cidrs.py INPUT_JSON OUTPUT_JSON`
- Input JSON:

  - `vpc_cidr`: one strict IPv4 CIDR string;
  - `occupied_subnet_cidrs`: an array of strict IPv4 CIDR strings; and
  - `eligible_availability_zones`: an array of enabled AZ-name strings.

- Produces private JSON with exactly `database_a` and `database_b`, each holding
  `availability_zone` and `cidr_block`.
- Prints only `Slice 3 private subnet selection written` on success.

- [x] **Step 1: Write failing selector tests**

Use `unittest` with documentation-only RFC 5737-style fixtures. Assert:

1. occupied networks are excluded;
2. candidates sort by numeric network address;
3. AZs sort lexically and the first two distinct enabled AZs are used;
4. the output keys are exactly `database_a` and `database_b`;
5. the two selected networks are `/24`, are inside the VPC, and do not overlap;
6. fewer than two free `/24` networks fails;
7. fewer than two distinct AZs fails;
8. a non-IPv4 or non-canonical CIDR fails;
9. success stdout contains no CIDR or AZ; and
10. failure output is generic and contains no input value.

Run:

```bash
python3 scripts/select-terraform-slice3-cidrs_test.py
```

Expected: FAIL because the selector does not exist.

- [x] **Step 2: Implement the minimum selector**

Use only `argparse`, `ipaddress`, `json`, `os`, `pathlib`, and `tempfile`.
Implement this exact algorithm:

```text
parse the VPC as strict IPv4
require VPC prefix length <= 24
parse every occupied subnet as strict IPv4 inside the VPC
enumerate vpc.subnets(new_prefix=24) in numeric order
discard every candidate overlapping an occupied subnet
sort and deduplicate non-empty AZ strings
require at least two candidates and two AZs
map the first candidate/AZ to database_a and the second to database_b
write mode 0600 through a same-directory temporary file and os.replace
print only the fixed success sentence
```

Do not add a fallback prefix. Lack of two free `/24` networks is an ambiguity
and capacity stop, not permission to silently create smaller subnets. A `/24`
provides 251 AWS-usable IPv4 addresses per AZ, leaving maintenance and
replacement headroom without adding cost.

- [x] **Step 3: Run GREEN verification**

```bash
python3 scripts/select-terraform-slice3-cidrs_test.py
git diff --check
```

Expected: all selector tests pass and no whitespace errors appear.

- [x] **Step 4: Commit**

```bash
git add scripts/select-terraform-slice3-cidrs.py \
  scripts/select-terraform-slice3-cidrs_test.py
git commit -m "test: make Slice 3 CIDR selection deterministic"
```

### Task 2: Add Refresh-Only GitHub OIDC Access

**Files:**

- Modify: `infra/terraform/bootstrap/identity.tf`
- Modify: `infra/terraform/bootstrap/tests/identity.tftest.hcl`
- Modify: `scripts/check-terraform.sh`

**Interfaces:**

- Produces `data.aws_iam_policy_document.production_slice3_read`,
  `aws_iam_policy.production_slice3_read`, and
  `aws_iam_role_policy_attachment.production_slice3_read`.
- The policy attaches to `aws_iam_role.production` and contains only read
  actions needed by a plan-only refresh.

- [x] **Step 1: Write the failing IAM assertions**

Add a Terraform test that compares the statement action sets exactly:

```hcl
[
  "ec2:DescribeSecurityGroupRules",
  "rds:DescribeDBEngineVersions",
  "rds:DescribeDBInstances",
  "rds:DescribeDBParameterGroups",
  "rds:DescribeDBParameters",
  "rds:DescribeDBSubnetGroups",
  "rds:DescribeOrderableDBInstanceOptions",
  "rds:ListTagsForResource",
  "s3:GetAccelerateConfiguration",
  "s3:GetBucketAcl",
  "s3:GetBucketCORS",
  "s3:GetBucketLocation",
  "s3:GetBucketLogging",
  "s3:GetBucketObjectLockConfiguration",
  "s3:GetBucketOwnershipControls",
  "s3:GetBucketPolicy",
  "s3:GetBucketPolicyStatus",
  "s3:GetBucketPublicAccessBlock",
  "s3:GetBucketRequestPayment",
  "s3:GetBucketTagging",
  "s3:GetBucketVersioning",
  "s3:GetBucketWebsite",
  "s3:GetEncryptionConfiguration",
  "s3:GetLifecycleConfiguration",
  "s3:GetReplicationConfiguration",
  "s3:ListBucket",
  "secretsmanager:DescribeSecret",
  "secretsmanager:GetResourcePolicy",
  "secretsmanager:ListSecretVersionIds",
]
```

Use `resources = ["*"]`: RDS list/describe calls do not support resource-level
scoping, and the future bucket/secret ARNs do not exist when the bootstrap
policy is planned. Assert separately that no action contains `Create`, `Put`,
`Update`, `Delete`, `Modify`, `Restore`, `Rotate`, `Replicate`, `PassRole`,
`GetSecretValue`, or `BatchGetSecretValue`.

Run:

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap test \
  -filter=tests/identity.tftest.hcl
```

Expected: FAIL because the policy does not exist.

- [x] **Step 2: Add the policy and attachment**

Add one policy document with the exact actions above, one managed policy named
`JobcronTerraformProductionSlice3Read`, and one attachment to the existing
production role. Add `prevent_destroy = true` to the managed policy.

Do not modify `production_network_read`; creation-only bootstrap policy is
reviewable without updating a working Slice 2 policy.

- [x] **Step 3: Lock the static ceiling**

Teach `scripts/check-terraform.sh` to compare the exact action multiset and
reject all write verbs and secret-value reads. Do not replace the existing
Slice 2 network ceiling.

- [x] **Step 4: Run GREEN verification**

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap test \
  -filter=tests/identity.tftest.hcl
CHECK_TERRAFORM_FIXTURE_MODE=1 ./scripts/check-terraform.sh
git diff --check
```

Expected: pass.

- [x] **Step 5: Commit**

```bash
git add infra/terraform/bootstrap/identity.tf \
  infra/terraform/bootstrap/tests/identity.tftest.hcl \
  scripts/check-terraform.sh
git commit -m "feat: add Slice 3 refresh-only plan access"
```

### Task 3: Declare The Private Database Network And PostgreSQL

**Files:**

- Modify: `infra/terraform/production/variables.tf`
- Create: `infra/terraform/production/database.tf`
- Create: `infra/terraform/production/tests/database.tftest.hcl`

**Interfaces:**

- Consumes sensitive `var.private_database_config`:

```hcl
object({
  private_subnets = map(object({
    availability_zone = string
    cidr_block        = string
  }))
  database_identifier       = string
  database_name             = string
  master_username           = string
  final_snapshot_identifier = string
  runtime_secret_name       = string
  recovery_bucket_name      = string
})
```

- Produces the first eleven production addresses in the production allow-list.
- Later tasks consume `aws_security_group.origin.id`,
  `aws_db_instance.production.endpoint`,
  `aws_secretsmanager_secret.runtime.arn`, and
  `aws_s3_bucket.recovery.id`; none is a tracked output.

- [x] **Step 1: Write failing variable and network tests**

Use `mock_provider "aws" {}` and a documentation-only
`private_database_config`. Assert:

- subnet keys equal `database_a` and `database_b`;
- AZs and CIDRs are distinct;
- both subnets use `aws_vpc.canonical.id`;
- both have `map_public_ip_on_launch = false`;
- the database route table uses the canonical VPC;
- exactly two explicit associations bind the two database subnets;
- `database.tf` contains no `aws_route`;
- origin and database security groups use the canonical VPC;
- the only database ingress rule has `from_port = 5432`,
  `to_port = 5432`, `ip_protocol = "tcp"`, and
  `referenced_security_group_id = aws_security_group.origin.id`; and
- neither security group declares public ingress;
- the origin group's entire tag map equals
  `jobcron:edge-target = origin-security-group`;
- the database group's tag map is empty; and
- the canonical VPC does not receive the discovery tag.

Run:

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/database.tftest.hcl
```

Expected: FAIL because the variable and resources do not exist.

- [x] **Step 2: Add the sensitive input boundary**

Add validation that:

```hcl
toset(keys(var.private_database_config.private_subnets)) ==
toset(["database_a", "database_b"])
```

Also require two distinct non-empty AZs, two distinct CIDRs, non-empty private
names, `database_name` matching `^[a-z][a-z0-9_]*$`, and `master_username`
matching PostgreSQL's identifier form. Error messages must not interpolate a
private value.

- [x] **Step 3: Add private networking and security groups**

Implement:

```hcl
resource "aws_subnet" "database" {
  for_each = toset(["database_a", "database_b"])

  vpc_id                  = aws_vpc.canonical.id
  availability_zone       = var.private_database_config.private_subnets[each.key].availability_zone
  cidr_block              = var.private_database_config.private_subnets[each.key].cidr_block
  map_public_ip_on_launch = false

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route_table" "database" {
  vpc_id = aws_vpc.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route_table_association" "database" {
  for_each = aws_subnet.database

  subnet_id      = each.value.id
  route_table_id = aws_route_table.database.id
}
```

Do not declare a route block or `aws_route`; AWS supplies only the immutable
VPC-local route.

Create `aws_security_group.origin` with no ingress and its normal outbound
egress, `aws_security_group.database` with no inline ingress, and exactly one
`aws_vpc_security_group_ingress_rule.database_postgresql_from_origin`.
Add `prevent_destroy` to both security groups and the ingress rule. Add exactly
one tag to the origin group:

```hcl
tags = {
  "jobcron:edge-target" = "origin-security-group"
}
```

Do not tag or update `aws_vpc.canonical`. The Step 1 tests and
`scripts/check-terraform.sh` in Task 5 require that key/value exactly once,
reject it on the VPC or any other resource, and reject any additional origin
tag. This gives Slice 5 one deterministic SG-only discovery selector.

- [x] **Step 4: Write failing PostgreSQL assertions**

Assert exact settings:

```text
engine = postgres
engine_version = 18.4
instance_class = db.t4g.micro
allocated_storage = 20
storage_type = gp3
storage_encrypted = true
multi_az = false
publicly_accessible = false
port = 5432
backup_retention_period = 7
backup_window = 18:00-18:30
maintenance_window = sun:19:00-sun:19:30
auto_minor_version_upgrade = true
deletion_protection = true
manage_master_user_password = true
copy_tags_to_snapshot = true
skip_final_snapshot = false
```

Assert the DB subnet group contains exactly the two private subnet IDs, the DB
uses only the database security group, the parameter-group family is
`postgres18`, and its sole parameter is `rds.force_ssl = 1`.
Assert `aws_db_instance.production` has `prevent_destroy = true`.

- [x] **Step 5: Add the subnet group, parameter group, and RDS**

Use `var.private_database_config` for the identifier, database name, master
username, and final snapshot identifier. Set:

```hcl
parameter {
  name         = "rds.force_ssl"
  value        = "1"
  apply_method = "immediate"
}
```

Do not add a password, master-user-secret data source, public output,
`password` variable, or secret-version resource.

- [x] **Step 6: Run GREEN verification**

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production fmt -check -recursive
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/database.tftest.hcl
git diff --check
```

Expected: pass.

- [x] **Step 7: Commit**

```bash
git add infra/terraform/production/variables.tf \
  infra/terraform/production/database.tf \
  infra/terraform/production/tests/database.tftest.hcl
git commit -m "feat: declare the private PostgreSQL tier"
```

### Task 4: Add The Empty Runtime Secret And Recovery Bucket

**Files:**

- Create: `infra/terraform/production/secrets.tf`
- Create: `infra/terraform/production/recovery.tf`
- Create: `infra/terraform/production/tests/secrets.tftest.hcl`
- Create: `infra/terraform/production/tests/recovery.tftest.hcl`

**Interfaces:**

- Consumes private secret and bucket names from
  `var.private_database_config`.
- Produces the final seven production allow-list addresses.
- Produces no secret version and no Terraform output.

- [x] **Step 1: Write failing secret tests**

Assert `aws_secretsmanager_secret.runtime` uses the private name, has a
30-day recovery window, and has `prevent_destroy = true`. Scan all production
HCL and fail if `aws_secretsmanager_secret_version`, `secret_string`,
`secret_binary`, `GetSecretValue`, or a secret data source appears.

Run:

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/secrets.tftest.hcl
```

Expected: FAIL.

- [x] **Step 2: Add only the secret container**

```hcl
resource "aws_secretsmanager_secret" "runtime" {
  name                    = var.private_database_config.runtime_secret_name
  recovery_window_in_days = 30

  lifecycle {
    prevent_destroy = true
  }
}
```

No task in this slice writes a value. Slice 4 may put the lower-privilege,
TLS-required `DATABASE_URL` after it creates the host and SSM tunnel.

- [x] **Step 3: Write failing recovery-bucket tests**

Assert:

- the bucket name comes from the sensitive config;
- all four public-access-block booleans are true;
- versioning status is `Enabled`;
- default encryption is `AES256`;
- the policy denies `s3:*` on the bucket and objects when
  `aws:SecureTransport = false`;
- the lifecycle configuration has exactly two enabled rules:
  - `expire-verified-after-off-cloud-copy` filters on
    `macbook-copy = verified` and expires matching current versions after 14
    days; and
  - `expire-all-objects` has an empty all-object filter and expires every
    current version after 90 days;
- bucket, versioning, encryption, policy, and lifecycle resources have
  `prevent_destroy = true`; and
- each reviewed rule permanently expires a resulting noncurrent data version
  after one day; and
- no ACL, website, public policy allow, object, transition, additional
  lifecycle rule, `newer_noncurrent_versions`, or delete-marker rule is
  declared.

Run:

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/recovery.tftest.hcl
```

Expected: FAIL.

- [x] **Step 4: Implement the protected bucket**

Follow the existing bootstrap state-bucket pattern with:

```text
aws_s3_bucket.recovery
aws_s3_bucket_public_access_block.recovery
aws_s3_bucket_versioning.recovery
aws_s3_bucket_server_side_encryption_configuration.recovery
data.aws_iam_policy_document.recovery_bucket
aws_s3_bucket_policy.recovery
aws_s3_bucket_lifecycle_configuration.recovery
```

Implement the lifecycle resource exactly:

```hcl
resource "aws_s3_bucket_lifecycle_configuration" "recovery" {
  bucket = aws_s3_bucket.recovery.id

  rule {
    id     = "expire-verified-after-off-cloud-copy"
    status = "Enabled"

    filter {
      tag {
        key   = "macbook-copy"
        value = "verified"
      }
    }

    expiration {
      days = 14
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }

  rule {
    id     = "expire-all-objects"
    status = "Enabled"

    filter {}

    expiration {
      days = 90
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [aws_s3_bucket_versioning.recovery]
}
```

The two overlapping rules are intentional: a MacBook-verified current version
becomes eligible at day 14, while every current version becomes eligible at day
90. Under
[AWS's versioned-bucket expiration behavior](https://docs.aws.amazon.com/AmazonS3/latest/userguide/lifecycle-expire-general-considerations.html),
current-version expiration creates a delete marker and leaves the data as a
noncurrent version. Each rule therefore uses the provider's
[`noncurrent_version_expiration` block](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/s3_bucket_lifecycle_configuration)
to permanently expire that data one day later, the minimum S3-supported delay.
The bucket starts empty. Slice 4 owns archive upload behavior; do not add
objects, credentials, replication, transitions, or any other expiration rule.

- [x] **Step 5: Run GREEN verification**

```bash
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/secrets.tftest.hcl
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test \
  -filter=tests/recovery.tftest.hcl
git diff --check
```

Expected: pass.

- [x] **Step 6: Commit**

```bash
git add infra/terraform/production/secrets.tf \
  infra/terraform/production/recovery.tf \
  infra/terraform/production/tests/secrets.tftest.hcl \
  infra/terraform/production/tests/recovery.tftest.hcl
git commit -m "feat: add empty secret and protected recovery bucket"
```

### Task 5: Enforce Exact Plans And Private Workflow Inputs

**Files:**

- Modify: `scripts/check-terraform-plan.sh`
- Modify: `scripts/check-terraform-plan_test.sh`
- Modify: `scripts/check-terraform.sh`
- Modify: `scripts/check-terraform-workflows_test.sh`
- Modify: `.github/workflows/terraform-production-plan.yml`

**Interfaces:**

- `scripts/check-terraform-plan.sh slice3-bootstrap PLAN_JSON`
- `scripts/check-terraform-plan.sh slice3-production PLAN_JSON`
- Workflow maps protected secret `TF_VAR_PRIVATE_DATABASE_CONFIG` to
  `TF_VAR_private_database_config` exactly once.

- [ ] **Step 1: Write failing plan-checker fixtures**

Generate minimal JSON fixtures containing the exact allow-lists in this plan.
Require acceptance only when every allow-listed address is present once with
`["create"]`, all other changes are approved Slice 2 `["no-op"]` addresses,
and no import metadata exists.

Add one negative fixture for each:

```text
missing allow-listed address
extra address
duplicate address
no-op/update/delete/replace action on an allow-listed address
create/update/delete on an adopted address
import metadata
aws_route
aws_nat_gateway
aws_instance
aws_eip or aws_eip_association
aws_secretsmanager_secret_version
missing aws_s3_bucket_lifecycle_configuration.recovery
extra or differently addressed lifecycle configuration
Cloudflare or DNS resource
malformed JSON
unknown mode
```

Each failure must emit only:

```text
Terraform saved plan violates the Slice 3 contract
```

The error must not include an address, action, identifier, or plan value.

- [ ] **Step 2: Write failing workflow mutations**

Require this exact mapping once:

```yaml
TF_VAR_private_database_config: ${{ secrets.TF_VAR_PRIVATE_DATABASE_CONFIG }}
```

Mutations removing it, duplicating it, printing it, invoking `env`/`printenv`,
uploading a plan artifact, or adding `terraform apply` must fail with a generic
message.

Run:

```bash
./scripts/check-terraform-plan_test.sh
./scripts/check-terraform-workflows_test.sh
```

Expected: FAIL.

- [ ] **Step 3: Implement the two plan modes**

Reuse the current Bash/`jq` checker. Compare sorted `address + actions` tuples
to hard-coded public resource addresses. Keep the existing Slice 2 modes
unchanged. Never print rejected JSON.

On success print exactly:

```text
Slice 3 bootstrap plan contract verified
```

or:

```text
Slice 3 production plan contract verified
```

- [ ] **Step 4: Extend the static checker**

Require the exact resources and safety attributes from Tasks 2 through 4.
Reject forbidden Terraform resource types repo-wide. Reject every route in the
database route table except AWS's implicit local route by forbidding both inline
`route` blocks and `aws_route` additions in `database.tf`.

Require the origin group's entire tag map to equal exactly
`jobcron:edge-target = origin-security-group`; reject that discovery tag on the
canonical VPC or any other resource. Require
`aws_s3_bucket_lifecycle_configuration.recovery`, its dependency on enabled
versioning, `prevent_destroy`, and exactly the two reviewed rules:
tag-filtered `macbook-copy = verified` at 14 days and all current versions at
90 days. Require `noncurrent_days = 1` in each rule so versioning cannot retain
the expired data indefinitely. Reject transitions, additional expiration rules,
`newer_noncurrent_versions`, any other noncurrent delay, and delete-marker
expiration.

Require the workflow mapping once and forbid printing or artifact upload. The
workflow remains `workflow_dispatch`, OIDC, masked-account, plan-only.

- [ ] **Step 5: Run GREEN verification**

```bash
./scripts/check-terraform-plan_test.sh
./scripts/check-terraform-workflows_test.sh
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
git diff --check
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add scripts/check-terraform-plan.sh \
  scripts/check-terraform-plan_test.sh \
  scripts/check-terraform.sh \
  scripts/check-terraform-workflows_test.sh \
  .github/workflows/terraform-production-plan.yml
git commit -m "ci: enforce the Slice 3 creation-only contract"
```

### Task 6: Build The Private Candidate And Aggregate Cost Packet

**Files:**

- Create privately: `$SDD_WORKSPACE/inventory/*.json`
- Create privately: `$SDD_WORKSPACE/private-subnet-input.json`
- Create privately: `$SDD_WORKSPACE/private-subnets.json`
- Create privately: `$SDD_WORKSPACE/private-database-config.json`
- Create privately:
  `infra/terraform/production/private-database.auto.tfvars.json`
- Create privately: `$SDD_WORKSPACE/cost/slice3-cost-private.md`
- Tracked files: none

**Interfaces:**

- Consumes current authenticated AWS inventory and the completed Slice 2
  private cost record.
- Produces one deterministic private configuration and a value-blind cost
  verdict.

- [ ] **Step 1: Reauthenticate without printing identity**

```bash
aws sso login --profile jobcron-admin --no-browser
aws sts get-caller-identity --profile jobcron-admin \
  --query 'length(Account)' --output text >/dev/null
test "$(aws configure get region --profile jobcron-admin)" = "ap-northeast-2"
```

Stop if the expected account/role/region check cannot run without printing a
private value.

- [ ] **Step 2: Capture current inventory privately**

Write full JSON responses, never stdout, for:

```text
ec2 describe-availability-zones
ec2 describe-vpcs
ec2 describe-instances
ec2 describe-addresses
ec2 describe-subnets
ec2 describe-route-tables
ec2 describe-security-groups
rds describe-db-instances
rds describe-db-engine-versions for postgres 18.4
rds describe-account-attributes
service-quotas list-service-quotas for VPC and RDS
s3api list-buckets
secretsmanager list-secrets
```

Use `--profile jobcron-admin --region ap-northeast-2 --output json` on every
regional call and mode `0600` on every private file.

- [ ] **Step 3: Derive and select the CIDRs**

Privately derive the canonical VPC CIDR from the Terraform-managed VPC, all
occupied subnet CIDRs in that VPC, and all enabled AZ names. Require the
Terraform state binding and AWS inventory to agree before selection.

```bash
python3 scripts/select-terraform-slice3-cidrs.py \
  "$SDD_WORKSPACE/private-subnet-input.json" \
  "$SDD_WORKSPACE/private-subnets.json"
```

Expected safe output:

```text
Slice 3 private subnet selection written
```

Stop if selection is ambiguous, fewer than two `/24`s remain, the two AZs are
not distinct, or live inventory differs from the reviewed assumptions.

- [ ] **Step 4: Derive deterministic private resource names**

Capture the account ID in a shell variable without printing it. Hash the UTF-8
string `account_id + ":" + repository_name` with SHA-256, use the first twelve
lowercase hex characters as a private namespace suffix, and derive:

```text
database_identifier = "jobcron-production-" + namespace_suffix
final_snapshot_identifier = "jobcron-production-final-" + namespace_suffix
runtime_secret_name = "jobcron/production/runtime-" + namespace_suffix
recovery_bucket_name = "jobcron-recovery-" + namespace_suffix
database_name = jobcron
master_username = jobcron_admin
```

Merge these values with `private-subnets.json` into
`private-database-config.json`, then wrap it as
`{"private_database_config": ...}` in the ignored auto tfvars file. Set mode
`0600` and verify both paths are ignored.

- [ ] **Step 5: Prove absence and collision checks**

Privately require that the derived DB identifier, secret name, and bucket name
do not already exist. Prove the PostgreSQL 18.4 offering is available, the
subnet/route-table/security-group/RDS quotas can carry the exact additions, and
fingerprint the old EC2, old RDS, and unattached-EIP rollback resources. Stop
instead of adopting, importing, renaming, replacing a collision, or proceeding
with insufficient capacity.

- [ ] **Step 6: Calculate the aggregate cost gate**

Record the pricing source and date, quantities, recurring upper bound, one-time
upper bound, and cumulative launch total. Use 744 hours/month and include:

```text
1 Single-AZ db.t4g.micro PostgreSQL instance
20 GiB-month gp3 RDS storage
7-day automated backup worst-case above the free allocation
2 Secrets Manager secrets: the RDS-managed master secret and the empty runtime
container
recovery-bucket storage and requests at a conservative first-month bound,
including 14-day verified and 90-day all-current-version retention plus
one-day noncurrent data-version expiration
the already approved unattached public IPv4
the planned Slice 4 compute/storage/registry bounds already in the launch packet
Cloudflare and all earlier approved launch costs
```

The subnets, route table, associations, security groups, DB subnet group, and
parameter group have no hourly charge. Empty S3 and Secrets Manager containers
still receive their documented service minimum/worst-case bounds.

Write only this value-blind verdict outside the private packet:

```text
Aggregate recurring ceiling: PASS
Aggregate one-time ceiling: PASS
Pricing source and date: recorded privately
```

Stop if the total may exceed USD 100/month or USD 200 one-time, or if any
component lacks a defensible upper bound.

- [ ] **Step 7: Persist a value-blind checkpoint**

Record on the task bead only that discovery is unambiguous, `/24` capacity and
distinct-AZ gates pass, collision checks pass, and aggregate cost gates pass.
Do not attach the files or values.

### Task 7: Create Exact Saved Plans And Independent Review

**Files:**

- Create privately:
  `infra/terraform/bootstrap/jobcron.backend.hcl`
- Create privately:
  `infra/terraform/production/jobcron.backend.hcl`
- Create privately: `$SDD_WORKSPACE/plans/slice3-bootstrap.tfplan`
- Create privately: `$SDD_WORKSPACE/plans/slice3-bootstrap.json`
- Create privately: `$SDD_WORKSPACE/plans/slice3-production.tfplan`
- Create privately: `$SDD_WORKSPACE/plans/slice3-production.json`
- Create privately: `$SDD_WORKSPACE/review/controller-private.md`
- Create privately: `$SDD_WORKSPACE/review/reviewer-private.md`
- Tracked files: none

**Interfaces:**

- Consumes the exact code commit, current state, private config, cost packet,
  and current credentials.
- Produces two digested saved plans and one independent `APPROVED` verdict.

- [ ] **Step 1: Initialize both protected backends**

Copy the private bootstrap backend configuration for production. Run
`terraform init -reconfigure` in both roots without printing backend values.

- [ ] **Step 2: Save and check the bootstrap plan**

```bash
terraform -chdir=infra/terraform/bootstrap plan \
  -input=false -out="$SDD_WORKSPACE/plans/slice3-bootstrap.tfplan" \
  >"$SDD_WORKSPACE/plans/slice3-bootstrap.log" 2>&1
terraform -chdir=infra/terraform/bootstrap show -json \
  "$SDD_WORKSPACE/plans/slice3-bootstrap.tfplan" \
  >"$SDD_WORKSPACE/plans/slice3-bootstrap.json"
scripts/check-terraform-plan.sh slice3-bootstrap \
  "$SDD_WORKSPACE/plans/slice3-bootstrap.json"
```

Expected safe output:

```text
Slice 3 bootstrap plan contract verified
```

- [ ] **Step 3: Apply only the bootstrap saved plan**

The production plan workflow cannot refresh Slice 3 resources until the
read-only policy exists. First record the saved-plan SHA-256 privately, confirm
the state serial and code commit still match the review packet, obtain an
independent approval of that exact digest, then run:

```bash
terraform -chdir=infra/terraform/bootstrap apply \
  -input=false "$SDD_WORKSPACE/plans/slice3-bootstrap.tfplan"
```

This apply may create only the two bootstrap allow-list addresses. Stop on any
drift, changed digest, or reviewer verdict other than `APPROVED`.

- [ ] **Step 4: Save and check the production plan**

Re-run live inventory, CIDR, collision, credentials, and aggregate cost gates.
Then:

```bash
terraform -chdir=infra/terraform/production plan \
  -input=false -out="$SDD_WORKSPACE/plans/slice3-production.tfplan" \
  >"$SDD_WORKSPACE/plans/slice3-production.log" 2>&1
terraform -chdir=infra/terraform/production show -json \
  "$SDD_WORKSPACE/plans/slice3-production.tfplan" \
  >"$SDD_WORKSPACE/plans/slice3-production.json"
scripts/check-terraform-plan.sh slice3-production \
  "$SDD_WORKSPACE/plans/slice3-production.json"
```

Expected safe output:

```text
Slice 3 production plan contract verified
```

- [ ] **Step 5: Build the private controller report**

Record:

- exact code commit and clean tracked diff;
- saved-plan SHA-256 digests;
- backend state serial/lineage bindings;
- exact address/action counts;
- zero update/delete/replace/import counts;
- value-blind current-account/role/region result;
- private CIDR/AZ-selection evidence and collision results;
- aggregate cost calculation;
- private old EC2/current RDS/EIP/network fingerprints;
- absence of NAT, routes beyond local, secret versions, EC2, EIP association,
  Cloudflare, and DNS;
- publication/secret-scan results; and
- recovery steps if either apply stops.

- [ ] **Step 6: Obtain independent exact-digest review**

A reviewer other than the implementer must inspect the raw saved plan, plan
JSON, private input, cost packet, state binding, tracked diff, fingerprints,
and checker output. The reviewer writes `APPROVED` or `REJECTED` plus the exact
reviewed digests to the ignored reviewer packet.

Do not send private digests through chat or mail. Mail may contain only the
value-blind verdict and the local private packet path.

- [ ] **Step 7: Enforce the final production policy gate**

The value-blind summary must be exactly:

```text
Bootstrap: 2 creates, 0 updates, 0 replacements, 0 destroys
Production: 18 creates, 0 updates, 0 replacements, 0 destroys
Imports: none
NAT/routes beyond local/secret versions/EC2/EIP association/Cloudflare/DNS: none
Old EC2/current RDS/unattached EIP/adopted public network changes: none
Aggregate recurring and one-time ceilings: PASS
Plan digests and state bindings: recorded privately
Independent review: APPROVED
```

The production count is 18 because the seven S3/secret addresses follow the
eleven database/network addresses listed above.

### Task 8: Apply And Prove Recovery

**Files:**

- Create privately: `$SDD_WORKSPACE/evidence/post-apply-*.json`
- Create privately: `$SDD_WORKSPACE/evidence/state-recovery-private.md`
- Create privately: `$SDD_WORKSPACE/evidence/slice3-private-verdict.md`
- Tracked files: none

**Interfaces:**

- Consumes the approved production saved plan.
- Produces value-blind exit evidence and private recovery identifiers.

- [ ] **Step 1: Revalidate immediately before apply**

Require unchanged code commit, plan digest, state serial, credentials,
inventory fingerprints, CIDR availability, collisions, and cost verdict.
Re-run the production plan checker. Any difference invalidates review and
requires a regenerated plan and new reviewer approval.

- [ ] **Step 2: Apply only the reviewed production plan**

```bash
terraform -chdir=infra/terraform/production apply \
  -input=false "$SDD_WORKSPACE/plans/slice3-production.tfplan"
```

Do not use `-auto-approve`; the saved-plan filename is the authorization
boundary. Do not target resources.

- [ ] **Step 3: Verify network and security privately**

Capture read-only AWS responses to files and assert without printing values:

- both database subnets exist in distinct AZs and have no public-IP mapping;
- both explicitly associate with the database route table;
- that route table contains exactly the VPC-local route and no internet/NAT
  route;
- the origin group has no ingress;
- the database group has exactly one TCP 5432 ingress rule whose source is the
  origin group; and
- there is no public CIDR ingress.

- [ ] **Step 4: Verify RDS and secret behavior privately**

Wait for RDS `available`, then assert:

- PostgreSQL engine version is 18.4;
- Single-AZ `db.t4g.micro`, 20 GiB gp3, encrypted;
- public accessibility is false;
- backup retention is seven days and deletion protection is true;
- the DB subnet group contains exactly the two private subnets;
- only the database security group is attached;
- the parameter group reports `rds.force_ssl = 1`;
- RDS reports managed master-secret metadata; and
- `secretsmanager list-secret-version-ids` for the runtime container returns
  zero versions.

Never call `GetSecretValue`.

- [ ] **Step 5: Verify recovery bucket privately**

Assert all public-access-block flags, `Enabled` versioning, AES256 default
encryption, and the TLS-deny policy. Assert the enabled
`expire-verified-after-off-cloud-copy` rule filters on
`macbook-copy = verified` and expires at day 14; assert the enabled
`expire-all-objects` rule applies to every current version at day 90. Assert
both rules permanently expire the resulting noncurrent data version one day
later. Confirm there are no other lifecycle rules and the bucket is empty. Do
not upload a test object in this creation-only slice.

- [ ] **Step 6: Prove Terraform and state recovery**

Run a refresh-only saved plan and require exit code 0:

```bash
terraform -chdir=infra/terraform/production plan \
  -refresh-only -input=false -detailed-exitcode \
  -out="$SDD_WORKSPACE/plans/slice3-post-apply-refresh.tfplan" \
  >"$SDD_WORKSPACE/plans/slice3-post-apply-refresh.log" 2>&1
```

Privately record:

- current state serial and lineage;
- current S3 state object version identifier;
- successful retrieval of that exact version to a mode-0600 private file;
- successful `terraform show -json` parse of the retrieved state;
- database identifier/ARN, managed-secret ARN, automated-backup restore
  metadata, subnet-group name, runtime-secret ARN, and recovery-bucket name;
  and
- confirmation that the old EC2/current RDS/unattached EIP fingerprints still
  match.

Do not restore over live state and do not create a DB snapshot. Retrieval and
parse prove the recovery path without adding an unallow-listed mutation.

- [ ] **Step 7: Write the private exit verdict**

Record only:

```text
Private network: PASS
PostgreSQL privacy/encryption/TLS/backups/protection: PASS
RDS-managed master password: PASS
Empty runtime secret: PASS
Recovery bucket controls: PASS
Refresh-only no-change plan: PASS
State-version retrieval and parse: PASS
Rollback resources unchanged: PASS
Private identifiers published: none
```

Any failure stops Slice 4.

### Task 9: Publish Sanitized Closeout Evidence

**Files:**

- Modify: `docs/architecture.md`
- Modify: `deploy/production/README.md`
- Modify: `deploy/production/HUMAN_DEPLOY_GUIDE.md`
- Modify:
  `docs/superpowers/specs/260726-terraform-first-production-launch-human-blocked-steps.md`
- Modify:
  `docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md`
- Modify: `docs/superpowers/README.md`
- Create:
  `docs/superpowers/archive/2026-07-28-terraform-slice-3/260728-terraform-slice-3-verification.md`
- Move this plan into the same archive directory.

**Interfaces:**

- Consumes the private Task 8 verdict.
- Produces sanitized durable architecture and activates Slice 4 planning.

- [ ] **Step 1: Update maintained documentation**

Document only the resource classes and security boundaries. Do not publish
names, IDs, CIDRs, AZs, endpoints, state versions, or plan digests. State that
the runtime secret is empty and Slice 4 owns its first value. Record the
14-day verified/90-day all-current-version recovery retention contract, with
permanent noncurrent data-version expiration one day later, and the
SG-only `jobcron:edge-target = origin-security-group` discovery contract.
Explicitly note that the canonical VPC remains untagged because Window 1
forbids updating the adopted VPC; Slice 5 derives the VPC from the tagged
security group's `vpc_id`.

- [ ] **Step 2: Close Slice 3 gates and activate Slice 4**

Mark only the Slice 3 authorization checklist items supported by Task 8.
Update the roadmap completion baseline, archive links, and Slice 4 status.

- [ ] **Step 3: Write sanitized verification**

Include commit range, test commands/pass counts, value-blind plan counts, exact
resource-address allow-list, recovery checks, rollback-resource equality, and
confirmation that no private value was published. Do not include private
digests.

- [ ] **Step 4: Run publication gates**

```bash
git diff --check
python3 - <<'PY'
from pathlib import Path
import re

for path in [
    Path("docs/superpowers/README.md"),
    Path("docs/superpowers/plans/260726-terraform-first-production-launch-roadmap.md"),
]:
    text = path.read_text()
    for target in re.findall(r"\[[^\]]+\]\(([^)#]+)", text):
        candidate = (path.parent / target).resolve()
        if not candidate.exists():
            raise SystemExit(f"missing local link target in {path}")
PY
"${HOME}/.agents/skills/gstack/bin/gstack-redact" \
  --from-file \
  docs/superpowers/archive/2026-07-28-terraform-slice-3/260728-terraform-slice-3-verification.md \
  --repo-visibility public --json
git add docs deploy
gitleaks git --staged --redact --no-banner
git diff --cached
```

Manually inspect the complete staged diff for credentials, personal data,
private topology, identifiers, endpoints, names, and operational evidence.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs: close Terraform infrastructure slice 3"
```

## Stop And Recovery Rules

Stop and return to the human if any Window 1 stop condition occurs, including:

- credentials are missing, expired, unexpected, or broader than intended;
- discovery is ambiguous or differs from the reviewed packet;
- two free `/24` networks or two distinct AZs are unavailable;
- a private name collides;
- an aggregate cost ceiling may be exceeded;
- a saved plan has any unexpected address/action/import;
- a digest, code commit, state serial, or inventory fingerprint changes after
  review;
- private data appears in public output;
- post-apply privacy, TLS, backup, secret, bucket, refresh, or recovery checks
  fail; or
- rollback resources change.

Before a production apply, recovery is to discard the invalid saved plan and
regenerate it. After a partial provider failure, do not rerun apply blindly:
pull and preserve the current versioned state, run refresh-only inspection,
compare actual resources to the allow-list, obtain a new independent review,
and either finish with a new creation-only saved plan or return to the human.
No destroy or replacement is authorized by this plan.

## Final Verification

Before declaring the implementation complete:

```bash
python3 scripts/select-terraform-slice3-cidrs_test.py
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap fmt -check -recursive
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap validate
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/bootstrap test
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production fmt -check -recursive
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production validate
PATH=/opt/homebrew/bin:$PATH \
  terraform -chdir=infra/terraform/production test
./scripts/check-terraform-plan_test.sh
./scripts/check-terraform-workflows_test.sh
AWS_PROFILE=jobcron-admin PATH=/opt/homebrew/bin:$PATH \
  ./scripts/check-terraform.sh
git diff --check
git status --short
```

Require the private Task 8 verdict, independent exact-digest approval, clean
tracked tree, and no unreviewed commit before Slice 4 begins.

[window-1-contract]:
  ../specs/260728-pre-batch-1-window-1-authorization-contract.md
