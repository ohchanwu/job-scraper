#!/usr/bin/env python3

import argparse
import ipaddress
import json
import os
import pathlib
import tempfile


SUCCESS = b"Cloudflare IPv4 set verified\n"
FAILURE = b"Cloudflare IPv4 set rejected\n"
DEFAULT_ROUTE = ipaddress.ip_network("0.0.0.0/0")
FORBIDDEN = tuple(
    map(
        ipaddress.ip_network,
        (
            "127.0.0.0/8",
            "169.254.0.0/16",
            "224.0.0.0/4",
            "10.0.0.0/8",
            "172.16.0.0/12",
            "192.168.0.0/16",
        ),
    )
)


class PrivateArgumentParser(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError from None


def normalize(input_text):
    networks = []
    seen = set()
    for line in input_text.splitlines():
        value = line.strip()
        if not value:
            continue
        network = ipaddress.ip_network(value, strict=True)
        if (
            network.version != 4
            or network == DEFAULT_ROUTE
            or any(network.overlaps(boundary) for boundary in FORBIDDEN)
        ):
            raise ValueError
        canonical = network.with_prefixlen
        if canonical in seen:
            raise ValueError
        seen.add(canonical)
        networks.append(network)

    if not 10 <= len(networks) <= 20:
        raise ValueError
    networks.sort(key=lambda network: (int(network.network_address), network.prefixlen))
    return {
        "cloudflare_ipv4_cidrs": [
            network.with_prefixlen for network in networks
        ]
    }


def write_json(path, payload):
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
    parser.add_argument("input")
    parser.add_argument("output")
    try:
        arguments = parser.parse_args()
        input_path = pathlib.Path(arguments.input)
        output_path = pathlib.Path(arguments.output)
        payload = normalize(input_path.read_text(encoding="utf-8"))
        write_json(output_path, payload)
    except (OSError, TypeError, ValueError):
        os.write(2, FAILURE)
        return 1

    os.write(1, SUCCESS)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
