#!/usr/bin/env python3
import datetime as dt
import ipaddress
import json
import pathlib
import re
import sys


ERROR = "Terraform saved plan violates the Slice 5 contract"
MODES = {
    "slice5-bootstrap": "Slice 5 bootstrap plan contract verified",
    "slice5-edge-create": "Slice 5 edge create plan contract verified",
    "slice5-edge-refresh": "Slice 5 edge refresh plan contract verified",
}
BOOTSTRAP_CREATES = {
    "aws_iam_policy.edge_prefix_list",
    "aws_iam_role_policy_attachment.edge_prefix_list",
}
PREFIX_LIST = "aws_ec2_managed_prefix_list.cloudflare_ipv4"
INGRESS = "aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare"
COST_CATEGORIES = {
    "aws_compute",
    "public_ipv4",
    "database",
    "storage",
    "backup",
    "registry",
    "cloudflare",
}
FORBIDDEN_BOOTSTRAP_PREFIXES = (
    "aws_instance.",
    "aws_db_",
    "aws_rds_",
    "aws_secretsmanager_",
    "aws_eip",
    "aws_route53_",
    "cloudflare_",
)
FORBIDDEN_CIDRS = tuple(
    ipaddress.ip_network(cidr)
    for cidr in (
        "127.0.0.0/8",
        "169.254.0.0/16",
        "224.0.0.0/4",
        "10.0.0.0/8",
        "172.16.0.0/12",
        "192.168.0.0/16",
    )
)


class ContractError(Exception):
    pass


def require(condition):
    if not condition:
        raise ContractError


def exact_keys(value, keys):
    require(isinstance(value, dict) and set(value) == set(keys))


def load_json(path):
    with pathlib.Path(path).open(encoding="utf-8") as handle:
        return json.load(handle)


def validate_timestamp(value, require_fresh):
    require(isinstance(value, str) and value.endswith("Z"))
    checked = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    now = dt.datetime.now(dt.timezone.utc)
    age = now - checked
    require(checked.tzinfo is not None and age >= dt.timedelta())
    if require_fresh:
        require(age <= dt.timedelta(hours=24))


def number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def validate_cost(cost, require_fresh):
    exact_keys(cost, {"checked_at", "currency", "aggregate", "categories"})
    validate_timestamp(cost["checked_at"], require_fresh)
    require(cost["currency"] == "USD")
    exact_keys(
        cost["aggregate"],
        {"recurring_monthly_upper_bound", "one_time_upper_bound"},
    )
    categories = cost["categories"]
    require(isinstance(categories, list) and len(categories) == len(COST_CATEGORIES))
    require(all(isinstance(category, dict) for category in categories))
    require(
        {category.get("name") for category in categories} == COST_CATEGORIES
        and len({category.get("name") for category in categories}) == len(categories)
    )
    recurring = 0
    one_time = 0
    today = dt.datetime.now(dt.timezone.utc).date()
    for category in categories:
        exact_keys(
            category,
            {
                "name",
                "source",
                "source_date",
                "quantity",
                "recurring_monthly_upper_bound",
                "one_time_upper_bound",
            },
        )
        require(isinstance(category["source"], str) and category["source"].strip())
        source_date = dt.date.fromisoformat(category["source_date"])
        require(source_date <= today)
        for key in (
            "quantity",
            "recurring_monthly_upper_bound",
            "one_time_upper_bound",
        ):
            require(number(category[key]) and category[key] >= 0)
        recurring += category["recurring_monthly_upper_bound"]
        one_time += category["one_time_upper_bound"]
    aggregate = cost["aggregate"]
    require(
        number(aggregate["recurring_monthly_upper_bound"])
        and recurring <= aggregate["recurring_monthly_upper_bound"] <= 100
    )
    require(
        number(aggregate["one_time_upper_bound"])
        and one_time <= aggregate["one_time_upper_bound"] <= 200
    )


def validate_checkpoint(checkpoint, require_fresh):
    exact_keys(
        checkpoint,
        {
            "checked_at",
            "integrated_commit",
            "state_binding_current",
            "replacement_host_health",
            "origin_group_attached",
            "rollback_preserved",
            "public_cutover_actions",
        },
    )
    validate_timestamp(checkpoint["checked_at"], require_fresh)
    require(
        isinstance(checkpoint["integrated_commit"], str)
        and re.fullmatch(r"[0-9a-f]{40}", checkpoint["integrated_commit"])
    )
    require(checkpoint["state_binding_current"] is True)
    require(checkpoint["replacement_host_health"] == "PASS")
    require(checkpoint["origin_group_attached"] is True)
    require(checkpoint["rollback_preserved"] is True)
    require(checkpoint["public_cutover_actions"] == 0)


def cidr_sort_key(value):
    network = ipaddress.ip_network(value, strict=True)
    require(isinstance(network, ipaddress.IPv4Network))
    require(
        network.prefixlen != 0
        and not any(network.overlaps(forbidden) for forbidden in FORBIDDEN_CIDRS)
    )
    return int(network.network_address), network.prefixlen


def validate_cidrs(values):
    require(isinstance(values, list) and 10 <= len(values) <= 20)
    require(all(isinstance(value, str) for value in values))
    require(len(set(values)) == len(values))
    keys = [cidr_sort_key(value) for value in values]
    require(keys == sorted(keys))


def resource_map(plan):
    require(isinstance(plan, dict))
    resources = plan.get("resource_changes")
    require(isinstance(resources, list))
    require(plan.get("output_changes", {}) == {})
    require(plan.get("diagnostics", []) == [])
    result = {}
    for resource in resources:
        require(isinstance(resource, dict))
        address = resource.get("address")
        require(isinstance(address, str) and address not in result)
        require(resource.get("previous_address") is None)
        change = resource.get("change")
        require(isinstance(change, dict) and change.get("importing") is None)
        actions = change.get("actions")
        require(
            isinstance(actions, list)
            and actions
            and all(
                action in {"no-op", "create", "update", "delete"}
                for action in actions
            )
        )
        result[address] = resource
    return result


def validate_bootstrap(resources):
    require(
        {
            address
            for address, resource in resources.items()
            if resource["change"]["actions"] == ["create"]
        }
        == BOOTSTRAP_CREATES
    )
    for address, resource in resources.items():
        actions = resource["change"]["actions"]
        require(
            (address in BOOTSTRAP_CREATES and actions == ["create"])
            or (
                address not in BOOTSTRAP_CREATES
                and actions == ["no-op"]
                and not address.startswith(FORBIDDEN_BOOTSTRAP_PREFIXES)
            )
        )


def validate_known(after_unknown, fields, allow_unknown=()):
    require(isinstance(after_unknown, dict))
    for field in fields:
        require(field in allow_unknown or not after_unknown.get(field, False))


def validate_prefix(resource, actions, expected_cidrs):
    change = resource["change"]
    require(change["actions"] == actions)
    after = change.get("after")
    require(isinstance(after, dict))
    required = {"address_family", "max_entries", "entry", "tags"}
    validate_known(change.get("after_unknown", {}), required)
    require(after.get("address_family") == "IPv4")
    require(after.get("max_entries") == 20)
    require(after.get("tags") == {"jobcron:edge-source": "cloudflare-ipv4"})
    entries = after.get("entry")
    require(
        isinstance(entries, list)
        and all(
            isinstance(entry, dict)
            and set(entry) == {"cidr"}
            and isinstance(entry["cidr"], str)
            for entry in entries
        )
    )
    planned_cidrs = [entry["cidr"] for entry in entries]
    validate_cidrs(planned_cidrs)
    require(set(planned_cidrs) == set(expected_cidrs))


def validate_ingress(resource, actions, allow_unknown_prefix):
    change = resource["change"]
    require(change["actions"] == actions)
    after = change.get("after")
    require(isinstance(after, dict))
    fields = {
        "security_group_id",
        "prefix_list_id",
        "ip_protocol",
        "from_port",
        "to_port",
        "cidr_ipv4",
        "cidr_ipv6",
        "referenced_security_group_id",
        "tags",
    }
    validate_known(
        change.get("after_unknown", {}),
        fields,
        {"prefix_list_id"} if allow_unknown_prefix else set(),
    )
    require(
        isinstance(after.get("security_group_id"), str)
        and after["security_group_id"]
    )
    if allow_unknown_prefix:
        require(
            after.get("prefix_list_id") is None
            and change.get("after_unknown", {}).get("prefix_list_id") is True
        )
    else:
        require(
            isinstance(after.get("prefix_list_id"), str)
            and after["prefix_list_id"]
        )
    require(after.get("ip_protocol") == "tcp")
    require(after.get("from_port") == 443 and after.get("to_port") == 443)
    require(after.get("cidr_ipv4") is None and after.get("cidr_ipv6") is None)
    require(after.get("referenced_security_group_id") is None)
    require(
        after.get("tags")
        == {"jobcron:edge-rule": "origin-https-from-cloudflare"}
    )


def validate_edge(mode, resources, cidr_tfvars):
    exact_keys(cidr_tfvars, {"cloudflare_ipv4_cidrs"})
    expected_cidrs = cidr_tfvars["cloudflare_ipv4_cidrs"]
    validate_cidrs(expected_cidrs)
    require(set(resources) == {PREFIX_LIST, INGRESS})
    if mode == "slice5-edge-create":
        validate_prefix(resources[PREFIX_LIST], ["create"], expected_cidrs)
        validate_ingress(resources[INGRESS], ["create"], True)
    else:
        validate_prefix(resources[PREFIX_LIST], ["update"], expected_cidrs)
        validate_ingress(resources[INGRESS], ["no-op"], False)


def main(argv):
    require(len(argv) in {5, 6})
    mode = argv[1]
    require(mode in MODES)
    require((mode == "slice5-bootstrap" and len(argv) == 5) or len(argv) == 6)
    plan = load_json(argv[2])
    cost = load_json(argv[3])
    checkpoint = load_json(argv[4])
    require_fresh = mode != "slice5-edge-refresh"
    validate_cost(cost, require_fresh)
    validate_checkpoint(checkpoint, require_fresh)
    resources = resource_map(plan)
    if mode == "slice5-bootstrap":
        validate_bootstrap(resources)
    else:
        validate_edge(mode, resources, load_json(argv[5]))
    print(MODES[mode])


if __name__ == "__main__":
    try:
        main(sys.argv)
    except Exception:
        print(ERROR, file=sys.stderr)
        raise SystemExit(1)
