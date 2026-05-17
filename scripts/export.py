#!/usr/bin/env python3
"""
Export all entries from the personal-timeline SQLite database to JSON.

Works offline — needs only the .db file (and the key if encrypted).

Usage:
    python3 export.py timeline.db export.json
    python3 export.py timeline.db export.json --key <base64-ENCRYPTION_KEY>

Requires: pip install cryptography   (only when the database is encrypted)
"""

import argparse
import base64
import json
import sqlite3
import struct
import sys


def _load_aesgcm(key_bytes):
    try:
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        return AESGCM(key_bytes)
    except ImportError:
        print(
            "error: 'cryptography' package is required for encrypted databases.\n"
            "       install with: pip install cryptography",
            file=sys.stderr,
        )
        sys.exit(1)


def decrypt(aesgcm, blob, aad):
    """Decrypt nonce(12) || ciphertext+authTag from AES-256-GCM blob."""
    blob = bytes(blob)
    nonce, ct = blob[:12], blob[12:]
    return aesgcm.decrypt(nonce, ct, bytes(aad))


def decode_geo(b):
    """Unpack 16 big-endian bytes -> (lat, lon) float64, matching encodeGeo in crypto.go."""
    lat, lon = struct.unpack(">dd", bytes(b))
    return lat, lon


def export(db_path, key_b64, output_path):
    key_bytes = None
    if key_b64:
        key_bytes = base64.b64decode(key_b64)
        if len(key_bytes) != 32:
            print(f"error: key must decode to 32 bytes (got {len(key_bytes)})", file=sys.stderr)
            sys.exit(1)
        aesgcm = _load_aesgcm(key_bytes)
    else:
        aesgcm = None

    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    cur.execute("""
        SELECT
            e.id,
            e.text,
            e.text_cipher,
            e.created_at,
            e.edited_at,
            e.automated,
            e.lat,
            e.lon,
            e.geo_cipher,
            e.entry_hash,
            GROUP_CONCAT(h.tag ORDER BY h.tag) AS tags
        FROM entries e
        LEFT JOIN hashtags h ON h.entry_id = e.id
        GROUP BY e.id
        ORDER BY e.created_at ASC
    """)

    entries = []
    for row in cur.fetchall():
        text = row["text"]
        lat = row["lat"]
        lon = row["lon"]
        entry_hash = row["entry_hash"]

        if row["text_cipher"] is not None:
            if aesgcm is None:
                print(
                    f"error: entry {row['id']} is encrypted — provide --key",
                    file=sys.stderr,
                )
                sys.exit(1)
            text = decrypt(aesgcm, row["text_cipher"], entry_hash).decode("utf-8")

        if row["geo_cipher"] is not None:
            if aesgcm is None:
                print(
                    f"error: entry {row['id']} has encrypted geo — provide --key",
                    file=sys.stderr,
                )
                sys.exit(1)
            lat, lon = decode_geo(decrypt(aesgcm, row["geo_cipher"], entry_hash))

        entry = {
            "id": row["id"],
            "text": text,
            "created_at": row["created_at"],
            "edited_at": row["edited_at"],
            "automated": bool(row["automated"]),
            "hashtags": row["tags"].split(",") if row["tags"] else [],
        }
        if lat is not None:
            entry["lat"] = lat
            entry["lon"] = lon

        entries.append(entry)

    con.close()

    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(entries, f, ensure_ascii=False, indent=2)

    print(f"exported {len(entries)} entries → {output_path}")


def main():
    parser = argparse.ArgumentParser(
        description="Export personal-timeline database to JSON"
    )
    parser.add_argument("db", help="path to the SQLite database file")
    parser.add_argument("output", help="output JSON file")
    parser.add_argument(
        "--key", "-k",
        metavar="BASE64_KEY",
        help="base64-encoded 32-byte AES key (value of ENCRYPTION_KEY env var)",
    )
    args = parser.parse_args()
    export(args.db, args.key, args.output)


if __name__ == "__main__":
    main()
