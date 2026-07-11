package outlookstore

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
)

func (s *Store) SyncRegisteredAddresses(ctx context.Context, addrs []domain.RegisteredAddress) ([]domain.RegisteredAddress, error) {
	now := stamp(time.Now())
	out := make([]domain.RegisteredAddress, 0, len(addrs))
	for _, a := range addrs {
		headers, _ := json.Marshal(a.Headers)
		prefixes, _ := json.Marshal(a.SubjectPrefixes)
		if _, e := s.DB.ExecContext(ctx, `INSERT INTO registered_addresses(address,name,enabled,headers,subject_prefixes,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(address) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,headers=excluded.headers,subject_prefixes=excluded.subject_prefixes,updated_at=excluded.updated_at`, a.Address, a.Name, a.Enabled, string(headers), string(prefixes), now, now); e != nil {
			return nil, e
		}
		if e := s.DB.QueryRowContext(ctx, `SELECT id FROM registered_addresses WHERE address=?`, a.Address).Scan(&a.ID); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) ListRegisteredAddresses(ctx context.Context) ([]domain.RegisteredAddress, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,address,name,enabled,headers,subject_prefixes FROM registered_addresses ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.RegisteredAddress
	for rows.Next() {
		var a domain.RegisteredAddress
		var enabled int
		var headers, prefixes string
		if e = rows.Scan(&a.ID, &a.Address, &a.Name, &enabled, &headers, &prefixes); e != nil {
			return nil, e
		}
		a.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(headers), &a.Headers)
		_ = json.Unmarshal([]byte(prefixes), &a.SubjectPrefixes)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) UpsertMailFolder(ctx context.Context, f domain.MailFolder) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO mail_folders(id,display_name,well_known_name,parent_id,enabled,total_count,unread_count,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,well_known_name=excluded.well_known_name,parent_id=excluded.parent_id,enabled=excluded.enabled,total_count=excluded.total_count,unread_count=excluded.unread_count,updated_at=excluded.updated_at`, f.ID, f.DisplayName, f.WellKnownName, f.ParentID, f.Enabled, f.TotalCount, f.UnreadCount, stamp(time.Now()))
	return e
}
func (s *Store) ListMailFolders(ctx context.Context) ([]domain.MailFolder, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,display_name,well_known_name,parent_id,enabled,total_count,unread_count FROM mail_folders ORDER BY display_name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.MailFolder
	for rows.Next() {
		var f domain.MailFolder
		var enabled int
		if e = rows.Scan(&f.ID, &f.DisplayName, &f.WellKnownName, &f.ParentID, &enabled, &f.TotalCount, &f.UnreadCount); e != nil {
			return nil, e
		}
		f.Enabled = enabled != 0
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) UpsertMailMessage(ctx context.Context, m domain.MailMessage) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	_, e = tx.ExecContext(ctx, `INSERT INTO mail_messages(id,internet_message_id,conversation_id,conversation_index,folder_id,subject,body_html,body_text,body_preview,from_address,from_name,sender_address,sender_name,received_at,sent_at,created_at,modified_at,deleted_at,importance,is_read,is_draft,has_attachments,web_url,etag,raw_json,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET internet_message_id=excluded.internet_message_id,conversation_id=excluded.conversation_id,conversation_index=excluded.conversation_index,folder_id=excluded.folder_id,subject=excluded.subject,body_html=excluded.body_html,body_text=excluded.body_text,body_preview=excluded.body_preview,from_address=excluded.from_address,from_name=excluded.from_name,sender_address=excluded.sender_address,sender_name=excluded.sender_name,received_at=excluded.received_at,sent_at=excluded.sent_at,modified_at=excluded.modified_at,deleted_at=excluded.deleted_at,importance=excluded.importance,is_read=excluded.is_read,is_draft=excluded.is_draft,has_attachments=excluded.has_attachments,web_url=excluded.web_url,etag=excluded.etag,raw_json=excluded.raw_json,indexed_at=excluded.indexed_at`, m.ID, m.InternetMessageID, m.ConversationID, m.ConversationIndex, m.FolderID, m.Subject, m.BodyHTML, m.BodyText, m.BodyPreview, m.FromAddress, m.FromName, m.SenderAddress, m.SenderName, stamp(m.ReceivedAt), stamp(m.SentAt), stampPtr(m.CreatedAt), stampPtr(m.ModifiedAt), stampPtr(m.DeletedAt), m.Importance, m.IsRead, m.IsDraft, m.HasAttachments, m.WebURL, m.ETag, string(m.RawJSON), now)
	if e != nil {
		return e
	}
	var row int64
	if e = tx.QueryRowContext(ctx, `SELECT row_id FROM mail_messages WHERE id=?`, m.ID).Scan(&row); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM mail_recipients WHERE message_row_id=?", "DELETE FROM mail_message_addresses WHERE message_row_id=?", "DELETE FROM mail_headers WHERE message_row_id=?", "DELETE FROM mail_attachments WHERE message_row_id=?", "DELETE FROM mail_categories WHERE message_row_id=?", "DELETE FROM mail_fts WHERE message_row_id=?"} {
		if _, e = tx.ExecContext(ctx, q, row); e != nil {
			return e
		}
	}
	for _, r := range m.Recipients {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_recipients(message_row_id,recipient_type,address,display_name,normalized_address) VALUES(?,?,?,?,?)`, row, r.Type, r.Address, r.Name, r.Normalized); e != nil {
			return e
		}
	}
	for _, x := range m.Matches {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_message_addresses(message_row_id,registered_address_id,matched_by,matched_value) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, row, x.RegisteredID, x.MatchedBy, x.MatchedValue); e != nil {
			return e
		}
	}
	for _, h := range m.Headers {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_headers(message_row_id,name,value) VALUES(?,?,?)`, row, h.Name, h.Value); e != nil {
			return e
		}
	}
	for _, a := range m.Attachments {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_attachments(message_row_id,id,name,content_type,size,is_inline) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, row, a.ID, a.Name, a.ContentType, a.Size, a.IsInline); e != nil {
			return e
		}
	}
	for _, c := range m.Categories {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_categories(message_row_id,category) VALUES(?,?)`, row, c); e != nil {
			return e
		}
	}
	if m.DeletedAt == nil {
		content := text.SearchTokens(strings.Join([]string{m.Subject, m.BodyText, m.FromName, m.FromAddress}, " "))
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail_fts(message_row_id,content) VALUES(?,?)`, row, content); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// TombstoneMail clears the body and index of a message removed from folderID
// while keeping its ID and deletion time. The folder guard means a move
// (add in the new folder, @removed in the old one) never clobbers the moved
// message regardless of processing order.
func (s *Store) TombstoneMail(ctx context.Context, id, folderID string, at time.Time) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE mail_messages SET body_html=NULL,body_text=NULL,body_preview=NULL,deleted_at=? WHERE id=? AND folder_id=?`, stamp(at), id, folderID)
	if e != nil {
		return e
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit()
	}
	var row int64
	if e = tx.QueryRowContext(ctx, `SELECT row_id FROM mail_messages WHERE id=?`, id).Scan(&row); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM mail_recipients WHERE message_row_id=?", "DELETE FROM mail_headers WHERE message_row_id=?", "DELETE FROM mail_attachments WHERE message_row_id=?", "DELETE FROM mail_fts WHERE message_row_id=?"} {
		if _, e = tx.ExecContext(ctx, q, row); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) SearchMail(ctx context.Context, f domain.MailSearchFilter) ([]domain.MailSearchResult, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	where := []string{"(m.deleted_at IS NULL OR m.deleted_at='')"}
	args := []any{}
	if f.Query != "" {
		where = append(where, "(m.body_text LIKE ? OR m.subject LIKE ?)")
		args = append(args, "%"+f.Query+"%", "%"+f.Query+"%")
	}
	if f.Address != "" {
		where = append(where, "m.row_id IN (SELECT a.message_row_id FROM mail_message_addresses a JOIN registered_addresses r ON r.id=a.registered_address_id WHERE r.address=?)")
		args = append(args, f.Address)
	}
	if f.Sender != "" {
		where = append(where, "(m.from_address=? OR m.sender_address=?)")
		args = append(args, f.Sender, f.Sender)
	}
	if f.FolderID != "" {
		where = append(where, "m.folder_id=?")
		args = append(args, f.FolderID)
	}
	if f.From != nil {
		where = append(where, "m.received_at>=?")
		args = append(args, stamp(*f.From))
	}
	if f.To != nil {
		where = append(where, "m.received_at<?")
		args = append(args, stamp(*f.To))
	}
	args = append(args, f.Limit)
	rows, e := s.DB.QueryContext(ctx, `SELECT m.id,m.folder_id,COALESCE(fo.display_name,''),m.from_address,m.from_name,m.subject,m.body_text,m.web_url,m.received_at FROM mail_messages m LEFT JOIN mail_folders fo ON fo.id=m.folder_id WHERE `+strings.Join(where, " AND ")+` ORDER BY m.received_at DESC LIMIT ?`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.MailSearchResult
	for rows.Next() {
		var r domain.MailSearchResult
		var body, received string
		if e = rows.Scan(&r.ID, &r.FolderID, &r.FolderName, &r.FromAddress, &r.FromName, &r.Subject, &body, &r.WebURL, &received); e != nil {
			return nil, e
		}
		if t := parse(received); t != nil {
			r.ReceivedAt = *t
		}
		r.Snippet = text.Snippet(body, f.Query)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) GetMailMessage(ctx context.Context, id string) (domain.MailMessage, error) {
	var m domain.MailMessage
	var received, sent, created, modified, deleted, raw string
	var isRead, isDraft, hasAtt int
	e := s.DB.QueryRowContext(ctx, `SELECT id,internet_message_id,conversation_id,folder_id,subject,COALESCE(body_html,''),COALESCE(body_text,''),COALESCE(body_preview,''),from_address,from_name,sender_address,sender_name,received_at,sent_at,created_at,modified_at,deleted_at,importance,is_read,is_draft,has_attachments,web_url,etag,raw_json FROM mail_messages WHERE id=?`, id).Scan(&m.ID, &m.InternetMessageID, &m.ConversationID, &m.FolderID, &m.Subject, &m.BodyHTML, &m.BodyText, &m.BodyPreview, &m.FromAddress, &m.FromName, &m.SenderAddress, &m.SenderName, &received, &sent, &created, &modified, &deleted, &m.Importance, &isRead, &isDraft, &hasAtt, &m.WebURL, &m.ETag, &raw)
	if e != nil {
		return m, e
	}
	m.IsRead, m.IsDraft, m.HasAttachments = isRead != 0, isDraft != 0, hasAtt != 0
	m.RawJSON = []byte(raw)
	if t := parse(received); t != nil {
		m.ReceivedAt = *t
	}
	if t := parse(sent); t != nil {
		m.SentAt = *t
	}
	m.CreatedAt, m.ModifiedAt, m.DeletedAt = parse(created), parse(modified), parse(deleted)
	var row int64
	if e = s.DB.QueryRowContext(ctx, `SELECT row_id FROM mail_messages WHERE id=?`, id).Scan(&row); e != nil {
		return m, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT recipient_type,address,display_name,normalized_address FROM mail_recipients WHERE message_row_id=?`, row)
	if e != nil {
		return m, e
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.MailRecipient
		if e = rows.Scan(&r.Type, &r.Address, &r.Name, &r.Normalized); e != nil {
			return m, e
		}
		m.Recipients = append(m.Recipients, r)
	}
	if e = rows.Err(); e != nil {
		return m, e
	}
	att, e := s.DB.QueryContext(ctx, `SELECT id,name,content_type,size,is_inline FROM mail_attachments WHERE message_row_id=?`, row)
	if e != nil {
		return m, e
	}
	defer att.Close()
	for att.Next() {
		var a domain.MailAttachment
		var inline int
		if e = att.Scan(&a.ID, &a.Name, &a.ContentType, &a.Size, &inline); e != nil {
			return m, e
		}
		a.IsInline = inline != 0
		m.Attachments = append(m.Attachments, a)
	}
	return m, att.Err()
}
func (s *Store) MailThread(ctx context.Context, id string) ([]domain.MailSearchResult, error) {
	var conversation string
	if e := s.DB.QueryRowContext(ctx, `SELECT conversation_id FROM mail_messages WHERE id=?`, id).Scan(&conversation); e != nil {
		return nil, e
	}
	if conversation == "" {
		m, e := s.GetMailMessage(ctx, id)
		if e != nil {
			return nil, e
		}
		return []domain.MailSearchResult{{ID: m.ID, FolderID: m.FolderID, FromAddress: m.FromAddress, FromName: m.FromName, Subject: m.Subject, Snippet: text.Snippet(m.BodyText, ""), WebURL: m.WebURL, ReceivedAt: m.ReceivedAt}}, nil
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT m.id,m.folder_id,COALESCE(fo.display_name,''),m.from_address,m.from_name,m.subject,COALESCE(m.body_text,''),m.web_url,m.received_at FROM mail_messages m LEFT JOIN mail_folders fo ON fo.id=m.folder_id WHERE m.conversation_id=? ORDER BY m.received_at`, conversation)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.MailSearchResult
	for rows.Next() {
		var r domain.MailSearchResult
		var body, received string
		if e = rows.Scan(&r.ID, &r.FolderID, &r.FolderName, &r.FromAddress, &r.FromName, &r.Subject, &body, &r.WebURL, &received); e != nil {
			return nil, e
		}
		if t := parse(received); t != nil {
			r.ReceivedAt = *t
		}
		r.Snippet = text.Snippet(body, "")
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) MailStats(ctx context.Context) (map[string]any, error) {
	stats := map[string]any{}
	for k, q := range map[string]string{
		"messages":         "SELECT count(*) FROM mail_messages",
		"deleted_messages": "SELECT count(*) FROM mail_messages WHERE deleted_at IS NOT NULL AND deleted_at<>''",
		"folders":          "SELECT count(*) FROM mail_folders",
		"addresses":        "SELECT count(*) FROM registered_addresses",
	} {
		var n int
		if e := s.DB.QueryRowContext(ctx, q).Scan(&n); e != nil {
			return nil, e
		}
		stats[k] = n
	}
	return stats, nil
}
