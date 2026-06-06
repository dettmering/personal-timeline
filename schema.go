package main

import "net/http"

// schemaJSON is a static, self-describing document of the API's output formats,
// written for LLM consumers so they can understand the JSON they receive without
// reverse-engineering it. It is intentionally hand-maintained (not reflected) so
// the descriptions stay human/LLM-readable. Keep it in sync with the structs in
// store.go / seal.go and the handlers in handlers.go.
const schemaJSON = `{
  "name": "Personal Timeline API",
  "description": "Day-logging microblog. All responses are JSON with Content-Type 'application/json; charset=utf-8'. Errors use HTTP status >= 400 with the shape {\"error\": \"<message>\"}.",
  "conventions": {
    "timestamps": "RFC3339 with nanoseconds, in UTC (suffix 'Z'), e.g. '2026-06-06T14:03:21.123456789Z'.",
    "dates": "Calendar days as 'YYYY-MM-DD', interpreted in the server timezone (TZ env), not UTC.",
    "byte_fields": "Fields documented as 'base64 bytes' are SHA-256 digests (32 bytes) JSON-encoded by Go as standard base64 strings.",
    "optional_fields": "Fields marked optional may be absent from the object entirely (Go 'omitempty'), not present-but-null.",
    "errors": "{\"error\": \"<message>\"}; status codes: 400 bad input, 401 missing/invalid API key, 403 not editable/deletable, 404 not found, 500 server error."
  },
  "objects": {
    "Entry": {
      "description": "A single timeline post.",
      "fields": {
        "id": {"type": "integer", "description": "Unique entry id."},
        "text": {"type": "string", "description": "Post body, max 10000 characters. May contain #hashtags."},
        "created_at": {"type": "string (RFC3339Nano UTC)", "description": "Creation timestamp."},
        "edited_at": {"type": "string (RFC3339Nano UTC)", "optional": true, "description": "Last edit timestamp; absent if never edited."},
        "automated": {"type": "boolean", "description": "True if the entry was created via an automated/API-key request rather than by a human in the UI."},
        "hashtags": {"type": "array of string", "description": "Lowercased hashtags extracted from text (without the leading '#'). Empty array if none."},
        "lat": {"type": "number", "optional": true, "description": "Latitude (-90..90). Present only if the entry has geo coordinates; always paired with 'lon'."},
        "lon": {"type": "number", "optional": true, "description": "Longitude (-180..180). Present only with 'lat'."}
      },
      "example": {
        "id": 42,
        "text": "Morgenlauf am See #sport #morgen",
        "created_at": "2026-06-06T05:12:44.123456789Z",
        "automated": false,
        "hashtags": ["sport", "morgen"],
        "lat": 52.5163,
        "lon": 13.3777
      }
    },
    "DaySeal": {
      "description": "Tamper-evident seal over all entries of one past calendar day. Days are sealed after they end; the seals form a hash chain across days.",
      "fields": {
        "date": {"type": "string (YYYY-MM-DD)", "description": "The sealed calendar day in server TZ."},
        "entry_count": {"type": "integer", "description": "Number of entries on that day."},
        "merkle_root": {"type": "string (base64 bytes)", "description": "SHA-256 Merkle root over the sorted per-entry hashes."},
        "prev_seal_hash": {"type": "string (base64 bytes)", "optional": true, "description": "seal_hash of the previous sealed day; absent for the first seal."},
        "seal_hash": {"type": "string (base64 bytes)", "description": "SHA-256(date || merkle_root || prev_seal_hash); links the chain."},
        "sealed_at": {"type": "string (RFC3339Nano UTC)", "description": "When the seal was written."},
        "has_ots_proof": {"type": "boolean", "description": "True if an OpenTimestamps .ots proof has been stored for this seal."},
        "ots_upgraded_at": {"type": "string (RFC3339Nano UTC)", "optional": true, "description": "Set once the OTS proof carries a Bitcoin block attestation; absent while still pending."}
      }
    },
    "VerifyResult": {
      "description": "Result of verifying the full seal chain.",
      "fields": {
        "entries_checked": {"type": "integer"},
        "days_checked": {"type": "integer"},
        "chain_ok": {"type": "boolean", "description": "True if every seal still matches its entries and the chain is intact."},
        "first_broken_day": {"type": "string (YYYY-MM-DD)", "optional": true, "description": "First day where verification failed; absent if chain_ok."},
        "break_reason": {"type": "string", "optional": true, "description": "Human-readable reason for the break; absent if chain_ok."},
        "ots_present": {"type": "integer", "description": "Number of seals that have an OTS proof."},
        "ots_upgraded": {"type": "integer", "description": "Number of seals whose OTS proof has a Bitcoin attestation."}
      }
    }
  },
  "endpoints": {
    "GET /api/entries": {
      "description": "List entries. The query parameters select the mode and the response envelope.",
      "modes": {
        "day (default)": {
          "params": "?date=YYYY-MM-DD (default: today)",
          "response": "{\"date\": \"YYYY-MM-DD\", \"entries\": [Entry, ...]}"
        },
        "range": {
          "params": "?from=YYYY-MM-DD&to=YYYY-MM-DD (both required)",
          "response": "{\"from\": \"YYYY-MM-DD\", \"to\": \"YYYY-MM-DD\", \"entries\": [Entry, ...]}"
        },
        "hashtag": {
          "params": "?hashtag=<tag>&limit=<n> (leading '#' optional; limit optional positive int)",
          "response": "{\"hashtag\": \"<lowercased tag>\", \"entries\": [Entry, ...]}"
        },
        "search": {
          "params": "?q=<text>&limit=<n> (limit default 20)",
          "response": "{\"entries\": [Entry, ...]}"
        }
      }
    },
    "GET /api/entries/{id}": {"description": "Single entry by id.", "response": "Entry"},
    "POST /api/entries": {
      "description": "Create an entry. Body: {\"text\": string (required), \"automated\": bool, \"lat\": number, \"lon\": number}. lat/lon must be supplied together; automated=true requires the API key.",
      "response": "Entry (HTTP 201)"
    },
    "PUT /api/entries/{id}": {
      "description": "Edit text of a same-day, non-automated entry. Body: {\"text\": string}.",
      "response": "Entry"
    },
    "DELETE /api/entries/{id}": {"description": "Delete a same-day, non-automated entry.", "response": "empty (HTTP 204)"},
    "GET /api/hashtags": {"description": "All known hashtags sorted by usage count.", "response": "{\"hashtags\": [\"tag\", ...]}"},
    "GET /api/config": {"description": "UI feature flags.", "response": "{\"show_permalink\": bool, \"show_quote\": bool}"},
    "GET /api/health": {"description": "Liveness probe.", "response": "{\"status\": \"ok\"}"},
    "GET /api/verify": {"description": "Verify the whole seal chain.", "response": "VerifyResult"},
    "GET /api/seals": {"description": "All day seals.", "response": "{\"seals\": [DaySeal, ...]}"},
    "GET /api/seals/{date}": {"description": "Single seal for a date.", "response": "DaySeal"},
    "GET /api/seals/{date}/proof.ots": {"description": "Binary OpenTimestamps proof.", "response": "application/vnd.opentimestamps.ots (binary attachment)"},
    "POST /api/seals/{date}": {"description": "Manually seal a past day (requires API key).", "response": "{\"seal\": DaySeal, \"created\": bool}"},
    "GET /api/schema": {"description": "This document.", "response": "this schema"}
  }
}
`

// schema serves a static, self-describing document of the output formats so that
// LLM clients can understand the JSON they receive.
func (a *API) schema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(schemaJSON))
}
