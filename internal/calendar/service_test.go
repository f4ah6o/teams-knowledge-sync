package calendar

import (
	"context"
	"encoding/json"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	"strings"
	"testing"
	"time"
)

type fakeGraph struct {
	calendar          json.RawMessage
	calendars, events []json.RawMessage
	paths             []string
}

func (f *fakeGraph) Do(_ context.Context, _ string, path string, _ any, out any) error {
	f.paths = append(f.paths, path)
	p := out.(*json.RawMessage)
	*p = f.calendar
	return nil
}
func (f *fakeGraph) Page(_ context.Context, path string, fn func(json.RawMessage) error) error {
	f.paths = append(f.paths, path)
	values := f.events
	if strings.Contains(path, "me/calendars?$top") {
		values = f.calendars
	}
	for _, raw := range values {
		if err := fn(raw); err != nil {
			return err
		}
	}
	return nil
}
func boolp(v bool) *bool { return &v }
func TestParseGraphDateTime(t *testing.T) {
	got, err := ParseGraphDateTime("2026-07-10T09:00:00", "Tokyo Standard Time")
	if err != nil {
		t.Fatal(err)
	}
	if got.UTC().Format(time.RFC3339) != "2026-07-10T00:00:00Z" {
		t.Fatalf("got=%s", got)
	}
	if _, err = ParseGraphDateTime("2026-07-10T09:00:00", "Unknown Custom Zone"); err == nil {
		t.Fatal("expected unsupported timezone")
	}
}
func TestParseEventKindsAndTeamsMeeting(t *testing.T) {
	for _, kind := range []string{"singleInstance", "occurrence", "exception", "seriesMaster"} {
		raw := json.RawMessage(`{"id":"e1","type":"` + kind + `","start":{"dateTime":"2026-07-10T09:00:00","timeZone":"Tokyo Standard Time"},"end":{"dateTime":"2026-07-10T10:00:00","timeZone":"Tokyo Standard Time"},"isAllDay":true,"isCancelled":true,"isOnlineMeeting":true,"onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/x"}}`)
		e, err := parseEvent(raw, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if e.Type != kind || !e.AllDay || !e.Cancelled || e.TeamsJoinURL == "" || e.StartUTC.Hour() != 0 {
			t.Fatalf("event=%+v", e)
		}
	}
}
func TestSyncUpsertsCalendarViewAndMasksPrivateDetails(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary", Enabled: boolp(true)}}
	cfg.Calendar.Range.PastDays = 1
	cfg.Calendar.Range.FutureDays = 1
	cal := json.RawMessage(`{"id":"c1","name":"Calendar","isDefaultCalendar":true,"owner":{"name":"User","address":"user@example.com"}}`)
	public := json.RawMessage(`{"id":"e1","iCalUId":"ical1","type":"occurrence","seriesMasterId":"master1","subject":"工事改善","body":{"contentType":"html","content":"<p>確認事項</p>"},"start":{"dateTime":"2026-07-10T09:00:00","timeZone":"Tokyo Standard Time"},"end":{"dateTime":"2026-07-10T10:00:00","timeZone":"Tokyo Standard Time"},"originalStartTimeZone":"Tokyo Standard Time","originalEndTimeZone":"Tokyo Standard Time","organizer":{"emailAddress":{"name":"Owner","address":"owner@example.com"}},"attendees":[{"type":"required","status":{"response":"accepted"},"emailAddress":{"name":"Member","address":"member@example.com"}}],"locations":[{"displayName":"Room","locationEmailAddress":"room@example.com"}],"isOnlineMeeting":true,"onlineMeeting":{"joinUrl":"https://teams.microsoft.com/meeting"},"webLink":"https://outlook.office.com/calendar/e1","hasAttachments":true,"attachments":[{"id":"a1","name":"agenda.pdf","contentType":"application/pdf","size":42}]}`)
	updated := json.RawMessage(strings.Replace(string(public), "工事改善", "工事改善 updated", 1))
	private := json.RawMessage(`{"id":"e2","type":"singleInstance","subject":"Secret Project","sensitivity":"private","body":{"contentType":"text","content":"secret body"},"start":{"dateTime":"2026-07-10T11:00:00Z","timeZone":"UTC"},"end":{"dateTime":"2026-07-10T12:00:00Z","timeZone":"UTC"},"attendees":[{"emailAddress":{"address":"secret@example.com"}}]}`)
	g := &fakeGraph{calendar: cal, events: []json.RawMessage{public, updated, private}}
	s := Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) }}
	if err = s.Sync(context.Background(), "", nil, nil); err != nil {
		t.Fatal(err)
	}
	results, err := db.SearchCalendarEvents(context.Background(), domain.CalendarSearchFilter{Query: "updated"})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	detail, err := db.CalendarEvent(context.Background(), "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attendees) != 1 || len(detail.Locations) != 1 || len(detail.Attachments) != 1 || detail.TeamsJoinURL == "" {
		t.Fatalf("detail=%+v", detail)
	}
	masked, err := db.CalendarEvent(context.Background(), "e2")
	if err != nil {
		t.Fatal(err)
	}
	if masked.Subject != "Private event" || masked.BodyText != "" || len(masked.Attendees) != 0 || string(masked.RawJSON) != "{}" {
		t.Fatalf("masked=%+v raw=%s", masked, string(masked.RawJSON))
	}
	if len(g.paths) < 2 || !strings.Contains(g.paths[1], "startDateTime=2026-07-09T00%3A00%3A00Z") || !strings.Contains(g.paths[1], "endDateTime=2026-07-11T00%3A00%3A00Z") {
		t.Fatalf("paths=%v", g.paths)
	}
	from := time.Date(2026, 7, 10, 0, 30, 0, 0, time.UTC)
	to := time.Date(2026, 7, 10, 0, 45, 0, 0, time.UTC)
	window, err := db.SearchCalendarEvents(context.Background(), domain.CalendarSearchFilter{From: &from, To: &to})
	if err != nil || len(window) != 1 || window[0].ID != "e1" {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}
func TestDiscoverCalendarsMarksPrimarySelection(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary"}}
	g := &fakeGraph{calendars: []json.RawMessage{json.RawMessage(`{"id":"c1","name":"Main","isDefaultCalendar":true}`), json.RawMessage(`{"id":"c2","name":"Other"}`)}}
	values, err := (&Service{Graph: g, Store: db, Config: cfg}).DiscoverCalendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || !values[0].Enabled || values[1].Enabled {
		t.Fatalf("values=%+v", values)
	}
}
