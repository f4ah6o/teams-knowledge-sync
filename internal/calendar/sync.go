package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

type GraphAPI interface {
	Do(ctx context.Context, method, path string, body any, out any) error
	GetPage(ctx context.Context, pageURL string, headers map[string]string) (graph.PageResult, error)
}

// Selection mirrors the calendar.calendars config entries; ID "primary"
// matches the default calendar.
type Selection struct {
	ID      string
	Enabled bool
}

type Service struct {
	Graph        GraphAPI
	Store        *outlookstore.Store
	Selections   []Selection
	Private      PrivatePolicy
	PastDays     int
	FutureDays   int
	RecentMonths int
	HistMonths   int
	FutureMonths int
	Now          func() time.Time
}

const eventSelect = "id,iCalUId,seriesMasterId,type,subject,body,bodyPreview,start,end,originalStart,originalStartTimeZone,originalEndTimeZone,isAllDay,isCancelled,isOrganizer,isOnlineMeeting,hasAttachments,showAs,importance,sensitivity,webLink,createdDateTime,lastModifiedDateTime,categories,onlineMeetingUrl,onlineMeeting,organizer,location,locations,attendees,responseStatus"

func headers() map[string]string {
	return map[string]string{"Prefer": `outlook.timezone="UTC", odata.maxpagesize=50`}
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Range returns the configured sync horizon [now-past_days, now+future_days).
func (s *Service) Range() (time.Time, time.Time) {
	now := s.now().UTC()
	past := s.PastDays
	if past == 0 {
		past = 1095
	}
	future := s.FutureDays
	if future == 0 {
		future = 365
	}
	return now.Add(-time.Duration(past) * 24 * time.Hour), now.Add(time.Duration(future) * 24 * time.Hour)
}

// SyncCalendars fetches the calendar list, marks configured ones enabled, and
// returns the enabled set.
func (s *Service) SyncCalendars(ctx context.Context) ([]domain.Calendar, error) {
	var enabled []domain.Calendar
	pageURL := "me/calendars?$top=50"
	for pageURL != "" {
		page, err := s.Graph.GetPage(ctx, pageURL, nil)
		if err != nil {
			return nil, err
		}
		for _, raw := range page.Value {
			var v struct {
				ID                  string `json:"id"`
				Name                string `json:"name"`
				HexColor            string `json:"hexColor"`
				IsDefaultCalendar   bool   `json:"isDefaultCalendar"`
				CanEdit             bool   `json:"canEdit"`
				CanViewPrivateItems bool   `json:"canViewPrivateItems"`
				Owner               struct {
					Address string `json:"address"`
				} `json:"owner"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			c := domain.Calendar{ID: v.ID, Name: v.Name, Color: v.HexColor, Owner: v.Owner.Address, IsDefault: v.IsDefaultCalendar, CanEdit: v.CanEdit, CanViewPrivate: v.CanViewPrivateItems, Enabled: s.selected(v.ID, v.IsDefaultCalendar), RawJSON: raw}
			if err := s.Store.UpsertCalendar(ctx, c); err != nil {
				return nil, err
			}
			if c.Enabled {
				enabled = append(enabled, c)
			}
		}
		pageURL = page.NextLink
	}
	return enabled, nil
}
func (s *Service) selected(id string, isDefault bool) bool {
	for _, sel := range s.Selections {
		if !sel.Enabled {
			continue
		}
		if sel.ID == id || (sel.ID == "primary" && isDefault) {
			return true
		}
	}
	return false
}

// SyncAll runs windowed delta sync for every enabled calendar, isolating
// per-calendar and per-window failures.
func (s *Service) SyncAll(ctx context.Context) error {
	calendars, err := s.SyncCalendars(ctx)
	if err != nil {
		return err
	}
	failures := 0
	for _, c := range calendars {
		if err := s.SyncWindows(ctx, c.ID); err != nil {
			failures++
			log.Printf("calendar %s (%s): %v", c.Name, c.ID, err)
		}
	}
	if failures > 0 && failures == len(calendars) {
		return fmt.Errorf("all %d calendars failed", failures)
	}
	return nil
}

// EnsureWindows computes the desired window set from the configured horizon
// and creates missing state rows; existing windows keep their delta state.
// Because the set is recomputed from the current time, new future windows are
// created automatically once the synced future range falls below the
// configured value.
func (s *Service) EnsureWindows(ctx context.Context, calendarID string) ([]Window, error) {
	past, future := s.PastDays, s.FutureDays
	if past == 0 {
		past = 1095
	}
	if future == 0 {
		future = 365
	}
	ws := Windows(s.now(), past, future, s.RecentMonths, s.HistMonths, s.FutureMonths)
	for _, w := range ws {
		if err := s.Store.EnsureCalendarWindow(ctx, calendarID, w.Start, w.End); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

// SyncWindows delta-syncs every window in the calendar's horizon. A window
// failure is recorded and does not stop the remaining windows.
func (s *Service) SyncWindows(ctx context.Context, calendarID string) error {
	ws, err := s.EnsureWindows(ctx, calendarID)
	if err != nil {
		return err
	}
	failures := 0
	for _, w := range ws {
		if err := s.SyncWindow(ctx, calendarID, w); err != nil {
			failures++
			log.Printf("calendar %s window [%s,%s): %v", calendarID, w.Start.Format("2006-01-02"), w.End.Format("2006-01-02"), err)
		}
	}
	if failures > 0 && failures == len(ws) {
		return fmt.Errorf("all %d windows failed", failures)
	}
	return nil
}

// SyncWindow runs the window's delta state machine: resume a stored nextLink,
// else continue from the stored deltaLink, else start a full delta
// initialization with the window range fixed on the first request. The
// deltaLink is committed only after every page has been applied.
func (s *Service) SyncWindow(ctx context.Context, calendarID string, w Window) error {
	return s.syncWindowDelta(ctx, calendarID, w, true)
}
func (s *Service) syncWindowDelta(ctx context.Context, calendarID string, w Window, allowReset bool) error {
	started := s.now().UTC()
	st, err := s.Store.GetCalendarWindowState(ctx, calendarID, w.Start, w.End)
	if err != nil {
		return err
	}
	pageURL := st.NextLink
	if pageURL == "" {
		pageURL = st.DeltaLink
	}
	if pageURL == "" {
		pageURL = "me/calendars/" + graph.Escape(calendarID) + "/calendarView/delta?startDateTime=" + url.QueryEscape(w.Start.Format(time.RFC3339)) + "&endDateTime=" + url.QueryEscape(w.End.Format(time.RFC3339))
	}
	fetchedMasters := map[string]bool{}
	for {
		page, err := s.Graph.GetPage(ctx, pageURL, headers())
		if err != nil {
			if graph.IsSyncStateInvalid(err) && allowReset {
				// expired delta token: reinitialize this window only
				if e := s.Store.ResetCalendarWindowState(ctx, calendarID, w.Start, w.End); e != nil {
					return e
				}
				return s.syncWindowDelta(ctx, calendarID, w, false)
			}
			_ = s.Store.RecordCalendarWindowFailure(ctx, calendarID, w.Start, w.End, started, err)
			return err
		}
		for _, raw := range page.Value {
			if err := s.applyDeltaItem(ctx, raw, calendarID, w, fetchedMasters); err != nil {
				_ = s.Store.RecordCalendarWindowFailure(ctx, calendarID, w.Start, w.End, started, err)
				return err
			}
		}
		if page.NextLink != "" {
			if err := s.Store.SaveCalendarWindowNextLink(ctx, calendarID, w.Start, w.End, page.NextLink); err != nil {
				return err
			}
			pageURL = page.NextLink
			continue
		}
		deltaLink := page.DeltaLink
		if deltaLink == "" {
			deltaLink = st.DeltaLink
		}
		return s.Store.CommitCalendarWindowDeltaLink(ctx, calendarID, w.Start, w.End, deltaLink, started, s.now().UTC())
	}
}

// applyDeltaItem reflects one delta entry. Removal notices can reference
// events outside this window; the stored event's bounds are checked before
// changing local state so only the owning window tombstones it.
func (s *Service) applyDeltaItem(ctx context.Context, raw json.RawMessage, calendarID string, w Window, fetchedMasters map[string]bool) error {
	var probe struct {
		ID      string `json:"id"`
		Removed *struct {
			Reason string `json:"reason"`
		} `json:"@removed"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	if probe.Removed == nil {
		return s.storeEvent(ctx, raw, calendarID, fetchedMasters)
	}
	start, end, stored, err := s.Store.EventBounds(ctx, calendarID, probe.ID)
	if err != nil {
		return err
	}
	if !stored || !start.Before(w.End) || !end.After(w.Start) {
		return nil
	}
	return s.Store.TombstoneCalendarEvent(ctx, calendarID, probe.ID, s.now().UTC())
}

// SyncRange pages the calendarView for the half-open interval [from,to) and
// upserts every returned event, fetching unseen series masters once.
func (s *Service) SyncRange(ctx context.Context, calendarID string, from, to time.Time) error {
	pageURL := "me/calendars/" + graph.Escape(calendarID) + "/calendarView?startDateTime=" + url.QueryEscape(from.UTC().Format(time.RFC3339)) + "&endDateTime=" + url.QueryEscape(to.UTC().Format(time.RFC3339)) + "&$top=50&$select=" + eventSelect
	fetchedMasters := map[string]bool{}
	for pageURL != "" {
		page, err := s.Graph.GetPage(ctx, pageURL, headers())
		if err != nil {
			return err
		}
		for _, raw := range page.Value {
			if err := s.storeEvent(ctx, raw, calendarID, fetchedMasters); err != nil {
				return err
			}
		}
		pageURL = page.NextLink
	}
	return nil
}
func (s *Service) storeEvent(ctx context.Context, raw json.RawMessage, calendarID string, fetchedMasters map[string]bool) error {
	ev, err := Event(raw, calendarID, s.Private)
	if err != nil {
		return err
	}
	if err := s.Store.UpsertCalendarEvent(ctx, ev); err != nil {
		return err
	}
	if ev.SeriesMasterID != "" && !fetchedMasters[ev.SeriesMasterID] {
		fetchedMasters[ev.SeriesMasterID] = true
		stored, err := s.Store.HasEvent(ctx, calendarID, ev.SeriesMasterID)
		if err != nil {
			return err
		}
		if !stored {
			if err := s.fetchSeriesMaster(ctx, calendarID, ev.SeriesMasterID); err != nil {
				// the master is structural metadata; occurrences are already stored
				log.Printf("series master %s: %v", ev.SeriesMasterID, err)
			}
		}
	}
	return nil
}
func (s *Service) fetchSeriesMaster(ctx context.Context, calendarID, id string) error {
	var raw json.RawMessage
	if err := s.Graph.Do(ctx, http.MethodGet, "me/events/"+graph.Escape(id)+"?$select="+eventSelect, nil, &raw); err != nil {
		return err
	}
	master, err := Event(raw, calendarID, s.Private)
	if err != nil {
		return err
	}
	if master.EventType == "" || master.EventType == "singleInstance" {
		master.EventType = "seriesMaster"
	}
	return s.Store.UpsertCalendarEvent(ctx, master)
}
