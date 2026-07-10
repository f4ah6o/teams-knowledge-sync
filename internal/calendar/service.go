package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	textutil "github.com/obr-grp/teams-knowledge-sync/internal/text"
)

type GraphAPI interface {
	Do(context.Context, string, string, any, any) error
	Page(context.Context, string, func(json.RawMessage) error) error
}
type Service struct {
	Graph  GraphAPI
	Store  *store.Store
	Config config.Config
	Now    func() time.Time
}

func (s Service) DiscoverCalendars(ctx context.Context) ([]domain.Calendar, error) {
	var out []domain.Calendar
	err := s.Graph.Page(ctx, "me/calendars?$top=100", func(raw json.RawMessage) error {
		c, err := parseCalendar(raw)
		if err != nil {
			return err
		}
		c.Enabled = s.calendarEnabled(c)
		if err = s.Store.UpsertCalendar(ctx, c); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	return out, err
}
func (s Service) Sync(ctx context.Context, only string, from, to *time.Time) error {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if from == nil {
		v := now().UTC().AddDate(0, 0, -s.Config.Calendar.Range.PastDays)
		from = &v
	}
	if to == nil {
		v := now().UTC().AddDate(0, 0, s.Config.Calendar.Range.FutureDays)
		to = &v
	}
	if !from.Before(*to) {
		return fmt.Errorf("calendar range must satisfy from < to")
	}
	var ids []string
	if only != "" {
		ids = []string{only}
	} else {
		for _, c := range s.Config.Calendar.Calendars {
			if c.IsEnabled() {
				ids = append(ids, c.ID)
			}
		}
	}
	for _, id := range ids {
		c, err := s.resolveCalendar(ctx, id)
		if err != nil {
			return err
		}
		if err = s.syncCalendar(ctx, c, *from, *to); err != nil {
			return fmt.Errorf("calendar %s: %w", c.Name, err)
		}
	}
	return nil
}
func (s Service) resolveCalendar(ctx context.Context, id string) (domain.Calendar, error) {
	path := "me/calendars/" + graph.Escape(id)
	if strings.EqualFold(id, "primary") {
		path = "me/calendar"
	}
	var raw json.RawMessage
	if err := s.Graph.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return domain.Calendar{}, err
	}
	c, err := parseCalendar(raw)
	if err != nil {
		return c, err
	}
	c.Enabled = true
	if strings.EqualFold(id, "primary") {
		c.Default = true
	}
	return c, s.Store.UpsertCalendar(ctx, c)
}
func (s Service) syncCalendar(ctx context.Context, c domain.Calendar, from, to time.Time) error {
	path := "me/calendars/" + graph.Escape(c.ID) + "/calendarView?startDateTime=" + url.QueryEscape(from.Format(time.RFC3339)) + "&endDateTime=" + url.QueryEscape(to.Format(time.RFC3339)) + "&$top=100&$expand=attachments($select=id,name,contentType,size,isInline)"
	return s.Graph.Page(ctx, path, func(raw json.RawMessage) error {
		e, err := parseEvent(raw, c.ID)
		if err != nil {
			return err
		}
		if strings.EqualFold(e.Sensitivity, "private") && !s.Config.Calendar.StorePrivateDetails() {
			maskPrivate(&e)
		}
		return s.Store.UpsertCalendarEvent(ctx, e)
	})
}
func (s Service) calendarEnabled(c domain.Calendar) bool {
	for _, selected := range s.Config.Calendar.Calendars {
		if !selected.IsEnabled() {
			continue
		}
		if strings.EqualFold(selected.ID, c.ID) || strings.EqualFold(selected.ID, "primary") && c.Default {
			return true
		}
	}
	return false
}
func parseCalendar(raw json.RawMessage) (domain.Calendar, error) {
	var v struct {
		ID, Name, Color, HexColor                                 string
		IsDefaultCalendar, CanEdit, CanShare, CanViewPrivateItems bool
		Owner                                                     struct {
			Name    string `json:"name"`
			Address string `json:"address"`
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return domain.Calendar{}, err
	}
	return domain.Calendar{ID: v.ID, Name: v.Name, Color: v.Color, HexColor: v.HexColor, OwnerName: v.Owner.Name, OwnerAddress: v.Owner.Address, Default: v.IsDefaultCalendar, CanEdit: v.CanEdit, CanShare: v.CanShare, CanViewPrivate: v.CanViewPrivateItems}, nil
}
func parseEvent(raw json.RawMessage, calendarID string) (domain.CalendarEvent, error) {
	type email struct{ Name, Address string }
	type party struct {
		Email email `json:"emailAddress"`
	}
	type location struct {
		DisplayName                          string
		LocationEmailAddress                 string
		LocationType, UniqueID, UniqueIDType string
	}
	var v struct {
		ID                                                                           string `json:"id"`
		ICalUID                                                                      string `json:"iCalUId"`
		SeriesMasterID                                                               string `json:"seriesMasterId"`
		Type, Subject, BodyPreview                                                   string
		Body                                                                         struct{ ContentType, Content string }
		Start, End                                                                   struct{ DateTime, TimeZone string }
		OriginalStartTimeZone, OriginalEndTimeZone                                   string
		IsAllDay, IsCancelled, IsOnlineMeeting, IsOrganizer, IsDraft, HasAttachments bool
		Organizer                                                                    party
		Attendees                                                                    []struct {
			Type         string
			Status       struct{ Response string }
			EmailAddress email
		}
		Location    location
		Locations   []location
		Categories  []string
		Attachments []struct {
			ID, Name, ContentType string
			Size                  int
			IsInline              bool
		}
		OnlineMeeting struct {
			JoinURL string `json:"joinUrl"`
		}
		OnlineMeetingURL                                                                string
		WebLink, Sensitivity, ShowAs, Importance, CreatedDateTime, LastModifiedDateTime string
		ResponseStatus                                                                  struct{ Response string }
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return domain.CalendarEvent{}, err
	}
	start, err := ParseGraphDateTime(v.Start.DateTime, v.Start.TimeZone)
	if err != nil {
		return domain.CalendarEvent{}, fmt.Errorf("start: %w", err)
	}
	end, err := ParseGraphDateTime(v.End.DateTime, v.End.TimeZone)
	if err != nil {
		return domain.CalendarEvent{}, fmt.Errorf("end: %w", err)
	}
	e := domain.CalendarEvent{ID: v.ID, CalendarID: calendarID, ICalUID: v.ICalUID, SeriesMasterID: v.SeriesMasterID, Type: v.Type, Subject: v.Subject, BodyPreview: v.BodyPreview, StartUTC: start.UTC(), EndUTC: end.UTC(), StartTimezone: v.Start.TimeZone, EndTimezone: v.End.TimeZone, OriginalStartTimezone: v.OriginalStartTimeZone, OriginalEndTimezone: v.OriginalEndTimeZone, AllDay: v.IsAllDay, Cancelled: v.IsCancelled, OnlineMeeting: v.IsOnlineMeeting, Organizer: v.IsOrganizer, Draft: v.IsDraft, HasAttachments: v.HasAttachments, OrganizerName: v.Organizer.Email.Name, OrganizerAddress: v.Organizer.Email.Address, Response: v.ResponseStatus.Response, TeamsJoinURL: v.OnlineMeeting.JoinURL, WebURL: v.WebLink, Sensitivity: v.Sensitivity, ShowAs: v.ShowAs, Importance: v.Importance, CreatedAt: parseRFC3339(v.CreatedDateTime), ModifiedAt: parseRFC3339(v.LastModifiedDateTime), RawJSON: append([]byte(nil), raw...), Categories: v.Categories}
	if e.TeamsJoinURL == "" {
		e.TeamsJoinURL = v.OnlineMeetingURL
	}
	if strings.EqualFold(v.Body.ContentType, "html") {
		e.BodyHTML = v.Body.Content
		e.BodyText = textutil.PlainHTML(v.Body.Content)
	} else {
		e.BodyText = v.Body.Content
	}
	for _, a := range v.Attendees {
		e.Attendees = append(e.Attendees, domain.CalendarAttendee{Type: a.Type, Name: a.EmailAddress.Name, Address: a.EmailAddress.Address, Response: a.Status.Response})
	}
	locations := v.Locations
	if len(locations) == 0 && v.Location.DisplayName != "" {
		locations = []location{v.Location}
	}
	for _, l := range locations {
		e.Locations = append(e.Locations, domain.CalendarLocation{Name: l.DisplayName, Address: l.LocationEmailAddress, LocationType: l.LocationType, UniqueID: l.UniqueID, UniqueIDType: l.UniqueIDType})
	}
	for _, a := range v.Attachments {
		b, _ := json.Marshal(a)
		e.Attachments = append(e.Attachments, domain.CalendarAttachment{ID: a.ID, Name: a.Name, ContentType: a.ContentType, Size: a.Size, Inline: a.IsInline, RawJSON: b})
	}
	return e, nil
}
func maskPrivate(e *domain.CalendarEvent) {
	e.Subject = "Private event"
	e.BodyHTML = ""
	e.BodyText = ""
	e.BodyPreview = ""
	e.OrganizerName = ""
	e.OrganizerAddress = ""
	e.Attendees = nil
	e.Locations = nil
	e.Categories = nil
	e.Attachments = nil
	e.HasAttachments = false
	e.TeamsJoinURL = ""
	e.RawJSON = []byte(`{}`)
}
func ParseGraphDateTime(value, zone string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	layout := "2006-01-02T15:04:05.9999999"
	loc, err := graphLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		t, err = time.ParseInLocation("2006-01-02T15:04:05", value, loc)
	}
	return t, err
}
func graphLocation(zone string) (*time.Location, error) {
	if zone == "" || strings.EqualFold(zone, "UTC") {
		return time.UTC, nil
	}
	aliases := map[string]string{"Tokyo Standard Time": "Asia/Tokyo", "Eastern Standard Time": "America/New_York", "Pacific Standard Time": "America/Los_Angeles", "GMT Standard Time": "Europe/London"}
	if v := aliases[zone]; v != "" {
		zone = v
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("unsupported timezone %q", zone)
	}
	return loc, nil
}
func parseRFC3339(v string) *time.Time {
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil
	}
	return &t
}
