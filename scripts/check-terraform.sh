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
