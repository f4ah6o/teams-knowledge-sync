package calendar

import "time"

// Window is a half-open [Start,End) delta-sync range, aligned to UTC month
// boundaries so an event on a boundary belongs to exactly one window.
type Window struct {
	Start, End time.Time
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Windows splits the sync horizon [now-pastDays, now+futureDays) into
// contiguous month-aligned windows: histMonths-sized windows stepping back
// from the current month, recentMonths-sized windows for the next two months,
// and futureMonths-sized windows beyond. Boundaries are anchored at the
// current month, so as time advances new windows appear (extending the future
// range) while windows that left the horizon are simply no longer returned.
func Windows(now time.Time, pastDays, futureDays, recentMonths, histMonths, futureMonths int) []Window {
	if recentMonths <= 0 {
		recentMonths = 1
	}
	if histMonths <= 0 {
		histMonths = 3
	}
	if futureMonths <= 0 {
		futureMonths = 3
	}
	cur := monthStart(now)
	horizonStart := now.UTC().Add(-time.Duration(pastDays) * 24 * time.Hour)
	horizonEnd := now.UTC().Add(time.Duration(futureDays) * 24 * time.Hour)
	var past []Window
	for end := cur; end.After(horizonStart); {
		start := end.AddDate(0, -histMonths, 0)
		past = append(past, Window{start, end})
		end = start
	}
	ws := make([]Window, 0, len(past)+8)
	for i := len(past) - 1; i >= 0; i-- {
		ws = append(ws, past[i])
	}
	recentEnd := cur.AddDate(0, 2, 0)
	for start := cur; start.Before(recentEnd); {
		end := start.AddDate(0, recentMonths, 0)
		if end.After(recentEnd) {
			end = recentEnd
		}
		ws = append(ws, Window{start, end})
		start = end
	}
	for start := recentEnd; start.Before(horizonEnd); {
		end := start.AddDate(0, futureMonths, 0)
		ws = append(ws, Window{start, end})
		start = end
	}
	return ws
}
