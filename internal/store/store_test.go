package store

import (
	"context"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestUpsertSearchAndDelete(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.UpsertContainer(ctx, domain.Container{ID: "c", Type: "team_channel", TeamID: "t", DisplayName: "開発", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	m := domain.Message{ID: "m", ContainerID: "c", SenderName: "田中", BodyText: "工事引継の確認です", CreatedAt: time.Now(), RawJSON: []byte("{}")}
	if e = s.UpsertMessage(ctx, m); e != nil {
		t.Fatal(e)
	}
	r, e := s.Search(ctx, domain.SearchFilter{Query: "工事引継", TeamIDs: []string{"t"}})
	if e != nil || len(r) != 1 {
		t.Fatalf("search=%v err=%v", r, e)
	}
	if e = s.Tombstone(ctx, "c", "m", time.Now()); e != nil {
		t.Fatal(e)
	}
	r, e = s.Search(ctx, domain.SearchFilter{Query: "工事引継"})
	if e != nil || len(r) != 0 {
		t.Fatalf("deleted search=%v err=%v", r, e)
	}
}

func TestMailMoveDoesNotTombstoneNewFolderCopy(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.ReplaceMailAddresses(ctx, []domain.MailAddress{{Address: "user@example.com"}}); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []domain.MailFolder{{ID: "old", DisplayName: "Old", Enabled: true}, {ID: "new", DisplayName: "New", Enabled: true}} {
		if err = s.UpsertMailFolder(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}
	m := domain.MailMessage{ID: "m1", FolderID: "old", Subject: "move", BodyText: "body", RawJSON: []byte(`{}`), Matches: []domain.MailMatch{{Address: "user@example.com", MatchedBy: "to"}}}
	if err = s.UpsertMailMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.FolderID = "new"
	if err = s.UpsertMailMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = s.TombstoneMailMessage(ctx, "old", "m1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.MailMessage(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FolderID != "new" || got.DeletedAt != nil || got.BodyText != "body" {
		t.Fatalf("message=%+v", got)
	}
}
