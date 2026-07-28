#!/usr/bin/env python3

import argparse
import ipaddress
import json
import os
import pathlib
import tempfile


SUCCESS = b"Slice 3 private subnet selection written\n"
FAILURE = b"Slice 3 private subnet selection failed\n"
INPUT_KEYS = {
    "vpc_cidr",
    "occupied_subnet_cidrs",
    "eligible_availability_zones",
}


class PrivateArgumentParser(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError from None


def select(payload):
    if not isinstance(payload, dict) or set(payload) != INPUT_KEYS:
        raise ValueError

    vpc_value = payload["vpc_cidr"]
    occupied_values = payload["occupied_subnet_cidrs"]
    zone_values = payload["eligible_availability_zones"]
    if (
        not isinstance(vpc_value, str)
        or not isinstance(occupied_values, list)
        or not isinstance(zone_values, list)
        or any(not isinstance(value, str) for value in occupied_values)
        or any(not isinstance(value, str) for value in zone_values)
    ):
        raise ValueError

    vpc = ipaddress.ip_network(vpc_value, strict=True)
    if vpc.version != 4 or vpc.prefixlen > 24:
        raise ValueError

    occupied = []
    for value in occupied_values:
        network = ipaddress.ip_network(value, strict=True)
        if network.version != 4 or not network.subnet_of(vpc):
            raise ValueError
        occupied.append(network)

    candidates = []
    for network in vpc.subnets(new_prefix=24):
        if not any(network.overlaps(existing) for existing in occupied):
            candidates.append(network)
            if len(candidates) == 2:
                break
    zones = sorted({zone for zone in zone_values if zone})
    if len(candidates) < 2 or len(zones) < 2:
        raise ValueError

    return {
        "database_a": {
            "availability_zone": zones[0],
            "cidr_block": str(candidates[0]),
        },
        "database_b": {
            "availability_zone": zones[1],
            "cidr_block": str(candidates[1]),
        },
    }


def write_private(path, payload):
    temporary_path = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=path.parent,
            prefix=f".{path.name}.",
            delete=False,
        ) as output:
            temporary_path = pathlib.Path(output.name)
            os.chmod(temporary_path, 0o600)
            json.dump(payload, output, sort_keys=True, separators=(",", ":"))
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary_path, path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)


def main():
    parser = PrivateArgumentParser(add_help=False)
    parser.add_argument("input_json")
    parser.add_argument("output_json")
    try:
        arguments = parser.parse_args()
        input_path = pathlib.Path(arguments.input_json)
        output_path = pathlib.Path(arguments.output_json)
        payload = json.loads(input_path.read_text(encoding="utf-8"))
        write_private(output_path, select(payload))
    except (OSError, TypeError, ValueError):
        os.write(2, FAILURE)
        return 1

    os.write(1, SUCCESS)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
