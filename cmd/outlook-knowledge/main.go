package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/mail"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

var scopes = []string{"offline_access", "User.Read", "Mail.Read", "Calendars.Read"}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: outlook-knowledge [-config outlook-config.yaml] <auth|mail|daemon> ...")
}
func main() {
	fs := flag.NewFlagSet("outlook-knowledge", flag.ContinueOnError)
	cfgPath := fs.String("config", "outlook-config.yaml", "config path")
	_ = fs.Parse(os.Args[1:])
	if *cfgPath == "outlook-config.yaml" {
		if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
			if exe, e := os.Executable(); e == nil {
				candidate := filepath.Join(filepath.Dir(exe), "outlook-config.yaml")
				if _, e = os.Stat(candidate); e == nil {
					*cfgPath = candidate
				}
			}
		}
	}
	args := fs.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cfg, e := config.LoadOutlook(*cfgPath)
	if e != nil {
		log.Fatal(e)
	}
	db, e := outlookstore.Open(cfg.Database.Path)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	a := auth.NewFor(cfg.Entra.TenantID, cfg.Entra.ClientID, "outlook-knowledge", scopes)
	ctx := context.Background()
	switch args[0] {
	case "auth":
		authCmd(ctx, a, args[1:])
	case "mail":
		mailCmd(ctx, db, a, cfg, args[1:])
	case "daemon":
		daemonCmd(ctx, db, a, cfg)
	default:
		usage()
		os.Exit(2)
	}
}
func authCmd(ctx context.Context, a *auth.Manager, args []string) {
	if len(args) == 0 {
		log.Fatal("auth login|status|logout required")
	}
	switch args[0] {
	case "login":
		if e := a.Login(ctx, func(m string) { fmt.Println(m) }); e != nil {
			log.Fatal(e)
		}
		fmt.Println("authenticated")
	case "logout":
		if e := a.Logout(); e != nil {
			log.Fatal(e)
		}
	case "status":
		if _, e := a.AccessToken(ctx); e != nil {
			fmt.Println("not authenticated")
			return
		}
		fmt.Println("authenticated")
	default:
		log.Fatal("auth login|status|logout required")
	}
}

// daemonCmd runs the sync loop on the configured interval until interrupted.
// Per-folder failures are recorded in mail_sync_states and never abort the
// loop.
func daemonCmd(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config) {
	s := mailService(db, a, cfg)
	run := func() {
		if err := s.SyncAll(ctx); err != nil {
			log.Printf("mail sync: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(cfg.Sync.Interval)
	defer ticker.Stop()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case <-ticker.C:
			run()
		case <-ch:
			return
		}
	}
}
func mailService(db *outlookstore.Store, a *auth.Manager, cfg config.Config) *mail.Service {
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	var regs []domain.RegisteredAddress
	for _, x := range cfg.Mail.Addresses {
		regs = append(regs, domain.RegisteredAddress{Address: x.Address, Name: x.Name, Enabled: x.Enabled == nil || *x.Enabled, Headers: x.Match.Headers, SubjectPrefixes: x.Match.SubjectPrefixes})
	}
	return &mail.Service{Graph: g, Store: db, Addresses: regs, IncludeReceived: cfg.Mail.IncludeReceived == nil || *cfg.Mail.IncludeReceived, IncludeSent: cfg.Mail.IncludeSent == nil || *cfg.Mail.IncludeSent, FolderInclude: cfg.Mail.Folders.Include, FolderExclude: cfg.Mail.Folders.Exclude, LookbackDays: cfg.Sync.MailInitialLookbackDays}
}
