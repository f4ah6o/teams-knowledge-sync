package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	textutil "github.com/obr-grp/teams-knowledge-sync/internal/text"
)

func (s *Store) ReplaceMailAddresses(ctx context.Context, addresses []domain.MailAddress) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	if _, err = tx.ExecContext(ctx, `UPDATE registered_addresses SET enabled=0,updated_at=?`, now); err != nil {
		return err
	}
	for _, a := range addresses {
		if _, err = tx.ExecContext(ctx, `INSERT INTO registered_addresses(address,display_name,enabled,created_at,updated_at) VALUES(?,?,1,?,?) ON CONFLICT(address) DO UPDATE SET display_name=excluded.display_name,enabled=1,updated_at=excluded.updated_at`, a.Address, a.Name, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MailAddresses(ctx context.Context) ([]domain.MailAddress, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT address,display_name FROM registered_addresses WHERE enabled=1 ORDER BY address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MailAddress
	for rows.Next() {
		var a domain.MailAddress
		if err = rows.Scan(&a.Address, &a.Name); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpsertMailFolder(ctx context.Context, f domain.MailFolder) error {
	now := stamp(time.Now())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mail_folders(id,parent_folder_id,display_name,well_known_name,child_folder_count,total_item_count,unread_item_count,is_hidden,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_folder_id=excluded.parent_folder_id,display_name=excluded.display_name,well_known_name=excluded.well_known_name,child_folder_count=excluded.child_folder_count,total_item_count=excluded.total_item_count,unread_item_count=excluded.unread_item_count,is_hidden=excluded.is_hidden,enabled=excluded.enabled,updated_at=excluded.updated_at`, f.ID, f.ParentID, f.DisplayName, f.WellKnownName, f.ChildCount, f.TotalCount, f.UnreadCount, f.Hidden, f.Enabled, now, now)
	return err
}

func (s *Store) MailFolders(ctx context.Context) ([]domain.MailFolder, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,parent_folder_id,display_name,well_known_name,child_folder_count,total_item_count,unread_item_count,is_hidden,enabled FROM mail_folders ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MailFolder
	for rows.Next() {
		var f domain.MailFolder
		if err = rows.Scan(&f.ID, &f.ParentID, &f.DisplayName, &f.WellKnownName, &f.ChildCount, &f.TotalCount, &f.UnreadCount, &f.Hidden, &f.Enabled); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) UpsertMailMessage(ctx context.Context, m domain.MailMessage) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	_, err = tx.ExecContext(ctx, `INSERT INTO mail_messages(id,internet_message_id,conversation_id,conversation_index,folder_id,subject,body_html,body_text,body_preview,body_content_type,sender_address,sender_name,from_address,from_name,received_at,sent_at,created_at,modified_at,importance,is_read,is_draft,has_attachments,flag_status,web_url,etag,raw_json,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET internet_message_id=excluded.internet_message_id,conversation_id=excluded.conversation_id,conversation_index=excluded.conversation_index,folder_id=excluded.folder_id,subject=excluded.subject,body_html=excluded.body_html,body_text=excluded.body_text,body_preview=excluded.body_preview,body_content_type=excluded.body_content_type,sender_address=excluded.sender_address,sender_name=excluded.sender_name,from_address=excluded.from_address,from_name=excluded.from_name,received_at=excluded.received_at,sent_at=excluded.sent_at,created_at=excluded.created_at,modified_at=excluded.modified_at,importance=excluded.importance,is_read=excluded.is_read,is_draft=excluded.is_draft,has_attachments=excluded.has_attachments,flag_status=excluded.flag_status,web_url=excluded.web_url,etag=excluded.etag,raw_json=excluded.raw_json,indexed_at=excluded.indexed_at`, m.ID, m.InternetMessageID, m.ConversationID, m.ConversationIndex, m.FolderID, m.Subject, m.BodyHTML, m.BodyText, m.BodyPreview, m.BodyContentType, m.SenderAddress, m.SenderName, m.FromAddress, m.FromName, stampPtr(m.ReceivedAt), stampPtr(m.SentAt), stampPtr(m.CreatedAt), stampPtr(m.ModifiedAt), m.Importance, m.Read, m.Draft, m.HasAttachments, m.FlagStatus, m.WebURL, m.ETag, string(m.RawJSON), now)
	if err != nil {
		return err
	}
	var rowID int64
	if err = tx.QueryRowContext(ctx, `SELECT row_id FROM mail_messages WHERE id=?`, m.ID).Scan(&rowID); err != nil {
		return err
	}
	for _, table := range []string{"mail_recipients", "mail_message_addresses", "mail_headers", "mail_attachments", "mail_categories", "mail_fts"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE message_row_id=?`, rowID); err != nil {
			return err
		}
	}
	for _, r := range m.Recipients {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_recipients(message_row_id,recipient_type,address,display_name,normalized_address) VALUES(?,?,?,?,?)`, rowID, r.Type, r.Address, r.Name, r.NormalizedAddress); err != nil {
			return err
		}
	}
	for _, h := range m.Headers {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_headers(message_row_id,name,value) VALUES(?,?,?)`, rowID, h.Name, h.Value); err != nil {
			return err
		}
	}
	for _, a := range m.Attachments {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_attachments(id,message_row_id,name,content_type,size,is_inline,content_id,attachment_type,raw_json) VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, rowID, a.Name, a.ContentType, a.Size, a.Inline, a.ContentID, a.Type, string(a.RawJSON)); err != nil {
			return err
		}
	}
	for _, c := range m.Categories {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_categories(message_row_id,category) VALUES(?,?)`, rowID, c); err != nil {
			return err
		}
	}
	for _, match := range m.Matches {
		var addressID int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM registered_addresses WHERE address=? AND enabled=1`, match.Address).Scan(&addressID); err != nil {
			return fmt.Errorf("registered address %s: %w", match.Address, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_message_addresses(message_row_id,registered_address_id,matched_by,matched_value) VALUES(?,?,?,?)`, rowID, addressID, match.MatchedBy, match.MatchedValue); err != nil {
			return err
		}
	}
	content := textutil.SearchTokens(strings.Join([]string{m.Subject, m.BodyText, m.FromName, m.FromAddress}, " "))
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail_fts(message_row_id,content) VALUES(?,?)`, rowID, content); err != nil {
		return err
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
	where, args := []string{"1=1"}, []any{}
	if f.Query != "" {
		where = append(where, "(m.subject LIKE ? OR m.body_text LIKE ?)")
		args = append(args, "%"+f.Query+"%", "%"+f.Query+"%")
	}
	if f.Sender != "" {
		where = append(where, "(m.from_address LIKE ? OR m.sender_address LIKE ?)")
		args = append(args, "%"+f.Sender+"%", "%"+f.Sender+"%")
	}
	if f.FolderID != "" {
		where = append(where, "m.folder_id=?")
		args = append(args, f.FolderID)
	}
	if f.Address != "" {
		where = append(where, `EXISTS(SELECT 1 FROM mail_message_addresses mma JOIN registered_addresses ra ON ra.id=mma.registered_address_id WHERE mma.message_row_id=m.row_id AND ra.address=?)`)
		args = append(args, f.Address)
	}
	if f.From != nil {
		where = append(where, "COALESCE(m.received_at,m.sent_at)>=?")
		args = append(args, stamp(*f.From))
	}
	if f.To != nil {
		where = append(where, "COALESCE(m.received_at,m.sent_at)<?")
		args = append(args, stamp(*f.To))
	}
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id,m.internet_message_id,m.conversation_id,m.folder_id,m.subject,m.body_text,m.body_preview,m.from_address,m.from_name,m.sender_address,m.sender_name,m.received_at,m.sent_at,m.web_url,m.has_attachments FROM mail_messages m WHERE `+strings.Join(where, " AND ")+` ORDER BY COALESCE(m.received_at,m.sent_at) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MailSearchResult
	for rows.Next() {
		var r domain.MailSearchResult
		var received, sent string
		if err = rows.Scan(&r.ID, &r.InternetMessageID, &r.ConversationID, &r.FolderID, &r.Subject, &r.BodyText, &r.BodyPreview, &r.FromAddress, &r.FromName, &r.SenderAddress, &r.SenderName, &received, &sent, &r.WebURL, &r.HasAttachments); err != nil {
			return nil, err
		}
		r.ReceivedAt = parse(received)
		r.SentAt = parse(sent)
		r.Snippet = textutil.Snippet(r.BodyText, f.Query)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MailMessage(ctx context.Context, id string) (domain.MailMessage, error) {
	rows, err := s.SearchMailDetails(ctx, "m.id=?", id)
	if err != nil {
		return domain.MailMessage{}, err
	}
	if len(rows) == 0 {
		return domain.MailMessage{}, sql.ErrNoRows
	}
	return rows[0], nil
}
func (s *Store) MailThread(ctx context.Context, id string) ([]domain.MailMessage, error) {
	var conversation string
	if err := s.DB.QueryRowContext(ctx, `SELECT conversation_id FROM mail_messages WHERE id=?`, id).Scan(&conversation); err != nil {
		return nil, err
	}
	if conversation == "" {
		message, err := s.MailMessage(ctx, id)
		if err != nil {
			return nil, err
		}
		return []domain.MailMessage{message}, nil
	}
	return s.SearchMailDetails(ctx, "m.conversation_id=?", conversation)
}
func (s *Store) SearchMailDetails(ctx context.Context, where string, arg any) ([]domain.MailMessage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id,m.internet_message_id,m.conversation_id,m.conversation_index,m.folder_id,m.subject,m.body_html,m.body_text,m.body_preview,m.body_content_type,m.sender_address,m.sender_name,m.from_address,m.from_name,m.received_at,m.sent_at,m.created_at,m.modified_at,m.importance,m.is_read,m.is_draft,m.has_attachments,m.flag_status,m.web_url,m.etag,m.raw_json FROM mail_messages m WHERE `+where+` ORDER BY COALESCE(m.received_at,m.sent_at)`, arg)
	if err != nil {
		return nil, err
	}
	var out []domain.MailMessage
	for rows.Next() {
		var m domain.MailMessage
		var received, sent, created, modified string
		if err = rows.Scan(&m.ID, &m.InternetMessageID, &m.ConversationID, &m.ConversationIndex, &m.FolderID, &m.Subject, &m.BodyHTML, &m.BodyText, &m.BodyPreview, &m.BodyContentType, &m.SenderAddress, &m.SenderName, &m.FromAddress, &m.FromName, &received, &sent, &created, &modified, &m.Importance, &m.Read, &m.Draft, &m.HasAttachments, &m.FlagStatus, &m.WebURL, &m.ETag, &m.RawJSON); err != nil {
			return nil, err
		}
		m.ReceivedAt, m.SentAt, m.CreatedAt, m.ModifiedAt = parse(received), parse(sent), parse(created), parse(modified)
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.hydrateMailMessage(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) hydrateMailMessage(ctx context.Context, m *domain.MailMessage) error {
	var rowID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT row_id FROM mail_messages WHERE id=?`, m.ID).Scan(&rowID); err != nil {
		return err
	}
	recipients, err := s.DB.QueryContext(ctx, `SELECT recipient_type,address,display_name,normalized_address FROM mail_recipients WHERE message_row_id=?`, rowID)
	if err != nil {
		return err
	}
	for recipients.Next() {
		var r domain.MailRecipient
		if err = recipients.Scan(&r.Type, &r.Address, &r.Name, &r.NormalizedAddress); err != nil {
			recipients.Close()
			return err
		}
		m.Recipients = append(m.Recipients, r)
	}
	if err = recipients.Close(); err != nil {
		return err
	}
	headers, err := s.DB.QueryContext(ctx, `SELECT name,value FROM mail_headers WHERE message_row_id=?`, rowID)
	if err != nil {
		return err
	}
	for headers.Next() {
		var h domain.MailHeader
		if err = headers.Scan(&h.Name, &h.Value); err != nil {
			headers.Close()
			return err
		}
		m.Headers = append(m.Headers, h)
	}
	if err = headers.Close(); err != nil {
		return err
	}
	attachments, err := s.DB.QueryContext(ctx, `SELECT id,name,content_type,size,is_inline,content_id,attachment_type,raw_json FROM mail_attachments WHERE message_row_id=?`, rowID)
	if err != nil {
		return err
	}
	for attachments.Next() {
		var a domain.MailAttachment
		if err = attachments.Scan(&a.ID, &a.Name, &a.ContentType, &a.Size, &a.Inline, &a.ContentID, &a.Type, &a.RawJSON); err != nil {
			attachments.Close()
			return err
		}
		m.Attachments = append(m.Attachments, a)
	}
	if err = attachments.Close(); err != nil {
		return err
	}
	matches, err := s.DB.QueryContext(ctx, `SELECT ra.address,mma.matched_by,mma.matched_value FROM mail_message_addresses mma JOIN registered_addresses ra ON ra.id=mma.registered_address_id WHERE mma.message_row_id=?`, rowID)
	if err != nil {
		return err
	}
	for matches.Next() {
		var match domain.MailMatch
		if err = matches.Scan(&match.Address, &match.MatchedBy, &match.MatchedValue); err != nil {
			matches.Close()
			return err
		}
		m.Matches = append(m.Matches, match)
	}
	if err = matches.Close(); err != nil {
		return err
	}
	categories, err := s.DB.QueryContext(ctx, `SELECT category FROM mail_categories WHERE message_row_id=? ORDER BY category`, rowID)
	if err != nil {
		return err
	}
	for categories.Next() {
		var category string
		if err = categories.Scan(&category); err != nil {
			categories.Close()
			return err
		}
		m.Categories = append(m.Categories, category)
	}
	return categories.Close()
}

func (s *Store) MailStats(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for key, query := range map[string]string{"messages": "SELECT count(*) FROM mail_messages", "folders": "SELECT count(*) FROM mail_folders", "addresses": "SELECT count(*) FROM registered_addresses WHERE enabled=1"} {
		var n int
		if err := s.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}
