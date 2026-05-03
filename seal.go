package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DaySeal is a cryptographic commitment to all entries of one calendar day.
type DaySeal struct {
	Date          string     `json:"date"`
	EntryCount    int        `json:"entry_count"`
	MerkleRoot    []byte     `json:"merkle_root"`
	PrevSealHash  []byte     `json:"prev_seal_hash,omitempty"`
	SealHash      []byte     `json:"seal_hash"`
	SealedAt      time.Time  `json:"sealed_at"`
	HasOTSProof   bool       `json:"has_ots_proof"`
	OTSUpgradedAt *time.Time `json:"ots_upgraded_at,omitempty"`
}

// SealDay computes and stores the seal for the given date (YYYY-MM-DD in server TZ).
// Returns (seal, created) where created=false if the date was already sealed.
func (s *Store) SealDay(date string) (*DaySeal, bool, error) {
	if existing, err := s.GetSeal(date); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	d, err := time.ParseInLocation("2006-01-02", date, s.tz)
	if err != nil {
		return nil, false, fmt.Errorf("invalid date %q: %w", date, err)
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, s.tz)
	end := start.Add(24 * time.Hour)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT entry_hash FROM entries
		 WHERE created_at >= ? AND created_at < ?
		 ORDER BY id ASC`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, false, err
	}

	merkle := sha256.New()
	count := 0
	for rows.Next() {
		var eh []byte
		if err := rows.Scan(&eh); err != nil {
			rows.Close()
			return nil, false, err
		}
		if eh == nil {
			rows.Close()
			return nil, false, fmt.Errorf("entry on %s has NULL entry_hash; run backfill first", date)
		}
		merkle.Write(eh)
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	merkleRoot := merkle.Sum(nil)

	var prevSeal []byte
	err = tx.QueryRow(
		`SELECT seal_hash FROM day_seals WHERE date < ? ORDER BY date DESC LIMIT 1`, date,
	).Scan(&prevSeal)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}

	seal := computeSealHash(date, merkleRoot, prevSeal)
	sealedAt := time.Now().UTC()

	_, err = tx.Exec(
		`INSERT INTO day_seals(date, entry_count, merkle_root, prev_seal_hash, seal_hash, sealed_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		date, count, merkleRoot, prevSeal, seal, sealedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	return &DaySeal{
		Date:         date,
		EntryCount:   count,
		MerkleRoot:   merkleRoot,
		PrevSealHash: prevSeal,
		SealHash:     seal,
		SealedAt:     sealedAt,
	}, true, nil
}

func computeSealHash(date string, merkleRoot, prevSeal []byte) []byte {
	h := sha256.New()
	h.Write([]byte(date))
	h.Write(merkleRoot)
	if prevSeal != nil {
		h.Write(prevSeal)
	}
	return h.Sum(nil)
}

// GetSeal returns the seal for a date or ErrNotFound.
func (s *Store) GetSeal(date string) (*DaySeal, error) {
	row := s.db.QueryRow(
		`SELECT date, entry_count, merkle_root, prev_seal_hash, seal_hash, sealed_at, ots_proof IS NOT NULL, ots_upgraded_at
		 FROM day_seals WHERE date = ?`, date,
	)
	return scanSeal(row)
}

// GetOTSProof returns the raw .ots proof bytes for a sealed date.
func (s *Store) GetOTSProof(date string) ([]byte, error) {
	var proof []byte
	err := s.db.QueryRow(`SELECT ots_proof FROM day_seals WHERE date = ?`, date).Scan(&proof)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return proof, err
}

// SetOTSProof stores a freshly received OTS proof for the given date.
func (s *Store) SetOTSProof(date string, proof []byte) error {
	_, err := s.db.Exec(`UPDATE day_seals SET ots_proof = ? WHERE date = ?`, proof, date)
	return err
}

// setOTSUpgraded stores an upgraded proof and records the upgrade timestamp.
func (s *Store) setOTSUpgraded(date string, proof []byte) error {
	_, err := s.db.Exec(
		`UPDATE day_seals SET ots_proof = ?, ots_upgraded_at = ? WHERE date = ?`,
		proof, time.Now().UTC().Format(time.RFC3339Nano), date,
	)
	return err
}

func scanSeal(r rowScanner) (*DaySeal, error) {
	var (
		ds            DaySeal
		sealedAt      string
		prevSeal      []byte
		hasProof      bool
		otsUpgradedAt sql.NullString
	)
	err := r.Scan(&ds.Date, &ds.EntryCount, &ds.MerkleRoot, &prevSeal, &ds.SealHash, &sealedAt, &hasProof, &otsUpgradedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, sealedAt)
	if err != nil {
		return nil, err
	}
	ds.SealedAt = t
	ds.PrevSealHash = prevSeal
	ds.HasOTSProof = hasProof
	if otsUpgradedAt.Valid {
		u, err := time.Parse(time.RFC3339Nano, otsUpgradedAt.String)
		if err == nil {
			ds.OTSUpgradedAt = &u
		}
	}
	return &ds, nil
}

// ListSeals returns all seals in chronological order.
func (s *Store) ListSeals() ([]*DaySeal, error) {
	rows, err := s.db.Query(
		`SELECT date, entry_count, merkle_root, prev_seal_hash, seal_hash, sealed_at, ots_proof IS NOT NULL, ots_upgraded_at
		 FROM day_seals ORDER BY date ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DaySeal
	for rows.Next() {
		ds, err := scanSeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ds)
	}
	return out, rows.Err()
}

// SealMissing seals every closed past day (strictly before today in server TZ) that
// doesn't have a seal yet. Returns the dates that were sealed this call.
func (s *Store) SealMissing(now time.Time) ([]string, error) {
	var minCreated sql.NullString
	err := s.db.QueryRow(`SELECT MIN(created_at) FROM entries`).Scan(&minCreated)
	if err != nil {
		return nil, err
	}
	if !minCreated.Valid {
		return nil, nil
	}
	min, err := time.Parse(time.RFC3339Nano, minCreated.String)
	if err != nil {
		return nil, err
	}
	nowLoc := now.In(s.tz)
	today := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), 0, 0, 0, 0, s.tz)
	minLoc := min.In(s.tz)
	cursor := time.Date(minLoc.Year(), minLoc.Month(), minLoc.Day(), 0, 0, 0, 0, s.tz)

	var sealed []string
	for cursor.Before(today) {
		dateStr := cursor.Format("2006-01-02")
		_, created, err := s.SealDay(dateStr)
		if err != nil {
			return sealed, fmt.Errorf("seal %s: %w", dateStr, err)
		}
		if created {
			sealed = append(sealed, dateStr)
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	return sealed, nil
}

// VerifyResult is the output of a full chain re-computation.
type VerifyResult struct {
	EntriesChecked int    `json:"entries_checked"`
	DaysChecked    int    `json:"days_checked"`
	ChainOK        bool   `json:"chain_ok"`
	FirstBrokenDay string `json:"first_broken_day,omitempty"`
	BreakReason    string `json:"break_reason,omitempty"`
	OTSPresent     int    `json:"ots_present"`
	OTSUpgraded    int    `json:"ots_upgraded"`
}

// VerifyChain re-computes every entry hash, merkle root, and seal in chronological
// order and returns at the first discrepancy found.
func (s *Store) VerifyChain() (*VerifyResult, error) {
	res := &VerifyResult{ChainOK: true}

	type sealRow struct {
		date          string
		count         int
		merkle        []byte
		prev          []byte
		seal          []byte
		hasProof      bool
		otsUpgradedAt sql.NullString
	}

	rows, err := s.db.Query(
		`SELECT date, entry_count, merkle_root, prev_seal_hash, seal_hash, ots_proof IS NOT NULL, ots_upgraded_at
		 FROM day_seals ORDER BY date ASC`,
	)
	if err != nil {
		return nil, err
	}
	var seals []sealRow
	for rows.Next() {
		var sr sealRow
		if err := rows.Scan(&sr.date, &sr.count, &sr.merkle, &sr.prev, &sr.seal, &sr.hasProof, &sr.otsUpgradedAt); err != nil {
			rows.Close()
			return nil, err
		}
		seals = append(seals, sr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var running []byte
	for _, sr := range seals {
		res.DaysChecked++
		if sr.hasProof {
			res.OTSPresent++
		}
		if sr.otsUpgradedAt.Valid {
			res.OTSUpgraded++
		}

		if !bytes.Equal(sr.prev, running) {
			res.ChainOK = false
			res.FirstBrokenDay = sr.date
			res.BreakReason = "prev_seal_hash does not match previous day's seal"
			return res, nil
		}

		merkleRecomputed, ecount, entryBreak, err := s.recomputeDay(sr.date)
		if err != nil {
			return nil, err
		}
		res.EntriesChecked += ecount
		if entryBreak != "" {
			res.ChainOK = false
			res.FirstBrokenDay = sr.date
			res.BreakReason = entryBreak
			return res, nil
		}
		if ecount != sr.count {
			res.ChainOK = false
			res.FirstBrokenDay = sr.date
			res.BreakReason = fmt.Sprintf("entry count changed (sealed=%d, found=%d)", sr.count, ecount)
			return res, nil
		}
		if !bytes.Equal(merkleRecomputed, sr.merkle) {
			res.ChainOK = false
			res.FirstBrokenDay = sr.date
			res.BreakReason = "merkle_root mismatch"
			return res, nil
		}
		if !bytes.Equal(computeSealHash(sr.date, sr.merkle, sr.prev), sr.seal) {
			res.ChainOK = false
			res.FirstBrokenDay = sr.date
			res.BreakReason = "seal_hash inconsistent with its own components"
			return res, nil
		}
		running = sr.seal
	}
	return res, nil
}

// recomputeDay walks entries for a date, re-hashes each, and returns the fresh merkle root.
// Returns a non-empty breakReason if any entry's stored hash disagrees with its content.
func (s *Store) recomputeDay(date string) (root []byte, count int, breakReason string, err error) {
	d, err := time.ParseInLocation("2006-01-02", date, s.tz)
	if err != nil {
		return nil, 0, "", err
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, s.tz)
	end := start.Add(24 * time.Hour)

	rows, err := s.db.Query(
		`SELECT id, text, created_at, automated, lat, lon, entry_hash FROM entries
		 WHERE created_at >= ? AND created_at < ?
		 ORDER BY id ASC`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var (
			id         int64
			text       string
			createdStr string
			automated  int
			lat        sql.NullFloat64
			lon        sql.NullFloat64
			storedHash []byte
		)
		if err := rows.Scan(&id, &text, &createdStr, &automated, &lat, &lon, &storedHash); err != nil {
			return nil, 0, "", err
		}
		created, err := time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			return nil, 0, "", err
		}
		e := &Entry{ID: id, Text: text, CreatedAt: created, Automated: automated != 0}
		if lat.Valid && lon.Valid {
			la, lo := lat.Float64, lon.Float64
			e.Lat, e.Lon = &la, &lo
		}
		recomputed := EntryHash(e, s.tz)
		count++
		if !bytes.Equal(recomputed, storedHash) {
			return nil, count, fmt.Sprintf("entry %d hash mismatch (text or timestamp changed after seal)", id), nil
		}
		h.Write(storedHash)
	}
	return h.Sum(nil), count, "", rows.Err()
}
