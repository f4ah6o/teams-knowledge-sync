package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

func mailUsage() {
	log.Fatal("mail address list|folder list|sync [--folder ID] [--full]|search QUERY|show ID|thread ID|status [--json]")
}
func mailCmd(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config, args []string) {
	if len(args) == 0 {
		mailUsage()
	}
	switch args[0] {
	case "address":
		if len(args) < 2 || args[1] != "list" {
			log.Fatal("mail address list")
		}
		mailAddressList(ctx, db, cfg)
	case "folder":
		if len(args) < 2 || args[1] != "list" {
			log.Fatal("mail folder list")
		}
		mailFolderList(ctx, db, a, cfg)
	case "sync":
		mailSync(ctx, db, a, cfg, args[1:])
	case "search":
		mailSearch(ctx, db, args[1:])
	case "show":
		if len(args) < 2 {
			log.Fatal("mail show MESSAGE_ID")
		}
		mailShow(ctx, db, args[1])
	case "thread":
		if len(args) < 2 {
			log.Fatal("mail thread MESSAGE_ID")
		}
		mailThread(ctx, db, args[1])
	case "status":
		mailStatus(ctx, db, args[1:])
	default:
		mailUsage()
	}
}
func mailAddressList(ctx context.Context, db *outlookstore.Store, cfg config.Config) {
	stored, err := db.ListRegisteredAddresses(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if len(stored) == 0 {
		for _, x := range cfg.Mail.Addresses {
			enabled := x.Enabled == nil || *x.Enabled
			fmt.Printf("%s\t%s\tenabled=%v\t(not yet synced)\n", x.Address, x.Name, enabled)
		}
		return
	}
	for _, x := range stored {
		fmt.Printf("%s\t%s\tenabled=%v\n", x.Address, x.Name, x.Enabled)
	}
}
func mailFolderList(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config) {
	s := mailService(db, a, cfg)
	folders, err := s.ResolveFolders(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range folders {
		fmt.Printf("%s\t%s\ttotal=%d\tunread=%d\n", f.ID, f.DisplayName, f.TotalCount, f.UnreadCount)
	}
}
func mailSync(ctx context.Context, db *outlookstore.Store, a *auth.Manager, cfg config.Config, args []string) {
	f := flag.NewFlagSet("mail sync", flag.ExitOnError)
	folder := f.String("folder", "", "sync a single folder ID")
	full := f.Bool("full", false, "reset delta state and resync")
	_ = f.Parse(args)
	s := mailService(db, a, cfg)
	if *folder != "" {
		target, err := s.ResolveFolder(ctx, *folder)
		if err != nil {
			log.Fatal(err)
		}
		if *full {
			if err := s.ResetFolder(ctx, target.ID); err != nil {
				log.Fatal(err)
			}
		}
		if err := s.SyncOne(ctx, target); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("synced folder %s\n", target.DisplayName)
		return
	}
	if *full {
		folders, err := s.ResolveFolders(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, x := range folders {
			if err := s.ResetFolder(ctx, x.ID); err != nil {
				log.Fatal(err)
			}
		}
	}
	if err := s.SyncAll(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("mail sync completed")
}
func mailSearch(ctx context.Context, db *outlookstore.Store, args []string) {
	f := flag.NewFlagSet("mail search", flag.ExitOnError)
	from := f.String("from", "", "RFC3339 lower bound")
	to := f.String("to", "", "RFC3339 upper bound (exclusive)")
	address := f.String("address", "", "registered address filter")
	sender := f.String("sender", "", "sender address filter")
	folder := f.String("folder", "", "folder ID filter")
	limit := f.Int("limit", 20, "limit")
	_ = f.Parse(args)
	var sf domain.MailSearchFilter
	sf.Query = strings.Join(f.Args(), " ")
	sf.Address = *address
	sf.Sender = *sender
	sf.FolderID = *folder
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
	rs, e := db.SearchMail(ctx, sf)
	if e != nil {
		log.Fatal(e)
	}
	for _, r := range rs {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", r.ReceivedAt.Format(time.RFC3339), r.FolderName, r.FromAddress, r.Subject, r.Snippet)
	}
}
func mailShow(ctx context.Context, db *outlookstore.Store, id string) {
	m, e := db.GetMailMessage(ctx, id)
	if e != nil {
		log.Fatal(e)
	}
	var to, cc []string
	for _, r := range m.Recipients {
		switch r.Type {
		case "to":
			to = append(to, r.Address)
		case "cc":
			cc = append(cc, r.Address)
		}
	}
	fmt.Printf("message: %s\nsubject: %s\nfrom: %s <%s>\nto: %s\ncc: %s\nreceived: %s\nfolder: %s\n", m.ID, m.Subject, m.FromName, m.FromAddress, strings.Join(to, ", "), strings.Join(cc, ", "), m.ReceivedAt.Format(time.RFC3339), m.FolderID)
	if len(m.Attachments) > 0 {
		names := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			names = append(names, a.Name)
		}
		fmt.Printf("attachments: %s\n", strings.Join(names, ", "))
	}
	if m.DeletedAt != nil {
		fmt.Printf("deleted: %s\n", m.DeletedAt.Format(time.RFC3339))
	}
	fmt.Printf("url: %s\n\n%s\n", m.WebURL, m.BodyText)
}
func mailThread(ctx context.Context, db *outlookstore.Store, id string) {
	rs, e := db.MailThread(ctx, id)
	if e != nil {
		log.Fatal(e)
	}
	for _, r := range rs {
		fmt.Printf("%s\t%s\t%s\t%s\n", r.ReceivedAt.Format(time.RFC3339), r.FromAddress, r.Subject, r.Snippet)
	}
}
func mailStatus(ctx context.Context, db *outlookstore.Store, args []string) {
	jsonOut := len(args) > 0 && args[0] == "--json"
	v, e := db.MailStats(ctx)
	if e != nil {
		log.Fatal(e)
	}
	states, e := db.ListMailSyncStates(ctx)
	if e != nil {
		log.Fatal(e)
	}
	if jsonOut {
		v["sync_states"] = states
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("messages: %v\ndeleted: %v\nfolders: %v\naddresses: %v\n", v["messages"], v["deleted_messages"], v["folders"], v["addresses"])
	for _, st := range states {
		success := "-"
		if st.LastSuccessAt != nil {
			success = st.LastSuccessAt.Format(time.RFC3339)
		}
		delta := "no"
		if st.DeltaLink != "" {
			delta = "yes"
		}
		fmt.Printf("folder %s\tlast_success=%s\tdelta=%s\tfailures=%d\t%s\n", st.FolderID, success, delta, st.ConsecutiveFailures, st.LastError)
	}
}
