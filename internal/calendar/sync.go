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
	Graph      GraphAPI
	Store      *outlookstore.Store
	Selections []Selection
	Private    PrivatePolicy
	PastDays   int
	FutureDays int
	Now        func() time.Time
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

// SyncAll performs a full-range fetch for every enabled calendar, isolating
// per-calendar failures.
func (s *Service) SyncAll(ctx context.Context) error {
	calendars, err := s.SyncCalendars(ctx)
	if err != nil {
		return err
	}
	from, to := s.Range()
	failures := 0
	for _, c := range calendars {
		if err := s.SyncRange(ctx, c.ID, from, to); err != nil {
			failures++
			log.Printf("calendar %s (%s): %v", c.Name, c.ID, err)
		}
	}
	if failures > 0 && failures == len(calendars) {
		return fmt.Errorf("all %d calendars failed", failures)
	}
	return nil
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
