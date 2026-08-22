#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'Terraform saved plan violates the Slice 4 contract\n' >&2
  exit 1
}

[[ "$#" -eq 3 || "$#" -eq 4 ]] || fail

plan_json="$1"
cost_json="$2"
checkpoint_json="$3"
mode=create
expected_user_data_hash=

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1" 2>/dev/null
}

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

if [[ "$#" -eq 4 ]]; then
  mode=replacement
  rendered_user_data="$4"
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

  [[ -f "$rendered_user_data" && ! -L "$rendered_user_data" ]] || fail
  [[ "$(file_mode "$rendered_user_data")" == 600 ]] || fail
  [[ -s "$rendered_user_data" ]] || fail

  while IFS='|' read -r target source; do
    asset_digest="$(digest "$repo_root/$source")" || fail
    grep -F "$target" "$rendered_user_data" >/dev/null 2>&1 || fail
    grep -F "$asset_digest" "$rendered_user_data" >/dev/null 2>&1 || fail
  done <<'ASSETS'
/opt/jobcron/compose.yaml|deploy/production/compose.yaml
/opt/jobcron/Caddyfile|deploy/production/Caddyfile
/opt/jobcron/jobcron-runtime.sh|deploy/production/jobcron-runtime.sh
/etc/systemd/system/jobcron.service|deploy/production/systemd/jobcron.service
/etc/systemd/system/jobcron-recovery.service|deploy/production/systemd/jobcron-recovery.service
/etc/systemd/system/jobcron-recovery.timer|deploy/production/systemd/jobcron-recovery.timer
ASSETS

  for marker in \
    docker-compose-linux-aarch64 \
    ff42489f5a9b879d5d117c5ffea6defc27390b3286da8ad52cbc9c6ab5df590e \
    'sha256sum -c -' \
    /etc/jobcron/runtime-secret-id \
    'systemctl enable docker.service' \
    'systemctl start docker.service' \
    'systemctl stop jobcron.service'; do
    grep -F "$marker" "$rendered_user_data" >/dev/null 2>&1 || fail
  done
  ! grep -F 'required deployment asset missing' \
    "$rendered_user_data" >/dev/null 2>&1 || fail

  if command -v sha1sum >/dev/null 2>&1; then
    expected_user_data_hash="$(sha1sum "$rendered_user_data" | awk '{print $1}')"
  else
    expected_user_data_hash="$(shasum "$rendered_user_data" | awk '{print $1}')"
  fi
fi

command -v jq >/dev/null 2>&1 || fail
[[ -f "$plan_json" && -f "$cost_json" && -f "$checkpoint_json" ]] || fail

jq -e --arg mode "$mode" --arg expected_user_data_hash "$expected_user_data_hash" '
  [
    "aws_iam_role.replacement_host",
    "aws_iam_role_policy_attachment.replacement_host_ssm",
    "aws_iam_role_policy.replacement_host_runtime",
    "aws_iam_instance_profile.replacement_host",
    "aws_instance.replacement_host"
  ] as $allowed_creates |
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
  ] as $allowed_noops |
  ([.resource_changes[] | select(.address == "aws_instance.replacement_host")][0] // {}) as $instance |
  (.resource_changes | type == "array") and
  ([.resource_changes[].address] | length == (unique | length)) and
  (
    if $mode == "create" then
      (
        [
          .resource_changes[] |
          select(.change.actions == ["create"]) |
          .address
        ] | sort == ($allowed_creates | sort)
      ) and
      all(
        .resource_changes[];
        (.address | type == "string") and
        (.previous_address // null) == null and
        (.change | type == "object") and
        (.change.importing // null) == null and
        (
          (
            .change.actions == ["create"] and
            (.address as $address | $allowed_creates | index($address)) != null
          ) or
          (
            .change.actions == ["no-op"] and
            (.address as $address | $allowed_noops | index($address)) != null
          )
        )
      )
    elif $mode == "replacement" then
      ([.resource_changes[].address] | sort == (($allowed_creates + $allowed_noops) | sort)) and
      all(
        .resource_changes[];
        (.address | type == "string") and
        (.previous_address // null) == null and
        (.change | type == "object") and
        (.change.importing // null) == null and
        (
          if .address == "aws_instance.replacement_host" then
            .change.actions == ["delete", "create"]
          else
            .change.actions == ["no-op"]
          end
        )
      ) and
      ($instance.change.before | type == "object") and
      ($instance.change.after | type == "object") and
      ($instance.change.after.key_name == null) and
      ($instance.change.after.associate_public_ip_address == true) and
      ($instance.change.before.vpc_security_group_ids | type == "array") and
      ($instance.change.after.vpc_security_group_ids | type == "array") and
      ($instance.change.after.vpc_security_group_ids | length == 1) and
      ($instance.change.after.vpc_security_group_ids == $instance.change.before.vpc_security_group_ids) and
      ($instance.change.after.metadata_options | type == "array") and
      ($instance.change.after.metadata_options | length == 1) and
      ($instance.change.after.metadata_options[0].http_endpoint == "enabled") and
      ($instance.change.after.metadata_options[0].http_tokens == "required") and
      ($instance.change.after.metadata_options[0].http_put_response_hop_limit == 1) and
      ($instance.change.after.root_block_device | type == "array") and
      ($instance.change.after.root_block_device | length == 1) and
      ($instance.change.after.root_block_device[0].encrypted == true) and
      ($instance.change.after.root_block_device[0].volume_type == "gp3") and
      ($instance.change.after.root_block_device[0].volume_size == 8) and
      ($instance.change.after.root_block_device[0].delete_on_termination == true) and
      ($instance.change.before.user_data | type == "string") and
      ($instance.change.after.user_data == $expected_user_data_hash) and
      ($instance.change.before.user_data != $instance.change.after.user_data) and
      (($instance.change.after_unknown.key_name // false) == false) and
      (($instance.change.after_unknown.associate_public_ip_address // false) == false) and
      (($instance.change.after_unknown.vpc_security_group_ids // false) == false) and
      (($instance.change.after_unknown.metadata_options // false) == false) and
      (($instance.change.after_unknown.root_block_device // false) == false) and
      (($instance.change.after_unknown.user_data // false) == false) and
      ((.diagnostics // []) == [])
    else
      false
    end
  ) and
  all(
    .resource_changes[];
    all(
      [(.change.after // {}) | .. | objects | .from_port?, .to_port?][];
      . != 22
    )
  ) and
  (.output_changes | type == "object") and
  (.output_changes | keys == ["replacement_instance_id"]) and
  (.output_changes.replacement_instance_id | type == "object") and
  (
    if $mode == "create" then
      .output_changes.replacement_instance_id.actions == ["create"]
    else
      (.output_changes.replacement_instance_id.actions == ["update"]) and
      (.output_changes.replacement_instance_id.before | type == "string") and
      (.output_changes.replacement_instance_id.after == null) and
      (.output_changes.replacement_instance_id.after_unknown == true) and
      (.output_changes.replacement_instance_id.before_sensitive == true)
    end
  ) and
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

if [[ "$mode" == replacement ]]; then
  resource_changes=1
  destroy_or_replace=1
else
  resource_changes=5
  destroy_or_replace=0
fi

printf '%s\n' \
  "resource_changes=$resource_changes" \
  'output_changes=1' \
  'sensitive_outputs=1' \
  "destroy_or_replace=$destroy_or_replace" \
  'aggregate_cost=PASS' \
  'slice3_checkpoint=PASS' \
  'PASS'
