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
