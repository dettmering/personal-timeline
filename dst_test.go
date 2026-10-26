package main

import (
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // DST tests must never silently skip for lack of zoneinfo
)

// newBerlinStore opens a temp-file Store whose server TZ observes DST.
// 2026-03-29 is 23 h long there, 2026-10-25 is 25 h long.
func newBerlinStore(t *testing.T) (*Store, *time.Location) {
	t.Helper()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"), berlin)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, berlin
}

func entryIDs(entries []*Entry) map[int64]bool {
	ids := map[int64]bool{}
	for _, e := range entries {
		ids[e.ID] = true
	}
	return ids
}

func TestDayBoundsAcrossDST(t *testing.T) {
	_, berlin := newBerlinStore(t)
	cases := []struct {
		day  time.Time
		want time.Duration
	}{
		{time.Date(2026, 3, 29, 12, 0, 0, 0, berlin), 23 * time.Hour},
		{time.Date(2026, 10, 25, 12, 0, 0, 0, berlin), 25 * time.Hour},
		{time.Date(2026, 10, 26, 12, 0, 0, 0, berlin), 24 * time.Hour},
	}
	for _, c := range cases {
		start, end := dayBounds(c.day, berlin)
		if got := end.Sub(start); got != c.want {
			t.Errorf("%s: day length %v, want %v", c.day.Format("2006-01-02"), got, c.want)
		}
		if h, m, _ := end.In(berlin).Clock(); h != 0 || m != 0 {
			t.Errorf("%s: end %v is not local midnight", c.day.Format("2006-01-02"), end.In(berlin))
		}
	}
}

// Regression: an entry at 23:00 CET on the 25-hour day 2026-10-25 (22:00Z) was
// listed for neither 25.10 nor 26.10 and never got sealed.
func TestListByDayIncludesLastHourOfLongDSTDay(t *testing.T) {
	s, berlin := newBerlinStore(t)
	late, err := s.Create("weekly review", time.Date(2026, 10, 25, 22, 0, 19, 0, time.UTC), true, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.ListByDay(time.Date(2026, 10, 25, 0, 0, 0, 0, berlin), berlin)
	if err != nil {
		t.Fatalf("ListByDay: %v", err)
	}
	if !entryIDs(got)[late.ID] {
		t.Fatal("23:00 entry missing from 2026-10-25")
	}
	next, err := s.ListByDay(time.Date(2026, 10, 26, 0, 0, 0, 0, berlin), berlin)
	if err != nil {
		t.Fatalf("ListByDay: %v", err)
	}
	if entryIDs(next)[late.ID] {
		t.Fatal("23:00 entry of 2026-10-25 leaked into 2026-10-26")
	}

	ranged, err := s.ListByRange(time.Date(2026, 10, 24, 0, 0, 0, 0, berlin), time.Date(2026, 10, 25, 0, 0, 0, 0, berlin), berlin)
	if err != nil {
		t.Fatalf("ListByRange: %v", err)
	}
	if !entryIDs(ranged)[late.ID] {
		t.Fatal("23:00 entry missing from range ending 2026-10-25")
	}
}

// On the 23-hour day start+24h would reach into the next day and show its
// first hour twice.
func TestListByDayExcludesNextDayAfterShortDSTDay(t *testing.T) {
	s, berlin := newBerlinStore(t)
	// 00:30 CEST on 2026-03-30.
	early, err := s.Create("just after midnight", time.Date(2026, 3, 29, 22, 30, 0, 0, time.UTC), false, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	short, err := s.ListByDay(time.Date(2026, 3, 29, 0, 0, 0, 0, berlin), berlin)
	if err != nil {
		t.Fatalf("ListByDay: %v", err)
	}
	if entryIDs(short)[early.ID] {
		t.Fatal("00:30 entry of 2026-03-30 shown on 2026-03-29")
	}
	next, err := s.ListByDay(time.Date(2026, 3, 30, 0, 0, 0, 0, berlin), berlin)
	if err != nil {
		t.Fatalf("ListByDay: %v", err)
	}
	if !entryIDs(next)[early.ID] {
		t.Fatal("00:30 entry missing from 2026-03-30")
	}
}

// Every entry must be committed to by exactly one day seal, including across
// both DST transitions; otherwise an unsealed entry could be altered unnoticed
// or a double-sealed one would make two days' seals depend on it.
func TestSealsCoverEachEntryOnceAcrossDST(t *testing.T) {
	s, _ := newBerlinStore(t)
	instants := []time.Time{
		time.Date(2026, 3, 28, 22, 30, 0, 0, time.UTC),  // 23:30 CET 28.03
		time.Date(2026, 3, 29, 21, 30, 0, 0, time.UTC),  // 23:30 CEST 29.03 (23 h day)
		time.Date(2026, 3, 29, 22, 30, 0, 0, time.UTC),  // 00:30 CEST 30.03
		time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC),   // 07:00 CET 25.10 (25 h day)
		time.Date(2026, 10, 25, 22, 0, 19, 0, time.UTC), // 23:00 CET 25.10
		time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC), // 00:30 CET 26.10
	}
	for _, at := range instants {
		if _, err := s.Create("e", at, true, nil, nil); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	if _, err := s.SealMissing(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SealMissing: %v", err)
	}
	seals, err := s.ListSeals()
	if err != nil {
		t.Fatalf("ListSeals: %v", err)
	}
	total := 0
	counts := map[string]int{}
	for _, ds := range seals {
		total += ds.EntryCount
		counts[ds.Date] = ds.EntryCount
	}
	if total != len(instants) {
		t.Fatalf("seals cover %d entries, want %d (%v)", total, len(instants), counts)
	}
	want := map[string]int{"2026-03-28": 1, "2026-03-29": 1, "2026-03-30": 1, "2026-10-25": 2, "2026-10-26": 1}
	for date, n := range want {
		if counts[date] != n {
			t.Errorf("%s: sealed %d entries, want %d", date, counts[date], n)
		}
	}

	res, err := s.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainOK || res.EntriesChecked != len(instants) {
		t.Fatalf("chain ok=%v entries=%d broke at %s: %s", res.ChainOK, res.EntriesChecked, res.FirstBrokenDay, res.BreakReason)
	}
}
