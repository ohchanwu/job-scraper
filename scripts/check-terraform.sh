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
