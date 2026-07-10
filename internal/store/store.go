package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

type SyncState struct {
	ResourceType        string
	ResourceID          string
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFullSyncAt      *time.Time
	LastError           string
	ConsecutiveFailures int
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db}
	if _, err = s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=NORMAL;`); err != nil {
		db.Close()
		return nil, err
	}
	return s, s.Migrate(context.Background())
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS containers (id TEXT PRIMARY KEY,type TEXT NOT NULL,team_id TEXT,channel_id TEXT,chat_id TEXT,display_name TEXT,description TEXT,web_url TEXT,is_enabled INTEGER NOT NULL DEFAULT 1,last_message_at TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (row_id INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL,container_id TEXT NOT NULL,parent_message_id TEXT,sender_id TEXT,sender_name TEXT,sender_type TEXT,body_html TEXT,body_text TEXT,message_type TEXT,subject TEXT,web_url TEXT,created_at TEXT,modified_at TEXT,deleted_at TEXT,etag TEXT,content_hash TEXT,raw_json TEXT NOT NULL,indexed_at TEXT,has_image INTEGER NOT NULL DEFAULT 0,UNIQUE(container_id,id),FOREIGN KEY(container_id) REFERENCES containers(id));
CREATE TABLE IF NOT EXISTS message_mentions (message_row_id INTEGER NOT NULL,mention_id INTEGER,mentioned_user_id TEXT,mentioned_name TEXT,mentioned_type TEXT,FOREIGN KEY(message_row_id) REFERENCES messages(row_id));
CREATE TABLE IF NOT EXISTS message_reactions (message_row_id INTEGER NOT NULL,reaction_type TEXT NOT NULL,user_id TEXT,user_name TEXT,created_at TEXT,FOREIGN KEY(message_row_id) REFERENCES messages(row_id));
CREATE TABLE IF NOT EXISTS attachments (id INTEGER PRIMARY KEY AUTOINCREMENT,message_row_id INTEGER NOT NULL,attachment_id TEXT,attachment_type TEXT,name TEXT,content_url TEXT,content_type TEXT,drive_item_id TEXT,raw_json TEXT,FOREIGN KEY(message_row_id) REFERENCES messages(row_id));
CREATE TABLE IF NOT EXISTS container_members (container_id TEXT NOT NULL,user_id TEXT NOT NULL,display_name TEXT,PRIMARY KEY(container_id,user_id));
CREATE TABLE IF NOT EXISTS sync_states (resource_type TEXT NOT NULL,resource_id TEXT NOT NULL,cursor TEXT,last_attempt_at TEXT,last_success_at TEXT,last_full_sync_at TEXT,last_error TEXT,consecutive_failures INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(resource_type,resource_id));
CREATE TABLE IF NOT EXISTS subscriptions (id TEXT PRIMARY KEY,resource TEXT NOT NULL,expiration_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS notification_events (id INTEGER PRIMARY KEY AUTOINCREMENT,received_at TEXT NOT NULL,payload TEXT NOT NULL,processed_at TEXT,error TEXT);
CREATE TABLE IF NOT EXISTS chat_exclusions (chat_id TEXT PRIMARY KEY,reason TEXT NOT NULL,excluded_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS app_state (key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(message_row_id UNINDEXED,content, tokenize='unicode61');
CREATE INDEX IF NOT EXISTS messages_container_created ON messages(container_id,created_at); CREATE INDEX IF NOT EXISTS messages_sender ON messages(sender_id);`)
	if err != nil {
		return err
	}
	if _, e := s.DB.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN has_image INTEGER NOT NULL DEFAULT 0`); e != nil && !strings.Contains(strings.ToLower(e.Error()), "duplicate column") {
		return e
	}
	return nil
}
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func parse(v string) *time.Time {
	if v == "" {
		return nil
	}
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		return nil
	}
	return &t
}
func (s *Store) UpsertContainer(ctx context.Context, c domain.Container) error {
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, e := s.DB.ExecContext(ctx, `INSERT INTO containers(id,type,team_id,channel_id,chat_id,display_name,description,web_url,is_enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET type=excluded.type,team_id=excluded.team_id,channel_id=excluded.channel_id,chat_id=excluded.chat_id,display_name=excluded.display_name,description=excluded.description,web_url=excluded.web_url,is_enabled=excluded.is_enabled,updated_at=excluded.updated_at`, c.ID, c.Type, c.TeamID, c.ChannelID, c.ChatID, c.DisplayName, c.Description, c.WebURL, c.Enabled, stamp(c.CreatedAt), stamp(c.UpdatedAt))
	return e
}
func (s *Store) UpsertMessage(ctx context.Context, m domain.Message) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	_, e = tx.ExecContext(ctx, `INSERT INTO messages(id,container_id,parent_message_id,sender_id,sender_name,sender_type,body_html,body_text,message_type,subject,web_url,created_at,modified_at,deleted_at,etag,raw_json,indexed_at,has_image) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(container_id,id) DO UPDATE SET parent_message_id=excluded.parent_message_id,sender_id=excluded.sender_id,sender_name=excluded.sender_name,sender_type=excluded.sender_type,body_html=excluded.body_html,body_text=excluded.body_text,message_type=excluded.message_type,subject=excluded.subject,web_url=excluded.web_url,modified_at=excluded.modified_at,deleted_at=excluded.deleted_at,etag=excluded.etag,raw_json=excluded.raw_json,indexed_at=excluded.indexed_at,has_image=excluded.has_image`, m.ID, m.ContainerID, m.ParentMessageID, m.SenderID, m.SenderName, m.SenderType, m.BodyHTML, m.BodyText, m.MessageType, m.Subject, m.WebURL, stamp(m.CreatedAt), stampPtr(m.ModifiedAt), stampPtr(m.DeletedAt), m.ETag, string(m.RawJSON), now, m.HasImage)
	if e != nil {
		return e
	}
	var row int64
	if e = tx.QueryRowContext(ctx, `SELECT row_id FROM messages WHERE container_id=? AND id=?`, m.ContainerID, m.ID).Scan(&row); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM message_mentions WHERE message_row_id=?", "DELETE FROM message_reactions WHERE message_row_id=?", "DELETE FROM attachments WHERE message_row_id=?", "DELETE FROM message_fts WHERE message_row_id=?"} {
		if _, e = tx.ExecContext(ctx, q, row); e != nil {
			return e
		}
	}
	if m.DeletedAt == nil {
		content := text.SearchTokens(strings.Join([]string{m.BodyText, m.Subject, m.SenderName}, " "))
		_, e = tx.ExecContext(ctx, `INSERT INTO message_fts(message_row_id,content) VALUES(?,?)`, row, content)
		if e != nil {
			return e
		}
	}
	for _, x := range m.Mentions {
		if _, e = tx.ExecContext(ctx, `INSERT INTO message_mentions VALUES(?,?,?,?,?)`, row, x.ID, x.UserID, x.Name, x.Type); e != nil {
			return e
		}
	}
	for _, x := range m.Reactions {
		if _, e = tx.ExecContext(ctx, `INSERT INTO message_reactions VALUES(?,?,?,?,?)`, row, x.Type, x.UserID, x.UserName, stampPtr(x.CreatedAt)); e != nil {
			return e
		}
	}
	for _, x := range m.Attachments {
		if _, e = tx.ExecContext(ctx, `INSERT INTO attachments(message_row_id,attachment_id,attachment_type,name,content_url,content_type,drive_item_id,raw_json) VALUES(?,?,?,?,?,?,?,?)`, row, x.ID, x.Type, x.Name, x.ContentURL, x.ContentType, x.DriveItemID, string(x.RawJSON)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func stampPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return stamp(*t)
}
func (s *Store) Tombstone(ctx context.Context, containerID, id string, at time.Time) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE messages SET body_html=NULL,body_text=NULL,deleted_at=? WHERE container_id=? AND id=?`, stamp(at), containerID, id)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, `DELETE FROM message_fts WHERE message_row_id=(SELECT row_id FROM messages WHERE container_id=? AND id=?)`, containerID, id)
	return e
}
func (s *Store) Search(ctx context.Context, f domain.SearchFilter) ([]domain.SearchResult, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	join := ""
	if f.Query != "" {
		where = append(where, "m.body_text LIKE ?")
		args = append(args, "%"+f.Query+"%")
	}
	if !f.IncludeDeleted {
		where = append(where, "(m.deleted_at IS NULL OR m.deleted_at='')")
	}
	if f.From != nil {
		where = append(where, "m.created_at>=?")
		args = append(args, stamp(*f.From))
	}
	if f.To != nil {
		where = append(where, "m.created_at<=?")
		args = append(args, stamp(*f.To))
	}
	if len(f.ContainerIDs) > 0 {
		where, args = appendIn(where, args, "m.container_id", f.ContainerIDs)
	}
	if len(f.TeamIDs) > 0 {
		where, args = appendIn(where, args, "c.team_id", f.TeamIDs)
	}
	if len(f.ChannelIDs) > 0 {
		where, args = appendIn(where, args, "c.channel_id", f.ChannelIDs)
	}
	if len(f.ChatIDs) > 0 {
		where, args = appendIn(where, args, "c.chat_id", f.ChatIDs)
	}
	if f.RootsOnly {
		where = append(where, "(m.parent_message_id IS NULL OR m.parent_message_id='')")
	}
	if f.RepliesOnly {
		where = append(where, "m.parent_message_id IS NOT NULL AND m.parent_message_id<>''")
	}
	score := "0"
	_ = text.SearchTokens
	q := `SELECT m.id,m.container_id,m.parent_message_id,m.sender_id,m.sender_name,m.body_text,m.subject,m.web_url,m.created_at,m.modified_at,m.deleted_at,c.display_name,` + score + ` FROM messages m JOIN containers c ON c.id=m.container_id ` + join + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY `
	q += "m.created_at DESC"
	q += " LIMIT ?"
	args = append(args, f.Limit)
	rows, e := s.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.SearchResult
	for rows.Next() {
		var r domain.SearchResult
		var created, mod, del string
		if e = rows.Scan(&r.ID, &r.ContainerID, &r.ParentMessageID, &r.SenderID, &r.SenderName, &r.BodyText, &r.Subject, &r.WebURL, &created, &mod, &del, &r.ContainerName, &r.Score); e != nil {
			return nil, e
		}
		if x := parse(created); x != nil {
			r.CreatedAt = *x
		}
		r.ModifiedAt = parse(mod)
		r.DeletedAt = parse(del)
		r.Snippet = text.Snippet(r.BodyText, f.Query)
		out = append(out, r)
	}
	return out, rows.Err()
}
func appendIn(where []string, args []any, column string, values []string) ([]string, []any) {
	marks := make([]string, len(values))
	for i, v := range values {
		marks[i] = "?"
		args = append(args, v)
	}
	return append(where, column+" IN ("+strings.Join(marks, ",")+")"), args
}
func (s *Store) QueueNotification(ctx context.Context, payload []byte) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO notification_events(received_at,payload) VALUES(?,?)`, stamp(time.Now()), string(payload))
	return e
}
func (s *Store) State(ctx context.Context, k string) (string, error) {
	var v string
	e := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, k).Scan(&v)
	return v, e
}
func (s *Store) SetState(ctx context.Context, k, v string) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return e
}

func (s *Store) GetSyncState(ctx context.Context, resourceType, resourceID string) (SyncState, error) {
	var state SyncState
	var attempt, success, full string
	state.ResourceType, state.ResourceID = resourceType, resourceID
	err := s.DB.QueryRowContext(ctx, `SELECT last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures FROM sync_states WHERE resource_type=? AND resource_id=?`, resourceType, resourceID).Scan(&attempt, &success, &full, &state.LastError, &state.ConsecutiveFailures)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.LastAttemptAt = parse(attempt)
	state.LastSuccessAt = parse(success)
	state.LastFullSyncAt = parse(full)
	return state, nil
}

func (s *Store) RecordSyncSuccess(ctx context.Context, resourceType, resourceID string, startedAt, completedAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sync_states(resource_type,resource_id,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures) VALUES(?,?,?,?,?,?,0) ON CONFLICT(resource_type,resource_id) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,last_success_at=excluded.last_success_at,last_full_sync_at=COALESCE(sync_states.last_full_sync_at,excluded.last_full_sync_at),last_error='',consecutive_failures=0`, resourceType, resourceID, stamp(completedAt), stamp(startedAt), stamp(startedAt), "")
	return err
}

func (s *Store) RecordSyncFailure(ctx context.Context, resourceType, resourceID string, attemptedAt time.Time, syncErr error) error {
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sync_states(resource_type,resource_id,last_attempt_at,last_error,consecutive_failures) VALUES(?,?,?,?,1) ON CONFLICT(resource_type,resource_id) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,last_error=excluded.last_error,consecutive_failures=sync_states.consecutive_failures+1`, resourceType, resourceID, stamp(attemptedAt), message)
	return err
}
func (s *Store) Stats(ctx context.Context) (map[string]any, error) {
	var messages, containers int
	if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM messages").Scan(&messages); e != nil {
		return nil, e
	}
	if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM containers").Scan(&containers); e != nil {
		return nil, e
	}
	return map[string]any{"messages": messages, "containers": containers}, nil
}
func (s *Store) IsChatExcluded(ctx context.Context, chatID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM chat_exclusions WHERE chat_id=?`, chatID).Scan(&n)
	return n > 0, err
}
func (s *Store) ExcludeChat(ctx context.Context, chatID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO chat_exclusions(chat_id,reason,excluded_at) VALUES(?,?,?) ON CONFLICT(chat_id) DO UPDATE SET reason=excluded.reason,excluded_at=excluded.excluded_at`, chatID, reason, stamp(time.Now()))
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE containers SET is_enabled=0,updated_at=? WHERE chat_id=?`, stamp(time.Now()), chatID)
	return err
}
func (s *Store) RequireContainer(ctx context.Context, id string) (domain.Container, error) {
	var c domain.Container
	var enabled int
	var created, updated string
	e := s.DB.QueryRowContext(ctx, `SELECT id,type,team_id,channel_id,chat_id,display_name,description,web_url,is_enabled,created_at,updated_at FROM containers WHERE id=?`, id).Scan(&c.ID, &c.Type, &c.TeamID, &c.ChannelID, &c.ChatID, &c.DisplayName, &c.Description, &c.WebURL, &enabled, &created, &updated)
	c.Enabled = enabled != 0
	if e != nil {
		return c, fmt.Errorf("container %q: %w", id, e)
	}
	if x := parse(created); x != nil {
		c.CreatedAt = *x
	}
	if x := parse(updated); x != nil {
		c.UpdatedAt = *x
	}
	return c, nil
}
func (s *Store) FindChannel(ctx context.Context, teamID, channelID string) (domain.Container, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM containers WHERE team_id=? AND channel_id=? LIMIT 1`, teamID, channelID).Scan(&id)
	if err != nil {
		return domain.Container{}, err
	}
	return s.RequireContainer(ctx, id)
}
func (s *Store) FindChat(ctx context.Context, chatID string) (domain.Container, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM containers WHERE chat_id=? LIMIT 1`, chatID).Scan(&id)
	if err != nil {
		return domain.Container{}, err
	}
	return s.RequireContainer(ctx, id)
}
func (s *Store) GetMessage(ctx context.Context, containerID, messageID string) (domain.Message, error) {
	var m domain.Message
	var created, modified, deleted string
	var raw string
	var hasImage int
	err := s.DB.QueryRowContext(ctx, `SELECT id,container_id,parent_message_id,sender_id,sender_name,sender_type,body_html,body_text,message_type,subject,web_url,created_at,modified_at,deleted_at,etag,raw_json,has_image FROM messages WHERE container_id=? AND id=?`, containerID, messageID).Scan(&m.ID, &m.ContainerID, &m.ParentMessageID, &m.SenderID, &m.SenderName, &m.SenderType, &m.BodyHTML, &m.BodyText, &m.MessageType, &m.Subject, &m.WebURL, &created, &modified, &deleted, &m.ETag, &raw, &hasImage)
	m.HasImage = hasImage != 0
	if err != nil {
		return m, err
	}
	m.RawJSON = []byte(raw)
	if t := parse(created); t != nil {
		m.CreatedAt = *t
	}
	m.ModifiedAt = parse(modified)
	m.DeletedAt = parse(deleted)
	return m, nil
}
