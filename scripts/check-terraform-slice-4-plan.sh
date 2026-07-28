#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'Terraform saved plan violates the Slice 4 contract\n' >&2
  exit 1
}

[[ "$#" -eq 3 ]] || fail

plan_json="$1"
cost_json="$2"
checkpoint_json="$3"

command -v jq >/dev/null 2>&1 || fail
[[ -f "$plan_json" && -f "$cost_json" && -f "$checkpoint_json" ]] || fail

jq -e '
  [
    "aws_iam_role.replacement_host",
    "aws_iam_role_policy_attachment.replacement_host_ssm",
    "aws_iam_role_policy.replacement_host_runtime",
    "aws_iam_instance_profile.replacement_host",
    "aws_instance.replacement_host"
  ] as $allowed |
  (.resource_changes | type == "array") and
  (.resource_changes | length == 5) and
  ([.resource_changes[].address] | sort == ($allowed | sort)) and
  ([.resource_changes[].address] | length == (unique | length)) and
  all(
    .resource_changes[];
    (.address | type == "string") and
    (.previous_address // null) == null and
    (.change | type == "object") and
    .change.actions == ["create"] and
    (.change.importing // null) == null and
    all(
      [(.change.after // {}) | .. | objects | .from_port?, .to_port?][];
      . != 22
    )
  ) and
  (.output_changes | type == "object") and
  (.output_changes | keys == ["replacement_instance_id"]) and
  (.output_changes.replacement_instance_id | type == "object") and
  (.output_changes.replacement_instance_id.actions == ["create"]) and
  (.output_changes.replacement_instance_id.after_sensitive == true) and
  (
    (.diagnostics // []) |
    type == "array" and
    all(
      .[];
      (
        tostring |
        test("private[-_ ]?value|secret[-_ ]?value|sensitive[-_ ]?value"; "i") |
        not
      )
    )
  )
' "$plan_json" >/dev/null 2>&1 || fail

jq -e '
  [
    "aws_compute",
    "public_ipv4",
    "database",
    "storage",
    "backup",
    "registry",
    "cloudflare"
  ] as $required |
  . as $cost |
  (try ($cost.checked_at | fromdateiso8601) catch null) as $checked |
  ([$cost.categories[]?.recurring_monthly_upper_bound] | add) as $recurring_sum |
  ([$cost.categories[]?.one_time_upper_bound] | add) as $one_time_sum |
  ($cost.checked_at | type == "string") and
  (
    ($checked != null) and
    ((now - $checked) >= 0) and
    ((now - $checked) <= 86400)
  ) and
  ($cost.currency == "USD") and
  ($cost.categories | type == "array") and
  ($cost.categories | length == 7) and
  ([$cost.categories[].name] | sort == ($required | sort)) and
  ([$cost.categories[].name] | length == (unique | length)) and
  all(
    $cost.categories[];
    (.name | type == "string") and
    (.source | type == "string") and
    (.source | test("\\S")) and
    (.quantity | type == "number") and
    (.quantity >= 0) and
    (.recurring_monthly_upper_bound | type == "number") and
    (.recurring_monthly_upper_bound >= 0) and
    (.one_time_upper_bound | type == "number") and
    (.one_time_upper_bound >= 0)
  ) and
  ($cost.aggregate | type == "object") and
  ($cost.aggregate.recurring_monthly_upper_bound | type == "number") and
  ($cost.aggregate.one_time_upper_bound | type == "number") and
  ($cost.aggregate.recurring_monthly_upper_bound >= $recurring_sum) and
  ($cost.aggregate.one_time_upper_bound >= $one_time_sum) and
  ($cost.aggregate.recurring_monthly_upper_bound <= 100) and
  ($cost.aggregate.one_time_upper_bound <= 200)
' "$cost_json" >/dev/null 2>&1 || fail

jq -e '
  [
    "aws_db_instance.production",
    "aws_security_group.origin",
    "aws_security_group.database",
    "aws_vpc_security_group_ingress_rule.database_postgresql_from_origin",
    "aws_secretsmanager_secret.runtime",
    "aws_s3_bucket.recovery",
    "aws_s3_bucket_lifecycle_configuration.recovery"
  ] as $required_addresses |
  (.checked_at | type == "string") and
  ((try (.checked_at | fromdateiso8601) catch null) as $checked |
    ($checked != null) and
    ((now - $checked) >= 0) and
    ((now - $checked) <= 86400)) and
  (.commit | type == "string") and
  (.commit | test("^[0-9a-f]{40}$")) and
  (.verdict == "PASS") and
  (.post_apply_plan == "clean") and
  (.addresses | type == "array") and
  ([.addresses[]] | sort == ($required_addresses | sort)) and
  ([.addresses[]] | length == (unique | length)) and
  (.private_rds == true) and
  (.runtime_secret_versions == 0) and
  (.recovery_bucket | type == "object") and
  (.recovery_bucket.encrypted == true) and
  (.recovery_bucket.versioned == true) and
  (.recovery_bucket.public_access_blocked == true) and
  (.recovery_bucket.verified_retention_days >= 14) and
  (.recovery_bucket.unverified_retention_days >= 90) and
  (.recovery_lifecycle_verdict == "PASS") and
  (.destroy_or_replace == 0) and
  (.old_resource_changes == 0)
' "$checkpoint_json" >/dev/null 2>&1 || fail

printf '%s\n' \
  'resource_changes=5' \
  'output_changes=1' \
  'sensitive_outputs=1' \
  'destroy_or_replace=0' \
  'aggregate_cost=PASS' \
  'slice3_checkpoint=PASS' \
  'PASS'
