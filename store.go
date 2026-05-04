package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

type Entry struct {
	ID        int64      `json:"id"`
	Text      string     `json:"text"`
	CreatedAt time.Time  `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
	Automated bool       `json:"automated"`
	Hashtags  []string   `json:"hashtags"`
	Lat       *float64   `json:"lat,omitempty"`
	Lon       *float64   `json:"lon,omitempty"`
}

type Store struct {
	db     *sql.DB
	tz     *time.Location
	cipher *Cipher // nil = encryption disabled
}

var ErrNotFound = errors.New("not found")
var ErrNotEditable = errors.New("entry not editable")
var ErrNotDeletable = errors.New("entry not deletable")

var hashtagRe = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)

func OpenStore(path string, tz *time.Location) (*Store, error) {
	if tz == nil {
		tz = time.Local
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = ensureDir(dir)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, tz: tz}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) TZ() *time.Location { return s.tz }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    text       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    edited_at  TEXT,
    automated  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at);

CREATE TABLE IF NOT EXISTS hashtags (
    entry_id INTEGER NOT NULL,
    tag      TEXT NOT NULL,
    PRIMARY KEY(entry_id, tag),
    FOREIGN KEY(entry_id) REFERENCES entries(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_hashtags_tag ON hashtags(tag);

CREATE TABLE IF NOT EXISTS day_seals (
    date             TEXT PRIMARY KEY,
    entry_count      INTEGER NOT NULL,
    merkle_root      BLOB NOT NULL,
    prev_seal_hash   BLOB,
    seal_hash        BLOB NOT NULL,
    sealed_at        TEXT NOT NULL,
    ots_proof        BLOB,
    ots_upgraded_at  TEXT
);
`); err != nil {
		return err
	}
	// Idempotent column add for entry_hash.
	if _, err := s.db.Exec(`ALTER TABLE entries ADD COLUMN entry_hash BLOB`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	for _, col := range []string{"lat", "lon"} {
		if _, err := s.db.Exec(`ALTER TABLE entries ADD COLUMN ` + col + ` REAL`); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	for _, col := range []string{"text_cipher", "geo_cipher"} {
		if _, err := s.db.Exec(`ALTER TABLE entries ADD COLUMN ` + col + ` BLOB`); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	return nil
}

// SetCipher enables at-rest encryption for new writes and decryption on read.
// Must be called before BackfillHashes/EncryptBackfill if migration is desired.
func (s *Store) SetCipher(c *Cipher) { s.cipher = c }

// BackfillHashes computes entry_hash for any rows where it's NULL.
// Returns the number of rows updated.
func (s *Store) BackfillHashes() (int, error) {
	rows, err := s.db.Query(
		`SELECT id, text, created_at, automated, lat, lon FROM entries WHERE entry_hash IS NULL`,
	)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id   int64
		hash []byte
	}
	var pendings []pending
	for rows.Next() {
		var (
			id         int64
			text       string
			createdStr string
			automated  int
			lat        sql.NullFloat64
			lon        sql.NullFloat64
		)
		if err := rows.Scan(&id, &text, &createdStr, &automated, &lat, &lon); err != nil {
			rows.Close()
			return 0, err
		}
		created, err := time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			rows.Close()
			return 0, err
		}
		e := &Entry{ID: id, Text: text, CreatedAt: created, Automated: automated != 0}
		if lat.Valid && lon.Valid {
			la, lo := lat.Float64, lon.Float64
			e.Lat, e.Lon = &la, &lo
		}
		pendings = append(pendings, pending{id: id, hash: EntryHash(e, s.tz)})
	}
	rows.Close()
	for _, p := range pendings {
		if _, err := s.db.Exec(`UPDATE entries SET entry_hash = ? WHERE id = ?`, p.hash, p.id); err != nil {
			return 0, err
		}
	}
	return len(pendings), nil
}

// EncryptBackfill encrypts the text and (if present) lat/lon of every plaintext
// row exactly once. Uses entry_hash as AAD, so BackfillHashes must run first.
//
// Critical invariant: entry_hash is never modified. The migration is a pure
// representation change — plaintext bytes in == plaintext bytes out after
// decrypt — so all existing day_seals and OTS proofs remain valid.
//
// No-op when cipher is nil. Idempotent: rows already migrated are skipped.
func (s *Store) EncryptBackfill() (int, error) {
	if s.cipher == nil {
		return 0, nil
	}
	rows, err := s.db.Query(
		`SELECT id, text, lat, lon, entry_hash
		 FROM entries
		 WHERE text_cipher IS NULL`,
	)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id         int64
		textCipher []byte
		geoCipher  []byte
	}
	var pendings []pending
	for rows.Next() {
		var (
			id        int64
			text      string
			lat, lon  sql.NullFloat64
			entryHash []byte
		)
		if err := rows.Scan(&id, &text, &lat, &lon, &entryHash); err != nil {
			rows.Close()
			return 0, err
		}
		if entryHash == nil {
			rows.Close()
			return 0, fmt.Errorf("entry %d has NULL entry_hash; run BackfillHashes first", id)
		}
		tc, err := s.cipher.Encrypt([]byte(text), entryHash)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("encrypt entry %d text: %w", id, err)
		}
		p := pending{id: id, textCipher: tc}
		if lat.Valid && lon.Valid {
			gc, err := s.cipher.Encrypt(encodeGeo(lat.Float64, lon.Float64), entryHash)
			if err != nil {
				rows.Close()
				return 0, fmt.Errorf("encrypt entry %d geo: %w", id, err)
			}
			p.geoCipher = gc
		}
		pendings = append(pendings, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range pendings {
		_, err := s.db.Exec(
			`UPDATE entries
			    SET text = '', text_cipher = ?, lat = NULL, lon = NULL, geo_cipher = ?
			  WHERE id = ?`,
			p.textCipher, p.geoCipher, p.id,
		)
		if err != nil {
			return 0, err
		}
	}
	return len(pendings), nil
}

func ExtractHashtags(text string) []string {
	matches := hashtagRe.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	out := []string{}
	for _, m := range matches {
		tag := strings.ToLower(m[1])
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func (s *Store) Create(text string, createdAt time.Time, automated bool, lat, lon *float64) (*Entry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	entry := &Entry{
		Text:      text,
		CreatedAt: createdAt,
		Automated: automated,
		Lat:       lat,
		Lon:       lon,
	}
	hash := EntryHash(entry, s.tz)

	storedText := text
	var textCipher, geoCipher []byte
	var latVal, lonVal any
	if s.cipher != nil {
		tc, err := s.cipher.Encrypt([]byte(text), hash)
		if err != nil {
			return nil, fmt.Errorf("encrypt text: %w", err)
		}
		textCipher = tc
		storedText = ""
		if lat != nil && lon != nil {
			gc, err := s.cipher.Encrypt(encodeGeo(*lat, *lon), hash)
			if err != nil {
				return nil, fmt.Errorf("encrypt geo: %w", err)
			}
			geoCipher = gc
		}
	} else if lat != nil && lon != nil {
		latVal = *lat
		lonVal = *lon
	}

	res, err := tx.Exec(
		`INSERT INTO entries(text, text_cipher, created_at, automated, entry_hash, lat, lon, geo_cipher)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		storedText, textCipher, createdAt.UTC().Format(time.RFC3339Nano), boolInt(automated), hash, latVal, lonVal, geoCipher,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	tags := ExtractHashtags(text)
	if err := insertTags(tx, id, tags); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	entry.ID = id
	entry.Hashtags = tags
	return entry, nil
}

func insertTags(tx *sql.Tx, id int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO hashtags(entry_id, tag) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, t := range tags {
		if _, err := stmt.Exec(id, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Get(id int64) (*Entry, error) {
	row := s.db.QueryRow(
		`SELECT `+entryColumns+` FROM entries WHERE id = ?`, id,
	)
	e, err := s.scanEntryRow(row)
	if err != nil {
		return nil, err
	}
	tags, err := s.hashtagsFor(id)
	if err != nil {
		return nil, err
	}
	e.Hashtags = tags
	return e, nil
}

func (s *Store) Update(id int64, text string, now time.Time, serverTZ *time.Location) (*Entry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var createdStr string
	var automated int
	var lat, lon sql.NullFloat64
	var geoCipher []byte
	var oldHash []byte
	err = tx.QueryRow(
		`SELECT created_at, automated, lat, lon, geo_cipher, entry_hash FROM entries WHERE id = ?`, id,
	).Scan(&createdStr, &automated, &lat, &lon, &geoCipher, &oldHash)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if automated != 0 {
		return nil, ErrNotEditable
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, err
	}
	if !sameDay(createdAt.In(serverTZ), now.In(serverTZ)) {
		return nil, ErrNotEditable
	}

	edited := now
	updated := &Entry{
		ID:        id,
		Text:      text,
		CreatedAt: createdAt,
		EditedAt:  &edited,
		Automated: automated != 0,
	}
	// Recover existing lat/lon — either from the REAL columns (legacy/plaintext)
	// or from geo_cipher (encrypted). Coords are immutable across edits.
	if geoCipher != nil {
		if s.cipher == nil {
			return nil, fmt.Errorf("entry %d has encrypted geo but no cipher configured", id)
		}
		raw, err := s.cipher.Decrypt(geoCipher, oldHash)
		if err != nil {
			return nil, fmt.Errorf("decrypt geo: %w", err)
		}
		la, lo, err := decodeGeo(raw)
		if err != nil {
			return nil, err
		}
		updated.Lat, updated.Lon = &la, &lo
	} else if lat.Valid && lon.Valid {
		la, lo := lat.Float64, lon.Float64
		updated.Lat, updated.Lon = &la, &lo
	}
	newHash := EntryHash(updated, s.tz)

	// Build the persisted payload. When cipher is on, the row is fully migrated
	// to the encrypted shape — even if it was previously plaintext.
	storedText := text
	var newTextCipher, newGeoCipher []byte
	var latVal, lonVal any
	if s.cipher != nil {
		tc, err := s.cipher.Encrypt([]byte(text), newHash)
		if err != nil {
			return nil, fmt.Errorf("encrypt text: %w", err)
		}
		newTextCipher = tc
		storedText = ""
		if updated.Lat != nil && updated.Lon != nil {
			gc, err := s.cipher.Encrypt(encodeGeo(*updated.Lat, *updated.Lon), newHash)
			if err != nil {
				return nil, fmt.Errorf("encrypt geo: %w", err)
			}
			newGeoCipher = gc
		}
	} else if updated.Lat != nil && updated.Lon != nil {
		latVal = *updated.Lat
		lonVal = *updated.Lon
	}

	_, err = tx.Exec(
		`UPDATE entries
		   SET text = ?, text_cipher = ?, lat = ?, lon = ?, geo_cipher = ?,
		       edited_at = ?, entry_hash = ?
		 WHERE id = ?`,
		storedText, newTextCipher, latVal, lonVal, newGeoCipher,
		now.UTC().Format(time.RFC3339Nano), newHash, id,
	)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM hashtags WHERE entry_id = ?`, id); err != nil {
		return nil, err
	}
	tags := ExtractHashtags(text)
	if err := insertTags(tx, id, tags); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	updated.Hashtags = tags
	return updated, nil
}

func (s *Store) Delete(id int64, now time.Time, serverTZ *time.Location) error {
	var createdStr string
	err := s.db.QueryRow(`SELECT created_at FROM entries WHERE id = ?`, id).Scan(&createdStr)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return err
	}
	// automated entries may be deleted today but never edited; same-day rule applies to both
	if !sameDay(createdAt.In(serverTZ), now.In(serverTZ)) {
		return ErrNotDeletable
	}
	_, err = s.db.Exec(`DELETE FROM entries WHERE id = ?`, id)
	return err
}

// ListByDay returns entries whose created_at falls within the given calendar day in tz.
func (s *Store) ListByDay(day time.Time, tz *time.Location) ([]*Entry, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
	end := start.Add(24 * time.Hour)
	rows, err := s.db.Query(
		`SELECT `+entryColumns+` FROM entries
		 WHERE created_at >= ? AND created_at < ?
		 ORDER BY created_at DESC`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	entries, err := s.scanEntries(rows)
	if err != nil {
		return nil, err
	}
	return s.attachHashtags(entries)
}

// ListByRange returns entries whose created_at falls within [from, to] (inclusive, calendar days in tz).
func (s *Store) ListByRange(from, to time.Time, tz *time.Location) ([]*Entry, error) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, tz)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, tz).Add(24 * time.Hour)
	rows, err := s.db.Query(
		`SELECT `+entryColumns+` FROM entries
		 WHERE created_at >= ? AND created_at < ?
		 ORDER BY created_at DESC`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	entries, err := s.scanEntries(rows)
	if err != nil {
		return nil, err
	}
	return s.attachHashtags(entries)
}

// SearchEntries returns the most recent entries whose text contains query
// (case-insensitive substring). If query is empty, the most recent entries are
// returned. limit is clamped to [1, 50].
//
// Filtering is done in Go after decryption — SQL LIKE on text_cipher would not
// match anything sensible. Acceptable for a personal journal.
func (s *Store) SearchEntries(query string, limit int) ([]*Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	if query == "" {
		rows, err := s.db.Query(
			`SELECT `+entryColumns+` FROM entries ORDER BY created_at DESC LIMIT ?`,
			limit,
		)
		if err != nil {
			return nil, err
		}
		entries, err := s.scanEntries(rows)
		if err != nil {
			return nil, err
		}
		return s.attachHashtags(entries)
	}

	// Stream rows in DESC order, decrypt, filter, stop once limit is full.
	rows, err := s.db.Query(
		`SELECT ` + entryColumns + ` FROM entries ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	needle := strings.ToLower(query)
	out := []*Entry{}
	for rows.Next() {
		e, err := s.scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(e.Text), needle) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachHashtags(out)
}

func (s *Store) ListByHashtag(tag string) ([]*Entry, error) {
	rows, err := s.db.Query(
		`SELECT `+entryColumns+`
		 FROM entries
		 INNER JOIN hashtags h ON h.entry_id = entries.id
		 WHERE h.tag = ?
		 ORDER BY entries.created_at DESC`,
		strings.ToLower(tag),
	)
	if err != nil {
		return nil, err
	}
	entries, err := s.scanEntries(rows)
	if err != nil {
		return nil, err
	}
	return s.attachHashtags(entries)
}

func (s *Store) attachHashtags(entries []*Entry) ([]*Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	ids := make([]any, len(entries))
	byID := make(map[int64]*Entry, len(entries))
	placeholders := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
		byID[e.ID] = e
		placeholders[i] = "?"
		e.Hashtags = []string{}
	}
	q := `SELECT entry_id, tag FROM hashtags WHERE entry_id IN (` + strings.Join(placeholders, ",") + `) ORDER BY tag`
	rows, err := s.db.Query(q, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		if e, ok := byID[id]; ok {
			e.Hashtags = append(e.Hashtags, tag)
		}
	}
	return entries, rows.Err()
}

func (s *Store) ListAllHashtags() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT tag, COUNT(*) AS cnt FROM hashtags GROUP BY tag ORDER BY cnt DESC, tag ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var tag string
		var cnt int
		if err := rows.Scan(&tag, &cnt); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *Store) hashtagsFor(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM hashtags WHERE entry_id = ? ORDER BY tag`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// entryColumns is the canonical SELECT list for scanEntryRow. Every call site
// reading entries must select exactly these columns in this order.
const entryColumns = `id, text, text_cipher, created_at, edited_at, automated, lat, lon, geo_cipher, entry_hash`

func (s *Store) scanEntryRow(r rowScanner) (*Entry, error) {
	var (
		id         int64
		text       string
		textCipher []byte
		createdStr string
		editedStr  sql.NullString
		automated  int
		lat        sql.NullFloat64
		lon        sql.NullFloat64
		geoCipher  []byte
		entryHash  []byte
	)
	if err := r.Scan(&id, &text, &textCipher, &createdStr, &editedStr, &automated, &lat, &lon, &geoCipher, &entryHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	e := &Entry{
		ID:        id,
		Text:      text,
		CreatedAt: created,
		Automated: automated != 0,
		Hashtags:  []string{},
	}
	if textCipher != nil {
		if s.cipher == nil {
			return nil, fmt.Errorf("entry %d is encrypted but no ENCRYPTION_KEY is configured", id)
		}
		pt, err := s.cipher.Decrypt(textCipher, entryHash)
		if err != nil {
			return nil, fmt.Errorf("decrypt entry %d text: %w", id, err)
		}
		e.Text = string(pt)
	}
	if editedStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, editedStr.String)
		if err == nil {
			e.EditedAt = &t
		}
	}
	if geoCipher != nil {
		if s.cipher == nil {
			return nil, fmt.Errorf("entry %d has encrypted geo but no ENCRYPTION_KEY is configured", id)
		}
		raw, err := s.cipher.Decrypt(geoCipher, entryHash)
		if err != nil {
			return nil, fmt.Errorf("decrypt entry %d geo: %w", id, err)
		}
		la, lo, err := decodeGeo(raw)
		if err != nil {
			return nil, err
		}
		e.Lat, e.Lon = &la, &lo
	} else if lat.Valid && lon.Valid {
		la, lo := lat.Float64, lon.Float64
		e.Lat, e.Lon = &la, &lo
	}
	return e, nil
}

func (s *Store) scanEntries(rows *sql.Rows) ([]*Entry, error) {
	defer rows.Close()
	out := []*Entry{}
	for rows.Next() {
		e, err := s.scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sameDay(a, b time.Time) bool {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	return ya == yb && ma == mb && da == db
}
