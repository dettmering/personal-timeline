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
}

type Store struct {
	db *sql.DB
}

var ErrNotFound = errors.New("not found")
var ErrNotEditable = errors.New("entry not editable")
var ErrNotDeletable = errors.New("entry not deletable")

var hashtagRe = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)

func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = ensureDir(dir)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
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
`)
	return err
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

func (s *Store) Create(text string, createdAt time.Time, automated bool) (*Entry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO entries(text, created_at, automated) VALUES(?, ?, ?)`,
		text, createdAt.UTC().Format(time.RFC3339Nano), boolInt(automated),
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
	return &Entry{
		ID:        id,
		Text:      text,
		CreatedAt: createdAt,
		Automated: automated,
		Hashtags:  tags,
	}, nil
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
		`SELECT id, text, created_at, edited_at, automated FROM entries WHERE id = ?`, id,
	)
	e, err := scanEntryRow(row)
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
	err = tx.QueryRow(
		`SELECT created_at, automated FROM entries WHERE id = ?`, id,
	).Scan(&createdStr, &automated)
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

	_, err = tx.Exec(
		`UPDATE entries SET text = ?, edited_at = ? WHERE id = ?`,
		text, now.UTC().Format(time.RFC3339Nano), id,
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
	edited := now
	return &Entry{
		ID:        id,
		Text:      text,
		CreatedAt: createdAt,
		EditedAt:  &edited,
		Automated: automated != 0,
		Hashtags:  tags,
	}, nil
}

func (s *Store) Delete(id int64, now time.Time, serverTZ *time.Location) error {
	var createdStr string
	var automated int
	err := s.db.QueryRow(`SELECT created_at, automated FROM entries WHERE id = ?`, id).Scan(&createdStr, &automated)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if automated != 0 {
		return ErrNotDeletable
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return err
	}
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
		`SELECT id, text, created_at, edited_at, automated FROM entries
		 WHERE created_at >= ? AND created_at < ?
		 ORDER BY created_at DESC`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	entries, err := scanEntries(rows)
	if err != nil {
		return nil, err
	}
	return s.attachHashtags(entries)
}

func (s *Store) ListByHashtag(tag string) ([]*Entry, error) {
	rows, err := s.db.Query(
		`SELECT e.id, e.text, e.created_at, e.edited_at, e.automated
		 FROM entries e
		 INNER JOIN hashtags h ON h.entry_id = e.id
		 WHERE h.tag = ?
		 ORDER BY e.created_at DESC`,
		strings.ToLower(tag),
	)
	if err != nil {
		return nil, err
	}
	entries, err := scanEntries(rows)
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

func scanEntryRow(r rowScanner) (*Entry, error) {
	var (
		id         int64
		text       string
		createdStr string
		editedStr  sql.NullString
		automated  int
	)
	if err := r.Scan(&id, &text, &createdStr, &editedStr, &automated); err != nil {
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
	if editedStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, editedStr.String)
		if err == nil {
			e.EditedAt = &t
		}
	}
	return e, nil
}

func scanEntries(rows *sql.Rows) ([]*Entry, error) {
	defer rows.Close()
	out := []*Entry{}
	for rows.Next() {
		e, err := scanEntryRow(rows)
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
