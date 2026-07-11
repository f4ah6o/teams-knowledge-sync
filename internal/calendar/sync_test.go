package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	return &Service{Graph: g, Store: db,
		Selections: []Selection{{ID: "primary", Enabled: true}},
		PastDays:   30, FutureDays: 30,
		Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }}
}

func viewURL(calID, from, to string) string {
	return "me/calendars/" + calID + "/calendarView?startDateTime=" + strings.ReplaceAll(from, ":", "%3A") + "&endDateTime=" + strings.ReplaceAll(to, ":", "%3A") + "&$top=50&$select=" + eventSelect
}

func TestSyncAllSelectsDefaultCalendarAndPages(t *testing.T) {
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
	first := viewURL("cal1", "2026-06-10T12:00:00Z", "2026-08-09T12:00:00Z")
	g.pages[first] = graph.PageResult{Value: []json.RawMessage{eventJSON("e1", "singleInstance", "one", "2026-07-10T01:00:00", "2026-07-10T02:00:00")}, NextLink: "https://graph.microsoft.com/v1.0/next2"}
	g.pages["https://graph.microsoft.com/v1.0/next2"] = graph.PageResult{Value: []json.RawMessage{eventJSON("e2", "singleInstance", "two", "2026-07-11T01:00:00", "2026-07-11T02:00:00")}}
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
	// rerun must not duplicate (UNIQUE(calendar_id,id))
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ = db.CalendarStats(context.Background())
	if stats["events"] != 2 {
		t.Fatalf("after rerun stats=%v", stats)
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
