package mail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
)

func boolp(v bool) *bool { return &v }

func TestNormalizeAddressAndClassify(t *testing.T) {
	if got := NormalizeAddress(` User <mailto:PERSON@Example.COM> `); got != "person@example.com" {
		t.Fatalf("normalized=%q", got)
	}
	a := config.MailAddress{Address: "person@example.com", Name: "person", Enabled: boolp(true)}
	a.Match.Headers = []string{"delivered-to"}
	a.Match.SubjectPrefixes = []string{"[project]"}
	m := domain.MailMessage{Subject: "[Project] update", Recipients: []domain.MailRecipient{{Type: "to", Address: "Person <PERSON@example.com>"}}, Headers: []domain.MailHeader{{Name: "Delivered-To", Value: "person@example.com"}}}
	matches := Classify(m, []config.MailAddress{a}, true, false)
	by := map[string]bool{}
	for _, match := range matches {
		by[match.MatchedBy] = true
	}
	for _, want := range []string{"to", "header", "subject_rule"} {
		if !by[want] {
			t.Fatalf("matches=%+v", matches)
		}
	}
}

type fakeGraph struct {
	folder   json.RawMessage
	messages []json.RawMessage
	paths    []string
}

func (f *fakeGraph) Do(_ context.Context, _ string, path string, _ any, out any) error {
	f.paths = append(f.paths, path)
	p := out.(*json.RawMessage)
	*p = f.folder
	return nil
}
func (f *fakeGraph) Page(_ context.Context, path string, fn func(json.RawMessage) error) error {
	f.paths = append(f.paths, path)
	for _, m := range f.messages {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

func TestSyncStoresMatchingMessagesAndUpsertsDuplicates(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Sync.MailInitialLookbackDays = 10
	cfg.Mail.IncludeReceived = boolp(true)
	cfg.Mail.IncludeSent = boolp(true)
	cfg.Mail.Folders.Include = []string{"inbox"}
	cfg.Mail.Folders.Exclude = []string{"deleteditems"}
	cfg.Mail.Addresses = []config.MailAddress{{Address: "user@example.com", Name: "User", Enabled: boolp(true)}}
	folder := json.RawMessage(`{"id":"folder-1","displayName":"Inbox"}`)
	matching := json.RawMessage(`{"id":"m1","internetMessageId":"<m1@example.com>","conversationId":"c1","subject":"工事引継","body":{"contentType":"html","content":"<p>確認です</p>"},"from":{"emailAddress":{"name":"Sender","address":"sender@example.com"}},"toRecipients":[{"emailAddress":{"name":"User","address":"USER@example.com"}}],"receivedDateTime":"2026-07-09T00:00:00Z","webLink":"https://outlook.office.com/mail/m1","hasAttachments":true,"attachments":[{"id":"a1","name":"plan.pdf","contentType":"application/pdf","size":42}]}`)
	nonmatching := json.RawMessage(`{"id":"m2","subject":"private","body":{"contentType":"text","content":"skip"},"toRecipients":[{"emailAddress":{"address":"other@example.com"}}],"receivedDateTime":"2026-07-09T00:00:00Z"}`)
	g := &fakeGraph{folder: folder, messages: []json.RawMessage{matching, nonmatching, matching}}
	s := Service{Graph: g, Store: db, Config: cfg, Now: func() time.Time { return time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) }}
	if err = s.Sync(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	results, err := db.SearchMail(context.Background(), domain.MailSearchFilter{Query: "確認"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "m1" {
		t.Fatalf("results=%+v", results)
	}
	detail, err := db.MailMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Recipients) != 1 || len(detail.Attachments) != 1 || len(detail.Matches) != 1 || detail.Matches[0].MatchedBy != "to" || detail.WebURL == "" {
		t.Fatalf("detail=%+v", detail)
	}
	if len(g.paths) < 2 || !strings.Contains(g.paths[1], "receivedDateTime+ge+2026-06-30T00%3A00%3A00Z") {
		t.Fatalf("paths=%v", g.paths)
	}
}

func TestSyncSkipsExcludedFolder(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Mail.Folders.Include = []string{"inbox", "deleteditems"}
	cfg.Mail.Folders.Exclude = []string{"deleteditems"}
	g := &fakeGraph{folder: json.RawMessage(`{"id":"folder-1","displayName":"Inbox"}`)}
	if err = (&Service{Graph: g, Store: db, Config: cfg}).Sync(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range g.paths {
		if strings.Contains(path, "deleteditems") {
			t.Fatalf("excluded folder requested: %v", g.paths)
		}
	}
}
