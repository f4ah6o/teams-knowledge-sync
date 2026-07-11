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
	objects  map[string]string
	errs     map[string]error
	requests []string
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
	return json.RawMessage(`{"id":"` + id + `","conversationId":"conv1","subject":"` + subject + `","receivedDateTime":"2026-07-09T10:00:00Z","sentDateTime":"2026-07-09T09:59:00Z","from":{"emailAddress":{"address":"other@example.com","name":"Other"}},"toRecipients":[{"emailAddress":{"address":"` + to + `"}}],"internetMessageHeaders":[{"name":"To","value":"` + to + `"}],"body":{"contentType":"html","content":"<p>hello<br>world</p>"},"webLink":"https://outlook.example/x"}`)
}
func removedJSON(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","@removed":{"reason":"deleted"}}`)
}

func service(g *fakeGraph, db *outlookstore.Store) *Service {
	return &Service{Graph: g, Store: db,
		Addresses:       []domain.RegisteredAddress{{Address: "me@example.com", Enabled: true, Headers: []string{"To"}}},
		IncludeReceived: true, IncludeSent: true,
		FolderInclude: []string{"inbox"}, FolderExclude: []string{"junkemail"},
		LookbackDays: 30, Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }}
}

const folderSelect = "?$select=id,displayName,parentFolderId,totalItemCount,unreadItemCount"

func initURL(folderID string) string {
	return "me/mailFolders/" + folderID + "/messages/delta?$filter=receivedDateTime+ge+2026-06-10T12%3A00%3A00Z&$select=" + messageSelect
}

func TestSyncAllPagesFiltersAndStores(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{
		objects: map[string]string{
			"me/mailFolders/inbox" + folderSelect:     `{"id":"F1","displayName":"Inbox","totalItemCount":3}`,
			"me/mailFolders/junkemail" + folderSelect: `{"id":"F9","displayName":"Junk"}`,
		},
		pages: map[string]graph.PageResult{},
	}
	s := service(g, db)
	g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "hit one"), mailJSON("m2", "someoneelse@example.com", "miss")}, NextLink: "https://graph.microsoft.com/v1.0/next2"}
	g.pages["https://graph.microsoft.com/v1.0/next2"] = graph.PageResult{Value: []json.RawMessage{mailJSON("m3", "me@example.com", "hit two")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-F1"}
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
	st, err := db.GetMailSyncState(context.Background(), "F1")
	if err != nil || st.DeltaLink != "https://graph.microsoft.com/v1.0/delta-F1" || st.NextLink != "" || st.LastSuccessAt == nil {
		t.Fatalf("state=%+v err=%v", st, err)
	}
	// second run continues from the stored delta link, no duplicates
	g.pages["https://graph.microsoft.com/v1.0/delta-F1"] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "hit one")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-F1b"}
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
}

func TestSyncFolderRequestHasLookbackFilter(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{pages: map[string]graph.PageResult{}, objects: map[string]string{}}
	s := service(g, db)
	err := s.SyncFolder(context.Background(), domain.MailFolder{ID: "F1"})
	if err == nil {
		t.Fatal("expected unexpected-page error")
	}
	if len(g.requests) != 1 || !strings.Contains(g.requests[0], "/messages/delta?$filter=receivedDateTime+ge+2026-06-10T12%3A00%3A00Z") {
		t.Fatalf("requests=%v", g.requests)
	}
	st, _ := db.GetMailSyncState(context.Background(), "F1")
	if st.ConsecutiveFailures != 1 || st.LastSuccessAt != nil {
		t.Fatalf("state=%+v", st)
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
	regs, err := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	if err != nil {
		t.Fatal(err)
	}
	s.Addresses = regs
	raw := json.RawMessage(`{"id":"m1","hasAttachments":true,"receivedDateTime":"2026-07-09T10:00:00Z","toRecipients":[{"emailAddress":{"address":"me@example.com"}}],"body":{"contentType":"text","content":"x"}}`)
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

func TestHeaderFallbackWhenDeltaOmitsHeaders(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{
		"me/messages/m1?$select=internetMessageHeaders": `{"internetMessageHeaders":[{"name":"To","value":"me@example.com"}]}`,
	}, pages: map[string]graph.PageResult{}}
	s := service(g, db)
	regs, _ := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	s.Addresses = regs
	// recipient list lacks the registered address and headers are absent
	raw := json.RawMessage(`{"id":"m1","receivedDateTime":"2026-07-09T10:00:00Z","toRecipients":[{"emailAddress":{"address":"list@example.com"}}],"body":{"contentType":"text","content":"x"}}`)
	if err := s.storeMatched(context.Background(), raw, "F1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMailMessage(context.Background(), "m1"); err != nil {
		t.Fatalf("message not stored via header fallback: %v", err)
	}
}

func TestDeltaCommitOnlyAfterAllPagesAndResume(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}, errs: map[string]error{}}
	s := service(g, db)
	regs, _ := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	s.Addresses = regs
	f := domain.MailFolder{ID: "F1"}
	next := "https://graph.microsoft.com/v1.0/next-F1"
	g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, NextLink: next}
	g.errs[next] = fmt.Errorf("boom")
	if err := s.SyncFolder(context.Background(), f); err == nil {
		t.Fatal("expected failure")
	}
	st, _ := db.GetMailSyncState(context.Background(), "F1")
	if st.DeltaLink != "" || st.NextLink != next || st.ConsecutiveFailures != 1 {
		t.Fatalf("state=%+v", st)
	}
	// retry resumes from the stored nextLink, not from the beginning
	delete(g.errs, next)
	g.pages[next] = graph.PageResult{Value: []json.RawMessage{mailJSON("m2", "me@example.com", "two")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-F1"}
	g.requests = nil
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if len(g.requests) == 0 || g.requests[0] != next {
		t.Fatalf("requests=%v", g.requests)
	}
	st, _ = db.GetMailSyncState(context.Background(), "F1")
	if st.DeltaLink == "" || st.NextLink != "" || st.ConsecutiveFailures != 0 || st.LastSuccessAt == nil {
		t.Fatalf("state=%+v", st)
	}
	// replaying the same delta page must not duplicate
	g.pages["https://graph.microsoft.com/v1.0/delta-F1"] = graph.PageResult{Value: []json.RawMessage{mailJSON("m2", "me@example.com", "two")}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-F1"}
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	stats, _ := db.MailStats(context.Background())
	if stats["messages"] != 2 {
		t.Fatalf("stats=%v", stats)
	}
}

func TestDeltaRemovedTombstones(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}}
	s := service(g, db)
	regs, _ := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	s.Addresses = regs
	f := domain.MailFolder{ID: "F1"}
	g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, DeltaLink: "https://graph.microsoft.com/v1.0/d1"}
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	g.pages["https://graph.microsoft.com/v1.0/d1"] = graph.PageResult{Value: []json.RawMessage{removedJSON("m1")}, DeltaLink: "https://graph.microsoft.com/v1.0/d2"}
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	m, err := db.GetMailMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.DeletedAt == nil || m.BodyText != "" {
		t.Fatalf("m=%+v", m)
	}
}

func TestMoveKeepsMessageRegardlessOfOrder(t *testing.T) {
	for name, addFirst := range map[string]bool{"add-then-removed": true, "removed-then-add": false} {
		t.Run(name, func(t *testing.T) {
			db := testStore(t)
			g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}}
			s := service(g, db)
			regs, _ := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
			s.Addresses = regs
			// message starts in F1
			g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, DeltaLink: "https://graph.microsoft.com/v1.0/d-F1"}
			if err := s.SyncFolder(context.Background(), domain.MailFolder{ID: "F1"}); err != nil {
				t.Fatal(err)
			}
			applyAdd := func() {
				g.pages[initURL("F2")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, DeltaLink: "https://graph.microsoft.com/v1.0/d-F2"}
				if err := s.SyncFolder(context.Background(), domain.MailFolder{ID: "F2"}); err != nil {
					t.Fatal(err)
				}
			}
			applyRemove := func() {
				g.pages["https://graph.microsoft.com/v1.0/d-F1"] = graph.PageResult{Value: []json.RawMessage{removedJSON("m1")}, DeltaLink: "https://graph.microsoft.com/v1.0/d-F1b"}
				if err := s.SyncFolder(context.Background(), domain.MailFolder{ID: "F1"}); err != nil {
					t.Fatal(err)
				}
			}
			if addFirst {
				applyAdd()
				applyRemove()
			} else {
				applyRemove()
				applyAdd()
			}
			m, err := db.GetMailMessage(context.Background(), "m1")
			if err != nil {
				t.Fatal(err)
			}
			if m.FolderID != "F2" || m.DeletedAt != nil {
				t.Fatalf("m folder=%s deleted=%v", m.FolderID, m.DeletedAt)
			}
		})
	}
}

func TestSyncStateInvalidResetsFolderOnly(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{objects: map[string]string{}, pages: map[string]graph.PageResult{}, errs: map[string]error{}}
	s := service(g, db)
	regs, _ := db.SyncRegisteredAddresses(context.Background(), s.Addresses)
	s.Addresses = regs
	f := domain.MailFolder{ID: "F1"}
	g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, DeltaLink: "https://graph.microsoft.com/v1.0/d1"}
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	// stored delta link is now expired
	g.errs["https://graph.microsoft.com/v1.0/d1"] = &graph.Error{Status: 410, Code: "SyncStateNotFound"}
	g.pages[initURL("F1")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m1", "me@example.com", "one")}, DeltaLink: "https://graph.microsoft.com/v1.0/d2"}
	if err := s.SyncFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	st, _ := db.GetMailSyncState(context.Background(), "F1")
	if st.DeltaLink != "https://graph.microsoft.com/v1.0/d2" {
		t.Fatalf("state=%+v", st)
	}
}

func TestFolderFailureDoesNotStopOthers(t *testing.T) {
	db := testStore(t)
	g := &fakeGraph{
		objects: map[string]string{
			"me/mailFolders/inbox" + folderSelect:     `{"id":"F1","displayName":"Inbox"}`,
			"me/mailFolders/sentitems" + folderSelect: `{"id":"F2","displayName":"Sent"}`,
		},
		pages: map[string]graph.PageResult{},
		errs:  map[string]error{},
	}
	s := service(g, db)
	s.FolderInclude = []string{"inbox", "sentitems"}
	s.FolderExclude = nil
	g.errs[initURL("F1")] = &graph.Error{Status: 403, Code: "ErrorAccessDenied"}
	g.pages[initURL("F2")] = graph.PageResult{Value: []json.RawMessage{mailJSON("m2", "me@example.com", "sent")}, DeltaLink: "https://graph.microsoft.com/v1.0/d-F2"}
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stF1, _ := db.GetMailSyncState(context.Background(), "F1")
	stF2, _ := db.GetMailSyncState(context.Background(), "F2")
	if stF1.ConsecutiveFailures != 1 || stF1.LastError == "" {
		t.Fatalf("F1=%+v", stF1)
	}
	if stF2.LastSuccessAt == nil || stF2.DeltaLink == "" {
		t.Fatalf("F2=%+v", stF2)
	}
}
