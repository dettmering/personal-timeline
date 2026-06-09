package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenStore(path, time.UTC)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// day returns midnight-plus-noon UTC for a fixed January 2026 day.
func day(d int) time.Time {
	return time.Date(2026, 1, d, 12, 0, 0, 0, time.UTC)
}

func TestSealMissingAndVerifyChain(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("entry one #a", day(10), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Create("entry two", day(10), false, ptr(52.0), ptr(13.0)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Create("next day", day(11), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	// "Now" is day 12, so days 10 and 11 are closed and should be sealed.
	sealed, err := s.SealMissing(day(12))
	if err != nil {
		t.Fatalf("SealMissing: %v", err)
	}
	if len(sealed) != 2 {
		t.Fatalf("want 2 sealed days, got %d (%v)", len(sealed), sealed)
	}

	res, err := s.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainOK {
		t.Fatalf("chain should be OK, broke at %s: %s", res.FirstBrokenDay, res.BreakReason)
	}
	if res.DaysChecked != 2 || res.EntriesChecked != 3 {
		t.Fatalf("counts off: days=%d entries=%d", res.DaysChecked, res.EntriesChecked)
	}
}

func TestSealDayIdempotent(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("x", day(10), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, created, err := s.SealDay("2026-01-10"); err != nil || !created {
		t.Fatalf("first seal: created=%v err=%v", created, err)
	}
	if _, created, err := s.SealDay("2026-01-10"); err != nil || created {
		t.Fatalf("second seal should be a no-op: created=%v err=%v", created, err)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("original text", day(10), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.SealDay("2026-01-10"); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Rewrite the stored text behind the seal's back, leaving entry_hash stale.
	if _, err := s.db.Exec(`UPDATE entries SET text = ? WHERE text = ?`, "tampered text", "original text"); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	res, err := s.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.ChainOK {
		t.Fatal("chain should be broken after tampering")
	}
	if res.FirstBrokenDay != "2026-01-10" {
		t.Fatalf("wrong broken day: %q (%s)", res.FirstBrokenDay, res.BreakReason)
	}
}

func TestVerifyChainEncrypted(t *testing.T) {
	s := newTestStore(t)
	s.SetCipher(newTestCipher(t))

	if _, err := s.Create("encrypted entry", day(10), false, ptr(48.0), ptr(11.0)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.SealDay("2026-01-10"); err != nil {
		t.Fatalf("seal: %v", err)
	}

	res, err := s.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainOK {
		t.Fatalf("encrypted chain should verify, broke: %s", res.BreakReason)
	}
	if res.EntriesChecked != 1 {
		t.Fatalf("want 1 entry checked, got %d", res.EntriesChecked)
	}
}

func TestSealChainLinksDays(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("d10", day(10), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Create("d11", day(11), false, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	first, _, err := s.SealDay("2026-01-10")
	if err != nil {
		t.Fatalf("seal d10: %v", err)
	}
	second, _, err := s.SealDay("2026-01-11")
	if err != nil {
		t.Fatalf("seal d11: %v", err)
	}
	if first.PrevSealHash != nil {
		t.Fatal("first seal should have no predecessor")
	}
	if len(second.PrevSealHash) == 0 {
		t.Fatal("second seal must chain to the first")
	}
	if string(second.PrevSealHash) != string(first.SealHash) {
		t.Fatal("second.prev_seal_hash must equal first.seal_hash")
	}
}
