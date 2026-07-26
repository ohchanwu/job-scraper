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

for forbidden in '"ec2:' '"rds:' '"iam:' '"secretsmanager:'; do
  if grep -Fiq "$forbidden" "$identity_file"; then
    printf 'Slice 1 identity policy contains forbidden action: %s\n' \
      "$forbidden" >&2
    exit 1
  fi
done
