#!/usr/bin/env python3
import copy
import datetime as dt
import json
import pathlib
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
CHECKER = ROOT / "scripts" / "check-terraform-slice-5-plan.py"
ERROR = "Terraform saved plan violates the Slice 5 contract"
CIDRS = [f"192.0.2.{index * 8}/29" for index in range(10)]
CATEGORIES = [
    "aws_compute",
    "public_ipv4",
    "database",
    "storage",
    "backup",
    "registry",
    "cloudflare",
]


def change(address, actions, after=None, after_unknown=None):
    return {
        "address": address,
        "previous_address": None,
        "change": {
            "actions": actions,
            "importing": None,
            "before": None if actions != ["no-op"] else after,
            "after": after,
            "after_unknown": after_unknown or {},
        },
    }


def prefix_after(cidrs=CIDRS, max_entries=20, tags=None):
    return {
        "name": "jobcron-cloudflare-ipv4",
        "address_family": "IPv4",
        "max_entries": max_entries,
        "entry": [{"cidr": cidr} for cidr in cidrs],
        "tags": tags or {"jobcron:edge-source": "cloudflare-ipv4"},
    }


def ingress_after(**overrides):
    result = {
        "security_group_id": "sg-synthetic",
        "prefix_list_id": None,
        "ip_protocol": "tcp",
        "from_port": 443,
        "to_port": 443,
        "cidr_ipv4": None,
        "cidr_ipv6": None,
        "referenced_security_group_id": None,
        "tags": {"jobcron:edge-rule": "origin-https-from-cloudflare"},
    }
    result.update(overrides)
    return result


def bootstrap_plan():
    return {
        "resource_changes": [
            change("aws_iam_policy.edge_prefix_list", ["create"], {}),
            change(
                "aws_iam_role_policy_attachment.edge_prefix_list",
                ["create"],
                {},
            ),
            change("aws_iam_role.edge", ["no-op"], {"name": "synthetic-edge"}),
        ],
        "output_changes": {},
        "diagnostics": [],
    }


def edge_create_plan(cidrs=CIDRS):
    return {
        "resource_changes": [
            change(
                "aws_ec2_managed_prefix_list.cloudflare_ipv4",
                ["create"],
                prefix_after(cidrs),
            ),
            change(
                "aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare",
                ["create"],
                ingress_after(),
                {"prefix_list_id": True},
            ),
        ],
        "output_changes": {},
        "diagnostics": [],
    }


def edge_refresh_plan(cidrs=CIDRS):
    return {
        "resource_changes": [
            change(
                "aws_ec2_managed_prefix_list.cloudflare_ipv4",
                ["update"],
                prefix_after(cidrs),
            ),
            change(
                "aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare",
                ["no-op"],
                ingress_after(prefix_list_id="pl-synthetic"),
            ),
        ],
        "output_changes": {},
        "diagnostics": [],
    }


def cost_packet():
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    today = now.date().isoformat()
    categories = [
        {
            "name": name,
            "source": "Synthetic pricing source",
            "source_date": today,
            "quantity": 1,
            "recurring_monthly_upper_bound": 10,
            "one_time_upper_bound": 10,
        }
        for name in CATEGORIES
    ]
    return {
        "checked_at": now.isoformat().replace("+00:00", "Z"),
        "currency": "USD",
        "aggregate": {
            "recurring_monthly_upper_bound": 70,
            "one_time_upper_bound": 70,
        },
        "categories": categories,
    }


def checkpoint():
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    return {
        "checked_at": now.isoformat().replace("+00:00", "Z"),
        "integrated_commit": "0123456789abcdef0123456789abcdef01234567",
        "state_binding_current": True,
        "replacement_host_health": "PASS",
        "origin_group_attached": True,
        "rollback_preserved": True,
        "public_cutover_actions": 0,
    }


def tfvars(cidrs=CIDRS):
    return {"cloudflare_ipv4_cidrs": list(cidrs)}


class Slice5PlanCheckerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def write_json(self, name, value):
        path = self.root / name
        path.write_text(json.dumps(value), encoding="utf-8")
        return path

    def run_checker(
        self,
        mode,
        plan,
        cost=None,
        slice4=None,
        cidr_tfvars=None,
    ):
        plan_path = (
            plan
            if isinstance(plan, pathlib.Path)
            else self.write_json("plan.json", plan)
        )
        cost_path = self.write_json("cost.json", cost or cost_packet())
        checkpoint_path = self.write_json(
            "checkpoint.json", slice4 or checkpoint()
        )
        command = [
            "python3",
            str(CHECKER),
            mode,
            str(plan_path),
            str(cost_path),
            str(checkpoint_path),
        ]
        if cidr_tfvars is not None:
            command.append(str(self.write_json("tfvars.json", cidr_tfvars)))
        return subprocess.run(command, capture_output=True, text=True)

    def assert_verified(self, mode, plan, expected, cidr_tfvars=None):
        result = self.run_checker(mode, plan, cidr_tfvars=cidr_tfvars)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), expected)
        self.assertEqual(result.stderr, "")

    def assert_rejected(self, mode, plan, **kwargs):
        result = self.run_checker(mode, plan, **kwargs)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr.strip(), ERROR)

    def test_accepts_all_three_exact_modes(self):
        self.assert_verified(
            "slice5-bootstrap",
            bootstrap_plan(),
            "Slice 5 bootstrap plan contract verified",
        )
        self.assert_verified(
            "slice5-edge-create",
            edge_create_plan(),
            "Slice 5 edge create plan contract verified",
            tfvars(),
        )
        self.assert_verified(
            "slice5-edge-refresh",
            edge_refresh_plan(),
            "Slice 5 edge refresh plan contract verified",
            tfvars(),
        )

    def test_rejects_unknown_mode_and_unreadable_or_malformed_inputs(self):
        self.assert_rejected("unknown", bootstrap_plan())
        malformed = self.root / "malformed.json"
        malformed.write_text("{malformed", encoding="utf-8")
        self.assert_rejected("slice5-bootstrap", malformed)
        missing = self.root / "missing.json"
        self.assert_rejected("slice5-bootstrap", missing)

    def test_rejects_address_and_action_mutations(self):
        mutations = {}
        plan = bootstrap_plan()
        missing = copy.deepcopy(plan)
        missing["resource_changes"].pop(0)
        mutations["missing"] = missing
        extra = copy.deepcopy(plan)
        extra["resource_changes"].append(
            change("aws_iam_policy.unexpected", ["create"], {})
        )
        mutations["extra"] = extra
        duplicate = copy.deepcopy(plan)
        duplicate["resource_changes"].append(
            copy.deepcopy(duplicate["resource_changes"][0])
        )
        mutations["duplicate"] = duplicate
        for action in (
            ["update"],
            ["delete"],
            ["delete", "create"],
            ["import"],
            ["move"],
            ["forget"],
            ["mystery"],
        ):
            changed = copy.deepcopy(plan)
            changed["resource_changes"][0]["change"]["actions"] = action
            mutations[str(action)] = changed
        importing = copy.deepcopy(plan)
        importing["resource_changes"][0]["change"]["importing"] = {
            "id": "synthetic-private-id"
        }
        mutations["importing"] = importing
        moved = copy.deepcopy(plan)
        moved["resource_changes"][0]["previous_address"] = "aws_iam_policy.old"
        mutations["moved"] = moved
        for name, mutation in mutations.items():
            with self.subTest(name=name):
                self.assert_rejected("slice5-bootstrap", mutation)

    def test_rejects_output_diagnostics_and_disclosure(self):
        plan = bootstrap_plan()
        output = copy.deepcopy(plan)
        output["output_changes"] = {"unexpected": {"actions": ["create"]}}
        self.assert_rejected("slice5-bootstrap", output)
        diagnostic = copy.deepcopy(plan)
        diagnostic["diagnostics"] = [{"summary": "PRIVATE_VALUE synthetic"}]
        result = self.run_checker("slice5-bootstrap", diagnostic)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stderr.strip(), ERROR)
        self.assertNotIn("PRIVATE_VALUE", result.stderr)

    def test_requires_fresh_evidence_for_creation_but_not_exact_refresh(self):
        stale_cost = cost_packet()
        stale_cost["checked_at"] = "2000-01-01T00:00:00Z"
        stale_checkpoint = checkpoint()
        stale_checkpoint["checked_at"] = "2000-01-01T00:00:00Z"
        for mode, plan, cidr_input in (
            ("slice5-bootstrap", bootstrap_plan(), None),
            ("slice5-edge-create", edge_create_plan(), tfvars()),
        ):
            with self.subTest(mode=mode, evidence="cost"):
                self.assert_rejected(
                    mode, plan, cost=stale_cost, cidr_tfvars=cidr_input
                )
            with self.subTest(mode=mode, evidence="checkpoint"):
                self.assert_rejected(
                    mode, plan, slice4=stale_checkpoint, cidr_tfvars=cidr_input
                )

        result = self.run_checker(
            "slice5-edge-refresh",
            edge_refresh_plan(),
            cost=stale_cost,
            slice4=stale_checkpoint,
            cidr_tfvars=tfvars(),
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout.strip(),
            "Slice 5 edge refresh plan contract verified",
        )

    def test_rejects_non_age_cost_and_checkpoint_mutations_in_refresh(self):
        cost_mutations = []
        missing_category = cost_packet()
        missing_category["categories"].pop()
        cost_mutations.append(("missing category", missing_category))
        missing_source_date = cost_packet()
        del missing_source_date["categories"][0]["source_date"]
        cost_mutations.append(("missing source date", missing_source_date))
        malformed_category = cost_packet()
        malformed_category["categories"][0] = None
        cost_mutations.append(("malformed category", malformed_category))
        for key, value in (
            ("recurring_monthly_upper_bound", 101),
            ("one_time_upper_bound", 201),
        ):
            over = cost_packet()
            over["aggregate"][key] = value
            cost_mutations.append((key, over))
        for name, mutation in cost_mutations:
            with self.subTest(cost=name):
                self.assert_rejected(
                    "slice5-edge-refresh",
                    edge_refresh_plan(),
                    cost=mutation,
                    cidr_tfvars=tfvars(),
                )

        checkpoint_mutations = []
        for key in checkpoint():
            incomplete = checkpoint()
            del incomplete[key]
            checkpoint_mutations.append((f"missing {key}", incomplete))
        for key, value in (
            ("checked_at", "not-a-timestamp"),
            ("integrated_commit", "not-a-commit"),
            ("state_binding_current", False),
            ("replacement_host_health", "FAIL"),
            ("origin_group_attached", False),
            ("rollback_preserved", False),
            ("public_cutover_actions", 1),
        ):
            invalid = checkpoint()
            invalid[key] = value
            checkpoint_mutations.append((f"invalid {key}", invalid))
        extra = checkpoint()
        extra["unexpected"] = True
        checkpoint_mutations.append(("extra key", extra))
        for name, mutation in checkpoint_mutations:
            with self.subTest(checkpoint=name):
                self.assert_rejected(
                    "slice5-edge-refresh",
                    edge_refresh_plan(),
                    slice4=mutation,
                    cidr_tfvars=tfvars(),
                )

    def test_rejects_cidr_and_prefix_list_mutations(self):
        cases = []
        cases.append(("missing tfvars", edge_create_plan(), None))
        cases.append(
            ("mismatch", edge_create_plan(), tfvars(CIDRS[:-1] + ["198.51.100.0/24"]))
        )
        cases.append(("below minimum", edge_create_plan(CIDRS[:9]), tfvars(CIDRS[:9])))
        above = [f"198.51.100.{index * 8}/29" for index in range(21)]
        cases.append(("above maximum", edge_create_plan(above), tfvars(above)))
        duplicate = CIDRS[:-1] + [CIDRS[0]]
        cases.append(("duplicate", edge_create_plan(duplicate), tfvars(duplicate)))
        unsorted = list(reversed(CIDRS))
        cases.append(("unsorted", edge_create_plan(unsorted), tfvars(unsorted)))
        ipv6 = CIDRS[:-1] + ["2001:db8::/32"]
        cases.append(("IPv6", edge_create_plan(ipv6), tfvars(ipv6)))
        private = CIDRS[:-1] + ["10.0.0.0/8"]
        cases.append(("private", edge_create_plan(private), tfvars(private)))
        overlap = CIDRS[:-1] + ["8.0.0.0/4"]
        cases.append(("forbidden overlap", edge_create_plan(overlap), tfvars(overlap)))
        wrong_max = edge_create_plan()
        wrong_max["resource_changes"][0]["change"]["after"]["max_entries"] = 21
        cases.append(("max entries", wrong_max, tfvars()))
        wrong_tags = edge_create_plan()
        wrong_tags["resource_changes"][0]["change"]["after"]["tags"][
            "unexpected"
        ] = "tag"
        cases.append(("additional tag", wrong_tags, tfvars()))
        for name, plan, cidr_input in cases:
            with self.subTest(name=name):
                self.assert_rejected(
                    "slice5-edge-create",
                    plan,
                    cidr_tfvars=cidr_input,
                )

    def test_rejects_ingress_and_refresh_mutations(self):
        mutations = []
        for field, value in (
            ("ip_protocol", "udp"),
            ("from_port", 80),
            ("to_port", 444),
            ("cidr_ipv4", "0.0.0.0/0"),
        ):
            plan = edge_create_plan()
            plan["resource_changes"][1]["change"]["after"][field] = value
            mutations.append((field, plan))
        wrong_tag = edge_create_plan()
        wrong_tag["resource_changes"][1]["change"]["after"]["tags"] = {
            "jobcron:edge-rule": "wrong"
        }
        mutations.append(("wrong tag", wrong_tag))
        second_rule = edge_create_plan()
        second_rule["resource_changes"].append(
            change(
                "aws_vpc_security_group_ingress_rule.second",
                ["create"],
                ingress_after(),
            )
        )
        mutations.append(("second rule", second_rule))
        refresh_ingress = edge_refresh_plan()
        refresh_ingress["resource_changes"][1]["change"]["actions"] = ["update"]
        mutations.append(("refresh ingress", refresh_ingress))
        for name, plan in mutations:
            with self.subTest(name=name):
                mode = (
                    "slice5-edge-refresh"
                    if name == "refresh ingress"
                    else "slice5-edge-create"
                )
                self.assert_rejected(mode, plan, cidr_tfvars=tfvars())

    def test_rejects_forbidden_resource_family_outside_bootstrap_mode(self):
        for address in (
            "aws_instance.unexpected",
            "aws_db_instance.unexpected",
            "aws_secretsmanager_secret.unexpected",
            "aws_eip_association.unexpected",
            "aws_route53_record.unexpected",
            "cloudflare_record.unexpected",
        ):
            plan = bootstrap_plan()
            plan["resource_changes"].append(change(address, ["no-op"], {}))
            with self.subTest(address=address):
                self.assert_rejected("slice5-bootstrap", plan)


if __name__ == "__main__":
    unittest.main()
