#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

mkdir -p \
  "$fixture_root/repo/.github/workflows" \
  "$fixture_root/repo/infra/terraform/bootstrap" \
  "$fixture_root/repo/infra/terraform/production" \
  "$fixture_root/repo/scripts" \
  "$fixture_root/bin"
cp "$repo_root/scripts/check-terraform.sh" "$fixture_root/repo/scripts/"
cp "$repo_root/infra/terraform/bootstrap/state.tf" \
  "$fixture_root/repo/infra/terraform/bootstrap/"
cp "$repo_root/infra/terraform/bootstrap/identity.tf" \
  "$fixture_root/repo/infra/terraform/bootstrap/"
cp "$repo_root/infra/terraform/production/network.tf" \
  "$fixture_root/repo/infra/terraform/production/"
cp "$repo_root/infra/terraform/production/variables.tf" \
  "$fixture_root/repo/infra/terraform/production/"
cp "$repo_root/infra/terraform/production/database.tf" \
  "$fixture_root/repo/infra/terraform/production/"
cp "$repo_root/infra/terraform/production/secrets.tf" \
  "$fixture_root/repo/infra/terraform/production/"
cp "$repo_root/infra/terraform/production/recovery.tf" \
  "$fixture_root/repo/infra/terraform/production/"

cat >"$fixture_root/bin/terraform" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$fixture_root/bin/terraform"

reset_fixtures() {
  cp "$repo_root/.github/workflows/"terraform-*.yml \
    "$fixture_root/repo/.github/workflows/"
  cp "$repo_root/infra/terraform/bootstrap/state.tf" \
    "$fixture_root/repo/infra/terraform/bootstrap/"
  cp "$repo_root/infra/terraform/bootstrap/identity.tf" \
    "$fixture_root/repo/infra/terraform/bootstrap/"
  cp "$repo_root/infra/terraform/production/network.tf" \
    "$fixture_root/repo/infra/terraform/production/"
  cp "$repo_root/infra/terraform/production/variables.tf" \
    "$fixture_root/repo/infra/terraform/production/"
  cp "$repo_root/infra/terraform/production/database.tf" \
    "$fixture_root/repo/infra/terraform/production/"
  cp "$repo_root/infra/terraform/production/secrets.tf" \
    "$fixture_root/repo/infra/terraform/production/"
  cp "$repo_root/infra/terraform/production/recovery.tf" \
    "$fixture_root/repo/infra/terraform/production/"
}

replace_once() {
  local file="$1"
  local old="$2"
  local new="$3"

  awk -v old="$old" -v new="$new" '
    !replaced && index($0, old) {
      sub(old, new)
      replaced = 1
    }
    { print }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

remove_exact_line() {
  local file="$1"
  local target="$2"

  awk -v target="$target" '
    $0 == target && !removed {
      removed = 1
      next
    }
    { print }
    END {
      if (!removed) {
        exit 1
      }
    }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

duplicate_exact_line() {
  local file="$1"
  local target="$2"

  awk -v target="$target" '
    $0 == target && !duplicated {
      print
      duplicated = 1
    }
    { print }
    END {
      if (!duplicated) {
        exit 1
      }
    }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_into_first_run_block() {
  local file="$1"
  local line="$2"

  awk -v line="$line" '
    !inserted && $0 == "        run: |" {
      print
      print line
      inserted = 1
      next
    }
    { print }
    END {
      if (!inserted) {
        exit 1
      }
    }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_inline_route() {
  local file="$1"

  awk '
    !inserted && $0 == "resource \"aws_route_table\" \"public\" {" {
      print
      print "  route {"
      print "    cidr_block = \"0.0.0.0/0\""
      print "  }"
      inserted = 1
      next
    }
    { print }
    END {
      if (!inserted) {
        exit 1
      }
    }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

run_checker() {
  CHECK_TERRAFORM_FIXTURE_MODE=1 \
    PATH="$fixture_root/bin:$PATH" \
    "$fixture_root/repo/scripts/check-terraform.sh" \
    >"$fixture_root/checker.out" 2>&1
}

expect_rejected() {
  local name="$1"
  local message="$2"
  shift 2

  reset_fixtures
  "$@"
  if run_checker; then
    printf 'FAIL: accepted %s\n' "$name" >&2
    return 1
  fi
  if ! grep -Fq "$message" "$fixture_root/checker.out"; then
    printf 'FAIL: rejected %s for the wrong reason\n' "$name" >&2
    return 1
  fi
  printf 'PASS: rejected %s\n' "$name"
}

checkout_sha="d23441a48e516b6c34aea4fa41551a30e30af803"
aws_action_sha="e6de054238d6b7531b4efff3b6587d9aade6a06c"
static_workflow="$fixture_root/repo/.github/workflows/terraform-check.yml"
production_workflow="$fixture_root/repo/.github/workflows/terraform-production-plan.yml"
state_file="$fixture_root/repo/infra/terraform/bootstrap/state.tf"
identity_file="$fixture_root/repo/infra/terraform/bootstrap/identity.tf"
network_file="$fixture_root/repo/infra/terraform/production/network.tf"
variables_file="$fixture_root/repo/infra/terraform/production/variables.tf"
database_file="$fixture_root/repo/infra/terraform/production/database.tf"
secrets_file="$fixture_root/repo/infra/terraform/production/secrets.tf"
recovery_file="$fixture_root/repo/infra/terraform/production/recovery.tf"
failures=0

reset_fixtures
if ! grep -Fq \
  "uses: aws-actions/configure-aws-credentials@$aws_action_sha" \
  "$production_workflow"; then
  printf 'FAIL: production workflow does not use the reviewed Node.js 24 AWS credentials action pin\n' >&2
  exit 1
fi
if ! grep -Fqx \
  '      TF_VAR_private_database_config: ${{ secrets.TF_VAR_PRIVATE_DATABASE_CONFIG }}' \
  "$production_workflow"; then
  printf 'FAIL: production workflow does not map the private database config\n' >&2
  exit 1
fi
if ! run_checker; then
  printf 'FAIL: rejected the unmodified workflows\n' >&2
  cat "$fixture_root/checker.out" >&2
  exit 1
fi

expect_rejected "missing private network config mapping" \
  "production workflow must map but never print private network config" \
  remove_exact_line "$production_workflow" \
  '      TF_VAR_canonical_network_config: ${{ secrets.TF_VAR_CANONICAL_NETWORK_CONFIG }}' ||
  failures=$((failures + 1))
expect_rejected "printed private network config" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          printf '\''%s\n'\'' "$TF_VAR_canonical_network_config"' ||
  failures=$((failures + 1))
expect_rejected "missing private database config mapping" \
  "production workflow must map but never print private database config" \
  remove_exact_line "$production_workflow" \
  '      TF_VAR_private_database_config: ${{ secrets.TF_VAR_PRIVATE_DATABASE_CONFIG }}' ||
  failures=$((failures + 1))
expect_rejected "duplicate private database config mapping" \
  "production workflow must map but never print private database config" \
  duplicate_exact_line "$production_workflow" \
  '      TF_VAR_private_database_config: ${{ secrets.TF_VAR_PRIVATE_DATABASE_CONFIG }}' ||
  failures=$((failures + 1))
expect_rejected "printed private database config" \
  "production workflow must map but never print private database config" \
  insert_into_first_run_block "$production_workflow" \
  '          printf '\''%s\n'\'' "$TF_VAR_private_database_config"' ||
  failures=$((failures + 1))
expect_rejected "bare env environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          env' || failures=$((failures + 1))
expect_rejected "bare printenv environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          printenv' || failures=$((failures + 1))
expect_rejected "uploaded production plan artifact" \
  "production workflow must not publish Terraform plan artifacts" \
  sh -c 'printf "\n      - uses: actions/upload-artifact@0000000000000000000000000000000000000000\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "renamed origin EIP resource" \
  "Terraform resource is missing bound destroy protection: aws_eip.origin" \
  replace_once "$network_file" \
  'resource "aws_eip" "origin" {' \
  'resource "aws_eip" "renamed_origin" {' || failures=$((failures + 1))
expect_rejected "inline public route" \
  "Production public route table must not use inline route blocks." \
  insert_inline_route "$network_file" ||
  failures=$((failures + 1))
expect_rejected "origin EIP association resource" \
  "Production origin EIP must remain unassociated until cutover." \
  sh -c 'printf "\nresource \"aws_eip_association\" \"origin\" {}\n" >>"$1"' \
  sh "$network_file" || failures=$((failures + 1))
expect_rejected "explicit subnet route-table association resource" \
  "Production public subnets must inherit the VPC main route table." \
  sh -c 'printf "\nresource \"aws_route_table_association\" \"unexpected\" {}\n" >>"$1"' \
  sh "$network_file" || failures=$((failures + 1))
expect_rejected "main route-table association resource" \
  "Production public subnets must inherit the VPC main route table." \
  sh -c 'printf "\nresource \"aws_main_route_table_association\" \"unexpected\" {}\n" >>"$1"' \
  sh "$network_file" || failures=$((failures + 1))
expect_rejected "canonical public subnet validation message" \
  "Canonical public subnet validation message changed." \
  replace_once "$variables_file" \
  "Canonical public subnet keys must be public_a through public_d." \
  "Canonical public subnet keys must include four entries." ||
  failures=$((failures + 1))
expect_rejected "exact aws_route resource in production" \
  "Slice 3 production contains a forbidden Terraform resource type" \
  sh -c 'printf "\nresource \"aws_route\" \"database\" {}\n" >>"$1"' \
  sh "$database_file" || failures=$((failures + 1))
expect_rejected "NAT gateway resource in production" \
  "Slice 3 production contains a forbidden Terraform resource type" \
  sh -c 'printf "\nresource \"aws_nat_gateway\" \"unexpected\" {}\n" >>"$1"' \
  sh "$database_file" || failures=$((failures + 1))
expect_rejected "EC2 instance resource in production" \
  "Slice 3 production contains a forbidden Terraform resource type" \
  sh -c 'printf "\nresource \"aws_instance\" \"unexpected\" {}\n" >>"$1"' \
  sh "$database_file" || failures=$((failures + 1))
expect_rejected "secret version resource in production" \
  "Slice 3 production contains a forbidden Terraform resource type" \
  sh -c 'printf "\nresource \"aws_secretsmanager_secret_version\" \"unexpected\" {}\n" >>"$1"' \
  sh "$secrets_file" || failures=$((failures + 1))
expect_rejected "inline database route" \
  "Production database route table must remain empty" \
  replace_once "$database_file" \
  'resource "aws_route_table" "database" {' \
  $'resource "aws_route_table" "database" {\n  route {}' ||
  failures=$((failures + 1))
expect_rejected "renamed origin discovery tag" \
  "Origin security group discovery tag contract changed" \
  replace_once "$database_file" \
  '"jobcron:edge-target" = "origin-security-group"' \
  '"jobcron:edge-target-renamed" = "origin-security-group"' ||
  failures=$((failures + 1))
expect_rejected "origin discovery tag copied to database security group" \
  "Origin security group discovery tag contract changed" \
  replace_once "$database_file" \
  'tags   = {}' \
  'tags   = { "jobcron:edge-target" = "origin-security-group" }' ||
  failures=$((failures + 1))
expect_rejected "database subnet missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_subnet.database" \
  replace_once "$database_file" \
  'resource "aws_subnet" "database" {' \
  'resource "aws_subnet" "renamed_database" {' ||
  failures=$((failures + 1))
expect_rejected "database route table missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_route_table.database" \
  replace_once "$database_file" \
  'resource "aws_route_table" "database" {' \
  'resource "aws_route_table" "renamed_database" {' ||
  failures=$((failures + 1))
expect_rejected "origin security group missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_security_group.origin" \
  replace_once "$database_file" \
  'resource "aws_security_group" "origin" {' \
  'resource "aws_security_group" "renamed_origin" {' ||
  failures=$((failures + 1))
expect_rejected "database security group missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_security_group.database" \
  replace_once "$database_file" \
  'resource "aws_security_group" "database" {' \
  'resource "aws_security_group" "renamed_database" {' ||
  failures=$((failures + 1))
expect_rejected "database ingress missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_vpc_security_group_ingress_rule.database_postgresql_from_origin" \
  replace_once "$database_file" \
  'resource "aws_vpc_security_group_ingress_rule" "database_postgresql_from_origin" {' \
  'resource "aws_vpc_security_group_ingress_rule" "renamed" {' ||
  failures=$((failures + 1))
expect_rejected "RDS instance missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_db_instance.production" \
  replace_once "$database_file" \
  'resource "aws_db_instance" "production" {' \
  'resource "aws_db_instance" "renamed_production" {' ||
  failures=$((failures + 1))
expect_rejected "runtime secret missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_secretsmanager_secret.runtime" \
  replace_once "$secrets_file" \
  'resource "aws_secretsmanager_secret" "runtime" {' \
  'resource "aws_secretsmanager_secret" "renamed_runtime" {' ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle missing destroy protection" \
  "Terraform resource is missing bound destroy protection: aws_s3_bucket_lifecycle_configuration.recovery" \
  replace_once "$recovery_file" \
  'resource "aws_s3_bucket_lifecycle_configuration" "recovery" {' \
  'resource "aws_s3_bucket_lifecycle_configuration" "renamed_recovery" {' ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle missing versioning dependency" \
  "Recovery bucket lifecycle contract changed" \
  remove_exact_line "$recovery_file" \
  '  depends_on = [aws_s3_bucket_versioning.recovery]' ||
  failures=$((failures + 1))
expect_rejected "recovery verified lifecycle delay changed" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '      days = 14' \
  '      days = 15' || failures=$((failures + 1))
expect_rejected "recovery all-object lifecycle delay changed" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '      days = 90' \
  '      days = 91' || failures=$((failures + 1))
expect_rejected "recovery lifecycle noncurrent delay changed" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '      noncurrent_days = 1' \
  '      noncurrent_days = 2' || failures=$((failures + 1))
expect_rejected "recovery lifecycle retained-version exception" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '      noncurrent_days = 1' \
  $'      noncurrent_days = 1\n      newer_noncurrent_versions = 1' ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle third rule" \
  "Recovery bucket lifecycle contract changed" \
  sh -c 'printf "\n  rule { id = \"unexpected\" }\n" >>"$1"' \
  sh "$recovery_file" || failures=$((failures + 1))
expect_rejected "recovery lifecycle transition" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '    expiration {' \
  $'    transition {\n      days = 7\n      storage_class = \"GLACIER\"\n    }\n\n    expiration {' ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle delete-marker expiration" \
  "Recovery bucket lifecycle contract changed" \
  replace_once "$recovery_file" \
  '      days = 14' \
  $'      days = 14\n      expired_object_delete_marker = true' ||
  failures=$((failures + 1))
expect_rejected "wildcard network read action" \
  "Slice 2 network read policy actions differ from the approved ceiling." \
  replace_once "$identity_file" \
  '"ec2:DescribeVpcs"' \
  '"ec2:Describe*"' || failures=$((failures + 1))
expect_rejected "network write action" \
  "Slice 2 network read policy actions differ from the approved ceiling." \
  replace_once "$identity_file" \
  '"ec2:DescribeVpcs"' \
  '"ec2:CreateVpc"' || failures=$((failures + 1))
expect_rejected "semantic action tag" \
  "Terraform workflows must pin actions by full commit SHA" \
  replace_once "$static_workflow" "$checkout_sha" "v6.0.0" || failures=$((failures + 1))
expect_rejected "symbolic action ref" \
  "Terraform workflows must pin actions by full commit SHA" \
  replace_once "$static_workflow" "$checkout_sha" "latest" || failures=$((failures + 1))
expect_rejected "different full action SHA" \
  "Terraform workflows changed reviewed action pin" \
  replace_once "$static_workflow" "$checkout_sha" \
  "0000000000000000000000000000000000000000" || failures=$((failures + 1))
expect_rejected "different AWS credentials action SHA" \
  "Terraform workflows changed reviewed action pin" \
  replace_once "$production_workflow" "$aws_action_sha" \
  "0000000000000000000000000000000000000000" || failures=$((failures + 1))
expect_rejected "missing OIDC permission" \
  "production workflow must request an OIDC id-token" \
  replace_once "$production_workflow" \
  "id-token: write" \
  "id-token: read" || failures=$((failures + 1))
expect_rejected "disabled account ID masking" \
  "production workflow must mask the AWS account ID" \
  replace_once "$production_workflow" \
  "mask-aws-account-id: true" \
  "mask-aws-account-id: false" || failures=$((failures + 1))
expect_rejected "apply after -chdir" \
  "production workflow must remain plan-only" \
  sh -c 'printf "\n          terraform -chdir=infra/terraform/production apply\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "apply after double space" \
  "production workflow must remain plan-only" \
  sh -c 'printf "\n          terraform  apply\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "state rm" \
  "production workflow must remain plan-only" \
  sh -c 'printf "\n          terraform -chdir=infra/terraform/production state rm aws_instance.app\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "force unlock" \
  "production workflow must remain plan-only" \
  sh -c 'printf "\n          terraform -chdir=infra/terraform/production force-unlock -force example-lock\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "full destroy command" \
  "production workflow must remain plan-only" \
  sh -c 'printf "\n          terraform -chdir=infra/terraform/production destroy -auto-approve\n" >>"$1"' \
  sh "$production_workflow" || failures=$((failures + 1))
expect_rejected "misbound destroy protection" \
  "Terraform resource is missing bound destroy protection" \
  replace_once "$state_file" \
  'resource "aws_s3_bucket_versioning" "state" {' \
  'resource "aws_s3_bucket_versioning" "unprotected" {' || failures=$((failures + 1))
expect_rejected "TLS allow policy" \
  "State bucket TLS policy contract is incomplete" \
  replace_once "$state_file" \
  'effect = "Deny"' \
  'effect = "Allow"' || failures=$((failures + 1))

test "$failures" -eq 0
