package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	calendarservice "github.com/obr-grp/teams-knowledge-sync/internal/calendar"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	mailservice "github.com/obr-grp/teams-knowledge-sync/internal/mail"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: outlook-knowledge [-config config.yaml] <mail|calendar> <command> ...")
}

func main() {
	fs := flag.NewFlagSet("outlook-knowledge", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.yaml", "config path")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		usage()
		return
	}
	if args[0] != "mail" && args[0] != "calendar" {
		usage()
		os.Exit(2)
	}
	if *cfgPath == "config.yaml" {
		if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
			if exe, e := os.Executable(); e == nil {
				candidate := filepath.Join(filepath.Dir(exe), "config.yaml")
				if _, e = os.Stat(candidate); e == nil {
					*cfgPath = candidate
				}
			}
		}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	scope := "Mail.Read"
	if args[0] == "calendar" {
		scope = "Calendars.Read"
	}
	a := auth.NewWithScopes(cfg.Entra.TenantID, cfg.Entra.ClientID, "User.Read", scope)
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	ctx := context.Background()
	if args[0] == "mail" {
		mailCmd(ctx, mailservice.Service{Graph: g, Store: db, Config: cfg}, db, a, args[1:])
		return
	}
	calendarCmd(ctx, calendarservice.Service{Graph: g, Store: db, Config: cfg}, db, a, cfg, args[1:])
}

func calendarCmd(ctx context.Context, s calendarservice.Service, db *store.Store, a *auth.Manager, cfg config.Config, args []string) {
	if len(args) == 0 {
		usage()
		return
	}
	switch args[0] {
	case "auth":
		authCmd(ctx, a, args[1:])
	case "list":
		values, err := s.DiscoverCalendars(ctx)
		must(err)
		for _, c := range values {
			fmt.Printf("%s\t%s\tdefault=%t\tenabled=%t\n", c.ID, c.Name, c.Default, c.Enabled)
		}
	case "show":
		if len(args) < 2 {
			log.Fatal("calendar show CALENDAR_ID")
		}
		c, err := db.Calendar(ctx, args[1])
		must(err)
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Println(string(b))
	case "sync":
		calendarSyncCmd(ctx, s, cfg, args[1:])
	case "search":
		calendarSearchCmd(ctx, db, cfg, args[1:])
	case "day":
		calendarDayCmd(ctx, db, cfg, args[1:])
	case "range":
		calendarRangeCmd(ctx, db, cfg, args[1:])
	case "show-event":
		if len(args) < 2 {
			log.Fatal("calendar show-event EVENT_ID")
		}
		e, err := db.CalendarEvent(ctx, args[1])
		must(err)
		printEvent(e, mustLocation(cfg.Calendar.DisplayTimezone))
	case "status":
		values, err := db.CalendarStats(ctx)
		must(err)
		if len(args) > 1 && args[1] == "--json" {
			b, _ := json.MarshalIndent(values, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Printf("calendars: %d\nevents: %d\n", values["calendars"], values["events"])
		}
	case "daemon":
		daemonCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := s.DeltaDaemon(daemonCtx); err != nil && !errors.Is(err, context.Canceled) {
			must(err)
		}
	default:
		usage()
	}
}
func calendarSyncCmd(ctx context.Context, s calendarservice.Service, cfg config.Config, args []string) {
	f := flag.NewFlagSet("calendar sync", flag.ExitOnError)
	id := f.String("calendar", "", "calendar ID or primary")
	from := f.String("from", "", "date or RFC3339")
	to := f.String("to", "", "date or RFC3339")
	_ = f.Parse(args)
	loc := mustLocation(cfg.Calendar.DisplayTimezone)
	fromTime := parseCalendarTime(*from, loc)
	toTime := parseCalendarTime(*to, loc)
	must(s.Sync(ctx, *id, fromTime, toTime))
}
func calendarSearchCmd(ctx context.Context, db *store.Store, cfg config.Config, args []string) {
	f := flag.NewFlagSet("calendar search", flag.ExitOnError)
	id := f.String("calendar", "", "calendar ID")
	from := f.String("from", "", "date or RFC3339")
	to := f.String("to", "", "date or RFC3339")
	limit := f.Int("limit", 50, "limit")
	_ = f.Parse(args)
	loc := mustLocation(cfg.Calendar.DisplayTimezone)
	filter := domain.CalendarSearchFilter{Query: strings.Join(f.Args(), " "), CalendarID: *id, From: parseCalendarTime(*from, loc), To: parseCalendarTime(*to, loc), Limit: *limit}
	values, err := db.SearchCalendarEvents(ctx, filter)
	must(err)
	for _, e := range values {
		printEvent(e.CalendarEvent, loc)
	}
}
func calendarDayCmd(ctx context.Context, db *store.Store, cfg config.Config, args []string) {
	if len(args) < 1 {
		log.Fatal("calendar day YYYY-MM-DD")
	}
	loc := mustLocation(cfg.Calendar.DisplayTimezone)
	from := mustDate(args[0], loc)
	to := from.AddDate(0, 0, 1)
	values, err := db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{From: &from, To: &to, Limit: 500})
	must(err)
	for _, e := range values {
		printEvent(e.CalendarEvent, loc)
	}
}
func calendarRangeCmd(ctx context.Context, db *store.Store, cfg config.Config, args []string) {
	if len(args) < 2 {
		log.Fatal("calendar range FROM TO")
	}
	loc := mustLocation(cfg.Calendar.DisplayTimezone)
	from := mustDate(args[0], loc)
	to := mustDate(args[1], loc)
	values, err := db.SearchCalendarEvents(ctx, domain.CalendarSearchFilter{From: &from, To: &to, Limit: 500})
	must(err)
	for _, e := range values {
		printEvent(e.CalendarEvent, loc)
	}
}
func printEvent(e domain.CalendarEvent, loc *time.Location) {
	fmt.Printf("%s\t%s - %s\t%s\t%s\n", e.ID, e.StartUTC.In(loc).Format(time.RFC3339), e.EndUTC.In(loc).Format(time.RFC3339), e.Subject, e.WebURL)
}
func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Fatal(err)
	}
	return loc
}
func parseCalendarTime(raw string, loc *time.Location) *time.Time {
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	t := mustDate(raw, loc)
	return &t
}
func mustDate(raw string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		log.Fatal(err)
	}
	return t
}

func mailCmd(ctx context.Context, s mailservice.Service, db *store.Store, a *auth.Manager, args []string) {
	if len(args) == 0 {
		usage()
		return
	}
	switch args[0] {
	case "auth":
		authCmd(ctx, a, args[1:])
	case "address":
		if len(args) < 2 || args[1] != "list" {
			log.Fatal("mail address list")
		}
		must(s.RegisterAddresses(ctx))
		values, err := db.MailAddresses(ctx)
		must(err)
		for _, v := range values {
			fmt.Printf("%s\t%s\n", v.Address, v.Name)
		}
	case "folder":
		if len(args) < 2 || args[1] != "list" {
			log.Fatal("mail folder list")
		}
		values, err := s.DiscoverFolders(ctx)
		must(err)
		for _, v := range values {
			fmt.Printf("%s\t%s\t%t\t%d\n", v.ID, v.DisplayName, v.Enabled, v.TotalCount)
		}
	case "sync":
		syncCmd(ctx, s, args[1:])
	case "search":
		searchCmd(ctx, db, args[1:])
	case "show":
		if len(args) < 2 {
			log.Fatal("mail show MESSAGE_ID")
		}
		m, err := db.MailMessage(ctx, args[1])
		must(err)
		printMessage(m)
	case "thread":
		if len(args) < 2 {
			log.Fatal("mail thread MESSAGE_ID")
		}
		values, err := db.MailThread(ctx, args[1])
		must(err)
		for _, m := range values {
			printMessage(m)
		}
	case "status":
		statusCmd(ctx, db, args[1:])
	default:
		usage()
	}
}

func authCmd(ctx context.Context, a *auth.Manager, args []string) {
	if len(args) == 0 {
		log.Fatal("auth login|status|logout")
	}
	switch args[0] {
	case "login":
		must(a.Login(ctx, func(v string) { fmt.Println(v) }))
		fmt.Println("authenticated")
	case "status":
		if _, err := a.AccessToken(ctx); err != nil {
			fmt.Println("not authenticated")
			return
		}
		fmt.Println("authenticated")
	case "logout":
		must(a.Logout())
	default:
		log.Fatal("auth login|status|logout")
	}
}
func syncCmd(ctx context.Context, s mailservice.Service, args []string) {
	f := flag.NewFlagSet("mail sync", flag.ExitOnError)
	folder := f.String("folder", "", "folder ID or well-known name")
	_ = f.Parse(args)
	must(s.Sync(ctx, *folder))
}
func searchCmd(ctx context.Context, db *store.Store, args []string) {
	f := flag.NewFlagSet("mail search", flag.ExitOnError)
	address := f.String("address", "", "registered address")
	sender := f.String("sender", "", "sender address")
	folder := f.String("folder", "", "folder ID")
	limit := f.Int("limit", 20, "limit")
	from := f.String("from", "", "RFC3339 timestamp")
	to := f.String("to", "", "RFC3339 timestamp")
	_ = f.Parse(args)
	filter := domain.MailSearchFilter{Query: strings.Join(f.Args(), " "), Address: mailservice.NormalizeAddress(*address), Sender: *sender, FolderID: *folder, Limit: *limit}
	filter.From = parseTime(*from)
	filter.To = parseTime(*to)
	values, err := db.SearchMail(ctx, filter)
	must(err)
	for _, v := range values {
		at := v.ReceivedAt
		if at == nil {
			at = v.SentAt
		}
		stamp := ""
		if at != nil {
			stamp = at.Format(time.RFC3339)
		}
		fmt.Printf("%s\t%s\t%s <%s>\t%s\t%s\n", stamp, v.Subject, v.FromName, v.FromAddress, v.Snippet, v.WebURL)
	}
}
func statusCmd(ctx context.Context, db *store.Store, args []string) {
	values, err := db.MailStats(ctx)
	must(err)
	if len(args) > 0 && args[0] == "--json" {
		b, _ := json.MarshalIndent(values, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("messages: %d\nfolders: %d\naddresses: %d\n", values["messages"], values["folders"], values["addresses"])
}
func printMessage(m domain.MailMessage) {
	at := m.ReceivedAt
	if at == nil {
		at = m.SentAt
	}
	stamp := ""
	if at != nil {
		stamp = at.Format(time.RFC3339)
	}
	var recipients, attachments, matches []string
	for _, r := range m.Recipients {
		recipients = append(recipients, fmt.Sprintf("%s:%s", r.Type, r.Address))
	}
	for _, a := range m.Attachments {
		attachments = append(attachments, a.Name)
	}
	for _, match := range m.Matches {
		matches = append(matches, match.Address+":"+match.MatchedBy)
	}
	fmt.Printf("id: %s\ndate: %s\nsubject: %s\nfrom: %s <%s>\nrecipients: %s\nmatched: %s\nattachments: %s\nbody: %s\nurl: %s\n\n", m.ID, stamp, m.Subject, m.FromName, m.FromAddress, strings.Join(recipients, ", "), strings.Join(matches, ", "), strings.Join(attachments, ", "), m.BodyText, m.WebURL)
}
func parseTime(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		log.Fatal(err)
	}
	return &t
}
func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
