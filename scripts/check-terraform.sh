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

test "$(grep -Fc 'prevent_destroy = true' "$state_file")" -eq 3
grep -Fq 'variable = "aws:SecureTransport"' "$state_file"
grep -Fq 'values   = ["false"]' "$state_file"

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

for forbidden in '"ec2:' '"rds:' '"iam:' '"secretsmanager:'; do
  if grep -Fiq "$forbidden" "$identity_file"; then
    printf 'Slice 1 identity policy contains forbidden action: %s\n' \
      "$forbidden" >&2
    exit 1
  fi
done

expected_policy_tokens="$(printf '%s\n' \
  '"s3:DeleteObject"' '"s3:DeleteObject"' \
  '"s3:GetBucketLocation"' '"s3:GetBucketLocation"' \
  '"s3:GetObject"' '"s3:GetObject"' \
  '"s3:ListBucket"' '"s3:ListBucket"' \
  '"s3:prefix"' '"s3:prefix"' \
  '"s3:PutObject"' '"s3:PutObject"' \
  '"sts:AssumeRoleWithWebIdentity"' '"sts:AssumeRoleWithWebIdentity"' |
  sort)"
actual_policy_tokens="$(
  grep -Eo '"[a-z0-9]+:[A-Za-z*]+"' "$identity_file" | sort
)"
if [[ "$actual_policy_tokens" != "$expected_policy_tokens" ]]; then
  printf 'Slice 1 identity policy actions differ from the approved ceiling.\n' >&2
  exit 1
fi

production_workflow="$repo_root/.github/workflows/terraform-production-plan.yml"
workflow_files=("$repo_root/.github/workflows/"terraform-*.yml)

grep -Fq 'id-token: write' "$production_workflow"
grep -Fq 'mask-aws-account-id: true' "$production_workflow"

if grep -Eq \
  '(^|[[:space:];|&])terraform([[:space:]]+-[^[:space:]]+)*[[:space:]]+apply([[:space:]]|$)' \
  "$production_workflow"; then
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
  1 "aws-actions/configure-aws-credentials@61815dcd50bd041e203e49132bacad1fd04d2708"

if [[ "${CHECK_TERRAFORM_FIXTURE_MODE:-0}" != 1 ]]; then
  "$repo_root/scripts/check-terraform-workflows_test.sh"
fi
