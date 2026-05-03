package main

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math"
	"time"
)

const (
	entryHashVersion   byte = 1 // text-only layout
	entryHashVersionV2 byte = 2 // includes geo coordinates
)

// EntryHash computes the canonical SHA-256 of an entry.
//
// Without geo (v1): version || LP(date_in_tz) || LP(created_at_utc) || LP(automated) || LP(text)
// With geo    (v2): version || LP(date_in_tz) || LP(created_at_utc) || LP(automated) || LP(text) || LP(lat_be64) || LP(lon_be64)
//
// lat_be64 and lon_be64 are the IEEE 754 binary64 bit-patterns of the coords
// in big-endian byte order (8 bytes each). Including them makes the coords
// part of the day's merkle root, so any later modification breaks the seal.
//
// edited_at is deliberately excluded; same-day edits rehash before the day is sealed.
func EntryHash(e *Entry, tz *time.Location) []byte {
	h := sha256.New()
	hasGeo := e.Lat != nil && e.Lon != nil
	if hasGeo {
		h.Write([]byte{entryHashVersionV2})
	} else {
		h.Write([]byte{entryHashVersion})
	}
	writeLP(h, []byte(e.CreatedAt.In(tz).Format("2006-01-02")))
	writeLP(h, []byte(e.CreatedAt.UTC().Format(time.RFC3339Nano)))
	var automated byte
	if e.Automated {
		automated = 1
	}
	writeLP(h, []byte{automated})
	writeLP(h, []byte(e.Text))
	if hasGeo {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(*e.Lat))
		writeLP(h, buf[:])
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(*e.Lon))
		writeLP(h, buf[:])
	}
	return h.Sum(nil)
}

func writeLP(w io.Writer, b []byte) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(b)))
	_, _ = w.Write(buf[:n])
	_, _ = w.Write(b)
}
