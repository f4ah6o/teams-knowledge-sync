package calendar

import (
	"testing"
	"time"
)

func TestWindowsAreContiguousMonthAlignedAndCoverHorizon(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	ws := Windows(now, 100, 100, 1, 3, 3)
	want := []Window{
		{time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
	}
	if len(ws) != len(want) {
		t.Fatalf("got %d windows: %+v", len(ws), ws)
	}
	for i, w := range want {
		if !ws[i].Start.Equal(w.Start) || !ws[i].End.Equal(w.End) {
			t.Fatalf("window %d = [%v,%v) want [%v,%v)", i, ws[i].Start, ws[i].End, w.Start, w.End)
		}
	}
	for i := 1; i < len(ws); i++ {
		if !ws[i].Start.Equal(ws[i-1].End) {
			t.Fatalf("gap between window %d and %d", i-1, i)
		}
	}
	if ws[0].Start.After(now.Add(-100 * 24 * time.Hour)) {
		t.Fatal("horizon start not covered")
	}
	if ws[len(ws)-1].End.Before(now.Add(100 * 24 * time.Hour)) {
		t.Fatal("horizon end not covered")
	}
}

func TestWindowsLongHorizonUsesConfiguredSizes(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	ws := Windows(now, 1095, 365, 1, 3, 3)
	if ws[0].End.Sub(ws[0].Start) < 28*24*time.Hour {
		t.Fatalf("first window too small: %+v", ws[0])
	}
	// recent region is two one-month windows starting at the current month
	cur := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	foundRecent := 0
	for _, w := range ws {
		if w.Start.Equal(cur) || w.Start.Equal(cur.AddDate(0, 1, 0)) {
			if w.End.Sub(w.Start) > 32*24*time.Hour {
				t.Fatalf("recent window not month-sized: %+v", w)
			}
			foundRecent++
		}
	}
	if foundRecent != 2 {
		t.Fatalf("recent windows=%d", foundRecent)
	}
}

func TestWindowsExtendAsTimeAdvances(t *testing.T) {
	past, future := 100, 100
	before := Windows(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), past, future, 1, 3, 3)
	after := Windows(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), past, future, 1, 3, 3)
	maxEnd := func(ws []Window) time.Time {
		m := time.Time{}
		for _, w := range ws {
			if w.End.After(m) {
				m = w.End
			}
		}
		return m
	}
	if !maxEnd(after).After(maxEnd(before)) {
		t.Fatalf("future range did not extend: before=%v after=%v", maxEnd(before), maxEnd(after))
	}
	if !after[len(after)-1].End.After(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC).Add(100 * 24 * time.Hour)) {
		t.Fatal("advanced horizon not covered")
	}
}
