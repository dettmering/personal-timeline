# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Personal Timeline is a day-logging microsite (similar to Twitter/microblog) for personal journaling. It's a single-binary Go server with an embedded vanilla JS frontend, backed by SQLite.

## Development Commands

**Go is not installed on the host.** Always use Docker for compilation and running:

```bash
# Compile check (no go toolchain on host)
docker run --rm -v $(pwd):/src -w /src golang:1.22-alpine go build ./...

# Build production binary
docker build -t personal-timeline .

# Run with Docker Compose
docker compose up --build
```

No test suite or linter is configured.

## Architecture

Go source files:

- **main.go** — env config (`DB_PATH`, `LISTEN_ADDR`, `API_KEY`, `TZ`, `WEBHOOK_URL`), embeds `static/`, registers routes, starts server, runs the sealing and OTS-upgrade background loops
- **handlers.go** — HTTP API (`GET/POST /api/entries`, `PUT /api/entries/{id}`, `DELETE /api/entries/{id}`, `GET /api/hashtags`, `GET /api/health`, `GET /api/verify`, `GET /api/seals`, `GET /api/seals/{date}`, `GET /api/seals/{date}/proof.ots`, `POST /api/seals/{date}`); enforces API key for automated entries + manual seal triggers, character limits, timezone-aware date parsing; fires outbound webhook on entry creation
- **store.go** — SQLite data layer; three tables (`entries`, `hashtags`, `day_seals`); WAL mode, single connection; enforces same-day edit/delete restriction at DB layer; timezone-aware calendar-day queries; writes `entry_hash` on Create/Update
- **hash.go** — canonical, versioned SHA-256 serialization of entries (version || LP(date-in-tz) || LP(created_at-utc) || LP(automated) || LP(text))
- **seal.go** — `DaySeal` type, `SealDay` / `SealMissing` / `VerifyChain`; Merkle-over-entry-hashes per day, seal-hash chain across days
- **ots.go** — minimal OpenTimestamps HTTP client (no external deps); submit digest to calendars, build valid `.ots` proof files, parse and upgrade pending proofs when Bitcoin attestation becomes available

Frontend (`static/`) is a single-page vanilla JS app with no build step. `app.js` manages state (`view`, `date`, `hashtag`) and communicates with the API. Static files are embedded in the binary at build time. Past days show a small seal badge (`🔒`) that opens a dialog with the seal metadata and a download link for the `.ots` proof.

## Key Design Decisions

- **Same-day edit restriction**: enforced in `store.go`, not just the frontend — only non-automated entries created today can be edited
- **API key auth**: optional; required only for `automated=true` entries (Bearer token or `X-API-Key` header)
- **Hashtag extraction**: regex `#([\p{L}\p{N}_]+)` in `store.go`; stored in a separate `hashtags` table for cross-day filtering
- **Timestamps**: stored as RFC3339Nano UTC; timezone conversion happens at query time using the configured `TZ` env var
- **Single binary deployment**: static files embedded via `go:embed`; SQLite via `modernc.org/sqlite` (no CGo needed when using the pure-Go driver, but build sets `CGO_ENABLED=0`)
- **Webhook on create**: if `WEBHOOK_URL` is set, every successful `POST /api/entries` fires an async `POST` to that URL with the created entry JSON (same shape as the API response). Dispatched in a goroutine with a 10s timeout; failures are logged and never block the API response.
- **Hashtag autocomplete**: `GET /api/hashtags` returns all known tags sorted by usage count; the frontend caches this list and shows a filtered dropdown when the caret sits on a `#`-token in the composer or edit textarea. Tab/Enter commits, arrow keys navigate, Escape closes. The cache is refreshed after create/update.
- **Tamper-evident past days**: once a calendar day (in `TZ`) is over, a background loop writes a `day_seals` row containing `merkle_root` (SHA-256 over sorted `entry_hash`es) and `seal_hash = SHA-256(date || merkle_root || prev_seal_hash)`. The chain links every sealed day; modifying or deleting a sealed entry breaks `VerifyChain` at that point. Goal is tamper-*evidence*, not tamper-*proofness* — the operator of the DB can still rewrite history, but a mismatched chain makes that visible.
- **External time anchor (OpenTimestamps)**: after a seal is written, the `seal_hash` is POSTed to OpenTimestamps calendars (`a.pool.opentimestamps.org`, `b.pool.opentimestamps.org`, `finney.calendar.eternitywall.com`) and the returned proof is stored as a valid `.ots` file in `day_seals.ots_proof`. A 1 h ticker calls `{cal}/timestamp/{hex(commitment)}` to upgrade the pending attestation with a Bitcoin block attestation; `ots_upgraded_at` is set only once a Bitcoin attestation tag is present in the merged proof. Users can download the `.ots` proof from `/api/seals/{date}/proof.ots` and verify it offline with the external `ots verify` CLI.
- **Canonical entry hash**: `EntryHash` in `hash.go` is a versioned, length-prefixed serialization of `(date-in-tz, created_at-utc, automated, text)`. `edited_at` is deliberately excluded so same-day edits rehash cleanly before the day gets sealed. `id` is excluded because `created_at` is nanosecond-precise and effectively unique. On server startup, `BackfillHashes` fills `entry_hash` for any rows predating the migration.
