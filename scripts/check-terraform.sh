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

state_file="$repo_root/infra/terraform/bootstrap/state.tf"
network_file="$repo_root/infra/terraform/production/network.tf"
variables_file="$repo_root/infra/terraform/production/variables.tf"

require_resource_prevent_destroy() {
  local resource_type="$1"
  local resource_name="$2"
  local source_file="$3"

  if ! awk -v header="resource \"$resource_type\" \"$resource_name\" {" '
    $0 == header {
      found_resource = 1
      depth = 1
      next
    }
    found_resource && depth > 0 {
      line = $0
      opens = gsub(/\{/, "{", line)
      closes = gsub(/\}/, "}", line)
      if ($0 ~ /^[[:space:]]*prevent_destroy[[:space:]]*=[[:space:]]*true[[:space:]]*$/) {
        found_guard = 1
      }
      depth += opens - closes
      if (depth == 0) {
        exit(found_guard ? 0 : 1)
      }
    }
    END {
      if (!found_resource || depth > 0) {
        exit 1
      }
    }
  ' "$source_file"; then
    printf 'Terraform resource is missing bound destroy protection: %s.%s\n' \
      "$resource_type" "$resource_name" >&2
    exit 1
  fi
}

require_resource_prevent_destroy aws_s3_bucket state "$state_file"
require_resource_prevent_destroy aws_s3_bucket_versioning state "$state_file"
require_resource_prevent_destroy \
  aws_s3_bucket_server_side_encryption_configuration state "$state_file"
require_resource_prevent_destroy aws_vpc canonical "$network_file"
require_resource_prevent_destroy aws_internet_gateway canonical "$network_file"
require_resource_prevent_destroy aws_subnet public "$network_file"
require_resource_prevent_destroy aws_route_table public "$network_file"
require_resource_prevent_destroy aws_route public_ipv4_default "$network_file"
require_resource_prevent_destroy aws_eip origin "$network_file"

if ! grep -Fqx \
  '    error_message = "Canonical public subnet keys must be public_a through public_d."' \
  "$variables_file"; then
  printf 'Canonical public subnet validation message changed.\n' >&2
  exit 1
fi

if grep -Eq '^[[:space:]]*route[[:space:]]*\{' "$network_file"; then
  printf 'Production public route table must not use inline route blocks.\n' >&2
  exit 1
fi

if grep -Eq \
  '^resource[[:space:]]+"aws_(route_table_association|main_route_table_association)"' \
  "$network_file"; then
  printf 'Production public subnets must inherit the VPC main route table.\n' >&2
  exit 1
fi

if grep -Eq \
  '^[[:space:]]*(instance|network_interface|associate_with_private_ip)[[:space:]]*=' \
  "$network_file" ||
  grep -Eq '^resource[[:space:]]+"aws_eip_association"' "$network_file"; then
  printf 'Production origin EIP must remain unassociated until cutover.\n' >&2
  exit 1
fi

for policy_token in \
  'sid    = "DenyInsecureTransport"' \
  'effect = "Deny"' \
  'type        = "*"' \
  'identifiers = ["*"]' \
  'actions = ["s3:*"]' \
  'aws_s3_bucket.state.arn' \
  '"${aws_s3_bucket.state.arn}/*"' \
  'test     = "Bool"' \
  'variable = "aws:SecureTransport"' \
  'values   = ["false"]'; do
  if ! grep -Fq "$policy_token" "$state_file"; then
    printf 'State bucket TLS policy contract is incomplete: %s\n' \
      "$policy_token" >&2
    exit 1
  fi
done

identity_file="$repo_root/infra/terraform/bootstrap/identity.tf"

grep -Fq \
  'repo:${var.github_repository}:environment:production' \
  "$identity_file"
grep -Fq \
  'repo:${var.github_repository}:environment:edge' \
  "$identity_file"
grep -Fq '"sts.amazonaws.com"' "$identity_file"
grep -Fq 'var.existing_github_oidc_provider_arn == null' "$identity_file"
grep -Fq 'to = aws_iam_openid_connect_provider.github' "$identity_file"
grep -Fq 'id = each.value' "$identity_file"
grep -Fq \
  'assume_role_policy = data.aws_iam_policy_document.production_assume.json' \
  "$identity_file"
grep -Fq \
  'assume_role_policy = data.aws_iam_policy_document.edge_assume.json' \
  "$identity_file"
grep -Fq 'prevent_destroy = true' "$identity_file"
if grep -Fq '"bootstrap/terraform.tfstate' "$identity_file"; then
  printf 'Production OIDC role must not access bootstrap state.\n' >&2
  exit 1
fi

require_resource_prevent_destroy \
  aws_iam_policy production_slice3_read "$identity_file"

identity_without_slice3="$(
  awk '
    $0 == "data \"aws_iam_policy_document\" \"production_slice3_read\" {" {
      skip = 1
      depth = 1
      next
    }
    skip {
      line = $0
      depth += gsub(/\{/, "{", line) - gsub(/\}/, "}", line)
      if (depth == 0) {
        skip = 0
      }
      next
    }
    { print }
  ' "$identity_file"
)"
for forbidden in '"rds:' '"iam:' '"secretsmanager:'; do
  if grep -Fiq "$forbidden" <<<"$identity_without_slice3"; then
    printf 'Slice 1 identity policy contains forbidden action: %s\n' \
      "$forbidden" >&2
    exit 1
  fi
done

expected_slice3_actions="$(printf '%s\n' \
  'ec2:DescribeSecurityGroupRules' \
  'rds:DescribeDBEngineVersions' \
  'rds:DescribeDBInstances' \
  'rds:DescribeDBParameterGroups' \
  'rds:DescribeDBParameters' \
  'rds:DescribeDBSubnetGroups' \
  'rds:DescribeOrderableDBInstanceOptions' \
  'rds:ListTagsForResource' \
  's3:GetAccelerateConfiguration' \
  's3:GetBucketAcl' \
  's3:GetBucketCORS' \
  's3:GetBucketLocation' \
  's3:GetBucketLogging' \
  's3:GetBucketObjectLockConfiguration' \
  's3:GetBucketOwnershipControls' \
  's3:GetBucketPolicy' \
  's3:GetBucketPolicyStatus' \
  's3:GetBucketPublicAccessBlock' \
  's3:GetBucketRequestPayment' \
  's3:GetBucketTagging' \
  's3:GetBucketVersioning' \
  's3:GetBucketWebsite' \
  's3:GetEncryptionConfiguration' \
  's3:GetLifecycleConfiguration' \
  's3:GetReplicationConfiguration' \
  's3:ListBucket' \
  'secretsmanager:DescribeSecret' \
  'secretsmanager:GetResourcePolicy' \
  'secretsmanager:ListSecretVersionIds' |
  sort)"
actual_slice3_actions="$(
  awk '
    $0 == "data \"aws_iam_policy_document\" \"production_slice3_read\" {" {
      found_document = 1
      document_depth = 1
      next
    }
    found_document && document_depth > 0 {
      line = $0
      opens = gsub(/\{/, "{", line)
      closes = gsub(/\}/, "}", line)
      if ($0 ~ /^[[:space:]]*actions[[:space:]]*=[[:space:]]*\[[[:space:]]*$/) {
        in_actions = 1
      } else if (in_actions &&
                 $0 ~ /^[[:space:]]*"[A-Za-z0-9:*]+"[,]?[[:space:]]*$/) {
        action = $0
        sub(/^[[:space:]]*"/, "", action)
        sub(/"[,]?[[:space:]]*$/, "", action)
        print action
      } else if (in_actions && $0 ~ /^[[:space:]]*\][[:space:]]*$/) {
        in_actions = 0
      }
      document_depth += opens - closes
      if (document_depth == 0) {
        exit
      }
    }
  ' "$identity_file" | sort
)"
if grep -Eq \
  '(Create|Put|Update|Delete|Modify|Restore|Rotate|Replicate|PassRole|GetSecretValue|BatchGetSecretValue)' \
  <<<"$actual_slice3_actions"; then
  printf 'Slice 3 refresh-only policy contains a write or secret-value action.\n' \
    >&2
  exit 1
fi
if [[ "$actual_slice3_actions" != "$expected_slice3_actions" ]]; then
  printf 'Slice 3 refresh-only policy actions differ from the approved ceiling.\n' \
    >&2
  exit 1
fi

expected_policy_tokens="$(printf '%s\n' \
  '"ec2:DescribeAddresses"' \
  '"ec2:DescribeAddressesAttribute"' \
  '"ec2:DescribeAvailabilityZones"' \
  '"ec2:DescribeInternetGateways"' \
  '"ec2:DescribeNetworkAcls"' \
  '"ec2:DescribeRouteTables"' \
  '"ec2:DescribeSecurityGroups"' \
  '"ec2:DescribeSubnetAttribute"' \
  '"ec2:DescribeSubnets"' \
  '"ec2:DescribeTags"' \
  '"ec2:DescribeVpcAttribute"' \
  '"ec2:DescribeVpcs"' \
  '"s3:DeleteObject"' '"s3:DeleteObject"' \
  '"s3:GetBucketLocation"' '"s3:GetBucketLocation"' \
  '"s3:GetObject"' '"s3:GetObject"' \
  '"s3:ListBucket"' '"s3:ListBucket"' \
  '"s3:prefix"' '"s3:prefix"' \
  '"s3:PutObject"' '"s3:PutObject"' \
  '"sts:AssumeRoleWithWebIdentity"' '"sts:AssumeRoleWithWebIdentity"' |
  sort)"
actual_policy_tokens="$(
  grep -Eo '"[a-z0-9]+:[A-Za-z*]+"' <<<"$identity_without_slice3" | sort
)"
if [[ "$actual_policy_tokens" != "$expected_policy_tokens" ]]; then
  printf 'Slice 2 network read policy actions differ from the approved ceiling.\n' >&2
  exit 1
fi

production_workflow="$repo_root/.github/workflows/terraform-production-plan.yml"
workflow_files=("$repo_root/.github/workflows/"terraform-*.yml)

mapping_count="$(
  grep -Fxc \
    '      TF_VAR_canonical_network_config: ${{ secrets.TF_VAR_CANONICAL_NETWORK_CONFIG }}' \
    "$production_workflow" || true
)"
variable_count="$(
  grep -Fo 'TF_VAR_canonical_network_config' "$production_workflow" |
    wc -l |
    tr -d ' ' || true
)"
secret_count="$(
  grep -Fo 'TF_VAR_CANONICAL_NETWORK_CONFIG' "$production_workflow" |
    wc -l |
    tr -d ' ' || true
)"
if [[ "$mapping_count" -ne 1 ||
  "$variable_count" -ne 1 ||
  "$secret_count" -ne 1 ]]; then
  printf 'production workflow must map but never print private network config\n' \
    >&2
  exit 1
fi
if grep -Eq '^[[:space:]]*(env|printenv)[[:space:]]*$' \
  "$production_workflow"; then
  printf 'production workflow must map but never print private network config\n' \
    >&2
  exit 1
fi

if ! grep -Fq 'id-token: write' "$production_workflow"; then
  printf 'production workflow must request an OIDC id-token\n' >&2
  exit 1
fi
if ! grep -Fq 'mask-aws-account-id: true' "$production_workflow"; then
  printf 'production workflow must mask the AWS account ID\n' >&2
  exit 1
fi

if ! awk '
  function trim(line) {
    sub(/^[[:space:]]+/, "", line)
    sub(/[[:space:]]+$/, "", line)
    return line
  }
  /(^|[^[:alnum:]_])terraform([^[:alnum:]_]|$)/ {
    line = trim($0)
    if (line == "uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e") {
      next
    }
    if (line == "terraform -chdir=infra/terraform/production init \\") {
      init_count++
      next
    }
    if (line == "terraform -chdir=infra/terraform/production plan \\") {
      plan_count++
      next
    }
    unexpected = 1
  }
  END {
    exit(unexpected || init_count != 1 || plan_count != 1)
  }
' "$production_workflow"; then
  printf 'production workflow must remain plan-only\n' >&2
  exit 1
fi

if grep -Eh \
  '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+[^[:space:]]+@' \
  "${workflow_files[@]}" |
  grep -Ev \
    '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+[^@[:space:]]+@[0-9a-f]{40}[[:space:]]*$'; then
  printf 'Terraform workflows must pin actions by full commit SHA\n' >&2
  exit 1
fi

require_action_pin() {
  local expected_count="$1"
  local pin="$2"
  local actual_count

  actual_count="$(
    awk -v pin="uses: $pin" '
      /^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+/ {
        line = $0
        sub(/^[[:space:]]*(-[[:space:]]+)?/, "", line)
        sub(/[[:space:]]*$/, "", line)
        if (line == pin) {
          count++
        }
      }
      END { print count + 0 }
    ' "${workflow_files[@]}"
  )"
  if [[ "$actual_count" -ne "$expected_count" ]]; then
    printf 'Terraform workflows changed reviewed action pin: %s\n' "$pin" >&2
    exit 1
  fi
}

require_action_pin \
  2 "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803"
require_action_pin \
  2 "hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e"
require_action_pin \
  1 "aws-actions/configure-aws-credentials@e6de054238d6b7531b4efff3b6587d9aade6a06c"

if [[ "${CHECK_TERRAFORM_FIXTURE_MODE:-0}" != 1 ]]; then
  "$repo_root/scripts/check-terraform-plan_test.sh"
  "$repo_root/scripts/check-terraform-workflows_test.sh"
fi
