#!/usr/bin/env python3
"""
Validate a personal-timeline day-seal against its OpenTimestamps proof.

Usage:
    verify_seal.py <date> [--api URL] [--mempool URL]
    verify_seal.py --ots <file> --seal-hash <hex> [--mempool URL]

Examples:
    verify_seal.py 2026-04-30
    verify_seal.py 2026-04-30 --api https://timeline.example.com
    verify_seal.py --ots 2026-04-30.ots \\
        --seal-hash 9f3a...c1

What it does:
1. Fetches the seal metadata + .ots proof from the personal-timeline API
   (or reads them from disk).
2. Confirms that the API-reported seal_hash matches the digest the .ots
   file commits to.
3. Walks the proof's SHA256 / append / prepend ops to compute the final
   commitment.
4. For a Bitcoin attestation: fetches the block header from mempool.space
   and verifies that the proof's commitment equals the block's merkle root.
5. For a pending attestation: prints the calendar URL and notes that the
   proof has not yet been anchored to Bitcoin.

Only the Python standard library is required.
"""

import argparse
import hashlib
import json
import sys
import urllib.request
from typing import Optional


OTS_MAGIC = bytes.fromhex(
    "004f70656e54696d657374616d7073000050726f6f6600bf89e2e884e89294"
)
OTS_VERSION = 0x01
OP_SHA256 = 0x08
OP_RIPEMD160 = 0x03
OP_APPEND = 0xF0
OP_PREPEND = 0xF1
ATTESTATION = 0x00
FORK = 0xFF

PENDING_TAG = bytes.fromhex("83dfe30d2ef90c8e")
BITCOIN_TAG = bytes.fromhex("0588960d73d71901")


def read_varint(buf: bytes, pos: int) -> tuple[int, int]:
    """Decode an LEB128/Bitcoin-style unsigned varint. Returns (value, bytes_read)."""
    result = 0
    shift = 0
    start = pos
    while True:
        if pos >= len(buf):
            raise ValueError("varint truncated")
        b = buf[pos]
        pos += 1
        result |= (b & 0x7F) << shift
        if (b & 0x80) == 0:
            break
        shift += 7
        if shift > 63:
            raise ValueError("varint too long")
    return result, pos - start


class Attestation:
    def __init__(self, kind: str, commitment: bytes, detail):
        self.kind = kind  # "pending" | "bitcoin" | "unknown"
        self.commitment = commitment
        self.detail = detail  # url for pending, height for bitcoin, tag for unknown


def walk_proof(proof: bytes) -> tuple[bytes, list[Attestation]]:
    """
    Parse a .ots proof and walk every branch.

    Returns (file_digest, attestations). Each attestation includes its commitment,
    i.e. the byte string standing at that point in the operations tree.
    """
    if len(proof) < len(OTS_MAGIC) + 1 + 1 + 32:
        raise ValueError("proof too short")
    if proof[: len(OTS_MAGIC)] != OTS_MAGIC:
        raise ValueError("bad magic header")
    pos = len(OTS_MAGIC)
    if proof[pos] != OTS_VERSION:
        raise ValueError(f"unsupported version 0x{proof[pos]:02x}")
    pos += 1
    if proof[pos] != OP_SHA256:
        raise ValueError(f"unsupported file-hash op 0x{proof[pos]:02x}")
    pos += 1
    file_digest = proof[pos : pos + 32]
    pos += 32

    attestations: list[Attestation] = []

    def walk(buf: bytes, pos: int, current: bytes) -> int:
        while pos < len(buf):
            op = buf[pos]
            if op == FORK:
                pos += 1
                # left branch
                pos = walk(buf, pos, current)
                # right branch continues from same `current`
                continue
            if op == ATTESTATION:
                pos += 1
                if pos + 8 > len(buf):
                    raise ValueError("truncated attestation tag")
                tag = buf[pos : pos + 8]
                pos += 8
                pl_len, n = read_varint(buf, pos)
                pos += n
                if pos + pl_len > len(buf):
                    raise ValueError("truncated attestation payload")
                payload = buf[pos : pos + pl_len]
                pos += pl_len
                if tag == PENDING_TAG:
                    url_len, m = read_varint(payload, 0)
                    url = payload[m : m + url_len].decode("utf-8", "replace")
                    attestations.append(Attestation("pending", current, url))
                elif tag == BITCOIN_TAG:
                    height, _ = read_varint(payload, 0)
                    attestations.append(Attestation("bitcoin", current, height))
                else:
                    attestations.append(Attestation("unknown", current, tag.hex()))
                return pos
            if op == OP_SHA256:
                pos += 1
                current = hashlib.sha256(current).digest()
                continue
            if op == OP_RIPEMD160:
                pos += 1
                current = hashlib.new("ripemd160", current).digest()
                continue
            if op in (OP_APPEND, OP_PREPEND):
                pos += 1
                l, n = read_varint(buf, pos)
                pos += n
                if pos + l > len(buf):
                    raise ValueError("truncated op data")
                data = buf[pos : pos + l]
                pos += l
                current = current + data if op == OP_APPEND else data + current
                continue
            raise ValueError(f"unsupported op 0x{op:02x} at offset {pos}")
        return pos

    walk(proof, pos, file_digest)
    return file_digest, attestations


def fetch(url: str, timeout: int = 30) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "verify_seal.py"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def fetch_block_merkle_root(height: int, mempool_base: str) -> bytes:
    """Return the Bitcoin block's merkle_root in OTS-internal byte order (LE)."""
    base = mempool_base.rstrip("/")
    block_hash = fetch(f"{base}/api/block-height/{height}").decode().strip()
    if len(block_hash) != 64:
        raise ValueError(f"unexpected block hash from mempool: {block_hash!r}")
    header_hex = fetch(f"{base}/api/block/{block_hash}/header").decode().strip()
    header = bytes.fromhex(header_hex)
    if len(header) != 80:
        raise ValueError(f"unexpected header length {len(header)}")
    return header[36:68]


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("date", nargs="?", help="YYYY-MM-DD; fetches seal+proof from --api")
    p.add_argument("--api", default="http://localhost:8080", help="personal-timeline base URL")
    p.add_argument("--ots", help="path to a local .ots file (skips API)")
    p.add_argument("--seal-hash", help="expected seal_hash hex (required with --ots)")
    p.add_argument("--mempool", default="https://mempool.space", help="mempool.space-compatible base URL")
    args = p.parse_args()

    expected_seal: Optional[bytes] = None
    proof: bytes
    label: str

    if args.ots:
        if not args.seal_hash:
            p.error("--seal-hash is required when using --ots")
        with open(args.ots, "rb") as f:
            proof = f.read()
        expected_seal = bytes.fromhex(args.seal_hash)
        label = args.ots
    else:
        if not args.date:
            p.error("provide a date or --ots")
        api = args.api.rstrip("/")
        meta = json.loads(fetch(f"{api}/api/seals/{args.date}").decode())
        # Go encodes []byte as base64 in JSON; fall back to hex for tools that
        # might serialize it differently.
        expected_seal = _decode_seal_field(meta["seal_hash"])
        proof = fetch(f"{api}/api/seals/{args.date}/proof.ots")
        label = f"{args.date} (via {api})"
        print(f"date:        {meta['date']}")
        print(f"entries:     {meta['entry_count']}")
        print(f"sealed_at:   {meta['sealed_at']}")

    print(f"proof:       {label}  ({len(proof)} bytes)")

    file_digest, attestations = walk_proof(proof)
    print(f"file_digest: {file_digest.hex()}")

    if expected_seal != file_digest:
        print(f"FAIL: seal_hash {expected_seal.hex()} does not match .ots digest")
        return 2
    print("OK:   seal_hash matches the digest the .ots file commits to")

    if not attestations:
        print("FAIL: proof contains no attestation")
        return 2

    overall_ok = True
    for i, att in enumerate(attestations, 1):
        print(f"\nattestation #{i}: {att.kind}")
        print(f"  commitment: {att.commitment.hex()}")
        if att.kind == "pending":
            print(f"  calendar:   {att.detail}")
            print("  status:     not yet anchored to Bitcoin (re-run after upgrade)")
        elif att.kind == "bitcoin":
            height = att.detail
            print(f"  block:      {height}")
            try:
                onchain = fetch_block_merkle_root(height, args.mempool)
            except Exception as e:
                print(f"  ERROR fetching block header: {e}")
                overall_ok = False
                continue
            print(f"  merkle:     {onchain.hex()}  (display: {onchain[::-1].hex()})")
            if onchain == att.commitment:
                print("  OK:         commitment matches Bitcoin block merkle_root")
            else:
                print("  FAIL:       commitment does NOT match block merkle_root")
                overall_ok = False
        else:
            print(f"  unknown attestation tag: {att.detail}")
            overall_ok = False

    has_btc = any(a.kind == "bitcoin" for a in attestations)
    print()
    if not has_btc:
        print("RESULT: proof is well-formed but only pending — no Bitcoin anchor yet.")
        return 1
    print("RESULT: VERIFIED" if overall_ok else "RESULT: FAILED")
    return 0 if overall_ok else 2


def _decode_seal_field(s: str) -> bytes:
    import base64
    if len(s) == 64:
        try:
            return bytes.fromhex(s)
        except ValueError:
            pass
    return base64.b64decode(s)


if __name__ == "__main__":
    sys.exit(main())
