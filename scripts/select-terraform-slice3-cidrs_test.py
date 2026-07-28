#!/usr/bin/env python3

import json
import stat
import subprocess
import sys
import tempfile
import unittest
from ipaddress import ip_network
from pathlib import Path


SCRIPT = Path(__file__).with_name("select-terraform-slice3-cidrs.py")
SUCCESS = "Slice 3 private subnet selection written\n"
FAILURE = "Slice 3 private subnet selection failed\n"


class SelectorTest(unittest.TestCase):
    def run_selector(self, payload, existing_output=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            input_path = root / "private-input.json"
            output_path = root / "private-output.json"
            if isinstance(payload, str):
                input_path.write_text(payload)
            else:
                input_path.write_text(json.dumps(payload))
            if existing_output is not None:
                output_path.write_text(existing_output)

            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(input_path), str(output_path)],
                capture_output=True,
                text=True,
                check=False,
            )
            output = output_path.read_text() if output_path.exists() else None
            mode = (
                stat.S_IMODE(output_path.stat().st_mode)
                if output_path.exists()
                else None
            )
            return result, output, mode

    def assert_private_failure(self, payload, private_values=()):
        result, output, _ = self.run_selector(payload)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, FAILURE)
        self.assertIsNone(output)
        for value in private_values:
            self.assertNotIn(value, result.stdout)
            self.assertNotIn(value, result.stderr)

    def test_selects_first_two_free_networks_and_distinct_sorted_zones(self):
        payload = {
            "vpc_cidr": "10.0.0.0/22",
            "occupied_subnet_cidrs": [
                "10.0.2.0/24",
                "10.0.0.0/24",
            ],
            "eligible_availability_zones": [
                "test-zone-c",
                "test-zone-a",
                "",
                "test-zone-b",
                "test-zone-a",
            ],
        }

        result, output, mode = self.run_selector(payload)

        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, SUCCESS)
        self.assertEqual(result.stderr, "")
        self.assertEqual(mode, 0o600)
        self.assertEqual(
            json.loads(output),
            {
                "database_a": {
                    "availability_zone": "test-zone-a",
                    "cidr_block": "10.0.1.0/24",
                },
                "database_b": {
                    "availability_zone": "test-zone-b",
                    "cidr_block": "10.0.3.0/24",
                },
            },
        )
        for private_value in (
            payload["vpc_cidr"],
            *payload["occupied_subnet_cidrs"],
            *payload["eligible_availability_zones"],
        ):
            if private_value:
                self.assertNotIn(private_value, result.stdout)

    def test_selected_networks_are_inside_vpc_and_do_not_overlap(self):
        payload = {
            "vpc_cidr": "10.8.0.0/21",
            "occupied_subnet_cidrs": ["10.8.0.0/23"],
            "eligible_availability_zones": ["test-zone-b", "test-zone-a"],
        }

        result, output, _ = self.run_selector(payload)

        self.assertEqual(result.returncode, 0)
        selected = [
            ip_network(item["cidr_block"], strict=True)
            for item in json.loads(output).values()
        ]
        vpc = ip_network(payload["vpc_cidr"], strict=True)
        self.assertEqual([network.prefixlen for network in selected], [24, 24])
        self.assertTrue(all(network.subnet_of(vpc) for network in selected))
        self.assertFalse(selected[0].overlaps(selected[1]))
        self.assertEqual(
            [str(network) for network in selected],
            ["10.8.2.0/24", "10.8.3.0/24"],
        )

    def test_rejects_capacity_below_two_free_slash_24_networks(self):
        payload = {
            "vpc_cidr": "10.12.0.0/24",
            "occupied_subnet_cidrs": [],
            "eligible_availability_zones": ["test-zone-a", "test-zone-b"],
        }

        self.assert_private_failure(payload, ("10.12.0.0/24",))

    def test_rejects_fewer_than_two_distinct_nonempty_zones(self):
        payload = {
            "vpc_cidr": "10.16.0.0/22",
            "occupied_subnet_cidrs": [],
            "eligible_availability_zones": ["", "test-private-zone"] * 2,
        }

        self.assert_private_failure(payload, ("test-private-zone",))

    def test_rejects_non_ipv4_and_noncanonical_vpc_cidrs(self):
        for vpc_cidr in ("2001:db8::/48", "10.20.0.1/22"):
            with self.subTest(vpc_cidr=vpc_cidr):
                payload = {
                    "vpc_cidr": vpc_cidr,
                    "occupied_subnet_cidrs": [],
                    "eligible_availability_zones": [
                        "test-zone-a",
                        "test-zone-b",
                    ],
                }
                self.assert_private_failure(payload, (vpc_cidr,))

    def test_rejects_invalid_occupied_networks_without_disclosure(self):
        for occupied in ("10.24.0.1/24", "10.28.0.0/24", "2001:db8::/64"):
            with self.subTest(occupied=occupied):
                payload = {
                    "vpc_cidr": "10.24.0.0/22",
                    "occupied_subnet_cidrs": [occupied],
                    "eligible_availability_zones": [
                        "test-zone-a",
                        "test-zone-b",
                    ],
                }
                self.assert_private_failure(payload, (occupied,))

    def test_rejects_malformed_or_wrong_shaped_json(self):
        payloads = (
            "{not-json",
            [],
            {},
            {
                "vpc_cidr": "10.32.0.0/22",
                "occupied_subnet_cidrs": "10.32.0.0/24",
                "eligible_availability_zones": ["test-zone-a", "test-zone-b"],
            },
            {
                "vpc_cidr": "10.32.0.0/22",
                "occupied_subnet_cidrs": [],
                "eligible_availability_zones": [1, 2],
            },
        )

        for payload in payloads:
            with self.subTest(payload=payload):
                self.assert_private_failure(payload)

    def test_failure_does_not_replace_existing_output(self):
        payload = {
            "vpc_cidr": "private-invalid-value",
            "occupied_subnet_cidrs": [],
            "eligible_availability_zones": ["test-zone-a", "test-zone-b"],
        }

        result, output, _ = self.run_selector(
            payload,
            existing_output="preserved-private-output",
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, FAILURE)
        self.assertEqual(output, "preserved-private-output")
        self.assertNotIn("private-invalid-value", result.stderr)


if __name__ == "__main__":
    unittest.main()
