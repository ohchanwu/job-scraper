#!/usr/bin/env bash
set -euo pipefail

fail_slice2() {
  printf 'Terraform saved plan violates the Slice 2 contract\n' >&2
  exit 1
}

fail_slice3() {
  printf 'Terraform saved plan violates the Slice 3 contract\n' >&2
  exit 1
}

verify_slice3() {
  local expected_creates="$1"
  local allowed_noops="$2"

  jq -e \
    --argjson expected_creates "$expected_creates" \
    --argjson allowed_noops "$allowed_noops" '
      (.resource_changes | type == "array") and
      (
        [.resource_changes[].address] as $addresses |
        ($addresses | length == ($addresses | unique | length)) and
        all(.resource_changes[]; .change.importing == null) and
        (
          [
            .resource_changes[] |
            select(.change.actions == ["create"]) |
            .address
          ] | sort
        ) == ($expected_creates | sort) and
        all(
          .resource_changes[];
          (
            (.address as $address | $expected_creates | index($address)) != null and
            .change.actions == ["create"]
          ) or
          (
            (.address as $address | $allowed_noops | index($address)) != null and
            .change.actions == ["no-op"]
          )
        )
      )
    ' "$plan_json" >/dev/null 2>&1
}

[[ "$#" -eq 2 ]] || fail_slice2

mode="$1"
plan_json="$2"

command -v jq >/dev/null 2>&1 || fail_slice2
[[ -f "$plan_json" ]] || fail_slice2

case "$mode" in
  bootstrap)
    jq -e '
      [
        "aws_iam_policy.production_network_read",
        "aws_iam_role_policy_attachment.production_network_read"
      ] as $expected |
      (.resource_changes | type == "array") and
      (
        [
          .resource_changes[] |
          select(.change.actions != ["no-op"])
        ] as $changes |
        ($changes | length == 2) and
        all($changes[]; .change.actions == ["create"]) and
        ([$changes[].address] | sort == ($expected | sort))
      )
    ' "$plan_json" >/dev/null 2>&1 || fail_slice2
    printf 'bootstrap plan contract verified\n'
    ;;
  adoption)
    jq -e '
      [
        "aws_vpc.canonical",
        "aws_internet_gateway.canonical",
        "aws_subnet.public[\"public_a\"]",
        "aws_subnet.public[\"public_b\"]",
        "aws_subnet.public[\"public_c\"]",
        "aws_subnet.public[\"public_d\"]",
        "aws_route_table.public",
        "aws_route.public_ipv4_default"
      ] as $expected_imports |
      "aws_eip.origin" as $expected_create |
      (.resource_changes | type == "array") and
      (
        [.resource_changes[] | select(.address != $expected_create)] as $imports |
        [.resource_changes[] | select(.address == $expected_create)] as $creates |
        (.resource_changes | length == 9) and
        ($imports | length == 8) and
        ($creates | length == 1) and
        all(
          $imports[];
          (.change.actions == ["no-op"]) and
          (.change.importing | type == "object") and
          (.change.importing.id | type == "string") and
          (.change.importing.id | test("\\S"))
        ) and
        ([$imports[].address] | sort == ($expected_imports | sort)) and
        all(
          $creates[];
          (.change.actions == ["create"]) and
          (.change.importing == null)
        )
      ) and
      all(
        .resource_changes[];
        (.change.actions == ["no-op"]) and
        (.address != $expected_create)
        or
        (
          (.address == $expected_create) and
          (.change.actions == ["create"])
        )
      )
    ' "$plan_json" >/dev/null 2>&1 || fail_slice2
    printf 'adoption plan contract verified\n'
    ;;
  slice3-bootstrap)
    verify_slice3 \
      '[
        "aws_iam_policy.production_slice3_read",
        "aws_iam_role_policy_attachment.production_slice3_read"
      ]' \
      '[
        "aws_s3_bucket.state",
        "aws_s3_bucket_public_access_block.state",
        "aws_s3_bucket_versioning.state",
        "aws_s3_bucket_server_side_encryption_configuration.state",
        "aws_s3_bucket_policy.state",
        "aws_iam_openid_connect_provider.github",
        "aws_iam_role.production",
        "aws_iam_role.edge",
        "aws_iam_policy.production_state",
        "aws_iam_role_policy_attachment.production_state",
        "aws_iam_policy.edge_state",
        "aws_iam_role_policy_attachment.edge_state",
        "aws_iam_policy.production_network_read",
        "aws_iam_role_policy_attachment.production_network_read"
      ]' || fail_slice3
    printf 'Slice 3 bootstrap plan contract verified\n'
    ;;
  slice3-production)
    verify_slice3 \
      '[
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
      ]' \
      '[
        "aws_vpc.canonical",
        "aws_internet_gateway.canonical",
        "aws_subnet.public[\"public_a\"]",
        "aws_subnet.public[\"public_b\"]",
        "aws_subnet.public[\"public_c\"]",
        "aws_subnet.public[\"public_d\"]",
        "aws_route_table.public",
        "aws_route.public_ipv4_default",
        "aws_eip.origin"
      ]' || fail_slice3
    printf 'Slice 3 production plan contract verified\n'
    ;;
  *)
    fail_slice3
    ;;
esac
