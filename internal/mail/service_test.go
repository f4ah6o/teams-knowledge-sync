package mail

import (
	"context"
	"encoding/json"
	"errors"
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
	folder        json.RawMessage
	messages      []json.RawMessage
	paths         []string
	folders       map[string]json.RawMessage
	deltas        map[string]deltaPage
	errors        map[string]error
	errorContains map[string]error
}

func (f *fakeGraph) Do(_ context.Context, _ string, path string, _ any, out any) error {
	f.paths = append(f.paths, path)
	if err := f.errors[path]; err != nil {
		return err
	}
	for part, err := range f.errorContains {
		if strings.Contains(path, part) {
			return err
		}
	}
	switch p := out.(type) {
	case *json.RawMessage:
		if raw := f.folders[path]; raw != nil {
			*p = raw
		} else {
			*p = f.folder
		}
	case *deltaPage:
		if page, ok := f.deltas[path]; ok {
			*p = page
		} else {
			*p = deltaPage{Value: f.messages, DeltaLink: "delta-1"}
		}
	default:
		return errors.New("unexpected output type")
	}
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

func TestDeltaFollowsNextLinkAndTombstonesRemovedMessage(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Sync.MailInitialLookbackDays = 10
	cfg.Mail.Folders.Include = []string{"inbox"}
	cfg.Mail.Addresses = []config.MailAddress{{Address: "user@example.com", Enabled: boolp(true)}}
	folder := json.RawMessage(`{"id":"f1","displayName":"Inbox"}`)
	message := json.RawMessage(`{"id":"m1","conversationId":"c1","subject":"keep","body":{"contentType":"text","content":"body"},"toRecipients":[{"emailAddress":{"address":"user@example.com"}}],"receivedDateTime":"2026-07-09T00:00:00Z"}`)
	g := &fakeGraph{folder: folder, messages: []json.RawMessage{message}}
	s := Service{Graph: g, Store: db, Config: cfg}
	if err = s.Sync(ctx, ""); err != nil {
		t.Fatal(err)
	}
	g.messages = nil
	g.deltas = map[string]deltaPage{"delta-1": {NextLink: "https://graph.microsoft.com/v1.0/next-opaque"}, "https://graph.microsoft.com/v1.0/next-opaque": {Value: []json.RawMessage{json.RawMessage(`{"id":"m1","@removed":{"reason":"deleted"}}`)}, DeltaLink: "https://graph.microsoft.com/v1.0/delta-new"}}
	if err = s.Sync(ctx, ""); err != nil {
		t.Fatal(err)
	}
	state, err := db.MailSyncState(ctx, "f1")
	if err != nil {
		t.Fatal(err)
	}
	if state.DeltaLink != "https://graph.microsoft.com/v1.0/delta-new" || state.NextLink != "" {
		t.Fatalf("state=%+v", state)
	}
	results, err := db.SearchMail(ctx, domain.MailSearchFilter{})
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	detail, err := db.MailMessage(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.DeletedAt == nil || detail.BodyText != "" {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestDeltaFailurePreservesProgressAndOtherFoldersContinue(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Sync.MailInitialLookbackDays = 10
	cfg.Mail.Folders.Include = []string{"inbox", "archive"}
	g := &fakeGraph{folders: map[string]json.RawMessage{"me/mailFolders/inbox": json.RawMessage(`{"id":"f1","displayName":"Inbox"}`), "me/mailFolders/archive": json.RawMessage(`{"id":"f2","displayName":"Archive"}`)}, errorContains: map[string]error{"mailFolders/f1/messages/delta": errors.New("graph GET: 403 Forbidden")}}
	s := Service{Graph: g, Store: db, Config: cfg}
	err = s.Sync(ctx, "")
	if err == nil {
		t.Fatal("expected one folder failure")
	}
	failed, _ := db.MailSyncState(ctx, "f1")
	succeeded, _ := db.MailSyncState(ctx, "f2")
	if failed.ConsecutiveFailures != 1 || failed.LastError == "" || succeeded.DeltaLink != "delta-1" {
		t.Fatalf("failed=%+v succeeded=%+v", failed, succeeded)
	}
}

func TestInvalidDeltaTokenReinitializesOnlyThatFolder(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Sync.MailInitialLookbackDays = 10
	cfg.Mail.Folders.Include = []string{"inbox"}
	_ = db.RecordMailSyncSuccess(ctx, "f1", "delta-old", time.Now())
	g := &fakeGraph{folder: json.RawMessage(`{"id":"f1","displayName":"Inbox"}`), errors: map[string]error{"delta-old": errors.New("410 Gone: SyncStateNotFound")}}
	if err = (&Service{Graph: g, Store: db, Config: cfg}).Sync(ctx, ""); err != nil {
		t.Fatal(err)
	}
	state, _ := db.MailSyncState(ctx, "f1")
	if state.DeltaLink != "delta-1" || state.ConsecutiveFailures != 0 {
		t.Fatalf("state=%+v", state)
	}
}

func TestDeltaFailureKeepsNextLinkWithoutAdvancingDeltaLink(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	_ = db.RecordMailSyncSuccess(ctx, "f1", "delta-old", time.Now())
	cfg := config.Config{}
	cfg.Sync.MailInitialLookbackDays = 1
	cfg.Mail.Folders.Include = []string{"inbox"}
	g := &fakeGraph{folder: json.RawMessage(`{"id":"f1","displayName":"Inbox"}`), deltas: map[string]deltaPage{"delta-old": {NextLink: "next-page"}}, errors: map[string]error{"next-page": errors.New("graph retry limit exceeded after 429")}}
	err = (&Service{Graph: g, Store: db, Config: cfg}).Sync(ctx, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	state, err := db.MailSyncState(ctx, "f1")
	if err != nil {
		t.Fatal(err)
	}
	if state.NextLink != "next-page" || state.DeltaLink != "delta-old" || state.ConsecutiveFailures != 1 {
		t.Fatalf("state=%+v", state)
	}
}

func TestDaemonRunsUntilContextCancellation(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	cfg.Sync.Interval = time.Millisecond
	cfg.Sync.MailInitialLookbackDays = 1
	cfg.Mail.Folders.Include = []string{"inbox"}
	g := &fakeGraph{folder: json.RawMessage(`{"id":"f1","displayName":"Inbox"}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err = (&Service{Graph: g, Store: db, Config: cfg}).Daemon(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if len(g.paths) < 4 {
		t.Fatalf("daemon did not repeat: %v", g.paths)
	}
}
