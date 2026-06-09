# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Personal Timeline is a day-logging microsite (similar to Twitter/microblog) for personal journaling. It's a single-binary Go server with an embedded vanilla JS frontend, backed by SQLite.

## Development Commands

**Go is not installed on the host.** Always use Docker for compilation and running:

```bash
# Compile check (no go toolchain on host)
docker run --rm -v $(pwd):/src -w /src golang:1.22-alpine go build ./...

# Run the unit tests (no go toolchain on host)
docker run --rm -v $(pwd):/src -w /src golang:1.22-alpine go test ./...

# Build production binary
docker build -t personal-timeline .

# Run with Docker Compose
docker compose up --build
```

No linter is configured. Unit tests live in `*_test.go` files (`hash_test.go`, `crypto_test.go`, `seal_test.go`) and run with the command above. Both Gitea workflows (`.gitea/workflows/dockerhub.yml`, `build-binary.yml`) run `go test ./...` as a gating step before building.

## Testing rules

- **Every feature ships with meaningful tests.** When you add or change behavior, add tests that would actually fail if the behavior regressed — not placeholder assertions. A test that can't fail is worse than no test.
- Prioritize the pure, security-critical logic that already has coverage: canonical hashing (`hash.go`), at-rest crypto (`crypto.go`), and the seal/merkle chain (`seal.go`). Extend those suites when you touch those areas.
- For store-level features, use a temp-file `Store` via the `newTestStore(t)` helper pattern in `seal_test.go` (`OpenStore(filepath.Join(t.TempDir(), "test.db"), time.UTC)`); don't mock the DB.
- Test the failure paths too (tampered ciphertext, wrong key, broken seal chain), not just the happy path. Tamper-evidence and encryption are the whole point of this project, so a green suite must prove the bad cases are caught.
- Run `go test ./...` (in Docker, per above) before considering a change done. Don't add a feature that lowers the bar by leaving its logic untested.

## Architecture

Go source files:

- **main.go** — env config (`DB_PATH`, `LISTEN_ADDR`, `API_KEY`, `TZ`, `WEBHOOK_URL`, `ENCRYPTION_KEY`, `SHOW_PERMALINK`, `SHOW_QUOTE`), embeds `static/`, registers routes, starts server, runs the sealing and OTS-upgrade background loops. Boot order: `migrate` → `BackfillHashes` → `EncryptBackfill` → serve.
- **handlers.go** — HTTP API (`GET/POST /api/entries`, `PUT /api/entries/{id}`, `DELETE /api/entries/{id}`, `GET /api/hashtags`, `GET /api/health`, `GET /api/config`, `GET /api/schema`, `GET /api/verify`, `GET /api/seals`, `GET /api/seals/{date}`, `GET /api/seals/{date}/proof.ots`, `POST /api/seals/{date}`); enforces API key for automated entries + manual seal triggers, character limits, timezone-aware date parsing; validates optional `lat`/`lon` ranges on create; fires outbound webhook on entry creation
- **schema.go** — serves `GET /api/schema`, a static, hand-maintained JSON document describing the API's output formats (conventions, the `Entry`/`DaySeal`/`VerifyResult` objects, and per-endpoint response envelopes) so LLM clients can understand the JSON they receive without reverse-engineering it
- **store.go** — SQLite data layer; three tables (`entries`, `hashtags`, `day_seals`); WAL mode, single connection; enforces same-day edit/delete restriction at DB layer; timezone-aware calendar-day queries; writes `entry_hash` on Create/Update; persists optional `lat`/`lon` REAL columns and preserves them across edits. When a cipher is configured, `text` and geo coords are stored encrypted in `text_cipher`/`geo_cipher` BLOB columns instead of their plaintext columns, and decrypted in `scanEntryRow` on read. `EncryptBackfill` migrates pre-existing plaintext rows once at startup without touching `entry_hash`.
- **hash.go** — canonical, versioned SHA-256 serialization of entries. v1: `version || LP(date-in-tz) || LP(created_at-utc) || LP(automated) || LP(text)`. v2 (when geo coords are present): same as v1 plus `|| LP(lat-be64) || LP(lon-be64)` where `*-be64` is the 8-byte big-endian IEEE 754 binary64 bit pattern. Entries without coords keep using v1 so existing seals stay valid.
- **crypto.go** — minimal AES-256-GCM wrapper (stdlib only). `NewCipher` decodes a base64 32-byte key; `Encrypt`/`Decrypt` produce/parse `nonce(12) || ciphertext || authTag(16)` and bind `entry_hash` as AAD. `encodeGeo`/`decodeGeo` pack/unpack lat/lon as 16 big-endian bytes — same layout the v2 hash already commits to.
- **seal.go** — `DaySeal` type, `SealDay` / `SealMissing` / `VerifyChain`; Merkle-over-entry-hashes per day, seal-hash chain across days. `recomputeDay` decrypts `text_cipher`/`geo_cipher` (when present) before re-deriving `EntryHash`, so verification works equally on plaintext and encrypted rows.
- **ots.go** — minimal OpenTimestamps HTTP client (no external deps); submit digest to calendars, build valid `.ots` proof files, parse and upgrade pending proofs when Bitcoin attestation becomes available

Frontend (`static/`) is a single-page vanilla JS app with no build step. `app.js` manages state (`view`, `date`, `hashtag`) and communicates with the API. Static files are embedded in the binary at build time. Past days show a small seal badge (`🔒`) that opens a dialog with the seal metadata and a download link for the `.ots` proof.

## Key Design Decisions

- **Same-day edit restriction**: enforced in `store.go`, not just the frontend — only non-automated entries created today can be edited
- **API key auth**: optional; required only for `automated=true` entries (Bearer token or `X-API-Key` header)
- **Hashtag extraction**: regex `#([\p{L}\p{N}_]+)` in `store.go`; stored in a separate `hashtags` table for cross-day filtering
- **Timestamps**: stored as RFC3339Nano UTC; timezone conversion happens at query time using the configured `TZ` env var
- **Single binary deployment**: static files embedded via `go:embed`; SQLite via `modernc.org/sqlite` (no CGo needed when using the pure-Go driver, but build sets `CGO_ENABLED=0`)
- **Optional UI buttons (permalink/quote)**: the per-entry permalink (🔗) and quote (❝) buttons are hidden by default. Set `SHOW_PERMALINK=true` and/or `SHOW_QUOTE=true` to enable them. The flags are surfaced to the frontend via `GET /api/config` (`{show_permalink, show_quote}`); `app.js` fetches them in `init()` before the first render and gates button creation in `renderEntries`. The underlying `copyPermalink`/`quoteEntry` handlers and the `#/entry/<id>` permalink routing stay intact regardless of the flags.
- **Webhook on create**: if `WEBHOOK_URL` is set, every successful `POST /api/entries` fires an async `POST` to that URL with the created entry JSON (same shape as the API response). Dispatched in a goroutine with a 10s timeout; failures are logged and never block the API response.
- **Hashtag autocomplete**: `GET /api/hashtags` returns all known tags sorted by usage count; the frontend caches this list and shows a filtered dropdown when the caret sits on a `#`-token in the composer or edit textarea. Tab/Enter commits, arrow keys navigate, Escape closes. The cache is refreshed after create/update.
- **Tamper-evident past days**: once a calendar day (in `TZ`) is over, a background loop writes a `day_seals` row containing `merkle_root` (SHA-256 over sorted `entry_hash`es) and `seal_hash = SHA-256(date || merkle_root || prev_seal_hash)`. The chain links every sealed day; modifying or deleting a sealed entry breaks `VerifyChain` at that point. Goal is tamper-*evidence*, not tamper-*proofness* — the operator of the DB can still rewrite history, but a mismatched chain makes that visible.
- **External time anchor (OpenTimestamps)**: after a seal is written, the `seal_hash` is POSTed to OpenTimestamps calendars (`a.pool.opentimestamps.org`, `b.pool.opentimestamps.org`, `finney.calendar.eternitywall.com`) and the returned proof is stored as a valid `.ots` file in `day_seals.ots_proof`. A 1 h ticker calls `{cal}/timestamp/{hex(commitment)}` to upgrade the pending attestation with a Bitcoin block attestation; `ots_upgraded_at` is set only once a Bitcoin attestation tag is present in the merged proof. Users can download the `.ots` proof from `/api/seals/{date}/proof.ots` and verify it offline with the external `ots verify` CLI.
- **Canonical entry hash**: `EntryHash` in `hash.go` is a versioned, length-prefixed serialization of `(date-in-tz, created_at-utc, automated, text [, lat, lon])`. `edited_at` is deliberately excluded so same-day edits rehash cleanly before the day gets sealed. `id` is excluded because `created_at` is nanosecond-precise and effectively unique. On server startup, `BackfillHashes` fills `entry_hash` for any rows predating the migration.
- **Geo coordinates**: `POST /api/entries` accepts optional `lat`/`lon` floats. Both must be present together; ranges `-90..90` and `-180..180`. When set they're stored as REAL columns and folded into the canonical hash via the v2 layout (8-byte big-endian `math.Float64bits` of each), so any later mutation of the coords breaks the day seal exactly like a text edit would. Coords are immutable across edits — `Update` re-reads the existing values and rehashes with them. Entries without coords use v1 hashing unchanged, keeping pre-existing seals valid.
- **Self-describing output schema**: `GET /api/schema` returns a static JSON document (`schemaJSON` in `schema.go`) that describes the API's output formats for LLM consumers — global conventions (timestamp/date formats, base64 byte fields, `omitempty`, error shape), the `Entry`/`DaySeal`/`VerifyResult` objects field-by-field, and the per-endpoint response envelopes (including the four modes of `GET /api/entries`). It is **hand-maintained, not reflected** — when you add/rename/remove fields on those structs or add endpoints, update `schemaJSON` to match. Validate the constant stays well-formed JSON after editing.
- **At-rest encryption**: optional, enabled when `ENCRYPTION_KEY` (base64-encoded 32-byte AES key) is set. Threat model is „attacker has the SQLite file" — backups, snapshots, stolen disk. API responses and the webhook payload remain plaintext. Encrypted fields: `text` and `lat`/`lon` (the latter as a single 16-byte blob in `geo_cipher`). Hashtags stay plaintext so JOIN-based tag filtering still works; the accepted leak is *which* tags exist, not the entry text. Format per blob: `nonce(12) || AES-256-GCM(plaintext, AAD=entry_hash) || tag(16)`. Binding `entry_hash` as AAD prevents swapping ciphertexts between rows. `Search` decrypts in memory and substring-filters in Go (acceptable for a personal journal). On startup with a key set, `EncryptBackfill` walks every plaintext row, encrypts `text` and (if present) the geo blob, and clears the plaintext columns — `entry_hash` is never touched, so all existing day seals and OTS Bitcoin anchors remain valid. Reading a row with `text_cipher IS NOT NULL` while no key is configured is a hard failure; reading with the wrong key fails the AEAD auth check and returns 500. The boot order `BackfillHashes` → `EncryptBackfill` is mandatory because `entry_hash` must exist before it can be used as AAD.
