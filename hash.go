package main

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"time"
)

const entryHashVersion byte = 1

// EntryHash computes the canonical SHA-256 of an entry.
// Layout: version || LP(date_in_tz) || LP(created_at_utc) || LP(automated) || LP(text)
// where LP(x) = uvarint(len(x)) || x.
// edited_at is deliberately excluded; same-day edits rehash before the day is sealed.
func EntryHash(e *Entry, tz *time.Location) []byte {
	h := sha256.New()
	h.Write([]byte{entryHashVersion})
	writeLP(h, []byte(e.CreatedAt.In(tz).Format("2006-01-02")))
	writeLP(h, []byte(e.CreatedAt.UTC().Format(time.RFC3339Nano)))
	var automated byte
	if e.Automated {
		automated = 1
	}
	writeLP(h, []byte{automated})
	writeLP(h, []byte(e.Text))
	return h.Sum(nil)
}

func writeLP(w io.Writer, b []byte) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(b)))
	_, _ = w.Write(buf[:n])
	_, _ = w.Write(b)
}
