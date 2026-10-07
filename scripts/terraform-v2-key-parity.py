#!/usr/bin/env python3
"""Compare protected environment keys without state or secret-value output."""

import hashlib
import hmac
import os
import re
import sys

DOMAIN = b"mycfc/v2-untaint-key-parity/v1:"


def parity_probe(key_text: str, sha: str) -> str:
    if not re.fullmatch(r"[0-9a-f]{64}", key_text) or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("invalid protected diagnostic input")
    return hmac.new(bytes.fromhex(key_text), DOMAIN + sha.encode("ascii"), hashlib.sha256).hexdigest()


def main() -> int:
    if len(sys.argv) != 3 or sys.argv[1] not in ("fingerprint", "compare"):
        print("key-parity diagnostic refused", file=sys.stderr)
        return 1
    try:
        probe = parity_probe(os.environ.get("TF_PLAN_HMAC_KEY", ""), sys.argv[2])
    except ValueError:
        print("key-parity configuration refused", file=sys.stderr)
        return 1
    if sys.argv[1] == "fingerprint":
        print(probe)  # Captured privately into a job output; never runner logs.
        return 0
    expected = os.environ.get("EXPECTED_PARITY_PROBE", "")
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        print("key-parity binding refused", file=sys.stderr)
        return 1
    if hmac.compare_digest(probe, expected):
        print("key_parity=match")
        return 0
    print("key_parity=mismatch")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
