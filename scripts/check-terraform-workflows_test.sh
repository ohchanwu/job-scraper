#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

mkdir -p \
  "$fixture_root/repo/.github/workflows" \
  "$fixture_root/repo/infra/terraform/bootstrap" \
  "$fixture_root/repo/infra/terraform/edge" \
  "$fixture_root/repo/infra/terraform/production" \
  "$fixture_root/repo/scripts" \
  "$fixture_root/bin"
cp "$repo_root/scripts/check-terraform.sh" "$fixture_root/repo/scripts/"
cp "$repo_root/infra/terraform/bootstrap/state.tf" \
  "$fixture_root/repo/infra/terraform/bootstrap/"
cp "$repo_root/infra/terraform/bootstrap/identity.tf" \
  "$fixture_root/repo/infra/terraform/bootstrap/"
cp "$repo_root/infra/terraform/edge/cloudflare.tf" \
  "$fixture_root/repo/infra/terraform/edge/"
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
cp "$repo_root/infra/terraform/production/compute.tf" \
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
  cp "$repo_root/infra/terraform/edge/cloudflare.tf" \
    "$fixture_root/repo/infra/terraform/edge/"
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
  cp "$repo_root/infra/terraform/production/compute.tf" \
    "$fixture_root/repo/infra/terraform/production/"
}

replace_once() {
  local file="$1"
  local old="$2"
  local new="$3"

  awk -v old="$old" -v new="$new" '
    !replaced && index($0, old) {
      start = index($0, old)
      $0 = substr($0, 1, start - 1) new substr($0, start + length(old))
      replaced = 1
    }
    { print }
    END {
      if (!replaced) {
        exit 1
      }
    }
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

replace_in_block() {
  local file="$1"
  local header="$2"
  local old="$3"
  local new="$4"

  awk -v header="$header" -v old="$old" -v new="$new" '
    $0 == header {
      in_resource = 1
      depth = 0
    }
    in_resource && !replaced && index($0, old) {
      start = index($0, old)
      $0 = substr($0, 1, start - 1) new substr($0, start + length(old))
      replaced = 1
    }
    {
      print
      if (in_resource) {
        line = $0
        depth += gsub(/\{/, "{", line) - gsub(/\}/, "}", line)
        if (depth == 0) {
          in_resource = 0
        }
      }
    }
    END { if (!replaced) exit 1 }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

remove_condition() {
  local file="$1"
  local variable="$2"
  local occurrence="${3:-1}"

  awk -v variable="$variable" -v occurrence="$occurrence" '
    !in_condition && $0 ~ /^[[:space:]]*condition \{$/ {
      in_condition = 1
      block = $0 ORS
      next
    }
    in_condition {
      block = block $0 ORS
      if (index($0, "variable = \"" variable "\"")) {
        matched = 1
      }
      if ($0 ~ /^[[:space:]]*\}$/) {
        if (matched) {
          matches++
        }
        if (!matched || matches != occurrence) {
          printf "%s", block
        } else {
          removed = 1
        }
        in_condition = 0
        matched = 0
        block = ""
      }
      next
    }
    { print }
    END { if (!removed) exit 1 }
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

insert_inline_database_route() {
  local file="$1"

  awk '
    !inserted && $0 == "resource \"aws_route_table\" \"database\" {" {
      print
      print "  route {}"
      inserted = 1
      next
    }
    { print }
    END { if (!inserted) exit 1 }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_lifecycle_newer_noncurrent_versions() {
  local file="$1"

  awk '
    !inserted && $0 == "      noncurrent_days = 1" {
      print
      print "      newer_noncurrent_versions = 1"
      inserted = 1
      next
    }
    { print }
    END { if (!inserted) exit 1 }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_lifecycle_third_rule() {
  local file="$1"

  awk '
    $0 == "resource \"aws_s3_bucket_lifecycle_configuration\" \"recovery\" {" {
      in_resource = 1
    }
    in_resource && !inserted && $0 == "  lifecycle {" {
      print "  rule {"
      print "    id     = \"unexpected\""
      print "    status = \"Enabled\""
      print "    filter {}"
      print "  }"
      print ""
      inserted = 1
    }
    { print }
    END { if (!inserted) exit 1 }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_lifecycle_transition() {
  local file="$1"

  awk '
    !inserted && $0 == "    expiration {" {
      print "    transition {"
      print "      days          = 7"
      print "      storage_class = \"GLACIER\""
      print "    }"
      print ""
      inserted = 1
    }
    { print }
    END { if (!inserted) exit 1 }
  ' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

insert_lifecycle_delete_marker() {
  local file="$1"

  awk '
    !inserted && $0 == "      days = 14" {
      print
      print "      expired_object_delete_marker = true"
      inserted = 1
      next
    }
    { print }
    END { if (!inserted) exit 1 }
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
  if ! "$@"; then
    printf 'FAIL: mutation setup failed for %s\n' "$name" >&2
    return 1
  fi
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
edge_workflow="$fixture_root/repo/.github/workflows/terraform-edge-prefix-list.yml"
state_file="$fixture_root/repo/infra/terraform/bootstrap/state.tf"
identity_file="$fixture_root/repo/infra/terraform/bootstrap/identity.tf"
cloudflare_file="$fixture_root/repo/infra/terraform/edge/cloudflare.tf"
network_file="$fixture_root/repo/infra/terraform/production/network.tf"
variables_file="$fixture_root/repo/infra/terraform/production/variables.tf"
database_file="$fixture_root/repo/infra/terraform/production/database.tf"
secrets_file="$fixture_root/repo/infra/terraform/production/secrets.tf"
recovery_file="$fixture_root/repo/infra/terraform/production/recovery.tf"
failures=0

reset_fixtures
if [[ ! -f "$edge_workflow" ]]; then
  printf 'FAIL: edge prefix-list workflow is absent\n' >&2
  exit 1
fi
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

edge_error="edge prefix-list workflow violates the reviewed contract"
expect_rejected "missing edge schedule" "$edge_error" \
  remove_exact_line "$edge_workflow" \
  '    - cron: "17 18 * * *"' || failures=$((failures + 1))
expect_rejected "missing edge manual trigger" "$edge_error" \
  replace_once "$edge_workflow" "workflow_dispatch:" "push:" ||
  failures=$((failures + 1))
expect_rejected "changed official Cloudflare URL" "$edge_error" \
  replace_once "$edge_workflow" \
  "https://www.cloudflare.com/ips-v4" \
  "https://example.com/ips-v4" || failures=$((failures + 1))
expect_rejected "disabled edge automation gate" "$edge_error" \
  replace_once "$edge_workflow" \
  "vars.EDGE_AUTOMATION_ENABLED == 'true'" \
  "vars.EDGE_AUTOMATION_ENABLED == 'false'" || failures=$((failures + 1))
expect_rejected "wrong edge environment" "$edge_error" \
  replace_once "$edge_workflow" \
  "environment: edge" "environment: production" ||
  failures=$((failures + 1))
expect_rejected "missing protected Slice 4 checkpoint" "$edge_error" \
  remove_exact_line "$edge_workflow" \
  '          TF_SLICE4_CHECKPOINT_JSON: ${{ secrets.TF_SLICE4_CHECKPOINT_JSON }}' ||
  failures=$((failures + 1))
expect_rejected "repository-variable Slice 4 checkpoint" "$edge_error" \
  replace_once "$edge_workflow" \
  'secrets.TF_SLICE4_CHECKPOINT_JSON' \
  'vars.TF_SLICE4_CHECKPOINT_JSON' || failures=$((failures + 1))
expect_rejected "job-wide state bucket secret" "$edge_error" \
  replace_once "$edge_workflow" \
  '          TF_STATE_BUCKET: ${{ secrets.TF_STATE_BUCKET }}' \
  '      TF_STATE_BUCKET: ${{ secrets.TF_STATE_BUCKET }}' ||
  failures=$((failures + 1))
expect_rejected "job-wide cost evidence secret" "$edge_error" \
  replace_once "$edge_workflow" \
  '          TF_AGGREGATE_COST_JSON: ${{ secrets.TF_AGGREGATE_COST_JSON }}' \
  '      TF_AGGREGATE_COST_JSON: ${{ secrets.TF_AGGREGATE_COST_JSON }}' ||
  failures=$((failures + 1))
expect_rejected "job-wide checkpoint secret" "$edge_error" \
  replace_once "$edge_workflow" \
  '          TF_SLICE4_CHECKPOINT_JSON: ${{ secrets.TF_SLICE4_CHECKPOINT_JSON }}' \
  '      TF_SLICE4_CHECKPOINT_JSON: ${{ secrets.TF_SLICE4_CHECKPOINT_JSON }}' ||
  failures=$((failures + 1))
expect_rejected "missing private-file umask" "$edge_error" \
  remove_exact_line "$edge_workflow" \
  '          umask 077' || failures=$((failures + 1))
expect_rejected "TF_DATA_DIR outside runner temp" "$edge_error" \
  replace_once "$edge_workflow" \
  'TF_DATA_DIR: ${{ runner.temp }}/terraform-data' \
  'TF_DATA_DIR: /tmp/terraform-data' || failures=$((failures + 1))
expect_rejected "Slice 4 checkpoint outside runner temp" "$edge_error" \
  replace_once "$edge_workflow" \
  '${RUNNER_TEMP}/slice-4-checkpoint.json' \
  'slice-4-checkpoint.json' || failures=$((failures + 1))
expect_rejected "printed Slice 4 checkpoint" "$edge_error" \
  insert_into_first_run_block "$edge_workflow" \
  '          printf '\''%s\n'\'' "$TF_SLICE4_CHECKPOINT_JSON"' ||
  failures=$((failures + 1))
expect_rejected "extra edge workflow permission" "$edge_error" \
  replace_once "$edge_workflow" \
  "contents: read" "contents: write" || failures=$((failures + 1))
expect_rejected "wrong edge checkout pin" "$edge_error" \
  replace_once "$edge_workflow" "$checkout_sha" \
  "0000000000000000000000000000000000000000" ||
  failures=$((failures + 1))
expect_rejected "wrong edge AWS action pin" "$edge_error" \
  replace_once "$edge_workflow" "$aws_action_sha" \
  "0000000000000000000000000000000000000000" ||
  failures=$((failures + 1))
expect_rejected "disabled edge account masking" "$edge_error" \
  replace_once "$edge_workflow" \
  "mask-aws-account-id: true" "mask-aws-account-id: false" ||
  failures=$((failures + 1))
expect_rejected "wrong edge concurrency group" "$edge_error" \
  replace_once "$edge_workflow" \
  "group: terraform-edge-prefix-list" "group: terraform-edge" ||
  failures=$((failures + 1))
expect_rejected "cancelled in-progress edge run" "$edge_error" \
  replace_once "$edge_workflow" \
  "cancel-in-progress: false" "cancel-in-progress: true" ||
  failures=$((failures + 1))
expect_rejected "raw response outside runner temp" "$edge_error" \
  replace_once "$edge_workflow" \
  '${RUNNER_TEMP}/cloudflare-ips-v4.txt' \
  'cloudflare-ips-v4.txt' || failures=$((failures + 1))
expect_rejected "missing detailed exit code" "$edge_error" \
  remove_exact_line "$edge_workflow" \
  '            -detailed-exitcode \' || failures=$((failures + 1))
expect_rejected "no-change branch no longer exits" "$edge_error" \
  replace_once "$edge_workflow" \
  'if [[ "$plan_rc" -eq 0 ]]; then' \
  'if [[ "$plan_rc" -eq 9 ]]; then' || failures=$((failures + 1))
expect_rejected "change branch no longer requires exit 2" "$edge_error" \
  replace_once "$edge_workflow" \
  'if [[ "$plan_rc" -ne 2 ]]; then' \
  'if [[ "$plan_rc" -ne 3 ]]; then' || failures=$((failures + 1))
expect_rejected "missing refresh checker" "$edge_error" \
  remove_exact_line "$edge_workflow" \
  '          python3 scripts/check-terraform-slice-5-plan.py \' ||
  failures=$((failures + 1))
expect_rejected "unsaved edge apply" "$edge_error" \
  replace_once "$edge_workflow" \
  '          if terraform -chdir=infra/terraform/edge apply -input=false \' \
  '          if terraform -chdir=infra/terraform/edge apply -input=false' ||
  failures=$((failures + 1))
expect_rejected "uploaded edge artifact" "$edge_error" \
  sh -c 'printf "\n      - uses: actions/upload-artifact@0000000000000000000000000000000000000000\n" >>"$1"' \
  sh "$edge_workflow" || failures=$((failures + 1))
expect_rejected "cached edge artifact" "$edge_error" \
  sh -c 'printf "\n      - uses: actions/cache@0000000000000000000000000000000000000000\n" >>"$1"' \
  sh "$edge_workflow" || failures=$((failures + 1))
expect_rejected "edge environment dump" "$edge_error" \
  insert_into_first_run_block "$edge_workflow" \
  '          env | sort' || failures=$((failures + 1))
expect_rejected "edge printenv dump" "$edge_error" \
  insert_into_first_run_block "$edge_workflow" \
  '          printenv' || failures=$((failures + 1))
expect_rejected "printed edge plan body" "$edge_error" \
  replace_once "$edge_workflow" \
  '            >"${RUNNER_TEMP}/edge-plan.log" 2>&1' \
  '            2>&1' || failures=$((failures + 1))
expect_rejected "visible Terraform init output" "$edge_error" \
  replace_once "$edge_workflow" \
  '            >"${RUNNER_TEMP}/edge-init.log" 2>&1; then' \
  '            ; then' || failures=$((failures + 1))
expect_rejected "visible Terraform apply output" "$edge_error" \
  replace_once "$edge_workflow" \
  '            >"${RUNNER_TEMP}/edge-apply.log" 2>&1; then' \
  '            ; then' || failures=$((failures + 1))
expect_rejected "missing fixed init status" "$edge_error" \
  replace_once "$edge_workflow" \
  "Terraform edge initialization succeeded" \
  "Terraform edge initialization complete" ||
  failures=$((failures + 1))
expect_rejected "missing fixed init failure status" "$edge_error" \
  replace_once "$edge_workflow" \
  "Terraform edge initialization failed" \
  "Terraform edge initialization error" ||
  failures=$((failures + 1))
expect_rejected "missing fixed apply status" "$edge_error" \
  replace_once "$edge_workflow" \
  "Terraform edge refresh applied" \
  "Terraform edge refresh complete" ||
  failures=$((failures + 1))
expect_rejected "missing fixed apply failure status" "$edge_error" \
  replace_once "$edge_workflow" \
  "Terraform edge refresh apply failed" \
  "Terraform edge refresh apply error" ||
  failures=$((failures + 1))
expect_rejected "tailed private Terraform log" "$edge_error" \
  insert_into_first_run_block "$edge_workflow" \
  '          tail "${RUNNER_TEMP}/edge-init.log"' ||
  failures=$((failures + 1))
expect_rejected "production root in edge workflow" "$edge_error" \
  replace_once "$edge_workflow" \
  "infra/terraform/edge" "infra/terraform/production" ||
  failures=$((failures + 1))
expect_rejected "Cloudflare credential in edge workflow" "$edge_error" \
  insert_into_first_run_block "$edge_workflow" \
  '          CLOUDFLARE_API_TOKEN=synthetic' ||
  failures=$((failures + 1))
expect_rejected "Cloudflare action in edge workflow" "$edge_error" \
  sh -c 'printf "\n      - uses: cloudflare/wrangler-action@0000000000000000000000000000000000000000\n" >>"$1"' \
  sh "$edge_workflow" || failures=$((failures + 1))

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
expect_rejected "piped env environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          env | sort' || failures=$((failures + 1))
expect_rejected "piped printenv environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          printenv | sed -n '\''1,10p'\''' || failures=$((failures + 1))
expect_rejected "argument printenv environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          printenv PATH' ||
  failures=$((failures + 1))
expect_rejected "argument env environment dump" \
  "production workflow must map but never print private network config" \
  insert_into_first_run_block "$production_workflow" \
  '          env -0' || failures=$((failures + 1))

reset_fixtures
insert_into_first_run_block "$production_workflow" \
  '          printf '\''env | sort is forbidden\n'\'''
if ! run_checker; then
  printf 'FAIL: rejected ordinary text containing env command words\n' >&2
  failures=$((failures + 1))
else
  printf 'PASS: accepted ordinary text containing env command words\n'
fi
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
  insert_inline_database_route "$database_file" ||
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
  insert_lifecycle_newer_noncurrent_versions "$recovery_file" ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle third rule" \
  "Recovery bucket lifecycle contract changed" \
  insert_lifecycle_third_rule "$recovery_file" ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle transition" \
  "Recovery bucket lifecycle contract changed" \
  insert_lifecycle_transition "$recovery_file" ||
  failures=$((failures + 1))
expect_rejected "recovery lifecycle delete-marker expiration" \
  "Recovery bucket lifecycle contract changed" \
  insert_lifecycle_delete_marker "$recovery_file" ||
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
expect_rejected "wildcard edge action" \
  "Slice 5 edge policy contract changed" \
  replace_in_block "$identity_file" \
  'data "aws_iam_policy_document" "edge_prefix_list" {' \
  '"ec2:DescribeTags",' \
  '"ec2:*",' || failures=$((failures + 1))
expect_rejected "prefix-list entries read on wildcard resource" \
  "Slice 5 edge policy contract changed" \
  replace_once "$identity_file" \
  'resources = [local.edge_prefix_list_arn]' \
  'resources = ["*"]' || failures=$((failures + 1))
expect_rejected "prefix-list entries read grouped with describes" \
  "Slice 5 edge policy contract changed" \
  replace_in_block "$identity_file" \
  'data "aws_iam_policy_document" "edge_prefix_list" {' \
  '"ec2:DescribeTags",' \
  '"ec2:DescribeTags", "ec2:GetManagedPrefixListEntries",' ||
  failures=$((failures + 1))
expect_rejected "missing prefix-list resource tag condition" \
  "Slice 5 edge policy contract changed" \
  remove_condition "$identity_file" \
  "aws:ResourceTag/jobcron:edge-source" || failures=$((failures + 1))
expect_rejected "wrong prefix-list resource tag value" \
  "Slice 5 edge policy contract changed" \
  replace_once "$identity_file" \
  'values   = ["cloudflare-ipv4"]' \
  'values   = ["other-source"]' || failures=$((failures + 1))
for forbidden_action in \
  "ec2:CreateSecurityGroup" \
  "ec2:DeleteManagedPrefixList" \
  "ec2:RevokeSecurityGroupIngress" \
  "ec2:DeleteTags"; do
  expect_rejected "$forbidden_action" \
    "Slice 5 edge policy contract changed" \
    replace_once "$identity_file" \
    '"ec2:ModifyManagedPrefixList"' \
    "\"$forbidden_action\"" || failures=$((failures + 1))
done
expect_rejected "unconditioned managed prefix-list creation" \
  "Slice 5 edge policy contract changed" \
  remove_condition "$identity_file" \
  "aws:RequestTag/jobcron:edge-source" || failures=$((failures + 1))
expect_rejected "unconditioned managed prefix-list modification" \
  "Slice 5 edge policy contract changed" \
  remove_condition "$identity_file" \
  "aws:ResourceTag/jobcron:edge-source" 2 || failures=$((failures + 1))
expect_rejected "unconditioned ingress authorization" \
  "Slice 5 edge policy contract changed" \
  remove_condition "$identity_file" \
  "aws:ResourceTag/jobcron:edge-target" || failures=$((failures + 1))
expect_rejected "additional managed prefix-list tag key" \
  "Slice 5 edge policy contract changed" \
  replace_once "$identity_file" \
  'values   = ["jobcron:edge-source"]' \
  'values   = ["jobcron:edge-source", "Name"]' ||
  failures=$((failures + 1))
expect_rejected "wrong origin semantic tag" \
  "Slice 5 edge policy contract changed" \
  replace_once "$identity_file" \
  'values   = ["origin-security-group"]' \
  'values   = ["other-security-group"]' || failures=$((failures + 1))
expect_rejected "edge policy attached to production role" \
  "Slice 5 edge policy contract changed" \
  replace_in_block "$identity_file" \
  'resource "aws_iam_role_policy_attachment" "edge_prefix_list" {' \
  'aws_iam_role.edge.name' \
  'aws_iam_role.production.name' || failures=$((failures + 1))
expect_rejected "missing edge policy destroy protection" \
  "Terraform resource is missing bound destroy protection" \
  replace_in_block "$identity_file" \
  'resource "aws_iam_policy" "edge_prefix_list" {' \
  'prevent_destroy = true' \
  'prevent_destroy = false' || failures=$((failures + 1))
expect_rejected "missing prefix-list destroy protection" \
  "Terraform resource is missing bound destroy protection" \
  replace_in_block "$cloudflare_file" \
  'resource "aws_ec2_managed_prefix_list" "cloudflare_ipv4" {' \
  'prevent_destroy = true' \
  'prevent_destroy = false' || failures=$((failures + 1))
expect_rejected "missing ingress-rule destroy protection" \
  "Terraform resource is missing bound destroy protection" \
  replace_in_block "$cloudflare_file" \
  'resource "aws_vpc_security_group_ingress_rule" "origin_https_from_cloudflare" {' \
  'prevent_destroy = true' \
  'prevent_destroy = false' || failures=$((failures + 1))
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
