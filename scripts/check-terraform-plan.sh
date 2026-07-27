#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'Terraform saved plan violates the Slice 2 contract\n' >&2
  exit 1
}

[[ "$#" -eq 2 ]] || fail

mode="$1"
plan_json="$2"

command -v jq >/dev/null 2>&1 || fail
[[ -f "$plan_json" ]] || fail

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
    ' "$plan_json" >/dev/null 2>&1 || fail
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
        "aws_route.public_ipv4_default",
        "aws_route_table_association.public[\"public_a\"]",
        "aws_route_table_association.public[\"public_b\"]",
        "aws_route_table_association.public[\"public_c\"]",
        "aws_route_table_association.public[\"public_d\"]",
        "aws_eip.origin"
      ] as $expected |
      (.resource_changes | type == "array") and
      (.resource_changes | length == 13) and
      all(
        .resource_changes[];
        (.change.actions == ["no-op"]) and
        (.change.importing | type == "object") and
        (.change.importing.id | type == "string") and
        (.change.importing.id | test("\\S"))
      ) and
      ([.resource_changes[].address] | sort == ($expected | sort))
    ' "$plan_json" >/dev/null 2>&1 || fail
    printf 'adoption plan contract verified\n'
    ;;
  *)
    fail
    ;;
esac
