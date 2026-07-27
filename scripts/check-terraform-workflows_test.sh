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
failures=0

reset_fixtures
if ! grep -Fq \
  "uses: aws-actions/configure-aws-credentials@$aws_action_sha" \
  "$production_workflow"; then
  printf 'FAIL: production workflow does not use the reviewed Node.js 24 AWS credentials action pin\n' >&2
  exit 1
fi
if ! run_checker; then
  printf 'FAIL: rejected the unmodified workflows\n' >&2
  cat "$fixture_root/checker.out" >&2
  exit 1
fi

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
expect_rejected "canonical public subnet validation message" \
  "Canonical public subnet validation message changed." \
  replace_once "$variables_file" \
  "Canonical public subnet keys must be public_a through public_d." \
  "Canonical public subnet keys must include four entries." ||
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
