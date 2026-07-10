package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
)

type deltaPage struct {
	Value     []json.RawMessage `json:"value"`
	NextLink  string            `json:"@odata.nextLink"`
	DeltaLink string            `json:"@odata.deltaLink"`
}

func (s Service) SyncDelta(ctx context.Context, only string) error {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	var ids []string
	if only != "" {
		ids = []string{only}
	} else {
		for _, selected := range s.Config.Calendar.Calendars {
			if selected.IsEnabled() {
				ids = append(ids, selected.ID)
			}
		}
	}
	var failures []error
	for _, id := range ids {
		calendar, err := s.resolveCalendar(ctx, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("calendar %s: %w", id, err))
			continue
		}
		windows, err := s.ensureWindows(ctx, calendar.ID, now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, window := range windows {
			if err = s.syncWindow(ctx, calendar, window, true); err != nil {
				failures = append(failures, fmt.Errorf("calendar %s window %s: %w", calendar.Name, window.StartUTC.Format(time.RFC3339), err))
			}
		}
	}
	return errors.Join(failures...)
}
func (s Service) ensureWindows(ctx context.Context, calendarID string, now time.Time) ([]domain.CalendarSyncWindow, error) {
	windows, err := s.Store.CalendarSyncWindows(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	pastDays := s.Config.Calendar.Range.PastDays
	if pastDays <= 0 {
		pastDays = 1095
	}
	futureDays := s.Config.Calendar.Range.FutureDays
	if futureDays <= 0 {
		futureDays = 365
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	desiredEnd := today.AddDate(0, 0, futureDays)
	var additions []domain.CalendarSyncWindow
	if len(windows) == 0 {
		start := today.AddDate(0, 0, -pastDays)
		for start.Before(today) {
			end := start.AddDate(0, 0, 90)
			if end.After(today) {
				end = today
			}
			additions = append(additions, domain.CalendarSyncWindow{CalendarID: calendarID, StartUTC: start, EndUTC: end})
			start = end
		}
		nearEnd := today.AddDate(0, 0, 30)
		if nearEnd.After(desiredEnd) {
			nearEnd = desiredEnd
		}
		if today.Before(nearEnd) {
			additions = append(additions, domain.CalendarSyncWindow{CalendarID: calendarID, StartUTC: today, EndUTC: nearEnd})
		}
		start = nearEnd
		for start.Before(desiredEnd) {
			end := start.AddDate(0, 0, 90)
			if end.After(desiredEnd) {
				end = desiredEnd
			}
			additions = append(additions, domain.CalendarSyncWindow{CalendarID: calendarID, StartUTC: start, EndUTC: end})
			start = end
		}
	} else {
		start := windows[len(windows)-1].EndUTC
		for start.Before(desiredEnd) {
			end := start.AddDate(0, 0, 90)
			if end.After(desiredEnd) {
				end = desiredEnd
			}
			additions = append(additions, domain.CalendarSyncWindow{CalendarID: calendarID, StartUTC: start, EndUTC: end})
			start = end
		}
	}
	for _, w := range additions {
		if err = s.Store.EnsureCalendarWindow(ctx, w); err != nil {
			return nil, err
		}
	}
	return s.Store.CalendarSyncWindows(ctx, calendarID)
}
func (s Service) syncWindow(ctx context.Context, c domain.Calendar, w domain.CalendarSyncWindow, allowReset bool) error {
	started := time.Now().UTC()
	if s.Now != nil {
		started = s.Now().UTC()
	}
	path := w.NextLink
	fullRound := w.DeltaLink == ""
	if fullRound && w.NextLink == "" {
		if err := s.Store.ClearCalendarWindowSeen(ctx, w); err != nil {
			return s.recordWindowFailure(ctx, w, started, err)
		}
	}
	if path == "" {
		path = w.DeltaLink
	}
	if path == "" {
		values := url.Values{"startDateTime": []string{w.StartUTC.Format(time.RFC3339)}, "endDateTime": []string{w.EndUTC.Format(time.RFC3339)}}
		if c.Default {
			path = "me/calendarView/delta?" + values.Encode()
		} else {
			path = "https://graph.microsoft.com/beta/me/calendars/" + graph.Escape(c.ID) + "/calendarView/delta?" + values.Encode()
		}
	}
	for path != "" {
		var page deltaPage
		if err := s.Graph.Do(ctx, http.MethodGet, path, nil, &page); err != nil {
			if allowReset && invalidCalendarDelta(err) {
				if resetErr := s.Store.ResetCalendarWindowToken(ctx, w); resetErr != nil {
					return errors.Join(err, resetErr)
				}
				w.NextLink, w.DeltaLink = "", ""
				return s.syncWindow(ctx, c, w, false)
			}
			return s.recordWindowFailure(ctx, w, started, err)
		}
		for _, raw := range page.Value {
			var marker struct {
				ID      string          `json:"id"`
				Removed json.RawMessage `json:"@removed"`
			}
			if err := json.Unmarshal(raw, &marker); err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			if len(marker.Removed) > 0 {
				if err := s.Store.RemoveCalendarWindowEvent(ctx, w, marker.ID, started); err != nil {
					return s.recordWindowFailure(ctx, w, started, err)
				}
				continue
			}
			event, err := parseEvent(raw, c.ID)
			if err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			if strings.EqualFold(event.Sensitivity, "private") && !s.Config.Calendar.StorePrivateDetails() {
				maskPrivate(&event)
			}
			if !event.StartUTC.Before(w.EndUTC) || !event.EndUTC.After(w.StartUTC) {
				continue
			}
			if err = s.Store.UpsertCalendarEvent(ctx, event); err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			if err = s.Store.LinkCalendarWindowEvent(ctx, w, event.ID); err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			if fullRound {
				if err = s.Store.MarkCalendarWindowSeen(ctx, w, event.ID); err != nil {
					return s.recordWindowFailure(ctx, w, started, err)
				}
			}
		}
		if page.NextLink != "" {
			if err := s.Store.RecordCalendarWindowProgress(ctx, w, page.NextLink, started); err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			path = page.NextLink
			continue
		}
		if page.DeltaLink == "" {
			return s.recordWindowFailure(ctx, w, started, fmt.Errorf("calendar delta response missing nextLink and deltaLink"))
		}
		if fullRound {
			seenIDs, err := s.Store.CalendarWindowSeenIDs(ctx, w)
			if err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			seen := map[string]bool{}
			for _, id := range seenIDs {
				seen[id] = true
			}
			ids, err := s.Store.CalendarWindowEventIDs(ctx, w)
			if err != nil {
				return s.recordWindowFailure(ctx, w, started, err)
			}
			for _, id := range ids {
				if !seen[id] {
					if err = s.Store.RemoveCalendarWindowEvent(ctx, w, id, started); err != nil {
						return s.recordWindowFailure(ctx, w, started, err)
					}
				}
			}
		}
		if err := s.Store.RecordCalendarWindowSuccess(ctx, w, page.DeltaLink, started); err != nil {
			return s.recordWindowFailure(ctx, w, started, err)
		}
		if fullRound {
			_ = s.Store.ClearCalendarWindowSeen(ctx, w)
		}
		return nil
	}
	return nil
}
func (s Service) recordWindowFailure(ctx context.Context, w domain.CalendarSyncWindow, at time.Time, syncErr error) error {
	if err := s.Store.RecordCalendarWindowFailure(ctx, w, at, syncErr); err != nil {
		return errors.Join(syncErr, err)
	}
	return syncErr
}
func invalidCalendarDelta(err error) bool {
	v := strings.ToLower(err.Error())
	return strings.Contains(v, "syncstatenotfound") || strings.Contains(v, "invaliddeltatoken") || strings.Contains(v, "410 gone")
}
func (s Service) DeltaDaemon(ctx context.Context) error {
	interval := s.Config.Sync.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	for {
		_ = s.SyncDelta(ctx, "")
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
