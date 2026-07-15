package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"github.com/obr-grp/teams-knowledge-sync/internal/calendar"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

func calendarUsage() {
	log.Fatal("calendar list|show ID|sync [--calendar ID] [--from YYYY-MM-DD --to YYYY-MM-DD] [--full]|day YYYY-MM-DD|range FROM TO|search QUERY|status [--json]")
}
func calendarService(db *outlookstore.Store, a *auth.Manager, cfg config.Config) *calendar.Service {
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	var sels []calendar.Selection
	for _, c := range cfg.Calendar.Calendars {
		sels = append(sels, calendar.Selection{ID: c.ID, Enabled: c.Enabled})
	}
	return &calendar.Service{Graph: g, Store: db, Selections: sels, Private: calendar.PrivatePolicy{StoreDetails: cfg.Calendar.PrivateEvents.StoreDetails}, PastDays: cfg.Calendar.Range.PastDays, FutureDays: cfg.Calendar.Range.FutureDays, RecentMonths: cfg.Calendar.SyncWindows.RecentMonthsPerWindow, HistMonths: cfg.Calendar.SyncWindows.HistoricalMonthsPerWindow, FutureMonths: cfg.Calendar.SyncWindows.FutureMonthsPerWindow}
}
func displayLocation(cfg config.Config) *time.Location {
	loc, err := time.LoadLocation(cfg.Calendar.DisplayTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}
func calendarCmd(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config, args []string) {
	if len(args) == 0 {
		calendarUsage()
	}
	loc := displayLocation(cfg)
	switch args[0] {
	case "list":
		calendars, err := db.ListCalendars(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, c := range calendars {
			def := ""
			if c.IsDefault {
				def = "default"
			}
			fmt.Printf("%s\t%s\t%s\tenabled=%v\t%s\n", c.ID, c.Name, def, c.Enabled, c.Owner)
		}
	case "show":
		if len(args) < 2 {
			log.Fatal("calendar show CALENDAR_ID|EVENT_ID")
		}
		calendarShow(ctx, db, args[1], loc)
	case "sync":
		calendarSync(ctx, db, a, cfg, args[1:], loc)
	case "day":
		if len(args) < 2 {
			log.Fatal("calendar day YYYY-MM-DD")
		}
		day, err := time.ParseInLocation("2006-01-02", args[1], loc)
		if err != nil {
			log.Fatal(err)
		}
		calendarPrintRange(ctx, db, day, day.AddDate(0, 0, 1), loc)
	case "range":
		if len(args) < 3 {
			log.Fatal("calendar range FROM TO")
		}
		from, err := time.ParseInLocation("2006-01-02", args[1], loc)
		if err != nil {
			log.Fatal(err)
		}
		to, err := time.ParseInLocation("2006-01-02", args[2], loc)
		if err != nil {
			log.Fatal(err)
		}
		calendarPrintRange(ctx, db, from, to.AddDate(0, 0, 1), loc)
	case "search":
		calendarSearch(ctx, db, args[1:], loc)
	case "status":
		calendarStatus(ctx, db, args[1:])
	default:
		calendarUsage()
	}
}
func calendarSync(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config, args []string, loc *time.Location) {
	f := flag.NewFlagSet("calendar sync", flag.ExitOnError)
	calID := f.String("calendar", "", "sync a single calendar ID")
	fromArg := f.String("from", "", "range start YYYY-MM-DD")
	toArg := f.String("to", "", "range end YYYY-MM-DD (inclusive)")
	full := f.Bool("full", false, "reset state and resync")
	_ = f.Parse(args)
	s := calendarService(db, a, cfg)
	if *fromArg != "" || *toArg != "" {
		if *fromArg == "" || *toArg == "" {
			log.Fatal("--from and --to must be used together")
		}
		from, err := time.ParseInLocation("2006-01-02", *fromArg, loc)
		if err != nil {
			log.Fatal(err)
		}
		to, err := time.ParseInLocation("2006-01-02", *toArg, loc)
		if err != nil {
			log.Fatal(err)
		}
		target := *calID
		if target == "" {
			calendars, err := s.SyncCalendars(ctx)
			if err != nil {
				log.Fatal(err)
			}
			for _, c := range calendars {
				if err := s.SyncRange(ctx, c.ID, from.UTC(), to.AddDate(0, 0, 1).UTC()); err != nil {
					log.Fatal(err)
				}
			}
		} else if err := s.SyncRange(ctx, target, from.UTC(), to.AddDate(0, 0, 1).UTC()); err != nil {
			log.Fatal(err)
		}
		fmt.Println("calendar sync completed")
		return
	}
	if *calID != "" {
		if *full {
			if err := db.ResetCalendarWindows(ctx, *calID); err != nil {
				log.Fatal(err)
			}
		}
		if err := s.SyncWindows(ctx, *calID); err != nil {
			log.Fatal(err)
		}
		fmt.Println("calendar sync completed")
		return
	}
	if *full {
		calendars, err := s.SyncCalendars(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, c := range calendars {
			if err := db.ResetCalendarWindows(ctx, c.ID); err != nil {
				log.Fatal(err)
			}
		}
	}
	if err := s.SyncAll(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("calendar sync completed")
}
func calendarShow(ctx context.Context, db *outlookstore.Store, id string, loc *time.Location) {
	if c, err := db.GetCalendar(ctx, id); err == nil {
		def := "no"
		if c.IsDefault {
			def = "yes"
		}
		fmt.Printf("calendar: %s\nname: %s\nowner: %s\ndefault: %s\nenabled: %v\n", c.ID, c.Name, c.Owner, def, c.Enabled)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Fatal(err)
	}
	ev, err := db.GetCalendarEvent(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("event: %s\nsubject: %s\ntype: %s\nwhen: %s\norganizer: %s <%s>\nlocation: %s\nsensitivity: %s\n", ev.ID, ev.Subject, ev.EventType, eventWhen(ev, loc), ev.OrganizerName, ev.OrganizerAddress, ev.LocationName, ev.Sensitivity)
	if ev.IsCancelled {
		fmt.Println("cancelled: yes")
	}
	if len(ev.Attendees) > 0 {
		var names []string
		for _, a := range ev.Attendees {
			names = append(names, fmt.Sprintf("%s (%s)", a.Name, a.Response))
		}
		fmt.Printf("attendees: %s\n", strings.Join(names, ", "))
	}
	if ev.JoinURL != "" {
		fmt.Printf("join: %s\n", ev.JoinURL)
	}
	fmt.Printf("url: %s\n", ev.WebURL)
	if ev.BodyText != "" {
		fmt.Printf("\n%s\n", ev.BodyText)
	}
}
func calendarPrintRange(ctx context.Context, db *outlookstore.Store, from, to time.Time, loc *time.Location) {
	events, err := db.EventsBetween(ctx, from.UTC(), to.UTC(), "")
	if err != nil {
		log.Fatal(err)
	}
	for _, ev := range events {
		teams := ""
		if ev.JoinURL != "" {
			teams = "\t[teams]"
		}
		fmt.Printf("%s\t%s\t%s%s\n", eventWhen(ev, loc), ev.Subject, ev.LocationName, teams)
	}
}
func calendarSearch(ctx context.Context, db *outlookstore.Store, args []string, loc *time.Location) {
	f := flag.NewFlagSet("calendar search", flag.ExitOnError)
	from := f.String("from", "", "RFC3339 lower bound")
	to := f.String("to", "", "RFC3339 upper bound (exclusive)")
	calID := f.String("calendar", "", "calendar ID filter")
	limit := f.Int("limit", 20, "limit")
	_ = f.Parse(args)
	var sf domain.EventSearchFilter
	sf.Query = strings.Join(f.Args(), " ")
	sf.CalendarID = *calID
	sf.Limit = *limit
	if *from != "" {
		t, e := time.Parse(time.RFC3339, *from)
		if e != nil {
			log.Fatal(e)
		}
		sf.From = &t
	}
	if *to != "" {
		t, e := time.Parse(time.RFC3339, *to)
		if e != nil {
			log.Fatal(e)
		}
		sf.To = &t
	}
	events, err := db.SearchEvents(ctx, sf)
	if err != nil {
		log.Fatal(err)
	}
	for _, ev := range events {
		fmt.Printf("%s\t%s\t%s\t%s\n", eventWhen(ev, loc), ev.Subject, ev.OrganizerName, ev.LocationName)
	}
}
func calendarStatus(ctx context.Context, db *outlookstore.Store, args []string) {
	jsonOut := len(args) > 0 && args[0] == "--json"
	v, err := db.CalendarStats(ctx)
	if err != nil {
		log.Fatal(err)
	}
	windows, err := db.ListCalendarWindowStates(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if jsonOut {
		v["sync_windows"] = windows
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("calendars: %v\nevents: %v\ncancelled: %v\ndeleted: %v\n", v["calendars"], v["events"], v["cancelled_events"], v["deleted_events"])
	for _, st := range windows {
		success := "-"
		if st.LastSuccessAt != nil {
			success = st.LastSuccessAt.Format(time.RFC3339)
		}
		delta := "no"
		if st.DeltaLink != "" {
			delta = "yes"
		}
		fmt.Printf("%s\t[%s,%s)\tlast_success=%s\tdelta=%s\tfailures=%d\t%s\n", st.CalendarID, st.WindowStart.Format("2006-01-02"), st.WindowEnd.Format("2006-01-02"), success, delta, st.ConsecutiveFailures, st.LastError)
	}
}

// eventWhen renders an event's time in the display timezone. All-day events
// are shown as dates in their original timezone (falling back to the display
// timezone) so the UTC-normalized boundaries do not shift the date.
func eventWhen(ev domain.CalendarEvent, loc *time.Location) string {
	if ev.IsAllDay {
		dayLoc := loc
		if ev.StartTimezone != "" {
			if l, err := time.LoadLocation(ev.StartTimezone); err == nil {
				dayLoc = l
			}
		}
		start := ev.StartUTC.In(dayLoc)
		end := ev.EndUTC.In(dayLoc).AddDate(0, 0, -1)
		if end.After(start) {
			return start.Format("2006-01-02") + "〜" + end.Format("2006-01-02") + " (終日)"
		}
		return start.Format("2006-01-02") + " (終日)"
	}
	start := ev.StartUTC.In(loc)
	end := ev.EndUTC.In(loc)
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return start.Format("2006-01-02 15:04") + "–" + end.Format("15:04")
	}
	return start.Format("2006-01-02 15:04") + "–" + end.Format("2006-01-02 15:04")
}
