package outlookstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

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
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS registered_addresses (id INTEGER PRIMARY KEY AUTOINCREMENT,address TEXT NOT NULL UNIQUE,name TEXT,enabled INTEGER NOT NULL DEFAULT 1,headers TEXT,subject_prefixes TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS mail_folders (id TEXT PRIMARY KEY,display_name TEXT,well_known_name TEXT,parent_id TEXT,enabled INTEGER NOT NULL DEFAULT 1,total_count INTEGER NOT NULL DEFAULT 0,unread_count INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS mail_messages (row_id INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,internet_message_id TEXT,conversation_id TEXT,conversation_index TEXT,folder_id TEXT NOT NULL,subject TEXT,body_html TEXT,body_text TEXT,body_preview TEXT,from_address TEXT,from_name TEXT,sender_address TEXT,sender_name TEXT,received_at TEXT,sent_at TEXT,created_at TEXT,modified_at TEXT,deleted_at TEXT,importance TEXT,is_read INTEGER NOT NULL DEFAULT 0,is_draft INTEGER NOT NULL DEFAULT 0,has_attachments INTEGER NOT NULL DEFAULT 0,web_url TEXT,etag TEXT,raw_json TEXT NOT NULL,indexed_at TEXT);
CREATE TABLE IF NOT EXISTS mail_recipients (message_row_id INTEGER NOT NULL,recipient_type TEXT NOT NULL,address TEXT,display_name TEXT,normalized_address TEXT,FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id));
CREATE TABLE IF NOT EXISTS mail_message_addresses (message_row_id INTEGER NOT NULL,registered_address_id INTEGER NOT NULL,matched_by TEXT NOT NULL,matched_value TEXT,PRIMARY KEY(message_row_id,registered_address_id,matched_by));
CREATE TABLE IF NOT EXISTS mail_headers (message_row_id INTEGER NOT NULL,name TEXT NOT NULL,value TEXT,FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id));
CREATE TABLE IF NOT EXISTS mail_attachments (message_row_id INTEGER NOT NULL,id TEXT NOT NULL,name TEXT,content_type TEXT,size INTEGER NOT NULL DEFAULT 0,is_inline INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(message_row_id,id));
CREATE TABLE IF NOT EXISTS mail_categories (message_row_id INTEGER NOT NULL,category TEXT NOT NULL,FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id));
CREATE TABLE IF NOT EXISTS mail_sync_states (folder_id TEXT PRIMARY KEY,next_link TEXT NOT NULL DEFAULT '',delta_link TEXT NOT NULL DEFAULT '',last_attempt_at TEXT NOT NULL DEFAULT '',last_success_at TEXT NOT NULL DEFAULT '',last_full_sync_at TEXT NOT NULL DEFAULT '',last_error TEXT NOT NULL DEFAULT '',consecutive_failures INTEGER NOT NULL DEFAULT 0);
CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts USING fts5(message_row_id UNINDEXED,content, tokenize='unicode61');
CREATE INDEX IF NOT EXISTS mail_messages_folder_received ON mail_messages(folder_id,received_at);
CREATE INDEX IF NOT EXISTS mail_messages_conversation ON mail_messages(conversation_id);
CREATE INDEX IF NOT EXISTS mail_messages_imid ON mail_messages(internet_message_id);
CREATE TABLE IF NOT EXISTS calendars (id TEXT PRIMARY KEY,name TEXT,owner TEXT,color TEXT,is_default INTEGER NOT NULL DEFAULT 0,can_edit INTEGER NOT NULL DEFAULT 0,can_view_private INTEGER NOT NULL DEFAULT 0,enabled INTEGER NOT NULL DEFAULT 1,raw_json TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS calendar_events (row_id INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL,calendar_id TEXT NOT NULL,ical_uid TEXT,series_master_id TEXT,event_type TEXT NOT NULL,subject TEXT,body_html TEXT,body_text TEXT,body_preview TEXT,start_utc TEXT NOT NULL,end_utc TEXT NOT NULL,start_timezone TEXT,end_timezone TEXT,original_start TEXT,is_all_day INTEGER NOT NULL DEFAULT 0,organizer_address TEXT,organizer_name TEXT,location_name TEXT,online_meeting_url TEXT,join_url TEXT,web_url TEXT,show_as TEXT,importance TEXT,sensitivity TEXT,response_status TEXT,is_cancelled INTEGER NOT NULL DEFAULT 0,is_organizer INTEGER NOT NULL DEFAULT 0,is_online_meeting INTEGER NOT NULL DEFAULT 0,has_attachments INTEGER NOT NULL DEFAULT 0,created_at TEXT,modified_at TEXT,deleted_at TEXT,etag TEXT,raw_json TEXT NOT NULL,indexed_at TEXT,UNIQUE(calendar_id,id));
CREATE TABLE IF NOT EXISTS calendar_attendees (event_row_id INTEGER NOT NULL,attendee_type TEXT NOT NULL,address TEXT,display_name TEXT,response TEXT,FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id));
CREATE TABLE IF NOT EXISTS calendar_locations (event_row_id INTEGER NOT NULL,name TEXT NOT NULL,FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id));
CREATE TABLE IF NOT EXISTS calendar_categories (event_row_id INTEGER NOT NULL,category TEXT NOT NULL,FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id));
CREATE TABLE IF NOT EXISTS calendar_attachments (event_row_id INTEGER NOT NULL,id TEXT NOT NULL,name TEXT,content_type TEXT,size INTEGER NOT NULL DEFAULT 0,is_inline INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(event_row_id,id));
CREATE VIRTUAL TABLE IF NOT EXISTS calendar_fts USING fts5(event_row_id UNINDEXED,content, tokenize='unicode61');
CREATE INDEX IF NOT EXISTS calendar_events_time ON calendar_events(calendar_id,start_utc,end_utc);`)
	return err
}
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func stampPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return stamp(*t)
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
func appendIn(where []string, args []any, column string, values []string) ([]string, []any) {
	marks := make([]string, len(values))
	for i, v := range values {
		marks[i] = "?"
		args = append(args, v)
	}
	return append(where, column+" IN ("+strings.Join(marks, ",")+")"), args
}
