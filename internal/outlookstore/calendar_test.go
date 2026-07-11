package outlookstore

import (
	"context"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
)

func sampleEvent(id string, start, end time.Time) domain.CalendarEvent {
	return domain.CalendarEvent{
		ID: id, CalendarID: "cal1", EventType: "singleInstance", Subject: "定例会議",
		BodyText: "議事録", StartUTC: start, EndUTC: end,
		StartTimezone: "Asia/Tokyo", EndTimezone: "Asia/Tokyo",
		OrganizerName: "Boss", LocationName: "会議室A", RawJSON: []byte(`{}`),
		Attendees: []domain.Attendee{{Type: "required", Address: "a@example.com", Name: "A", Response: "accepted"}},
	}
}

func TestUpsertCalendarEventUniquePerCalendar(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	ev := sampleEvent("e1", start, start.Add(time.Hour))
	if err := db.UpsertCalendarEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	ev.Subject = "更新後"
	ev.Attendees = append(ev.Attendees, domain.Attendee{Type: "optional", Address: "b@example.com"})
	if err := db.UpsertCalendarEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	stats, _ := db.CalendarStats(ctx)
	if stats["events"] != 1 {
		t.Fatalf("stats=%v", stats)
	}
	got, err := db.GetCalendarEvent(ctx, "e1")
	if err != nil || got.Subject != "更新後" || len(got.Attendees) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestEventsBetweenHalfOpenBoundaries(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		id         string
		start, end time.Time
		want       bool
	}{
		{"inside", from.Add(2 * time.Hour), from.Add(3 * time.Hour), true},
		{"ends-at-from", from.Add(-time.Hour), from, false},
		{"starts-at-to", to, to.Add(time.Hour), false},
		{"spans", from.Add(-time.Hour), to.Add(time.Hour), true},
	}
	for _, c := range cases {
		if err := db.UpsertCalendarEvent(ctx, sampleEvent(c.id, c.start, c.end)); err != nil {
			t.Fatal(err)
		}
	}
	// cancelled and seriesMaster rows must be excluded
	cancelled := sampleEvent("cancelled", from.Add(4*time.Hour), from.Add(5*time.Hour))
	cancelled.IsCancelled = true
	master := sampleEvent("master", from.Add(6*time.Hour), from.Add(7*time.Hour))
	master.EventType = "seriesMaster"
	for _, ev := range []domain.CalendarEvent{cancelled, master} {
		if err := db.UpsertCalendarEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.EventsBetween(ctx, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ev := range events {
		got[ev.ID] = true
	}
	for _, c := range cases {
		if got[c.id] != c.want {
			t.Errorf("%s: included=%v want %v", c.id, got[c.id], c.want)
		}
	}
	if got["cancelled"] || got["master"] {
		t.Errorf("cancelled/master leaked: %v", got)
	}
}

func TestSearchEvents(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	if err := db.UpsertCalendarEvent(ctx, sampleEvent("e1", start, start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	other := sampleEvent("e2", start.Add(2*time.Hour), start.Add(3*time.Hour))
	other.Subject = "現場立会"
	if err := db.UpsertCalendarEvent(ctx, other); err != nil {
		t.Fatal(err)
	}
	events, err := db.SearchEvents(ctx, domain.EventSearchFilter{Query: "定例"})
	if err != nil || len(events) != 1 || events[0].ID != "e1" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	events, _ = db.SearchEvents(ctx, domain.EventSearchFilter{Query: "会議室"})
	if len(events) != 2 {
		t.Fatalf("location search events=%+v", events)
	}
}
