package calendar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEventUTCConversionKeepsOriginalTimezone(t *testing.T) {
	raw := json.RawMessage(`{"id":"e1","type":"singleInstance","subject":"打合せ",
		"start":{"dateTime":"2026-07-10T00:30:00.0000000","timeZone":"UTC"},
		"end":{"dateTime":"2026-07-10T01:30:00.0000000","timeZone":"UTC"},
		"originalStartTimeZone":"Tokyo Standard Time","originalEndTimeZone":"Tokyo Standard Time",
		"body":{"contentType":"html","content":"<p>agenda</p>"}}`)
	ev, err := Event(raw, "cal1", PrivatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.StartUTC.Equal(time.Date(2026, 7, 10, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("start=%v", ev.StartUTC)
	}
	if ev.StartTimezone != "Tokyo Standard Time" || ev.EndTimezone != "Tokyo Standard Time" {
		t.Fatalf("tz=%q/%q", ev.StartTimezone, ev.EndTimezone)
	}
	if ev.BodyText != "agenda" {
		t.Fatalf("body=%q", ev.BodyText)
	}
}

func TestEventNonUTCPayloadNormalized(t *testing.T) {
	raw := json.RawMessage(`{"id":"e1",
		"start":{"dateTime":"2026-07-10T09:00:00","timeZone":"Asia/Tokyo"},
		"end":{"dateTime":"2026-07-10T10:00:00","timeZone":"Asia/Tokyo"}}`)
	ev, err := Event(raw, "cal1", PrivatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.StartUTC.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start=%v", ev.StartUTC)
	}
	if ev.EventType != "singleInstance" {
		t.Fatalf("type=%q", ev.EventType)
	}
}

func TestEventAllDayJST(t *testing.T) {
	// JST all-day 2026-07-10 arrives as 2026-07-09T15:00:00Z when UTC-normalized
	raw := json.RawMessage(`{"id":"e1","isAllDay":true,
		"start":{"dateTime":"2026-07-09T15:00:00.0000000","timeZone":"UTC"},
		"end":{"dateTime":"2026-07-10T15:00:00.0000000","timeZone":"UTC"},
		"originalStartTimeZone":"Asia/Tokyo"}`)
	ev, err := Event(raw, "cal1", PrivatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsAllDay {
		t.Fatal("all-day flag lost")
	}
	loc, _ := time.LoadLocation(ev.StartTimezone)
	if got := ev.StartUTC.In(loc).Format("2006-01-02"); got != "2026-07-10" {
		t.Fatalf("display date=%s", got)
	}
}

func TestEventTypes(t *testing.T) {
	for _, typ := range []string{"singleInstance", "occurrence", "exception", "seriesMaster"} {
		raw := json.RawMessage(`{"id":"e1","type":"` + typ + `","seriesMasterId":"sm1",
			"start":{"dateTime":"2026-07-10T00:00:00","timeZone":"UTC"},
			"end":{"dateTime":"2026-07-10T01:00:00","timeZone":"UTC"}}`)
		ev, err := Event(raw, "cal1", PrivatePolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if ev.EventType != typ || ev.SeriesMasterID != "sm1" {
			t.Fatalf("type=%q master=%q", ev.EventType, ev.SeriesMasterID)
		}
	}
}

func TestEventCancelled(t *testing.T) {
	raw := json.RawMessage(`{"id":"e1","isCancelled":true,
		"start":{"dateTime":"2026-07-10T00:00:00","timeZone":"UTC"},
		"end":{"dateTime":"2026-07-10T01:00:00","timeZone":"UTC"}}`)
	ev, err := Event(raw, "cal1", PrivatePolicy{})
	if err != nil || !ev.IsCancelled {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
}

func TestEventJoinURLPreference(t *testing.T) {
	body := `"body":{"contentType":"html","content":"<a href='https://teams.microsoft.com/l/meetup-join/19%3ameeting_x/0'>join</a>"}`
	times := `"start":{"dateTime":"2026-07-10T00:00:00","timeZone":"UTC"},"end":{"dateTime":"2026-07-10T01:00:00","timeZone":"UTC"}`
	cases := []struct {
		payload string
		want    string
	}{
		{`{"id":"e1","onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/graph"},"onlineMeetingUrl":"https://teams.microsoft.com/l/meetup-join/legacy",` + body + `,` + times + `}`, "https://teams.microsoft.com/l/meetup-join/graph"},
		{`{"id":"e1","onlineMeetingUrl":"https://teams.microsoft.com/l/meetup-join/legacy",` + body + `,` + times + `}`, "https://teams.microsoft.com/l/meetup-join/legacy"},
		{`{"id":"e1",` + body + `,` + times + `}`, "https://teams.microsoft.com/l/meetup-join/19%3ameeting_x/0"},
	}
	for i, c := range cases {
		ev, err := Event(json.RawMessage(c.payload), "cal1", PrivatePolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if ev.JoinURL != c.want {
			t.Fatalf("case %d: join=%q want %q", i, ev.JoinURL, c.want)
		}
	}
}

func TestEventPrivateMasked(t *testing.T) {
	raw := json.RawMessage(`{"id":"e1","subject":"人事面談","sensitivity":"private",
		"start":{"dateTime":"2026-07-10T00:00:00","timeZone":"UTC"},
		"end":{"dateTime":"2026-07-10T01:00:00","timeZone":"UTC"},
		"body":{"contentType":"text","content":"secret"},
		"attendees":[{"type":"required","emailAddress":{"address":"a@example.com"}}],
		"organizer":{"emailAddress":{"address":"boss@example.com","name":"Boss"}}}`)
	ev, err := Event(raw, "cal1", PrivatePolicy{StoreDetails: false})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Subject != maskedSubject || ev.BodyText != "" || len(ev.Attendees) != 0 {
		t.Fatalf("ev=%+v", ev)
	}
	if strings.Contains(string(ev.RawJSON), "secret") || strings.Contains(string(ev.RawJSON), "人事面談") {
		t.Fatalf("raw_json leaks details: %s", ev.RawJSON)
	}
	if ev.OrganizerAddress != "boss@example.com" || ev.StartUTC.IsZero() {
		t.Fatalf("times/organizer must be kept: %+v", ev)
	}
	ev, err = Event(raw, "cal1", PrivatePolicy{StoreDetails: true})
	if err != nil || ev.Subject != "人事面談" || ev.BodyText != "secret" {
		t.Fatalf("store_details ignored: %+v err=%v", ev, err)
	}
}
