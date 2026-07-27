#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-terraform-plan.sh"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

generic_error="Terraform saved plan violates the Slice 2 contract"
failures=0

expect_verified() {
  local name="$1"
  local mode="$2"
  local plan_json="$3"
  local expected="$4"
  local output

  if ! output="$("$checker" "$mode" "$plan_json" 2>&1)"; then
    printf 'FAIL: rejected valid %s plan\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  if [[ "$output" != "$expected" ]]; then
    printf 'FAIL: valid %s plan emitted unexpected output\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'PASS: verified %s plan\n' "$name"
}

expect_rejected() {
  local name="$1"
  local mode="$2"
  local plan_json="$3"
  local output
  local rc

  set +e
  output="$("$checker" "$mode" "$plan_json" 2>&1)"
  rc=$?
  set -e

  if [[ "$rc" -eq 0 ]]; then
    printf 'FAIL: accepted %s\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  if [[ "$output" != "$generic_error" ]]; then
    printf 'FAIL: %s did not fail with only the generic contract error\n' \
      "$name" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'PASS: rejected %s without plan disclosure\n' "$name"
}

cat >"$fixture_root/bootstrap-valid.json" <<'EOF'
{
  "resource_changes": [
    {
      "address": "aws_iam_policy.production_network_read",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"name": "test-only-private-plan-value"}
      }
    },
    {
      "address": "aws_iam_role_policy_attachment.production_network_read",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"role": "test-only-private-plan-value"}
      }
    },
    {
      "address": "aws_iam_role.production",
      "change": {
        "actions": ["no-op"],
        "before": {"name": "existing-test-only-role"},
        "after": {"name": "existing-test-only-role"}
      }
    }
  ]
}
EOF

jq '.resource_changes[0].change.actions = ["update"]' \
  "$fixture_root/bootstrap-valid.json" \
  >"$fixture_root/bootstrap-update.json"
jq '.resource_changes[0].change.actions = ["delete"]' \
  "$fixture_root/bootstrap-valid.json" \
  >"$fixture_root/bootstrap-delete.json"
jq '.resource_changes += [{
  "address": "aws_iam_policy.test_only_extra",
  "change": {
    "actions": ["create"],
    "before": null,
    "after": {"name": "test-only-private-plan-value"}
  }
}]' "$fixture_root/bootstrap-valid.json" \
  >"$fixture_root/bootstrap-extra.json"

adoption_addresses=(
  'aws_vpc.canonical'
  'aws_internet_gateway.canonical'
  'aws_subnet.public["public_a"]'
  'aws_subnet.public["public_b"]'
  'aws_subnet.public["public_c"]'
  'aws_subnet.public["public_d"]'
  'aws_route_table.public'
  'aws_route.public_ipv4_default'
  'aws_route_table_association.public["public_a"]'
  'aws_route_table_association.public["public_b"]'
  'aws_route_table_association.public["public_c"]'
  'aws_route_table_association.public["public_d"]'
  'aws_eip.origin'
)

printf '%s\n' "${adoption_addresses[@]}" |
  jq -R '{
    address: .,
    change: {
      actions: ["no-op"],
      importing: {id: "test-only-private-import-id"},
      before: null,
      after: {value: "test-only-private-plan-value"}
    }
  }' |
  jq -s '{resource_changes: .}' \
  >"$fixture_root/adoption-valid.json"

jq 'del(.resource_changes[0])' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-missing.json"
jq '.resource_changes += [{
  "address": "aws_instance.test_only_extra",
  "change": {
    "actions": ["no-op"],
    "importing": {id: "test-only-private-extra-id"},
    "before": null,
    "after": {value: "test-only-private-plan-value"}
  }
}]' "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-extra.json"
jq '.resource_changes += [{
  "address": "aws_iam_role.production",
  "change": {
    "actions": ["no-op"],
    "before": {"name": "existing-test-only-role"},
    "after": {"name": "existing-test-only-role"}
  }
}]' "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-extra-unimported-no-op.json"
jq '.resource_changes[0].change.importing = null' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-missing-import-metadata.json"
jq '.resource_changes[0].change.importing = {}' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-empty-import-metadata.json"
jq '.resource_changes[0].change.importing.id = ""' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-empty-import-id.json"
jq '.resource_changes[0].change.importing.id = 123' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-malformed-import-id.json"

for action in create update delete; do
  jq --arg action "$action" \
    '.resource_changes[0].change.actions = [$action]' \
    "$fixture_root/adoption-valid.json" \
    >"$fixture_root/adoption-$action.json"
done

printf '{malformed-json\n' >"$fixture_root/malformed.json"

expect_verified \
  "bootstrap" \
  bootstrap \
  "$fixture_root/bootstrap-valid.json" \
  "bootstrap plan contract verified"
expect_rejected \
  "bootstrap update" \
  bootstrap \
  "$fixture_root/bootstrap-update.json"
expect_rejected \
  "bootstrap delete" \
  bootstrap \
  "$fixture_root/bootstrap-delete.json"
expect_rejected \
  "bootstrap third address" \
  bootstrap \
  "$fixture_root/bootstrap-extra.json"

expect_verified \
  "adoption" \
  adoption \
  "$fixture_root/adoption-valid.json" \
  "adoption plan contract verified"
expect_rejected \
  "adoption missing import" \
  adoption \
  "$fixture_root/adoption-missing.json"
expect_rejected \
  "adoption extra address" \
  adoption \
  "$fixture_root/adoption-extra.json"
expect_rejected \
  "adoption extra unimported no-op" \
  adoption \
  "$fixture_root/adoption-extra-unimported-no-op.json"
expect_rejected \
  "adoption missing import metadata" \
  adoption \
  "$fixture_root/adoption-missing-import-metadata.json"
expect_rejected \
  "adoption empty import metadata" \
  adoption \
  "$fixture_root/adoption-empty-import-metadata.json"
expect_rejected \
  "adoption empty import id" \
  adoption \
  "$fixture_root/adoption-empty-import-id.json"
expect_rejected \
  "adoption malformed import id" \
  adoption \
  "$fixture_root/adoption-malformed-import-id.json"
for action in create update delete; do
  expect_rejected \
    "adoption $action action" \
    adoption \
    "$fixture_root/adoption-$action.json"
done

expect_rejected \
  "malformed JSON" \
  bootstrap \
  "$fixture_root/malformed.json"
expect_rejected \
  "unknown mode" \
  unknown \
  "$fixture_root/bootstrap-valid.json"

test "$failures" -eq 0
