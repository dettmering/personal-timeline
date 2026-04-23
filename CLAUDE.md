# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Personal Timeline is a day-logging microsite (similar to Twitter/microblog) for personal journaling. It's a single-binary Go server with an embedded vanilla JS frontend, backed by SQLite.

## Development Commands

```bash
# Run locally
DB_PATH=/tmp/timeline.db LISTEN_ADDR=:8080 go run ./

# Build binary
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o timeline ./

# Run with Docker Compose
docker-compose up --build
```

No test suite or linter is configured.

## Architecture

Three Go source files:

- **main.go** — env config (`DB_PATH`, `LISTEN_ADDR`, `API_KEY`, `TZ`, `WEBHOOK_URL`), embeds `static/`, registers routes, starts server
- **handlers.go** — HTTP API (`GET/POST /api/entries`, `PUT /api/entries/{id}`, `GET /api/health`); enforces API key for automated entries, character limits, timezone-aware date parsing; fires outbound webhook on entry creation
- **store.go** — SQLite data layer; two tables (`entries`, `hashtags`); WAL mode, single connection; enforces same-day edit restriction at DB layer; timezone-aware calendar-day queries

Frontend (`static/`) is a single-page vanilla JS app with no build step. `app.js` manages state (`view`, `date`, `hashtag`) and communicates with the API. Static files are embedded in the binary at build time.

## Key Design Decisions

- **Same-day edit restriction**: enforced in `store.go`, not just the frontend — only non-automated entries created today can be edited
- **API key auth**: optional; required only for `automated=true` entries (Bearer token or `X-API-Key` header)
- **Hashtag extraction**: regex `#([\p{L}\p{N}_]+)` in `store.go`; stored in a separate `hashtags` table for cross-day filtering
- **Timestamps**: stored as RFC3339Nano UTC; timezone conversion happens at query time using the configured `TZ` env var
- **Single binary deployment**: static files embedded via `go:embed`; SQLite via `modernc.org/sqlite` (no CGo needed when using the pure-Go driver, but build sets `CGO_ENABLED=0`)
- **Webhook on create**: if `WEBHOOK_URL` is set, every successful `POST /api/entries` fires an async `POST` to that URL with the created entry JSON (same shape as the API response). Dispatched in a goroutine with a 10s timeout; failures are logged and never block the API response.
