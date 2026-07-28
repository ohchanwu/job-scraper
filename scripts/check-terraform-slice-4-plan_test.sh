#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-terraform-slice-4-plan.sh"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

failures=0
generic_error="Terraform saved plan violates the Slice 4 contract"
expected_output='resource_changes=5
output_changes=1
sensitive_outputs=1
destroy_or_replace=0
aggregate_cost=PASS
slice3_checkpoint=PASS
PASS'

expect_verified() {
  local name="$1"
  local plan="$2"
  local cost="$3"
  local checkpoint="$4"
  local output

  if ! output="$("$checker" "$plan" "$cost" "$checkpoint" 2>&1)"; then
    printf 'FAIL: rejected valid %s fixture\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  if [[ "$output" != "$expected_output" ]]; then
    printf 'FAIL: valid %s fixture emitted unexpected output\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'PASS: verified %s fixture\n' "$name"
}

expect_rejected() {
  local name="$1"
  local plan="$2"
  local cost="$3"
  local checkpoint="$4"
  local output
  local rc

  set +e
  output="$("$checker" "$plan" "$cost" "$checkpoint" 2>&1)"
  rc=$?
  set -e

  if [[ "$rc" -eq 0 ]]; then
    printf 'FAIL: accepted %s\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  if [[ "$output" != "$generic_error" ]]; then
    printf 'FAIL: %s disclosed input or emitted a non-generic error\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'PASS: rejected %s without private output\n' "$name"
}

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

jq -n '{
  format_version: "1.2",
  resource_changes: (
    [
      "aws_iam_role.replacement_host",
      "aws_iam_role_policy_attachment.replacement_host_ssm",
      "aws_iam_role_policy.replacement_host_runtime",
      "aws_iam_instance_profile.replacement_host",
      "aws_instance.replacement_host"
    ] |
    map({
      address: .,
      previous_address: null,
      change: {
        actions: ["create"],
        importing: null,
        before: null,
        after: {synthetic: true}
      }
    })
  ) + (
    [
      "aws_vpc.canonical",
      "aws_internet_gateway.canonical",
      "aws_subnet.public[\"public_a\"]",
      "aws_subnet.public[\"public_b\"]",
      "aws_subnet.public[\"public_c\"]",
      "aws_subnet.public[\"public_d\"]",
      "aws_route_table.public",
      "aws_route.public_ipv4_default",
      "aws_eip.origin",
      "aws_subnet.database[\"database_a\"]",
      "aws_subnet.database[\"database_b\"]",
      "aws_route_table.database",
      "aws_route_table_association.database[\"database_a\"]",
      "aws_route_table_association.database[\"database_b\"]",
      "aws_security_group.origin",
      "aws_security_group.database",
      "aws_vpc_security_group_ingress_rule.database_postgresql_from_origin",
      "aws_db_subnet_group.production",
      "aws_db_parameter_group.production",
      "aws_db_instance.production",
      "aws_secretsmanager_secret.runtime",
      "aws_s3_bucket.recovery",
      "aws_s3_bucket_public_access_block.recovery",
      "aws_s3_bucket_versioning.recovery",
      "aws_s3_bucket_server_side_encryption_configuration.recovery",
      "aws_s3_bucket_policy.recovery",
      "aws_s3_bucket_lifecycle_configuration.recovery"
    ] |
    map({
      address: .,
      previous_address: null,
      change: {
        actions: ["no-op"],
        importing: null,
        before: {synthetic: true},
        after: {synthetic: true}
      }
    })
  ),
  output_changes: {
    replacement_instance_id: {
      actions: ["create"],
      before: null,
      after: "synthetic-sensitive-selector",
      after_sensitive: true
    }
  },
  diagnostics: []
}' >"$fixture_root/plan-valid.json"

jq -n --arg checked_at "$now" '{
  checked_at: $checked_at,
  currency: "USD",
  aggregate: {
    recurring_monthly_upper_bound: 98,
    one_time_upper_bound: 190
  },
  categories: [
    {
      name: "aws_compute",
      source: "AWS pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 20,
      one_time_upper_bound: 10
    },
    {
      name: "public_ipv4",
      source: "AWS pricing",
      quantity: 2,
      recurring_monthly_upper_bound: 10,
      one_time_upper_bound: 0
    },
    {
      name: "database",
      source: "AWS pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 30,
      one_time_upper_bound: 0
    },
    {
      name: "storage",
      source: "AWS pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 8,
      one_time_upper_bound: 10
    },
    {
      name: "backup",
      source: "AWS pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 10,
      one_time_upper_bound: 20
    },
    {
      name: "registry",
      source: "GitHub pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 10,
      one_time_upper_bound: 50
    },
    {
      name: "cloudflare",
      source: "Cloudflare pricing",
      quantity: 1,
      recurring_monthly_upper_bound: 10,
      one_time_upper_bound: 100
    }
  ]
}' >"$fixture_root/cost-valid.json"

jq -n --arg checked_at "$now" '{
  checked_at: $checked_at,
  commit: "0123456789abcdef0123456789abcdef01234567",
  verdict: "PASS",
  post_apply_plan: "clean",
  addresses: [
    "aws_db_instance.production",
    "aws_security_group.origin",
    "aws_security_group.database",
    "aws_vpc_security_group_ingress_rule.database_postgresql_from_origin",
    "aws_secretsmanager_secret.runtime",
    "aws_s3_bucket.recovery",
    "aws_s3_bucket_lifecycle_configuration.recovery"
  ],
  private_rds: true,
  runtime_secret_versions: 0,
  recovery_bucket: {
    encrypted: true,
    versioned: true,
    public_access_blocked: true,
    verified_retention_days: 14,
    unverified_retention_days: 90
  },
  recovery_lifecycle_verdict: "PASS",
  destroy_or_replace: 0,
  old_resource_changes: 0
}' >"$fixture_root/checkpoint-valid.json"

plan_mutation() {
  local name="$1"
  local filter="$2"

  jq "$filter" "$fixture_root/plan-valid.json" \
    >"$fixture_root/plan-$name.json"
  expect_rejected \
    "$name" \
    "$fixture_root/plan-$name.json" \
    "$fixture_root/cost-valid.json" \
    "$fixture_root/checkpoint-valid.json"
}

cost_mutation() {
  local name="$1"
  local filter="$2"

  jq "$filter" "$fixture_root/cost-valid.json" \
    >"$fixture_root/cost-$name.json"
  expect_rejected \
    "$name" \
    "$fixture_root/plan-valid.json" \
    "$fixture_root/cost-$name.json" \
    "$fixture_root/checkpoint-valid.json"
}

checkpoint_mutation() {
  local name="$1"
  local filter="$2"

  jq "$filter" "$fixture_root/checkpoint-valid.json" \
    >"$fixture_root/checkpoint-$name.json"
  expect_rejected \
    "$name" \
    "$fixture_root/plan-valid.json" \
    "$fixture_root/cost-valid.json" \
    "$fixture_root/checkpoint-$name.json"
}

expect_verified \
  "exact Slice 4" \
  "$fixture_root/plan-valid.json" \
  "$fixture_root/cost-valid.json" \
  "$fixture_root/checkpoint-valid.json"

plan_mutation "unexpected address" \
  '.resource_changes[0].address = "aws_instance.unexpected"'
plan_mutation "unexpected no-op address" \
  '.resource_changes += [{
    address: "aws_instance.old",
    previous_address: null,
    change: {
      actions: ["no-op"],
      importing: null,
      before: {synthetic: true},
      after: {synthetic: true}
    }
  }]'
for action in update delete forget mystery; do
  plan_mutation "$action action" \
    ".resource_changes[0].change.actions = [\"$action\"]"
done
plan_mutation "replace action" \
  '.resource_changes[0].change.actions = ["delete", "create"]'
plan_mutation "import action" \
  '.resource_changes[0].change.importing = {id: "synthetic-private-import"}'
plan_mutation "move action" \
  '.resource_changes[0].previous_address = "aws_iam_role.old"'
plan_mutation "port 22" \
  '.resource_changes[4].change.after.from_port = 22'

for address in \
  aws_security_group.unexpected \
  aws_vpc_security_group_ingress_rule.unexpected \
  aws_eip_association.unexpected \
  aws_key_pair.unexpected \
  aws_db_instance.unexpected \
  aws_secretsmanager_secret_version.unexpected \
  aws_instance.old \
  cloudflare_record.unexpected \
  aws_route53_record.unexpected; do
  jq --arg address "$address" \
    '.resource_changes += [{
      address: $address,
      previous_address: null,
      change: {
        actions: ["create"],
        importing: null,
        before: null,
        after: {synthetic: true}
      }
    }]' "$fixture_root/plan-valid.json" \
    >"$fixture_root/plan-forbidden.json"
  expect_rejected \
    "forbidden $address" \
    "$fixture_root/plan-forbidden.json" \
    "$fixture_root/cost-valid.json" \
    "$fixture_root/checkpoint-valid.json"
done

plan_mutation "missing output" \
  'del(.output_changes.replacement_instance_id)'
plan_mutation "renamed output" \
  '.output_changes.renamed = .output_changes.replacement_instance_id |
   del(.output_changes.replacement_instance_id)'
plan_mutation "unexpected output" \
  '.output_changes.unexpected = .output_changes.replacement_instance_id'
plan_mutation "non-sensitive output" \
  '.output_changes.replacement_instance_id.after_sensitive = false'
plan_mutation "updated output" \
  '.output_changes.replacement_instance_id.actions = ["update"]'
plan_mutation "private diagnostic marker" \
  '.diagnostics = [{summary: "PRIVATE_VALUE must never be printed"}]'

cost_mutation "recurring ceiling" \
  '.aggregate.recurring_monthly_upper_bound = 101'
cost_mutation "one-time ceiling" \
  '.aggregate.one_time_upper_bound = 201'
cost_mutation "understated aggregate" \
  '.aggregate.recurring_monthly_upper_bound = 1'
cost_mutation "stale cost evidence" \
  '.checked_at = "2000-01-01T00:00:00Z"'
cost_mutation "missing cost category" \
  '.categories |= map(select(.name != "cloudflare"))'
cost_mutation "duplicate cost category" \
  '.categories += [.categories[0]]'
cost_mutation "missing pricing source" \
  '.categories[0].source = ""'

checkpoint_mutation "stale Slice 3 checkpoint" \
  '.checked_at = "2000-01-01T00:00:00Z"'
checkpoint_mutation "missing Slice 3 address" \
  '.addresses |= map(select(. != "aws_db_instance.production"))'
checkpoint_mutation "renamed Slice 3 address" \
  '.addresses[0] = "aws_db_instance.renamed"'
checkpoint_mutation "dirty Slice 3 post-apply plan" \
  '.post_apply_plan = "changes"'
checkpoint_mutation "failed recovery lifecycle" \
  '.recovery_lifecycle_verdict = "FAIL"'
checkpoint_mutation "short verified retention" \
  '.recovery_bucket.verified_retention_days = 13'
checkpoint_mutation "short unverified retention" \
  '.recovery_bucket.unverified_retention_days = 89'
checkpoint_mutation "Slice 3 destroy" \
  '.destroy_or_replace = 1'
checkpoint_mutation "old resource change" \
  '.old_resource_changes = 1'

printf '{malformed' >"$fixture_root/plan-malformed.json"
expect_rejected \
  "malformed Terraform output" \
  "$fixture_root/plan-malformed.json" \
  "$fixture_root/cost-valid.json" \
  "$fixture_root/checkpoint-valid.json"

printf '{malformed' >"$fixture_root/cost-malformed.json"
expect_rejected \
  "malformed cost evidence" \
  "$fixture_root/plan-valid.json" \
  "$fixture_root/cost-malformed.json" \
  "$fixture_root/checkpoint-valid.json"

printf '{malformed' >"$fixture_root/checkpoint-malformed.json"
expect_rejected \
  "malformed Slice 3 checkpoint" \
  "$fixture_root/plan-valid.json" \
  "$fixture_root/cost-valid.json" \
  "$fixture_root/checkpoint-malformed.json"

if [[ "$failures" -ne 0 ]]; then
  printf '%d Slice 4 checker test(s) failed\n' "$failures" >&2
  exit 1
fi

printf 'All Terraform Slice 4 plan checker tests passed\n'
