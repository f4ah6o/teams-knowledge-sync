package outlookstore

import (
	"context"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleMail(id, folder string, received time.Time) domain.MailMessage {
	return domain.MailMessage{
		ID: id, ConversationID: "conv1", FolderID: folder,
		Subject: "工事引継の件", BodyHTML: "<p>本文</p>", BodyText: "本文",
		FromAddress: "other@example.com", FromName: "Other",
		ReceivedAt: received, SentAt: received,
		Recipients: []domain.MailRecipient{{Type: "to", Address: "me@example.com", Normalized: "me@example.com"}},
		Headers:    []domain.MailHeader{{Name: "To", Value: "me@example.com"}},
		Matches:    []domain.AddressMatch{{RegisteredID: 1, MatchedBy: "to", MatchedValue: "me@example.com"}},
		RawJSON:    []byte(`{}`),
	}
}

func TestUpsertMailMessageIdempotent(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	if _, err := db.SyncRegisteredAddresses(ctx, []domain.RegisteredAddress{{Address: "me@example.com", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	m := sampleMail("m1", "F1", time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC))
	if err := db.UpsertMailMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Subject = "更新後"
	m.Recipients = append(m.Recipients, domain.MailRecipient{Type: "cc", Address: "cc@example.com", Normalized: "cc@example.com"})
	if err := db.UpsertMailMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	stats, _ := db.MailStats(ctx)
	if stats["messages"] != 1 {
		t.Fatalf("stats=%v", stats)
	}
	got, err := db.GetMailMessage(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "更新後" || len(got.Recipients) != 2 {
		t.Fatalf("got=%+v", got)
	}
}

func TestSearchMailFilters(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	regs, err := db.SyncRegisteredAddresses(ctx, []domain.RegisteredAddress{{Address: "me@example.com", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	m1 := sampleMail("m1", "F1", base)
	m1.Matches = []domain.AddressMatch{{RegisteredID: regs[0].ID, MatchedBy: "to"}}
	m2 := sampleMail("m2", "F2", base.Add(time.Hour))
	m2.Subject = "別件"
	m2.BodyText = "予算会議"
	m2.FromAddress = "boss@example.com"
	m2.Matches = nil
	for _, m := range []domain.MailMessage{m1, m2} {
		if err := db.UpsertMailMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := db.SearchMail(ctx, domain.MailSearchFilter{Query: "工事引継"})
	if err != nil || len(rs) != 1 || rs[0].ID != "m1" {
		t.Fatalf("rs=%+v err=%v", rs, err)
	}
	rs, _ = db.SearchMail(ctx, domain.MailSearchFilter{Address: "me@example.com"})
	if len(rs) != 1 || rs[0].ID != "m1" {
		t.Fatalf("address rs=%+v", rs)
	}
	rs, _ = db.SearchMail(ctx, domain.MailSearchFilter{Sender: "boss@example.com"})
	if len(rs) != 1 || rs[0].ID != "m2" {
		t.Fatalf("sender rs=%+v", rs)
	}
	from := base.Add(30 * time.Minute)
	rs, _ = db.SearchMail(ctx, domain.MailSearchFilter{From: &from})
	if len(rs) != 1 || rs[0].ID != "m2" {
		t.Fatalf("from rs=%+v", rs)
	}
	rs, _ = db.SearchMail(ctx, domain.MailSearchFilter{FolderID: "F1"})
	if len(rs) != 1 || rs[0].ID != "m1" {
		t.Fatalf("folder rs=%+v", rs)
	}
}

func TestMailThreadOrdersByReceived(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	older := sampleMail("m1", "F1", base)
	newer := sampleMail("m2", "F1", base.Add(2*time.Hour))
	for _, m := range []domain.MailMessage{newer, older} {
		if err := db.UpsertMailMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := db.MailThread(ctx, "m2")
	if err != nil || len(rs) != 2 || rs[0].ID != "m1" || rs[1].ID != "m2" {
		t.Fatalf("rs=%+v err=%v", rs, err)
	}
}

func TestTombstoneMail(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	m := sampleMail("m1", "F1", time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC))
	if err := db.UpsertMailMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	// wrong folder: move-safe guard means nothing happens
	if err := db.TombstoneMail(ctx, "m1", "F2", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMailMessage(ctx, "m1")
	if got.DeletedAt != nil {
		t.Fatal("tombstoned despite folder mismatch")
	}
	if err := db.TombstoneMail(ctx, "m1", "F1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMailMessage(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeletedAt == nil || got.BodyText != "" || len(got.Recipients) != 0 {
		t.Fatalf("got=%+v", got)
	}
	rs, _ := db.SearchMail(ctx, domain.MailSearchFilter{Query: "工事引継"})
	if len(rs) != 0 {
		t.Fatalf("deleted message still searchable: %+v", rs)
	}
}
