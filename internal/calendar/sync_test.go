package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

type fakeGraph struct {
	pages    map[string]graph.PageResult
	objects  map[string]string
	errs     map[string]error
	requests []string
}

func (f *fakeGraph) Do(_ context.Context, _ string, path string, _ any, out any) error {
	f.requests = append(f.requests, path)
	if e, ok := f.errs[path]; ok {
		return e
	}
	body, ok := f.objects[path]
	if !ok {
		return fmt.Errorf("unexpected Do %s", path)
	}
	return json.Unmarshal([]byte(body), out)
}
func (f *fakeGraph) GetPage(_ context.Context, pageURL string, _ map[string]string) (graph.PageResult, error) {
	f.requests = append(f.requests, pageURL)
	if e, ok := f.errs[pageURL]; ok {
		return graph.PageResult{}, e
	}
	p, ok := f.pages[pageURL]
	if !ok {
		return graph.PageResult{}, fmt.Errorf("unexpected page %s", pageURL)
	}
	return p, nil
}

func testStore(t *testing.T) *outlookstore.Store {
	t.Helper()
	s, err := outlookstore.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func eventJSON(id, typ, subject, start, end string) json.RawMessage {
	master := ""
	if typ == "occurrence" || typ == "exception" {
		master = `"seriesMasterId":"sm1",`
	}
	return json.RawMessage(`{"id":"` + id + `","type":"` + typ + `",` + master + `"subject":"` + subject + `",
		"start":{"dateTime":"` + start + `","timeZone":"UTC"},
		"end":{"dateTime":"` + end + `","timeZone":"UTC"},
		"body":{"contentType":"html","content":"<p>x</p>"}}`)
}

func service(g *fakeGraph, db *outlookstore.Store) *Service {
	if err := db.UpsertCalendar(context.Background(), domain.Calendar{ID: "cal1", Name: "primary", IsDefault: true, Enabled: true}); err != nil {
		panic(err)
	}
	return &Service{Graph: g, Store: db,
		Selections: []Selection{{ID: "primary", Enabled: true}},
		PastDays:   30, FutureDays: 30,
		Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }}
}

func viewURL(calID, from, to string) string {
	return "me/calendars/" + calID + "/calendarView?startDateTime=" + strings.ReplaceAll(from, ":", "%3A") + "&endDateTime=" + strings.ReplaceAll(to, ":", "%3A") + "&$top=50&$select=" + eventSelect
}
func deltaURL(_ string, from, to string) string {
	return "me/calendarView/delta?startDateTime=" + strings.ReplaceAll(from, ":", "%3A") + "&endDateTime=" + strings.ReplaceAll(to, ":", "%3A")
}
func betaDeltaURL(calID, from, to string) string {
	return "https://graph.microsoft.com/beta/me/calendars/" + calID + "/calendarView/delta?startDateTime=" + strings.ReplaceAll(from, ":", "%3A") + "&endDateTime=" + strings.ReplaceAll(to, ":", "%3A")
}

// with Now=2026-07-10 and past/future 30 days, the horizon splits into
// [Apr1,Jul1), [Jul1,Aug1), [Aug1,Sep1)
var testWindows = []struct{ from, to string }{
	{"2026-04-01T00:00:00Z", "2026-07-01T00:00:00Z"},
	{"2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z"},
	{"2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z"},
}

func emptyWindowPages(g *fakeGraph, calID string) {
	for i, w := range testWindows {
		g.pages[deltaURL(calID, w.from, w.to)] = graph.PageResult{DeltaLink: fmt.Sprintf("https://graph.microsoft.com/v1.0/delta-%s-%d", calID, i)}
	}
}

func TestSyncAllSelectsDefaultCalendarAndPagesWindows(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{
		pages: map[string]graph.PageResult{
			"me/calendars?$top=50": {Value: []json.RawMessage{
				json.RawMessage(`{"id":"cal1","name":"予定表","isDefaultCalendar":true}`),
				json.RawMessage(`{"id":"cal2","name":"other","isDefaultCalendar":false}`),
			}},
		},
		objects: map[string]string{},
	}
	emptyWindowPages(g, "cal1")
	// window 2 pages twice before committing its delta link
	g.pages[deltaURL("cal1", testWindows[1].from, testWindows[1].to)] = graph.PageResult{Value: []json.RawMessage{eventJSON("e1", "singleInstance", "one", "2026-07-10T01:00:00", "2026-07-10T02:00:00")}, NextLink: "https://graph.microsoft.com/v1.0/next2"}
	g.pages["https://graph.microsoft.com/v1.0/next2"] = graph.PageResult{Value: []json.RawMessage{eventJSON("e2", "singleInstance", "two", "2026-07-11T01:00:00", "2026-07-11T02:00:00")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-w2"}
	s := service(g, db)
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ := db.CalendarStats(context.Background())
	if stats["calendars"] != 2 || stats["events"] != 2 {
		t.Fatalf("stats=%v", stats)
	}
	for _, r := range g.requests {
		if strings.Contains(r, "cal2/calendarView") {
			t.Fatalf("disabled calendar synced: %s", r)
		}
	}
	states, _ := db.ListCalendarWindowStates(context.Background())
	if len(states) != 3 {
		t.Fatalf("states=%+v", states)
	}
	for _, st := range states {
		if st.DeltaLink == "" || st.NextLink != "" || st.LastSuccessAt == nil {
			t.Fatalf("window state=%+v", st)
		}
	}
	// rerun continues from each window's delta link, no duplicates
	g.pages["https://graph.microsoft.com/v1.0/delta-cal1-0"] = graph.PageResult{DeltaLink: "https://graph.microsoft.com/v1.0/delta-cal1-0"}
	g.pages["https://graph.microsoft.com/v1.0/delta-cal1-2"] = graph.PageResult{DeltaLink: "https://graph.microsoft.com/v1.0/delta-cal1-2"}
	g.pages["https://graph.microsoft.com/v1.0/delta-w2"] = graph.PageResult{Value: []json.RawMessage{eventJSON("e1", "singleInstance", "one", "2026-07-10T01:00:00", "2026-07-10T02:00:00")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-w2"}
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ = db.CalendarStats(context.Background())
	if stats["events"] != 2 {
		t.Fatalf("after rerun stats=%v", stats)
	}
}

func TestWindowRemovedInRangeVsOutOfRange(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}}
	s := service(g, db)
	julWindow := Window{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	// store one event inside the window and one outside it
	for _, ev := range []json.RawMessage{
		eventJSON("in", "singleInstance", "in-range", "2026-07-10T01:00:00", "2026-07-10T02:00:00"),
		eventJSON("out", "singleInstance", "out-of-range", "2026-09-10T01:00:00", "2026-09-10T02:00:00"),
	} {
		if err := s.storeEvent(context.Background(), ev, "cal1", map[string]bool{}); err != nil {
			t.Fatal(err)
		}
	}
	g.pages[deltaURL("cal1", "2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z")] = graph.PageResult{Value: []json.RawMessage{
		json.RawMessage(`{"id":"in","@removed":{"reason":"deleted"}}`),
		json.RawMessage(`{"id":"out","@removed":{"reason":"deleted"}}`),
		json.RawMessage(`{"id":"unknown","@removed":{"reason":"deleted"}}`),
	}, DeltaLink: "https://graph.microsoft.com/v1.0/d1"}
	if err := s.SyncWindow(context.Background(), "cal1", julWindow); err != nil {
		t.Fatal(err)
	}
	inEv, err := db.GetCalendarEvent(context.Background(), "in")
	if err != nil || inEv.DeletedAt == nil {
		t.Fatalf("in-range event not tombstoned: %+v err=%v", inEv, err)
	}
	outEv, err := db.GetCalendarEvent(context.Background(), "out")
	if err != nil || outEv.DeletedAt != nil {
		t.Fatalf("out-of-range event touched: %+v err=%v", outEv, err)
	}
}

func TestWindowSyncStateInvalidResetsWindowOnly(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}, errs: map[string]error{}}
	s := service(g, db)
	w := Window{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	init := deltaURL("cal1", "2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z")
	g.pages[init] = graph.PageResult{DeltaLink: "https://graph.microsoft.com/v1.0/d1"}
	if err := s.SyncWindow(context.Background(), "cal1", w); err != nil {
		t.Fatal(err)
	}
	g.errs["https://graph.microsoft.com/v1.0/d1"] = &graph.Error{Status: 410, Code: "SyncStateNotFound"}
	g.pages[init] = graph.PageResult{DeltaLink: "https://graph.microsoft.com/v1.0/d2"}
	if err := s.SyncWindow(context.Background(), "cal1", w); err != nil {
		t.Fatal(err)
	}
	st, _ := db.GetCalendarWindowState(context.Background(), "cal1", w.Start, w.End)
	if st.DeltaLink != "https://graph.microsoft.com/v1.0/d2" {
		t.Fatalf("state=%+v", st)
	}
}

func TestWindowFailureDoesNotStopOthers(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}, errs: map[string]error{}}
	s := service(g, db)
	emptyWindowPages(g, "cal1")
	failing := deltaURL("cal1", testWindows[0].from, testWindows[0].to)
	delete(g.pages, failing)
	g.errs[failing] = &graph.Error{Status: 403, Code: "ErrorAccessDenied"}
	if err := s.SyncWindows(context.Background(), "cal1"); err != nil {
		t.Fatal(err)
	}
	states, _ := db.ListCalendarWindowStates(context.Background())
	if len(states) != 3 {
		t.Fatalf("states=%+v", states)
	}
	var failed, ok int
	for _, st := range states {
		if st.ConsecutiveFailures > 0 {
			failed++
		}
		if st.DeltaLink != "" {
			ok++
		}
	}
	if failed != 1 || ok != 2 {
		t.Fatalf("failed=%d ok=%d states=%+v", failed, ok, states)
	}
}

func TestSyncWindowUsesBetaForNonDefaultCalendar(t *testing.T) {
	db := testStore(t)
	if err := db.UpsertCalendar(context.Background(), domain.Calendar{ID: "cal2", Name: "other", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}}
	w := Window{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	g.pages[betaDeltaURL("cal2", "2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z")] = graph.PageResult{DeltaLink: "https://graph.microsoft.com/beta/delta-cal2"}
	s := service(g, db)
	if err := s.SyncWindow(context.Background(), "cal2", w); err != nil {
		t.Fatal(err)
	}
	if len(g.requests) != 1 || !strings.HasPrefix(g.requests[0], "https://graph.microsoft.com/beta/me/calendars/cal2/calendarView/delta?") {
		t.Fatalf("requests=%v", g.requests)
	}
}

func TestSyncRangeFetchesSeriesMasterOnce(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{pages: map[string]graph.PageResult{}, objects: map[string]string{
		"me/events/sm1?$select=" + eventSelect: `{"id":"sm1","type":"seriesMaster","subject":"定例",
			"start":{"dateTime":"2026-07-01T01:00:00","timeZone":"UTC"},
			"end":{"dateTime":"2026-07-01T02:00:00","timeZone":"UTC"}}`,
	}}
	s := service(g, db)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first := viewURL("cal1", "2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z")
	g.pages[first] = graph.PageResult{Value: []json.RawMessage{
		eventJSON("o1", "occurrence", "定例", "2026-07-01T01:00:00", "2026-07-01T02:00:00"),
		eventJSON("o2", "occurrence", "定例", "2026-07-08T01:00:00", "2026-07-08T02:00:00"),
	}}
	if err := s.SyncRange(context.Background(), "cal1", from, to); err != nil {
		t.Fatal(err)
	}
	fetches := 0
	for _, r := range g.requests {
		if strings.HasPrefix(r, "me/events/sm1") {
			fetches++
		}
	}
	if fetches != 1 {
		t.Fatalf("series master fetched %d times", fetches)
	}
	master, err := db.GetCalendarEvent(context.Background(), "sm1")
	if err != nil || master.EventType != "seriesMaster" {
		t.Fatalf("master=%+v err=%v", master, err)
	}
}
