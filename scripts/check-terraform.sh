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
database_file="$repo_root/infra/terraform/production/database.tf"
secrets_file="$repo_root/infra/terraform/production/secrets.tf"
recovery_file="$repo_root/infra/terraform/production/recovery.tf"

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
require_resource_prevent_destroy aws_subnet database "$database_file"
require_resource_prevent_destroy aws_route_table database "$database_file"
require_resource_prevent_destroy aws_security_group origin "$database_file"
require_resource_prevent_destroy aws_security_group database "$database_file"
require_resource_prevent_destroy \
  aws_vpc_security_group_ingress_rule database_postgresql_from_origin \
  "$database_file"
require_resource_prevent_destroy aws_db_instance production "$database_file"
require_resource_prevent_destroy \
  aws_secretsmanager_secret runtime "$secrets_file"
require_resource_prevent_destroy aws_s3_bucket recovery "$recovery_file"
require_resource_prevent_destroy \
  aws_s3_bucket_versioning recovery "$recovery_file"
require_resource_prevent_destroy \
  aws_s3_bucket_server_side_encryption_configuration recovery "$recovery_file"
require_resource_prevent_destroy aws_s3_bucket_policy recovery "$recovery_file"
require_resource_prevent_destroy \
  aws_s3_bucket_lifecycle_configuration recovery "$recovery_file"

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

if grep -Eq '^[[:space:]]*route[[:space:]]*\{' "$database_file"; then
  printf 'Production database route table must remain empty\n' >&2
  exit 1
fi

if grep -Eq '^resource[[:space:]]+"aws_route"' "$database_file" ||
  grep -ERq \
    '^resource[[:space:]]+"(aws_nat_gateway|aws_eip_association|aws_secretsmanager_secret_version|cloudflare_[^"]+)"' \
    "$repo_root/infra/terraform/production"; then
  printf 'Slice 3 production contains a forbidden Terraform resource type\n' \
    >&2
  exit 1
fi

instance_declarations="$(
  grep -ERh '^resource[[:space:]]+"aws_instance"' \
    "$repo_root/infra/terraform/production" || true
)"
if [[ "$instance_declarations" != \
  'resource "aws_instance" "replacement_host" {' ]]; then
  printf 'Slice 3 production contains a forbidden Terraform resource type\n' \
    >&2
  exit 1
fi

origin_discovery_tag='"jobcron:edge-target" = "origin-security-group"'
origin_discovery_tag_count="$(
  grep -Fh "$origin_discovery_tag" \
    "$repo_root/infra/terraform/production/"*.tf |
    wc -l |
    tr -d ' ' || true
)"
origin_resource="$(
  awk '
    $0 == "resource \"aws_security_group\" \"origin\" {" {
      found = 1
      depth = 1
      print
      next
    }
    found {
      print
      line = $0
      opens = gsub(/\{/, "{", line)
      closes = gsub(/\}/, "}", line)
      depth += opens - closes
      if (depth == 0) {
        exit
      }
    }
  ' "$database_file"
)"
if [[ "$origin_discovery_tag_count" -ne 1 ]] ||
  ! grep -Fq "$origin_discovery_tag" <<<"$origin_resource"; then
  printf 'Origin security group discovery tag contract changed\n' >&2
  exit 1
fi

recovery_lifecycle="$(
  awk '
    $0 == "resource \"aws_s3_bucket_lifecycle_configuration\" \"recovery\" {" {
      found = 1
      depth = 1
      print
      next
    }
    found {
      print
      line = $0
      depth += gsub(/\{/, "{", line) - gsub(/\}/, "}", line)
      if (depth == 0) {
        exit
      }
    }
  ' "$recovery_file" |
    tr -d '[:space:]'
)"
expected_recovery_lifecycle='resource"aws_s3_bucket_lifecycle_configuration""recovery"{bucket=aws_s3_bucket.recovery.idrule{id="expire-verified-after-off-cloud-copy"status="Enabled"filter{tag{key="macbook-copy"value="verified"}}expiration{days=14}noncurrent_version_expiration{noncurrent_days=1}}rule{id="expire-all-objects"status="Enabled"filter{}expiration{days=90}noncurrent_version_expiration{noncurrent_days=1}}lifecycle{prevent_destroy=true}depends_on=[aws_s3_bucket_versioning.recovery]}'
if [[ "$recovery_lifecycle" != "$expected_recovery_lifecycle" ]]; then
  printf 'Recovery bucket lifecycle contract changed\n' >&2
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
edge_file="$repo_root/infra/terraform/edge/cloudflare.tf"

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
require_resource_prevent_destroy \
  aws_iam_policy edge_prefix_list "$identity_file"
require_resource_prevent_destroy \
  aws_ec2_managed_prefix_list cloudflare_ipv4 "$edge_file"
require_resource_prevent_destroy \
  aws_vpc_security_group_ingress_rule \
  origin_https_from_cloudflare "$edge_file"

identity_without_slice3_or_edge="$(
  awk '
    $0 == "data \"aws_iam_policy_document\" \"production_slice3_read\" {" ||
    $0 == "data \"aws_iam_policy_document\" \"edge_prefix_list\" {" {
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
  if grep -Fiq "$forbidden" <<<"$identity_without_slice3_or_edge"; then
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
  grep -Eo '"[a-z0-9]+:[A-Za-z*]+"' \
    <<<"$identity_without_slice3_or_edge" | sort
)"
if [[ "$actual_policy_tokens" != "$expected_policy_tokens" ]]; then
  printf 'Slice 2 network read policy actions differ from the approved ceiling.\n' >&2
  exit 1
fi

edge_policy_block="$(
  awk '
    $0 == "data \"aws_iam_policy_document\" \"edge_prefix_list\" {" {
      found = 1
      depth = 1
      print
      next
    }
    found {
      print
      line = $0
      depth += gsub(/\{/, "{", line) - gsub(/\}/, "}", line)
      if (depth == 0) {
        exit
      }
    }
  ' "$identity_file"
)"
expected_edge_actions="$(printf '%s\n' \
  'ec2:AuthorizeSecurityGroupIngress' \
  'ec2:CreateManagedPrefixList' \
  'ec2:CreateTags' \
  'ec2:CreateTags' \
  'ec2:DescribeManagedPrefixLists' \
  'ec2:DescribeSecurityGroupRules' \
  'ec2:DescribeSecurityGroups' \
  'ec2:DescribeTags' \
  'ec2:GetManagedPrefixListEntries' \
  'ec2:ModifyManagedPrefixList' |
  sort)"
actual_edge_actions="$(
  awk '
    /^[[:space:]]*actions[[:space:]]*=[[:space:]]*\[[[:space:]]*$/ {
      in_actions = 1
      next
    }
    in_actions && /^[[:space:]]*"ec2:[A-Za-z*]+"[,]?[[:space:]]*$/ {
      action = $0
      sub(/^[[:space:]]*"/, "", action)
      sub(/"[,]?[[:space:]]*$/, "", action)
      print action
      next
    }
    in_actions && /^[[:space:]]*\][[:space:]]*$/ {
      in_actions = 0
      next
    }
    /^[[:space:]]*actions[[:space:]]*=[[:space:]]*\["ec2:[A-Za-z*]+"\][[:space:]]*$/ {
      action = $0
      sub(/^.*\["/, "", action)
      sub(/"\].*$/, "", action)
      print action
    }
  ' <<<"$edge_policy_block" |
    sort
)"
edge_contract_tokens="$(printf '%s\n' \
  '    resources = ["*"]' \
  '    resources = [local.edge_prefix_list_arn]' \
  '    resources = [local.edge_prefix_list_arn]' \
  '    resources = [local.edge_prefix_list_arn]' \
  '    resources = [local.edge_prefix_list_arn]' \
  '    resources = [local.edge_security_group_arn]' \
  '    resources = [local.edge_security_group_rule_arn]' \
  '      variable = "aws:ResourceTag/jobcron:edge-source"' \
  '      variable = "aws:ResourceTag/jobcron:edge-source"' \
  '      variable = "aws:RequestTag/jobcron:edge-source"' \
  '      variable = "aws:ResourceTag/jobcron:edge-target"' \
  '      variable = "aws:RequestTag/jobcron:edge-rule"' \
  '      variable = "aws:TagKeys"' \
  '      variable = "aws:TagKeys"' \
  '      variable = "aws:TagKeys"' \
  '      variable = "aws:TagKeys"' \
  '      variable = "ec2:CreateAction"' \
  '      variable = "ec2:CreateAction"' \
  '      values   = ["cloudflare-ipv4"]' \
  '      values   = ["cloudflare-ipv4"]' \
  '      values   = ["cloudflare-ipv4"]' \
  '      values   = ["origin-security-group"]' \
  '      values   = ["origin-https-from-cloudflare"]' \
  '      values   = ["jobcron:edge-source"]' \
  '      values   = ["jobcron:edge-source"]' \
  '      values   = ["jobcron:edge-rule"]' \
  '      values   = ["jobcron:edge-rule"]' \
  '      values   = ["CreateManagedPrefixList"]' \
  '      values   = ["AuthorizeSecurityGroupIngress"]' |
  sort)"
actual_edge_contract_tokens="$(
  grep -E \
    '^[[:space:]]+(resources|variable|values)[[:space:]]*=' \
    <<<"$edge_policy_block" |
    sort
)"
if [[ "$actual_edge_actions" != "$expected_edge_actions" ]] ||
   [[ "$actual_edge_contract_tokens" != "$edge_contract_tokens" ]] ||
   ! grep -Fq \
     'role       = aws_iam_role.edge.name' \
     <(
       awk '
         $0 == "resource \"aws_iam_role_policy_attachment\" \"edge_prefix_list\" {" {
           found = 1
           depth = 1
           print
           next
         }
         found {
           print
           line = $0
           depth += gsub(/\{/, "{", line) - gsub(/\}/, "}", line)
           if (depth == 0) exit
         }
       ' "$identity_file"
     ); then
  printf 'Slice 5 edge policy contract changed\n' >&2
  exit 1
fi

if grep -Eq '^[[:space:]]*output[[:space:]]+"' \
  "$repo_root/infra/terraform/edge/"*.tf; then
  printf 'Slice 5 edge root must not expose outputs\n' >&2
  exit 1
fi

production_workflow="$repo_root/.github/workflows/terraform-production-plan.yml"
edge_workflow="$repo_root/.github/workflows/terraform-edge-prefix-list.yml"
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

private_database_mapping_count="$(
  grep -Fxc \
    '      TF_VAR_private_database_config: ${{ secrets.TF_VAR_PRIVATE_DATABASE_CONFIG }}' \
    "$production_workflow" || true
)"
private_database_variable_count="$(
  grep -Fo 'TF_VAR_private_database_config' "$production_workflow" |
    wc -l |
    tr -d ' ' || true
)"
private_database_secret_count="$(
  grep -Fo 'TF_VAR_PRIVATE_DATABASE_CONFIG' "$production_workflow" |
    wc -l |
    tr -d ' ' || true
)"
if [[ "$private_database_mapping_count" -ne 1 ||
  "$private_database_variable_count" -ne 1 ||
  "$private_database_secret_count" -ne 1 ]]; then
  printf 'production workflow must map but never print private database config\n' \
    >&2
  exit 1
fi
if grep -Eq \
  '^[[:space:]]*(env|printenv)([[:space:]]|[|;&]|$)' \
  "$production_workflow"; then
  printf 'production workflow must map but never print private network config\n' \
    >&2
  exit 1
fi

if grep -Eq \
  '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+actions/upload-artifact@' \
  "$production_workflow"; then
  printf 'production workflow must not publish Terraform plan artifacts\n' >&2
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

fail_edge_workflow() {
  printf 'edge prefix-list workflow violates the reviewed contract\n' >&2
  exit 1
}

[[ -f "$edge_workflow" ]] || fail_edge_workflow

edge_permissions="$(
  awk '
    $0 == "permissions:" {
      found = 1
      next
    }
    found && /^[^[:space:]]/ {
      exit
    }
    found && /:/ {
      line = $0
      sub(/^[[:space:]]+/, "", line)
      print line
    }
  ' "$edge_workflow" | sort
)"
[[ "$edge_permissions" == $'contents: read\nid-token: write' ]] ||
  fail_edge_workflow

for literal in \
  '    - cron: "17 18 * * *"' \
  '  workflow_dispatch:' \
  "    if: \${{ vars.EDGE_AUTOMATION_ENABLED == 'true' }}" \
  '    environment: edge' \
  '  group: terraform-edge-prefix-list' \
  '  cancel-in-progress: false' \
  '      TF_DATA_DIR: ${{ runner.temp }}/terraform-data' \
  '          mask-aws-account-id: true' \
  '            -detailed-exitcode \' \
  '          if [[ "$plan_rc" -eq 0 ]]; then' \
  '          if [[ "$plan_rc" -ne 2 ]]; then' \
  '          python3 scripts/check-terraform-slice-5-plan.py \' \
  '          if terraform -chdir=infra/terraform/edge apply -input=false \' \
  "            printf 'Terraform edge initialization succeeded\\n'" \
  "            printf 'Terraform edge initialization failed\\n' >&2" \
  "            printf 'Terraform edge refresh applied\\n'" \
  "            printf 'Terraform edge refresh apply failed\\n' >&2"; do
  [[ "$(grep -Fxc "$literal" "$edge_workflow" || true)" -eq 1 ]] ||
    fail_edge_workflow
done
[[ "$(grep -Fxc '          umask 077' "$edge_workflow" || true)" -eq 4 ]] ||
  fail_edge_workflow

[[ "$(grep -Foc 'https://www.cloudflare.com/ips-v4' "$edge_workflow" || true)" -eq 1 ]] ||
  fail_edge_workflow
for curl_flag in \
  'curl --fail --silent --show-error' \
  "--proto '=https'" \
  '--tlsv1.2' \
  '--max-time 30' \
  '--output "${RUNNER_TEMP}/cloudflare-ips-v4.txt"'; do
  grep -Fq -- "$curl_flag" "$edge_workflow" || fail_edge_workflow
done

for mapping in \
  '          TF_STATE_BUCKET: ${{ secrets.TF_STATE_BUCKET }}' \
  '          TF_AGGREGATE_COST_JSON: ${{ secrets.TF_AGGREGATE_COST_JSON }}' \
  '          TF_SLICE4_CHECKPOINT_JSON: ${{ secrets.TF_SLICE4_CHECKPOINT_JSON }}'; do
  [[ "$(grep -Fxc "$mapping" "$edge_workflow" || true)" -eq 1 ]] ||
    fail_edge_workflow
done
[[ "$(grep -Foc 'TF_STATE_BUCKET' "$edge_workflow" || true)" -eq 2 ]] ||
  fail_edge_workflow
[[ "$(grep -Foc 'TF_AGGREGATE_COST_JSON' "$edge_workflow" || true)" -eq 2 ]] ||
  fail_edge_workflow
[[ "$(grep -Foc 'TF_SLICE4_CHECKPOINT_JSON' "$edge_workflow" || true)" -eq 2 ]] ||
  fail_edge_workflow
grep -Fq 'role-to-assume: ${{ secrets.AWS_ROLE_ARN }}' "$edge_workflow" ||
  fail_edge_workflow
if grep -Eq \
  'vars\.(TF_STATE_BUCKET|TF_AGGREGATE_COST_JSON|TF_SLICE4_CHECKPOINT_JSON)|^      (TF_STATE_BUCKET|TF_AGGREGATE_COST_JSON|TF_SLICE4_CHECKPOINT_JSON):' \
  "$edge_workflow"; then
  fail_edge_workflow
fi
[[ "$(grep -Foc 'TF_DATA_DIR' "$edge_workflow" || true)" -eq 1 ]] ||
  fail_edge_workflow

for artifact in \
  cloudflare-ips-v4.txt \
  cloudflare.auto.tfvars.json \
  aggregate-cost.json \
  slice-4-checkpoint.json \
  edge.tfplan \
  edge-plan.log \
  edge-plan.json \
  edge-init.log \
  edge-apply.log; do
  if grep -F "$artifact" "$edge_workflow" |
    grep -Fv "\${RUNNER_TEMP}/$artifact" >/dev/null; then
    fail_edge_workflow
  fi
done

if grep -Eiq \
  'actions/(upload-artifact|cache)|cloudflare/|CLOUDFLARE_(API_)?TOKEN|infra/terraform/production|^[[:space:]]+(env|printenv)([[:space:]|;&]|$)|(cat|tail|head|less)[[:space:]].*edge-(init|apply)\.log' \
  "$edge_workflow"; then
  fail_edge_workflow
fi

edge_line() {
  local literal="$1"
  local lines
  lines="$(grep -nF "$literal" "$edge_workflow" || true)"
  [[ "$(wc -l <<<"$lines" | tr -d ' ')" -eq 1 && -n "$lines" ]] ||
    fail_edge_workflow
  printf '%s\n' "${lines%%:*}"
}

edge_order=(
  "$(edge_line 'uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803')"
  "$(edge_line 'uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e')"
  "$(edge_line 'uses: aws-actions/configure-aws-credentials@e6de054238d6b7531b4efff3b6587d9aade6a06c')"
  "$(edge_line 'curl --fail --silent --show-error')"
  "$(edge_line 'python3 scripts/normalize-cloudflare-ipv4.py')"
  "$(edge_line 'printf '\''%s'\'' "$TF_AGGREGATE_COST_JSON"')"
  "$(edge_line 'printf '\''%s'\'' "$TF_SLICE4_CHECKPOINT_JSON"')"
  "$(edge_line 'if terraform -chdir=infra/terraform/edge init')"
  "$(edge_line 'terraform -chdir=infra/terraform/edge plan')"
  "$(edge_line 'if [[ "$plan_rc" -eq 0 ]]; then')"
  "$(edge_line 'if [[ "$plan_rc" -ne 2 ]]; then')"
  "$(edge_line 'terraform -chdir=infra/terraform/edge show -json')"
  "$(edge_line 'python3 scripts/check-terraform-slice-5-plan.py')"
  "$(edge_line 'if terraform -chdir=infra/terraform/edge apply -input=false')"
)
for ((index = 1; index < ${#edge_order[@]}; index++)); do
  ((edge_order[index - 1] < edge_order[index])) || fail_edge_workflow
done

apply_line="${edge_order[${#edge_order[@]} - 1]}"
apply_block="$(
  sed -n "${apply_line},$((apply_line + 1))p" "$edge_workflow" |
    tr -d '[:space:]'
)"
[[ "$apply_block" == \
  'ifterraform-chdir=infra/terraform/edgeapply-input=false\"${RUNNER_TEMP}/edge.tfplan"\' ]] ||
  fail_edge_workflow
grep -Fq '            >"${RUNNER_TEMP}/edge-plan.log" 2>&1' "$edge_workflow" ||
  fail_edge_workflow
grep -Fq '            >"${RUNNER_TEMP}/edge-init.log" 2>&1; then' "$edge_workflow" ||
  fail_edge_workflow
grep -Fq '            >"${RUNNER_TEMP}/edge-apply.log" 2>&1; then' "$edge_workflow" ||
  fail_edge_workflow

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
  3 "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803"
require_action_pin \
  3 "hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e"
require_action_pin \
  2 "aws-actions/configure-aws-credentials@e6de054238d6b7531b4efff3b6587d9aade6a06c"

if [[ "${CHECK_TERRAFORM_FIXTURE_MODE:-0}" != 1 ]]; then
  "$repo_root/scripts/check-terraform-plan_test.sh"
  "$repo_root/scripts/check-terraform-slice-4-plan_test.sh"
  python3 "$repo_root/scripts/normalize-cloudflare-ipv4_test.py"
  python3 "$repo_root/scripts/check-terraform-slice-5-plan_test.py"
  "$repo_root/scripts/check-terraform-workflows_test.sh"
fi
