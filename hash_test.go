package main

import (
	"bytes"
	"testing"
	"time"
)

func ptr(f float64) *float64 { return &f }

func baseEntry() *Entry {
	return &Entry{
		ID:        1,
		Text:      "hello #world",
		CreatedAt: time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC),
		Automated: false,
	}
}

func TestEntryHashDeterministic(t *testing.T) {
	a := EntryHash(baseEntry(), time.UTC)
	b := EntryHash(baseEntry(), time.UTC)
	if !bytes.Equal(a, b) {
		t.Fatalf("hash not deterministic: %x != %x", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("want 32-byte SHA-256, got %d bytes", len(a))
	}
}

func TestEntryHashVersionByte(t *testing.T) {
	// The version byte is hashed, not exposed, so assert it indirectly:
	// a geo entry (v2) must differ from the same entry without geo (v1).
	noGeo := baseEntry()
	geo := baseEntry()
	geo.Lat, geo.Lon = ptr(52.52), ptr(13.405)

	if bytes.Equal(EntryHash(noGeo, time.UTC), EntryHash(geo, time.UTC)) {
		t.Fatal("v1 (no geo) and v2 (geo) hashes must differ")
	}
}

func TestEntryHashSensitiveFields(t *testing.T) {
	ref := EntryHash(baseEntry(), time.UTC)

	cases := map[string]func(*Entry){
		"text":       func(e *Entry) { e.Text = "different" },
		"automated":  func(e *Entry) { e.Automated = true },
		"created_at": func(e *Entry) { e.CreatedAt = e.CreatedAt.Add(time.Nanosecond) },
		"lat":        func(e *Entry) { e.Lat, e.Lon = ptr(0.1), ptr(0.2) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := baseEntry()
			mutate(e)
			if bytes.Equal(ref, EntryHash(e, time.UTC)) {
				t.Fatalf("mutating %s did not change the hash", name)
			}
		})
	}
}

func TestEntryHashLatLonNotInterchangeable(t *testing.T) {
	a := baseEntry()
	a.Lat, a.Lon = ptr(10), ptr(20)
	b := baseEntry()
	b.Lat, b.Lon = ptr(20), ptr(10)
	if bytes.Equal(EntryHash(a, time.UTC), EntryHash(b, time.UTC)) {
		t.Fatal("swapping lat/lon must change the hash")
	}
}

func TestEntryHashTimezoneAffectsDate(t *testing.T) {
	// 23:30 UTC on Jan 15 is already Jan 16 in Berlin (UTC+1), so the
	// tz-local calendar date folded into the hash must differ.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	e := baseEntry()
	e.CreatedAt = time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)

	if bytes.Equal(EntryHash(e, time.UTC), EntryHash(e, berlin)) {
		t.Fatal("same instant in a tz that crosses midnight must hash differently")
	}
}

func TestEntryHashIDIndependent(t *testing.T) {
	a := baseEntry()
	b := baseEntry()
	b.ID = 999
	if !bytes.Equal(EntryHash(a, time.UTC), EntryHash(b, time.UTC)) {
		t.Fatal("id must not affect the hash")
	}
}

func TestEntryHashEditedAtIndependent(t *testing.T) {
	a := baseEntry()
	b := baseEntry()
	edited := b.CreatedAt.Add(time.Hour)
	b.EditedAt = &edited
	if !bytes.Equal(EntryHash(a, time.UTC), EntryHash(b, time.UTC)) {
		t.Fatal("edited_at must not affect the hash")
	}
}
