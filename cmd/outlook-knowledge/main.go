package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	mailservice "github.com/obr-grp/teams-knowledge-sync/internal/mail"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: outlook-knowledge [-config config.yaml] mail <auth|address|folder|sync|search|show|thread|status> ...")
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
	if args[0] != "mail" {
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
	a := auth.NewWithScopes(cfg.Entra.TenantID, cfg.Entra.ClientID, "User.Read", "Mail.Read")
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	s := mailservice.Service{Graph: g, Store: db, Config: cfg}
	ctx := context.Background()
	mailCmd(ctx, s, db, a, args[1:])
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
		log.Fatal("mail auth login|status|logout")
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
		log.Fatal("mail auth login|status|logout")
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
