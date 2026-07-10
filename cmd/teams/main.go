package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/mcpserver"
	"github.com/obr-grp/teams-knowledge-sync/internal/notify"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	syncservice "github.com/obr-grp/teams-knowledge-sync/internal/sync"
	"github.com/zalando/go-keyring"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: teams-knowledge [-config config.yaml] <auth|search|status|daemon> ...")
}
func main() {
	fs := flag.NewFlagSet("teams-knowledge", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.yaml", "config path")
	_ = fs.Parse(os.Args[1:])
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
	args := fs.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cfg, e := config.Load(*cfgPath)
	if e != nil {
		log.Fatal(e)
	}
	db, e := store.Open(cfg.Database.Path)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	a := auth.New(cfg.Entra.TenantID, cfg.Entra.ClientID)
	ctx := context.Background()
	switch args[0] {
	case "auth":
		authCmd(ctx, a, args[1:])
	case "search":
		searchCmd(ctx, db, args[1:])
	case "status":
		statusCmd(ctx, db, args[1:])
	case "daemon":
		daemonCmd(ctx, db, cfg)
	case "sync":
		syncCmd(ctx, db, a, cfg, args[1:])
	case "mcp":
		g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
		if e := mcpserver.Run(ctx, db, &syncservice.Service{Graph: g, Store: db}); e != nil {
			log.Fatal(e)
		}
	case "message":
		messageCmd(ctx, db, a, cfg, args[1:])
	default:
		usage()
		os.Exit(2)
	}
}
func messageCmd(ctx context.Context, db *store.Store, a *auth.Manager, cfg config.Config, args []string) {
	if len(args) < 2 || args[0] != "fetch" {
		log.Fatal("message fetch URL [--json]")
	}
	raw := args[1]
	jsonOut := len(args) > 2 && args[2] == "--json"
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	s := syncservice.Service{Graph: g, Store: db}
	m, e := s.FetchURL(ctx, raw)
	if e != nil {
		log.Fatal(e)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(b))
		return
	}
	imageMark := ""
	if m.HasImage {
		imageMark = "\nattachments: image present"
	}
	fmt.Printf("message: %s\nsender: %s\ncontainer: %s\nbody: %s%s\nurl: %s\n", m.ID, m.SenderName, m.ContainerID, m.BodyText, imageMark, m.WebURL)
}

func syncCmd(ctx context.Context, db *store.Store, a *auth.Manager, cfg config.Config, args []string) {
	if len(args) == 0 {
		log.Fatal("sync all|team TEAM_ID|chats required")
	}
	g := graph.New(a, cfg.Sync.RequestTimeout, cfg.Sync.MaxRetries)
	excluded := make(map[string]bool, len(cfg.Chats.ExcludeIDs))
	for _, id := range cfg.Chats.ExcludeIDs {
		excluded[id] = true
	}
	s := syncservice.Service{Graph: g, Store: db, ExcludedChats: excluded}
	switch args[0] {
	case "all":
		for _, t := range cfg.Teams {
			if t.Enabled {
				if e := s.SyncTeam(ctx, t.ID); e != nil {
					log.Printf("team %s: %v", t.ID, e)
				}
			}
		}
		if cfg.Chats.IncludeMyChats {
			if e := s.SyncChats(ctx); e != nil {
				log.Fatal(e)
			}
		}
	case "team":
		if len(args) < 2 {
			log.Fatal("team ID required")
		}
		if e := s.SyncTeam(ctx, args[1]); e != nil {
			log.Fatal(e)
		}
	case "chats":
		if e := s.SyncChats(ctx); e != nil {
			log.Fatal(e)
		}
	default:
		log.Fatal("sync all|team TEAM_ID|chats required")
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
func searchCmd(ctx context.Context, db *store.Store, args []string) {
	f := flag.NewFlagSet("search", flag.ExitOnError)
	team := f.String("team", "", "team/container ID")
	from := f.String("from", "", "RFC3339 timestamp")
	limit := f.Int("limit", 20, "limit")
	mentioned := f.Bool("mentioned-me", false, "only messages mentioning me")
	_ = f.Parse(args)
	q := strings.Join(f.Args(), " ")
	var sf domain.SearchFilter
	sf.Query = q
	sf.Limit = *limit
	sf.MentionedMe = *mentioned
	if *team != "" {
		sf.TeamIDs = []string{*team}
	}
	if *from != "" {
		t, e := time.Parse(time.RFC3339, *from)
		if e != nil {
			log.Fatal(e)
		}
		sf.From = &t
	}
	r, e := db.Search(ctx, sf)
	if e != nil {
		log.Fatal(e)
	}
	for _, x := range r {
		fmt.Printf("%s\t%s\t%s\t%s\n", x.CreatedAt.Format(time.RFC3339), x.ContainerName, x.SenderName, x.Snippet)
	}
}
func statusCmd(ctx context.Context, db *store.Store, args []string) {
	jsonOut := len(args) > 0 && args[0] == "--json"
	v, e := db.Stats(ctx)
	if e != nil {
		log.Fatal(e)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("messages: %v\ncontainers: %v\n", v["messages"], v["containers"])
}
func daemonCmd(ctx context.Context, db *store.Store, cfg config.Config) {
	secret, e := notificationSecret()
	if e != nil {
		log.Fatal(e)
	}
	srv := &http.Server{Addr: cfg.Notifications.ListenAddress, Handler: notify.Server{Store: db, Secret: secret}.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("webhook listening on %s", cfg.Notifications.ListenAddress)
		if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Printf("webhook: %v", e)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(stop)
}
func notificationSecret() ([]byte, error) {
	v, e := keyring.Get("teams-knowledge-sync", "notification-secret")
	if e == nil {
		return base64.StdEncoding.DecodeString(v)
	}
	if e != keyring.ErrNotFound {
		return nil, fmt.Errorf("OS credential store unavailable: %w", e)
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	if e = keyring.Set("teams-knowledge-sync", "notification-secret", base64.StdEncoding.EncodeToString(b)); e != nil {
		return nil, e
	}
	return b, nil
}
