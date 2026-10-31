# Personal Timeline

Tagebuch-Microsite zum Loggen des eigenen Tages, ähnlich einem privaten Microblog. Ein einzelnes Go-Binary mit eingebettetem Vanilla-JS-Frontend und SQLite als Speicher.

- Einträge mit `#hashtags`, Suche, Tages- und Zeitraumansicht
- Bearbeiten und Löschen nur am selben Tag
- **Manipulationsnachweis:** Jeder abgeschlossene Tag wird versiegelt (Merkle-Root über alle Einträge, Hash-Kette über die Tage) und per [OpenTimestamps](https://opentimestamps.org) in Bitcoin verankert
- Optionale Verschlüsselung der Datenbank (AES-256-GCM)
- Optionale Geo-Koordinaten pro Eintrag
- Webhook bei jedem neuen Eintrag

## Betrieb

```bash
docker compose up -d --build
```

Danach läuft die App auf <http://localhost:8080>, die Statistikseite auf `/dashboard`.

Fertige Images liegen auf Docker Hub unter `dettmering/personal-timeline` (`latest` = `master`, sonst der Git-Tag).

### Konfiguration

| Variable | Default | Bedeutung |
|---|---|---|
| `DB_PATH` | `/data/timeline.db` | Pfad zur SQLite-Datei |
| `LISTEN_ADDR` | `:8080` | Listen-Adresse |
| `TZ` | Systemzeitzone (`Europe/Berlin` im Image) | Zeitzone, in der Kalendertage gelten |
| `API_KEY` | – | Wenn gesetzt, nötig für automatisierte Einträge und manuelles Versiegeln |
| `ENCRYPTION_KEY` | – | Base64-kodierter 32-Byte-Schlüssel; aktiviert die Verschlüsselung |
| `WEBHOOK_URL` | – | Ziel für den Webhook bei neuen Einträgen |
| `SHOW_PERMALINK` | `false` | Permalink-Knopf (🔗) an Einträgen anzeigen |
| `SHOW_QUOTE` | `false` | Zitier-Knopf (❝) an Einträgen anzeigen |

Statt `ENCRYPTION_KEY` direkt zu setzen, kann der Schlüssel als Datei nach `/run/secrets/timeline_encryption_key` gemountet werden (siehe `docker-compose.yml`). Neuen Schlüssel erzeugen:

```bash
openssl rand -base64 32
```

Beim ersten Start mit Schlüssel werden alle bestehenden Klartext-Einträge verschlüsselt. Die Tagessiegel bleiben dabei gültig. **Ohne den Schlüssel sind die Einträge danach nicht mehr lesbar.**

## API

Basis-URL ist der Server selbst, z. B. `http://localhost:8080`. `GET /api/schema` liefert eine maschinenlesbare Beschreibung aller Ausgabeformate, gedacht für LLM-Clients.

### Konventionen

- Alle Antworten sind JSON (`application/json; charset=utf-8`), außer dem `.ots`-Download.
- **Zeitstempel:** RFC 3339 mit Nanosekunden in UTC, z. B. `2026-06-06T14:03:21.123456789Z`.
- **Datumsangaben:** `YYYY-MM-DD`, bezogen auf die Server-Zeitzone (`TZ`), nicht auf UTC.
- **Hashes** (`merkle_root`, `seal_hash`, …) sind SHA-256-Digests als Standard-Base64.
- **Optionale Felder** fehlen ganz, statt `null` zu sein.
- **Fehler:** `{"error": "<Meldung>"}` mit Status `400` (ungültige Eingabe), `401` (API-Key fehlt oder falsch), `403` (nicht bearbeitbar/löschbar), `404` (nicht gefunden) oder `500`.
- Listen von Einträgen sind immer neueste zuerst sortiert.

### Authentifizierung

Der API-Key wird nur verlangt für `POST /api/entries` mit `"automated": true` und für `POST /api/seals/{date}`. Alle anderen Endpunkte sind offen. Ist `API_KEY` nicht gesetzt, entfällt die Prüfung ganz.

Übergabe per Header, wahlweise:

```
Authorization: Bearer <API_KEY>
X-API-Key: <API_KEY>
```

### Objekte

**Entry**

| Feld | Typ | |
|---|---|---|
| `id` | Integer | |
| `text` | String | max. 10 000 Zeichen, Markdown, kann `#hashtags` enthalten |
| `created_at` | Zeitstempel | |
| `edited_at` | Zeitstempel, optional | fehlt, wenn nie bearbeitet |
| `automated` | Boolean | per API-Key angelegt statt in der Oberfläche |
| `hashtags` | String-Array | kleingeschrieben, ohne `#`; leer, wenn keine |
| `lat`, `lon` | Zahl, optional | nur gemeinsam vorhanden |

```json
{
  "id": 42,
  "text": "Morgenlauf am See #sport #morgen",
  "created_at": "2026-06-06T05:12:44.123456789Z",
  "automated": false,
  "hashtags": ["morgen", "sport"],
  "lat": 52.5163,
  "lon": 13.3777
}
```

**DaySeal**

| Feld | Typ | |
|---|---|---|
| `date` | Datum | versiegelter Tag |
| `entry_count` | Integer | Anzahl Einträge an dem Tag |
| `merkle_root` | Base64 | `SHA-256(entry_hash₁ ‖ entry_hash₂ ‖ …)` über die Eintrags-Hashes des Tages in `id`-Reihenfolge (trotz des Namens kein Merkle-Baum) |
| `prev_seal_hash` | Base64, optional | `seal_hash` des Vortags-Siegels; fehlt beim ersten Siegel |
| `seal_hash` | Base64 | `SHA-256(date ‖ merkle_root ‖ prev_seal_hash)` |
| `sealed_at` | Zeitstempel | |
| `has_ots_proof` | Boolean | OpenTimestamps-Beleg liegt vor |
| `ots_upgraded_at` | Zeitstempel, optional | gesetzt, sobald der Beleg in einem Bitcoin-Block verankert ist |

**VerifyResult**

| Feld | Typ | |
|---|---|---|
| `entries_checked` | Integer | |
| `days_checked` | Integer | |
| `chain_ok` | Boolean | alle Siegel passen zu ihren Einträgen, Kette intakt |
| `first_broken_day` | Datum, optional | erster Tag mit Abweichung |
| `break_reason` | String, optional | Grund der Abweichung |
| `ots_present` | Integer | Siegel mit OTS-Beleg |
| `ots_upgraded` | Integer | Siegel mit Bitcoin-Verankerung |

### Einträge

#### `GET /api/entries`

Die Query-Parameter bestimmen den Modus. Geprüft wird in dieser Reihenfolge, der erste passende gewinnt:

| Modus | Parameter | Antwort |
|---|---|---|
| Hashtag | `hashtag=<tag>` (`#` optional), `limit=<n>` optional | `{"hashtag": "<tag>", "entries": [Entry…]}` |
| Suche | `q=<text>`, `limit=<n>` (Default 20, max. 50) | `{"entries": [Entry…]}` |
| Zeitraum | `from=YYYY-MM-DD&to=YYYY-MM-DD` (beide Pflicht, inklusive) | `{"from": "…", "to": "…", "entries": [Entry…]}` |
| Tag (Default) | `date=YYYY-MM-DD` (Default: heute) | `{"date": "…", "entries": [Entry…]}` |

Die Suche findet Teilstrings ohne Beachtung von Groß- und Kleinschreibung. Ein leeres `q` liefert die neuesten Einträge.

```bash
curl 'http://localhost:8080/api/entries?date=2026-06-06'
curl 'http://localhost:8080/api/entries?from=2026-06-01&to=2026-06-07'
curl 'http://localhost:8080/api/entries?hashtag=sport&limit=10'
curl 'http://localhost:8080/api/entries?q=see'
```

#### `GET /api/entries/{id}`

Ein einzelner Eintrag. Antwort: `Entry`.

#### `POST /api/entries`

Legt einen Eintrag an. Antwort: `Entry` mit Status `201`.

| Feld | Pflicht | |
|---|---|---|
| `text` | ja | nicht leer, max. 10 000 Zeichen |
| `automated` | nein | `true` verlangt den API-Key |
| `lat`, `lon` | nein | nur gemeinsam; `-90..90` bzw. `-180..180`; Zahl oder String |

Koordinaten lassen sich später nicht mehr ändern. Ist `WEBHOOK_URL` gesetzt, geht der angelegte Eintrag als JSON per `POST` dorthin. Der Aufruf läuft asynchron mit 10 s Timeout, Fehler landen nur im Log.

```bash
curl -X POST http://localhost:8080/api/entries \
  -H 'Content-Type: application/json' \
  -d '{"text": "Kaffee getrunken #morgen"}'

curl -X POST http://localhost:8080/api/entries \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"text": "10 km gelaufen #sport", "automated": true, "lat": 52.5163, "lon": 13.3777}'
```

#### `PUT /api/entries/{id}`

Ändert den Text. Body: `{"text": "…"}`. Antwort: `Entry`. Erlaubt nur für nicht-automatisierte Einträge vom heutigen Tag, sonst `403`.

#### `DELETE /api/entries/{id}`

Löscht einen Eintrag vom heutigen Tag (auch automatisierte), sonst `403`. Antwort: `204` ohne Body.

### Siegel

Ein Hintergrundprozess versiegelt alle zehn Minuten jeden abgeschlossenen Tag, der noch kein Siegel hat. Danach wird der `seal_hash` bei den OpenTimestamps-Kalendern eingereicht. Stündlich wird geprüft, ob die Bitcoin-Bestätigung vorliegt, was meist ein bis zwei Stunden dauert.

| Endpunkt | Antwort |
|---|---|
| `GET /api/seals` | `{"seals": [DaySeal…]}` |
| `GET /api/seals/{date}` | `DaySeal` |
| `GET /api/seals/{date}/proof.ots` | binärer OTS-Beleg als Download (`404`, solange noch keiner vorliegt) |
| `POST /api/seals/{date}` | versiegelt einen vergangenen Tag sofort (API-Key nötig): `{"seal": DaySeal, "created": bool}`, Status `201` wenn neu, `200` wenn schon versiegelt |
| `GET /api/verify` | prüft die gesamte Kette: `VerifyResult` |

Beleg offline prüfen:

```bash
curl -O http://localhost:8080/api/seals/2026-06-06/proof.ots
ots verify 2026-06-06.ots                                  # offizielles OTS-CLI
scripts/verify_seal.py 2026-06-06 --api http://localhost:8080   # nur Python-Stdlib
```

### Sonstiges

| Endpunkt | Antwort |
|---|---|
| `GET /api/hashtags` | `{"hashtags": ["tag", …]}`, nach Häufigkeit sortiert |
| `GET /api/config` | `{"show_permalink": bool, "show_quote": bool}` |
| `GET /api/health` | `{"status": "ok"}` |
| `GET /api/schema` | Beschreibung der Ausgabeformate als JSON |

## Skripte

- `scripts/backup.sh`: Online-Backup der SQLite-Datei aus dem Docker-Volume, gzip-komprimiert, alte Backups werden nach `KEEP_DAYS` gelöscht
- `scripts/export.py`: exportiert alle Einträge direkt aus der `.db` als JSON, bei Bedarf mit `--key` entschlüsselt
- `scripts/verify_seal.py`: prüft ein Tagessiegel gegen seinen OTS-Beleg und die Bitcoin-Blockchain

## Entwicklung

Go muss nicht installiert sein, alles läuft über Docker:

```bash
docker run --rm -v $(pwd):/src -w /src golang:1.22-alpine go test ./...
docker run --rm -v $(pwd):/src -w /src golang:1.22-alpine go build ./...
```

Architektur und Designentscheidungen stehen in [`CLAUDE.md`](CLAUDE.md).
