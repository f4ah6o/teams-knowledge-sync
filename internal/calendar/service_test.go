package calendar

import (
	"context"
	"encoding/json"
	"errors"
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
	deltas            map[string]deltaPage
	errors            map[string]error
	errorContains     map[string]error
	calls             chan struct{}
}

func (f *fakeGraph) Do(_ context.Context, _ string, path string, _ any, out any) error {
	f.paths = append(f.paths, path)
	if f.calls != nil {
		f.calls <- struct{}{}
	}
	if err := f.errors[path]; err != nil {
		return err
	}
	for part, err := range f.errorContains {
		if strings.Contains(path, part) {
			return err
		}
	}
	switch p := out.(type) {
	case *json.RawMessage:
		*p = f.calendar
	case *deltaPage:
		if page, ok := f.deltas[path]; ok {
			*p = page
		} else {
			*p = deltaPage{Value: f.events, DeltaLink: "delta:" + path}
		}
	default:
		return errors.New("unexpected output type")
	}
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
	deltaUpdate := detail
	deltaUpdate.Subject = "delta update"
	deltaUpdate.Attachments = nil
	if err = db.UpsertCalendarEvent(context.Background(), deltaUpdate); err != nil {
		t.Fatal(err)
	}
	detail, err = db.CalendarEvent(context.Background(), "e1")
	if err != nil || len(detail.Attachments) != 1 {
		t.Fatalf("attachments lost on delta update: %+v err=%v", detail, err)
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

func TestBuildCalendarWindowsAndUsePrimaryV1Delta(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary"}}
	cfg.Calendar.Range.PastDays = 180
	cfg.Calendar.Range.FutureDays = 200
	g := &fakeGraph{calendar: json.RawMessage(`{"id":"c1","name":"Main","isDefaultCalendar":true}`)}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	s := Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return now }}
	if err = s.SyncDelta(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	windows, err := db.CalendarSyncWindows(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) < 5 {
		t.Fatalf("windows=%+v", windows)
	}
	for i, w := range windows {
		if !w.StartUTC.Before(w.EndUTC) || w.DeltaLink == "" {
			t.Fatalf("window=%+v", w)
		}
		if i > 0 && !windows[i-1].EndUTC.Equal(w.StartUTC) {
			t.Fatalf("gap/overlap=%+v", windows)
		}
	}
	found := false
	for _, path := range g.paths {
		if strings.HasPrefix(path, "me/calendarView/delta?") {
			found = true
		}
	}
	if !found {
		t.Fatalf("paths=%v", g.paths)
	}
}

func TestNonPrimaryCalendarUsesBetaDeltaEndpoint(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "c2"}}
	cfg.Calendar.Range.PastDays = 1
	cfg.Calendar.Range.FutureDays = 1
	g := &fakeGraph{calendar: json.RawMessage(`{"id":"c2","name":"Other"}`)}
	if err = (&Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) }}).SyncDelta(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, path := range g.paths {
		if strings.HasPrefix(path, "https://graph.microsoft.com/beta/me/calendars/c2/calendarView/delta?") {
			found = true
		}
	}
	if !found {
		t.Fatalf("paths=%v", g.paths)
	}
}

func TestCalendarDeltaRemovalAndBoundaryMapping(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary"}}
	cfg.Calendar.Range.PastDays = 1
	cfg.Calendar.Range.FutureDays = 1
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	boundary := json.RawMessage(`{"id":"e1","type":"exception","subject":"Moved","start":{"dateTime":"2026-07-10T00:00:00Z","timeZone":"UTC"},"end":{"dateTime":"2026-07-10T01:00:00Z","timeZone":"UTC"}}`)
	g := &fakeGraph{calendar: json.RawMessage(`{"id":"c1","name":"Main","isDefaultCalendar":true}`), events: []json.RawMessage{boundary}}
	s := Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return now }}
	if err = s.SyncDelta(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var mappings int
	if err = db.DB.QueryRowContext(ctx, `SELECT count(*) FROM calendar_window_events WHERE event_id='e1'`).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if mappings != 1 {
		t.Fatalf("boundary mappings=%d", mappings)
	}
	windows, _ := db.CalendarSyncWindows(ctx, "c1")
	var target domain.CalendarSyncWindow
	for _, w := range windows {
		if w.StartUTC.Equal(now) {
			target = w
		}
	}
	next := "https://graph.microsoft.com/v1.0/next"
	g.events = nil
	g.deltas = map[string]deltaPage{target.DeltaLink: {NextLink: next}, next: {Value: []json.RawMessage{json.RawMessage(`{"id":"e1","@removed":{"reason":"deleted"}}`)}, DeltaLink: "delta-new"}}
	if err = s.SyncDelta(ctx, ""); err != nil {
		t.Fatal(err)
	}
	results, err := db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{})
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	updated, _ := db.CalendarSyncWindows(ctx, "c1")
	for _, w := range updated {
		if w.StartUTC.Equal(target.StartUTC) && w.DeltaLink != "delta-new" {
			t.Fatalf("window=%+v", w)
		}
	}
}

func TestCalendarDeltaFailureIsolationTokenResetAndFutureExtension(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary"}}
	cfg.Calendar.Range.PastDays = 1
	cfg.Calendar.Range.FutureDays = 30
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	g := &fakeGraph{calendar: json.RawMessage(`{"id":"c1","name":"Main","isDefaultCalendar":true}`)}
	s := Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return now }}
	if err = s.SyncDelta(ctx, ""); err != nil {
		t.Fatal(err)
	}
	windows, _ := db.CalendarSyncWindows(ctx, "c1")
	first := windows[0]
	stale := domain.CalendarEvent{ID: "stale", CalendarID: "c1", Subject: "stale", StartUTC: first.StartUTC.Add(time.Hour), EndUTC: first.StartUTC.Add(2 * time.Hour), RawJSON: []byte(`{}`)}
	if err = db.UpsertCalendarEvent(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err = db.LinkCalendarWindowEvent(ctx, first, stale.ID); err != nil {
		t.Fatal(err)
	}
	g.errors = map[string]error{first.DeltaLink: errors.New("410 Gone: SyncStateNotFound")}
	if err = s.SyncDelta(ctx, ""); err != nil {
		t.Fatal(err)
	}
	afterReset, _ := db.CalendarSyncWindows(ctx, "c1")
	if afterReset[0].ConsecutiveFailures != 0 || afterReset[0].DeltaLink == "" {
		t.Fatalf("reset=%+v", afterReset[0])
	}
	visible, err := db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{Query: "stale"})
	if err != nil || len(visible) != 0 {
		t.Fatalf("stale event survived token reset: %+v err=%v", visible, err)
	}
	cfg.Calendar.Range.FutureDays = 200
	s.Config = cfg
	if err = s.SyncDelta(ctx, ""); err != nil {
		t.Fatal(err)
	}
	extended, _ := db.CalendarSyncWindows(ctx, "c1")
	if len(extended) <= len(windows) || !extended[len(extended)-1].EndUTC.After(windows[len(windows)-1].EndUTC) {
		t.Fatalf("before=%+v after=%+v", windows, extended)
	}
	bad := extended[0]
	forbidden := extended[1]
	g.errors = nil
	g.deltas = map[string]deltaPage{forbidden.DeltaLink: {NextLink: "next-403"}}
	g.errors = map[string]error{"next-403": errors.New("graph GET: 403 Forbidden")}
	g.errorContains = map[string]error{bad.DeltaLink: errors.New("graph retry limit exceeded after 429")}
	err = s.SyncDelta(ctx, "")
	if err == nil {
		t.Fatal("expected one window failure")
	}
	final, _ := db.CalendarSyncWindows(ctx, "c1")
	if final[0].ConsecutiveFailures != 1 || final[1].ConsecutiveFailures != 1 || final[1].NextLink != "next-403" || final[1].DeltaLink != forbidden.DeltaLink {
		t.Fatalf("failed=%+v", final[0])
	}
	successes := 0
	for _, w := range final {
		if w.ConsecutiveFailures == 0 && w.LastSuccessAt != nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatalf("other windows did not continue: %+v", final)
	}
}

func TestRemovedEventStaysUntilAllWindowMappingsAreGone(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	calendar := domain.Calendar{ID: "c1", Name: "Main", Enabled: true}
	if err = db.UpsertCalendar(ctx, calendar); err != nil {
		t.Fatal(err)
	}
	event := domain.CalendarEvent{ID: "e1", CalendarID: "c1", Subject: "long", StartUTC: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC), EndUTC: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), RawJSON: []byte(`{}`)}
	if err = db.UpsertCalendarEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	w1 := domain.CalendarSyncWindow{CalendarID: "c1", StartUTC: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC), EndUTC: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}
	w2 := domain.CalendarSyncWindow{CalendarID: "c1", StartUTC: w1.EndUTC, EndUTC: event.EndUTC}
	for _, w := range []domain.CalendarSyncWindow{w1, w2} {
		if err = db.EnsureCalendarWindow(ctx, w); err != nil {
			t.Fatal(err)
		}
		if err = db.LinkCalendarWindowEvent(ctx, w, "e1"); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.RemoveCalendarWindowEvent(ctx, w1, "e1", time.Now()); err != nil {
		t.Fatal(err)
	}
	values, err := db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{})
	if err != nil || len(values) != 1 {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	if err = db.RemoveCalendarWindowEvent(ctx, w2, "e1", time.Now()); err != nil {
		t.Fatal(err)
	}
	values, err = db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{})
	if err != nil || len(values) != 0 {
		t.Fatalf("values=%+v err=%v", values, err)
	}
}

func TestCalendarDeltaDaemonRepeatsUntilCancellation(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/calendar.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Sync.Interval = time.Millisecond
	cfg.Calendar.Calendars = []config.CalendarSelection{{ID: "primary"}}
	cfg.Calendar.Range.PastDays = 1
	cfg.Calendar.Range.FutureDays = 1
	g := &fakeGraph{calendar: json.RawMessage(`{"id":"c1","name":"Main","isDefaultCalendar":true}`), calls: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for range 4 {
			<-g.calls
		}
		cancel()
	}()
	err = (&Service{Graph: g, Store: db, Config: cfg}).DeltaDaemon(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(g.paths) < 4 {
		t.Fatalf("daemon did not repeat: %v", g.paths)
	}
}
