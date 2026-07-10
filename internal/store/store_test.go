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
