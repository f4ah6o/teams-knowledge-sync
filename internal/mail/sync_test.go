package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
)

type fakeGraph struct {
	pages    map[string]graph.PageResult
	requests []string
	objects  map[string]string
	errs     map[string]error
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

func mailJSON(id, to, subject string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","conversationId":"conv1","subject":"` + subject + `","receivedDateTime":"2026-07-09T10:00:00Z","sentDateTime":"2026-07-09T09:59:00Z","from":{"emailAddress":{"address":"other@example.com","name":"Other"}},"toRecipients":[{"emailAddress":{"address":"` + to + `"}}],"body":{"contentType":"html","content":"<p>hello<br>world</p>"},"webLink":"https://outlook.example/x"}`)
}

func service(g *fakeGraph, db *outlookstore.Store) *Service {
	return &Service{Graph: g, Store: db,
		Addresses:       []domain.RegisteredAddress{{Address: "me@example.com", Enabled: true, Headers: []string{"To"}}},
		IncludeReceived: true, IncludeSent: true,
		FolderInclude: []string{"inbox"}, FolderExclude: []string{"junkemail"},
		LookbackDays: 30, Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }}
}

const folderSelect = "?$select=id,displayName,parentFolderId,totalItemCount,unreadItemCount"

func TestSyncAllPagesFiltersAndStores(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{
		objects: map[string]string{
			"me/mailFolders/inbox" + folderSelect:     `{"id":"F1","displayName":"Inbox","totalItemCount":3}`,
			"me/mailFolders/junkemail" + folderSelect: `{"id":"F9","displayName":"Junk"}`,
		},
		pages: map[string]graph.PageResult{},
	}
	// initial page URL is deterministic; build it the same way the service does
	s := service(g, db)
	cutoff := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	first := "me/mailFolders/F1/messages?$top=50&$orderby=receivedDateTime+desc&$filter=receivedDateTime+ge+" + strings.ReplaceAll(cutoff, ":", "%3A") + "&$select=" + messageSelect
	g.pages[first] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "hit one"), mailJSON("m2", "someoneelse@example.com", "miss")}, NextLink: "https://graph.microsoft.com/v1.0/next2"}
	g.pages["https://graph.microsoft.com/v1.0/next2"] = graph.PageResult{Value: []json.RawMessage{mailJSON("m3", "me@example.com", "hit two")}}
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := db.MailStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats["messages"] != 2 {
		t.Fatalf("stats=%v", stats)
	}
	// re-run: upsert must not duplicate
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ = db.MailStats(context.Background())
	if stats["messages"] != 2 {
		t.Fatalf("after rerun stats=%v", stats)
	}
	for _, r := range g.requests {
		if strings.Contains(r, "F9") {
			t.Fatalf("excluded folder requested: %s", r)
		}
	}
	m, err := db.GetMailMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.BodyText != "hello\nworld" {
		t.Fatalf("body=%q", m.BodyText)
	}
	if len(m.Recipients) != 1 || m.Recipients[0].Normalized != "me@example.com" {
		t.Fatalf("recipients=%+v", m.Recipients)
	}
}

func TestSyncFolderRequestHasLookbackFilter(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{pages: map[string]graph.PageResult{}, objects: map[string]string{}}
	s := service(g, db)
	err := s.SyncFolder(context.Background(), domain.MailFolder{ID: "F1"})
	if err == nil {
		t.Fatal("expected unexpected-page error")
	}
	if len(g.requests) != 1 || !strings.Contains(g.requests[0], "receivedDateTime+ge+2026-06-10T12%3A00%3A00Z") {
		t.Fatalf("requests=%v", g.requests)
	}
}

func TestSyncAllSkipsSentWhenDisabled(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{
		"me/mailFolders/sentitems" + folderSelect: `{"id":"F2","displayName":"Sent Items"}`,
	}, pages: map[string]graph.PageResult{}}
	s := service(g, db)
	s.FolderInclude = []string{"sentitems"}
	s.FolderExclude = nil
	s.IncludeSent = false
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range g.requests {
		if strings.Contains(r, "/messages") {
			t.Fatalf("sent folder synced: %s", r)
		}
	}
}

func TestMessageAttachmentsFetched(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{
		"me/messages/m1/attachments?$select=id%2Cname%2CcontentType%2Csize%2CisInline": {Value: []json.RawMessage{json.RawMessage(`{"id":"a1","name":"spec.pdf","contentType":"application/pdf","size":123}`)}},
	}}
	s := service(g, db)
	raw := json.RawMessage(`{"id":"m1","hasAttachments":true,"receivedDateTime":"2026-07-09T10:00:00Z","toRecipients":[{"emailAddress":{"address":"me@example.com"}}],"body":{"contentType":"text","content":"x"}}`)
	regs, err := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	if err != nil {
		t.Fatal(err)
	}
	s.Addresses = regs
	var m domain.MailMessage
	m, err = Message(raw, "F1")
	if err != nil {
		t.Fatal(err)
	}
	m.Matches = Classify(&m, s.Addresses)
	if len(m.Matches) == 0 {
		t.Fatal("expected match")
	}
	if err := s.storeMatched(context.Background(), raw, "F1"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMailMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "spec.pdf" {
		t.Fatalf("attachments=%+v", got.Attachments)
	}
}
