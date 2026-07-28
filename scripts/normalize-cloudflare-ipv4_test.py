#!/usr/bin/env python3

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("normalize-cloudflare-ipv4.py")
SUCCESS = "Cloudflare IPv4 set verified\n"
FAILURE = "Cloudflare IPv4 set rejected\n"
VALID_NETWORKS = (
    "203.0.113.128/25",
    "192.0.2.64/26",
    "198.51.100.0/26",
    "203.0.113.0/25",
    "192.0.2.192/26",
    "198.51.100.128/26",
    "192.0.2.0/26",
    "198.51.100.192/26",
    "192.0.2.128/26",
    "198.51.100.64/26",
)
SORTED_NETWORKS = [
    "192.0.2.0/26",
    "192.0.2.64/26",
    "192.0.2.128/26",
    "192.0.2.192/26",
    "198.51.100.0/26",
    "198.51.100.64/26",
    "198.51.100.128/26",
    "198.51.100.192/26",
    "203.0.113.0/25",
    "203.0.113.128/25",
]


class NormalizerTest(unittest.TestCase):
    def run_normalizer(self, input_text):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            input_path = root / "cloudflare-ips-v4.txt"
            output_path = root / "cloudflare.auto.tfvars.json"
            input_path.write_text(input_text, encoding="utf-8")

            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(input_path), str(output_path)],
                capture_output=True,
                text=True,
                check=False,
            )
            output = (
                output_path.read_text(encoding="utf-8")
                if output_path.is_file()
                else None
            )
            return result, output

    def assert_rejected(self, input_text):
        result, output = self.run_normalizer(input_text)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, FAILURE)
        self.assertIsNone(output)

    def test_writes_only_numerically_sorted_canonical_networks(self):
        result, output = self.run_normalizer(
            "\n  " + "\n\n".join(VALID_NETWORKS) + "  \n"
        )

        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, SUCCESS)
        self.assertEqual(result.stderr, "")
        self.assertEqual(
            json.loads(output),
            {"cloudflare_ipv4_cidrs": SORTED_NETWORKS},
        )

    def test_rejects_invalid_response_classes_without_partial_output(self):
        nine_valid = "\n".join(VALID_NETWORKS[:9])
        cases = {
            "empty input": "",
            "9 entries": nine_valid,
            "21 entries": "\n".join(
                f"192.0.2.{index}/32" for index in range(1, 22)
            ),
            "exact duplicate": "\n".join(
                (*VALID_NETWORKS[:9], VALID_NETWORKS[0], VALID_NETWORKS[0])
            ),
            "duplicate after whitespace normalization": "\n".join(
                (*VALID_NETWORKS, f"  {VALID_NETWORKS[0]}  ")
            ),
            "IPv6": f"{nine_valid}\n2001:db8::/32",
            "host bits set": f"{nine_valid}\n203.0.113.1/25",
            "malformed CIDR": f"{nine_valid}\nnot-a-cidr",
            "default route": f"{nine_valid}\n0.0.0.0/0",
            "loopback": f"{nine_valid}\n127.0.0.0/8",
            "link local": f"{nine_valid}\n169.254.0.0/16",
            "multicast": f"{nine_valid}\n224.0.0.0/4",
            "RFC 1918 class A": f"{nine_valid}\n10.0.0.0/8",
            "RFC 1918 class B": f"{nine_valid}\n172.16.0.0/12",
            "RFC 1918 class C": f"{nine_valid}\n192.168.0.0/16",
        }

        for name, input_text in cases.items():
            with self.subTest(name=name):
                self.assert_rejected(input_text)

    def test_rejects_unreadable_input_without_partial_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            input_path = root / "missing.txt"
            output_path = root / "cloudflare.auto.tfvars.json"

            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(input_path), str(output_path)],
                capture_output=True,
                text=True,
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stdout, "")
            self.assertEqual(result.stderr, FAILURE)
            self.assertFalse(output_path.exists())

    def test_rejects_unwritable_output_without_partial_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            input_path = root / "cloudflare-ips-v4.txt"
            blocked_parent = root / "not-a-directory"
            output_path = blocked_parent / "cloudflare.auto.tfvars.json"
            input_path.write_text("\n".join(VALID_NETWORKS), encoding="utf-8")
            blocked_parent.write_text("blocked", encoding="utf-8")

            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(input_path), str(output_path)],
                capture_output=True,
                text=True,
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stdout, "")
            self.assertEqual(result.stderr, FAILURE)
            self.assertFalse(output_path.exists())


if __name__ == "__main__":
    unittest.main()
