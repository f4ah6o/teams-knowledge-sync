package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
)

type fakeGraph struct {
	paths []string
	pages [][]json.RawMessage
	err   error
}

func (f *fakeGraph) Do(context.Context, string, string, any, any) error              { return nil }
func (f *fakeGraph) Page(context.Context, string, func(json.RawMessage) error) error { return f.err }
func (f *fakeGraph) PageUntil(_ context.Context, path string, fn func(json.RawMessage) (bool, error)) error {
	f.paths = append(f.paths, path)
	for _, page := range f.pages {
		for _, raw := range page {
			cont, err := fn(raw)
			if err != nil || !cont {
				return err
			}
		}
	}
	return f.err
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func chatMessage(id, modified string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","createdDateTime":"` + modified + `","lastModifiedDateTime":"` + modified + `","body":{"content":"hello"}}`)
}

func TestSyncChatUsesInitialLookbackAndRecordsSuccess(t *testing.T) {
	db := testStore(t)
	c := domain.Container{ID: "chat:c1", Type: "group_chat", ChatID: "c1"}
	if err := db.UpsertContainer(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	g := &fakeGraph{pages: [][]json.RawMessage{{chatMessage("m1", "2026-07-09T12:00:00Z")}}}
	s := Service{Graph: g, Store: db, InitialLookbackDays: 3, Now: func() time.Time { return now }}
	if err := s.SyncChat(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.paths[0], "$filter=lastModifiedDateTime+gt+2026-07-07T12%3A00%3A00Z") && !strings.Contains(g.paths[0], "$filter=lastModifiedDateTime%20gt%202026-07-07T12%3A00%3A00Z") && !strings.Contains(g.paths[0], "$filter=lastModifiedDateTime gt 2026-07-07T12:00:00Z") {
		t.Fatalf("unexpected filter URL: %s", g.paths[0])
	}
	state, err := db.GetSyncState(context.Background(), "chat", c.ID)
	if err != nil || state.LastSuccessAt == nil {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestSyncFailureDoesNotAdvanceSuccess(t *testing.T) {
	db := testStore(t)
	c := domain.Container{ID: "chat:c1", Type: "group_chat", ChatID: "c1"}
	if err := db.UpsertContainer(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	if err := db.RecordSyncSuccess(context.Background(), "chat", c.ID, started, started); err != nil {
		t.Fatal(err)
	}
	g := &fakeGraph{err: errors.New("page failed")}
	s := Service{Graph: g, Store: db, Now: func() time.Time { return now }}
	if err := s.SyncChat(context.Background(), c); err == nil {
		t.Fatal("expected error")
	}
	state, err := db.GetSyncState(context.Background(), "chat", c.ID)
	if err != nil || state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(started) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("failures=%d", state.ConsecutiveFailures)
	}
}
