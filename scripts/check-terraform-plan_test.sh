#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-terraform-plan.sh"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

generic_error="Terraform saved plan violates the Slice 2 contract"
slice3_generic_error="Terraform saved plan violates the Slice 3 contract"
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
  local expected_error="$generic_error"

  if [[ "$mode" == slice3-* || "$mode" == unknown ]]; then
    expected_error="$slice3_generic_error"
  fi

  set +e
  output="$("$checker" "$mode" "$plan_json" 2>&1)"
  rc=$?
  set -e

  if [[ "$rc" -eq 0 ]]; then
    printf 'FAIL: accepted %s\n' "$name" >&2
    failures=$((failures + 1))
    return
  fi
  if [[ "$output" != "$expected_error" ]]; then
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

adoption_import_addresses=(
  'aws_vpc.canonical'
  'aws_internet_gateway.canonical'
  'aws_subnet.public["public_a"]'
  'aws_subnet.public["public_b"]'
  'aws_subnet.public["public_c"]'
  'aws_subnet.public["public_d"]'
  'aws_route_table.public'
  'aws_route.public_ipv4_default'
)

printf '%s\n' "${adoption_import_addresses[@]}" |
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
  >"$fixture_root/adoption-imports.json"
jq '.resource_changes += [{
  "address": "aws_eip.origin",
  "change": {
    "actions": ["create"],
    "before": null,
    "after": {
      "domain": "vpc",
      "value": "test-only-private-plan-value"
    }
  }
}]' "$fixture_root/adoption-imports.json" \
  >"$fixture_root/adoption-valid.json"

jq 'del(.resource_changes[0])' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-missing.json"
jq 'del(.resource_changes[] | select(.address == "aws_eip.origin"))' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-missing-eip.json"
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
jq '.resource_changes += [{
  "address": "aws_route_table_association.public[\"public_a\"]",
  "change": {
    "actions": ["no-op"],
    "importing": {"id": "test-only-private-association-id"},
    "before": null,
    "after": {"value": "test-only-private-plan-value"}
  }
}]' "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-association.json"
jq '.resource_changes[0].change.importing = null' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-missing-import-metadata.json"
jq '.resource_changes[0].change.importing = {}' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-empty-import-metadata.json"
jq '.resource_changes[0].change.importing.id = ""' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-empty-import-id.json"
jq '.resource_changes[0].change.importing.id = "   "' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-whitespace-import-id.json"
jq '.resource_changes[0].change.importing.id = 123' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-malformed-import-id.json"
jq '(.resource_changes[] | select(.address == "aws_eip.origin") |
  .change.importing) = {"id": "test-only-private-eip-id"}' \
  "$fixture_root/adoption-valid.json" \
  >"$fixture_root/adoption-eip-import.json"

for action in create update delete; do
  jq --arg action "$action" \
    '.resource_changes[0].change.actions = [$action]' \
    "$fixture_root/adoption-valid.json" \
    >"$fixture_root/adoption-$action.json"
done
for action in no-op update delete; do
  jq --arg action "$action" \
    '(.resource_changes[] | select(.address == "aws_eip.origin") |
      .change.actions) = [$action]' \
    "$fixture_root/adoption-valid.json" \
    >"$fixture_root/adoption-eip-$action.json"
done

jq -n '{
  resource_changes: (
    [
      "aws_iam_policy.production_slice3_read",
      "aws_iam_role_policy_attachment.production_slice3_read"
    ] |
    map({
      address: .,
      change: {
        actions: ["create"],
        before: null,
        after: {value: "test-only-private-plan-value"}
      }
    })
  ) + [
    {
      address: "aws_iam_role.production",
      change: {
        actions: ["no-op"],
        before: {name: "existing-test-only-role"},
        after: {name: "existing-test-only-role"}
      }
    }
  ]
}' >"$fixture_root/slice3-bootstrap-valid.json"

jq -n '{
  resource_changes: (
    [
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
      change: {
        actions: ["create"],
        before: null,
        after: {value: "test-only-private-plan-value"}
      }
    })
  ) + [
    {
      address: "aws_vpc.canonical",
      change: {
        actions: ["no-op"],
        before: {value: "existing-test-only-value"},
        after: {value: "existing-test-only-value"}
      }
    }
  ]
}' >"$fixture_root/slice3-production-valid.json"

for mode in bootstrap production; do
  source="$fixture_root/slice3-$mode-valid.json"

  jq 'del(.resource_changes[0])' "$source" \
    >"$fixture_root/slice3-$mode-missing.json"
  jq '.resource_changes += [.resource_changes[0]]' "$source" \
    >"$fixture_root/slice3-$mode-duplicate.json"
  jq '.resource_changes += [{
    address: "aws_iam_policy.test_only_extra",
    change: {
      actions: ["create"],
      before: null,
      after: {value: "test-only-private-plan-value"}
    }
  }]' "$source" >"$fixture_root/slice3-$mode-extra.json"
  jq '.resource_changes[0].change.importing = {
    id: "test-only-private-import-id"
  }' "$source" >"$fixture_root/slice3-$mode-import.json"

  for action in no-op update delete replace; do
    if [[ "$action" == replace ]]; then
      actions='["delete", "create"]'
    else
      actions="[\"$action\"]"
    fi
    jq --argjson actions "$actions" \
      '.resource_changes[0].change.actions = $actions' \
      "$source" >"$fixture_root/slice3-$mode-create-$action.json"
  done
done

for action in create update delete; do
  jq --arg action "$action" \
    '(.resource_changes[] |
      select(.address == "aws_iam_role.production") |
      .change.actions) = [$action]' \
    "$fixture_root/slice3-bootstrap-valid.json" \
    >"$fixture_root/slice3-bootstrap-adopted-$action.json"
  jq --arg action "$action" \
    '(.resource_changes[] |
      select(.address == "aws_vpc.canonical") |
      .change.actions) = [$action]' \
    "$fixture_root/slice3-production-valid.json" \
    >"$fixture_root/slice3-production-adopted-$action.json"
done

jq 'del(
  .resource_changes[] |
  select(.address == "aws_s3_bucket_lifecycle_configuration.recovery")
)' "$fixture_root/slice3-production-valid.json" \
  >"$fixture_root/slice3-production-missing-lifecycle.json"
jq '(
  .resource_changes[] |
  select(.address == "aws_s3_bucket_lifecycle_configuration.recovery") |
  .address
) = "aws_s3_bucket_lifecycle_configuration.different"' \
  "$fixture_root/slice3-production-valid.json" \
  >"$fixture_root/slice3-production-different-lifecycle.json"
jq '.resource_changes += [{
  address: "aws_s3_bucket_lifecycle_configuration.extra",
  change: {
    actions: ["create"],
    before: null,
    after: {value: "test-only-private-plan-value"}
  }
}]' "$fixture_root/slice3-production-valid.json" \
  >"$fixture_root/slice3-production-extra-lifecycle.json"

for forbidden in \
  aws_route.unexpected \
  aws_nat_gateway.unexpected \
  aws_instance.unexpected \
  aws_eip.unexpected \
  aws_eip_association.unexpected \
  aws_secretsmanager_secret_version.unexpected \
  cloudflare_record.unexpected \
  aws_route53_record.unexpected; do
  fixture_name="${forbidden//[^a-zA-Z0-9]/-}"
  jq --arg address "$forbidden" '.resource_changes += [{
    address: $address,
    change: {
      actions: ["create"],
      before: null,
      after: {value: "test-only-private-plan-value"}
    }
  }]' "$fixture_root/slice3-production-valid.json" \
    >"$fixture_root/slice3-production-$fixture_name.json"
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
  "adoption missing EIP create" \
  adoption \
  "$fixture_root/adoption-missing-eip.json"
expect_rejected \
  "adoption extra address" \
  adoption \
  "$fixture_root/adoption-extra.json"
expect_rejected \
  "adoption extra unimported no-op" \
  adoption \
  "$fixture_root/adoption-extra-unimported-no-op.json"
expect_rejected \
  "adoption association address" \
  adoption \
  "$fixture_root/adoption-association.json"
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
  "adoption whitespace-only import id" \
  adoption \
  "$fixture_root/adoption-whitespace-import-id.json"
expect_rejected \
  "adoption malformed import id" \
  adoption \
  "$fixture_root/adoption-malformed-import-id.json"
expect_rejected \
  "adoption EIP import metadata" \
  adoption \
  "$fixture_root/adoption-eip-import.json"
for action in create update delete; do
  expect_rejected \
    "adoption imported-resource $action action" \
    adoption \
    "$fixture_root/adoption-$action.json"
done
for action in no-op update delete; do
  expect_rejected \
    "adoption EIP $action action" \
    adoption \
    "$fixture_root/adoption-eip-$action.json"
done

expect_verified \
  "Slice 3 bootstrap" \
  slice3-bootstrap \
  "$fixture_root/slice3-bootstrap-valid.json" \
  "Slice 3 bootstrap plan contract verified"
expect_verified \
  "Slice 3 production" \
  slice3-production \
  "$fixture_root/slice3-production-valid.json" \
  "Slice 3 production plan contract verified"

for mode in bootstrap production; do
  expect_rejected \
    "Slice 3 $mode missing allow-listed address" \
    "slice3-$mode" \
    "$fixture_root/slice3-$mode-missing.json"
  expect_rejected \
    "Slice 3 $mode duplicate address" \
    "slice3-$mode" \
    "$fixture_root/slice3-$mode-duplicate.json"
  expect_rejected \
    "Slice 3 $mode extra address" \
    "slice3-$mode" \
    "$fixture_root/slice3-$mode-extra.json"
  expect_rejected \
    "Slice 3 $mode import metadata" \
    "slice3-$mode" \
    "$fixture_root/slice3-$mode-import.json"

  for action in no-op update delete replace; do
    expect_rejected \
      "Slice 3 $mode allow-listed $action action" \
      "slice3-$mode" \
      "$fixture_root/slice3-$mode-create-$action.json"
  done
  for action in create update delete; do
    expect_rejected \
      "Slice 3 $mode adopted-resource $action action" \
      "slice3-$mode" \
      "$fixture_root/slice3-$mode-adopted-$action.json"
  done
done

expect_rejected \
  "Slice 3 production missing lifecycle configuration" \
  slice3-production \
  "$fixture_root/slice3-production-missing-lifecycle.json"
expect_rejected \
  "Slice 3 production differently addressed lifecycle configuration" \
  slice3-production \
  "$fixture_root/slice3-production-different-lifecycle.json"
expect_rejected \
  "Slice 3 production extra lifecycle configuration" \
  slice3-production \
  "$fixture_root/slice3-production-extra-lifecycle.json"

for forbidden in \
  aws_route.unexpected \
  aws_nat_gateway.unexpected \
  aws_instance.unexpected \
  aws_eip.unexpected \
  aws_eip_association.unexpected \
  aws_secretsmanager_secret_version.unexpected \
  cloudflare_record.unexpected \
  aws_route53_record.unexpected; do
  fixture_name="${forbidden//[^a-zA-Z0-9]/-}"
  expect_rejected \
    "Slice 3 production forbidden $forbidden address" \
    slice3-production \
    "$fixture_root/slice3-production-$fixture_name.json"
done

expect_rejected \
  "malformed JSON" \
  bootstrap \
  "$fixture_root/malformed.json"
expect_rejected \
  "Slice 3 malformed JSON" \
  slice3-production \
  "$fixture_root/malformed.json"
expect_rejected \
  "unknown mode" \
  unknown \
  "$fixture_root/bootstrap-valid.json"

test "$failures" -eq 0
