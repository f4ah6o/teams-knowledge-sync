package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	textutil "github.com/obr-grp/teams-knowledge-sync/internal/text"
)

func (s *Store) UpsertCalendar(ctx context.Context, c domain.Calendar) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO calendars(id,name,color,hex_color,owner_name,owner_address,is_default,can_edit,can_share,can_view_private,enabled,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,color=excluded.color,hex_color=excluded.hex_color,owner_name=excluded.owner_name,owner_address=excluded.owner_address,is_default=excluded.is_default,can_edit=excluded.can_edit,can_share=excluded.can_share,can_view_private=excluded.can_view_private,enabled=excluded.enabled,updated_at=excluded.updated_at`, c.ID, c.Name, c.Color, c.HexColor, c.OwnerName, c.OwnerAddress, c.Default, c.CanEdit, c.CanShare, c.CanViewPrivate, c.Enabled, stamp(time.Now()))
	return err
}
func (s *Store) Calendars(ctx context.Context) ([]domain.Calendar, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,color,hex_color,owner_name,owner_address,is_default,can_edit,can_share,can_view_private,enabled FROM calendars ORDER BY is_default DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Calendar
	for rows.Next() {
		var c domain.Calendar
		if err = rows.Scan(&c.ID, &c.Name, &c.Color, &c.HexColor, &c.OwnerName, &c.OwnerAddress, &c.Default, &c.CanEdit, &c.CanShare, &c.CanViewPrivate, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Calendar(ctx context.Context, id string) (domain.Calendar, error) {
	var c domain.Calendar
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,color,hex_color,owner_name,owner_address,is_default,can_edit,can_share,can_view_private,enabled FROM calendars WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Color, &c.HexColor, &c.OwnerName, &c.OwnerAddress, &c.Default, &c.CanEdit, &c.CanShare, &c.CanViewPrivate, &c.Enabled)
	return c, err
}
func (s *Store) UpsertCalendarEvent(ctx context.Context, e domain.CalendarEvent) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO calendar_events(id,calendar_id,ical_uid,series_master_id,event_type,subject,body_html,body_text,body_preview,start_utc,end_utc,start_timezone,end_timezone,original_start_timezone,original_end_timezone,is_all_day,is_cancelled,is_online_meeting,is_organizer,is_draft,has_attachments,organizer_name,organizer_address,response,teams_join_url,web_url,sensitivity,show_as,importance,created_at,modified_at,deleted_at,raw_json,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(calendar_id,id) DO UPDATE SET ical_uid=excluded.ical_uid,series_master_id=excluded.series_master_id,event_type=excluded.event_type,subject=excluded.subject,body_html=excluded.body_html,body_text=excluded.body_text,body_preview=excluded.body_preview,start_utc=excluded.start_utc,end_utc=excluded.end_utc,start_timezone=excluded.start_timezone,end_timezone=excluded.end_timezone,original_start_timezone=excluded.original_start_timezone,original_end_timezone=excluded.original_end_timezone,is_all_day=excluded.is_all_day,is_cancelled=excluded.is_cancelled,is_online_meeting=excluded.is_online_meeting,is_organizer=excluded.is_organizer,is_draft=excluded.is_draft,has_attachments=excluded.has_attachments,organizer_name=excluded.organizer_name,organizer_address=excluded.organizer_address,response=excluded.response,teams_join_url=excluded.teams_join_url,web_url=excluded.web_url,sensitivity=excluded.sensitivity,show_as=excluded.show_as,importance=excluded.importance,created_at=excluded.created_at,modified_at=excluded.modified_at,deleted_at=excluded.deleted_at,raw_json=excluded.raw_json,indexed_at=excluded.indexed_at`, e.ID, e.CalendarID, e.ICalUID, e.SeriesMasterID, e.Type, e.Subject, e.BodyHTML, e.BodyText, e.BodyPreview, stamp(e.StartUTC), stamp(e.EndUTC), e.StartTimezone, e.EndTimezone, e.OriginalStartTimezone, e.OriginalEndTimezone, e.AllDay, e.Cancelled, e.OnlineMeeting, e.Organizer, e.Draft, e.HasAttachments, e.OrganizerName, e.OrganizerAddress, e.Response, e.TeamsJoinURL, e.WebURL, e.Sensitivity, e.ShowAs, e.Importance, stampPtr(e.CreatedAt), stampPtr(e.ModifiedAt), stampPtr(e.DeletedAt), string(e.RawJSON), stamp(time.Now()))
	if err != nil {
		return err
	}
	var rowID int64
	if err = tx.QueryRowContext(ctx, `SELECT row_id FROM calendar_events WHERE calendar_id=? AND id=?`, e.CalendarID, e.ID).Scan(&rowID); err != nil {
		return err
	}
	for _, table := range []string{"calendar_attendees", "calendar_locations", "calendar_categories", "calendar_fts"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE event_row_id=?`, rowID); err != nil {
			return err
		}
	}
	for _, a := range e.Attendees {
		if _, err = tx.ExecContext(ctx, `INSERT INTO calendar_attendees(event_row_id,attendee_type,name,address,response) VALUES(?,?,?,?,?)`, rowID, a.Type, a.Name, a.Address, a.Response); err != nil {
			return err
		}
	}
	for _, l := range e.Locations {
		if _, err = tx.ExecContext(ctx, `INSERT INTO calendar_locations(event_row_id,name,address,location_type,unique_id,unique_id_type) VALUES(?,?,?,?,?,?)`, rowID, l.Name, l.Address, l.LocationType, l.UniqueID, l.UniqueIDType); err != nil {
			return err
		}
	}
	for _, c := range e.Categories {
		if _, err = tx.ExecContext(ctx, `INSERT INTO calendar_categories(event_row_id,category) VALUES(?,?)`, rowID, c); err != nil {
			return err
		}
	}
	if e.Attachments != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM calendar_attachments WHERE event_row_id=?`, rowID); err != nil {
			return err
		}
		for _, a := range e.Attachments {
			if _, err = tx.ExecContext(ctx, `INSERT INTO calendar_attachments(event_row_id,id,name,content_type,size,is_inline,raw_json) VALUES(?,?,?,?,?,?,?)`, rowID, a.ID, a.Name, a.ContentType, a.Size, a.Inline, string(a.RawJSON)); err != nil {
				return err
			}
		}
	}
	content := textutil.SearchTokens(strings.Join([]string{e.Subject, e.BodyText, e.OrganizerName, e.OrganizerAddress}, " "))
	if _, err = tx.ExecContext(ctx, `INSERT INTO calendar_fts(event_row_id,content) VALUES(?,?)`, rowID, content); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SearchCalendarEvents(ctx context.Context, f domain.CalendarSearchFilter) ([]domain.CalendarSearchResult, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	where, args := []string{"(e.deleted_at IS NULL OR e.deleted_at='')"}, []any{}
	if f.Query != "" {
		where = append(where, "(e.subject LIKE ? OR e.body_text LIKE ? OR e.organizer_name LIKE ? OR e.organizer_address LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q, q, q)
	}
	if f.CalendarID != "" {
		where = append(where, "e.calendar_id=?")
		args = append(args, f.CalendarID)
	}
	if f.From != nil {
		where = append(where, "e.end_utc>?")
		args = append(args, stamp(*f.From))
	}
	if f.To != nil {
		where = append(where, "e.start_utc<?")
		args = append(args, stamp(*f.To))
	}
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT e.id,e.calendar_id,e.ical_uid,e.series_master_id,e.event_type,e.subject,e.body_text,e.body_preview,e.start_utc,e.end_utc,e.start_timezone,e.end_timezone,e.is_all_day,e.is_cancelled,e.is_online_meeting,e.organizer_name,e.organizer_address,e.teams_join_url,e.web_url,e.sensitivity,c.name FROM calendar_events e JOIN calendars c ON c.id=e.calendar_id WHERE `+strings.Join(where, " AND ")+` ORDER BY e.start_utc LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CalendarSearchResult
	for rows.Next() {
		var r domain.CalendarSearchResult
		var start, end string
		if err = rows.Scan(&r.ID, &r.CalendarID, &r.ICalUID, &r.SeriesMasterID, &r.Type, &r.Subject, &r.BodyText, &r.BodyPreview, &start, &end, &r.StartTimezone, &r.EndTimezone, &r.AllDay, &r.Cancelled, &r.OnlineMeeting, &r.OrganizerName, &r.OrganizerAddress, &r.TeamsJoinURL, &r.WebURL, &r.Sensitivity, &r.CalendarName); err != nil {
			return nil, err
		}
		if v := parse(start); v != nil {
			r.StartUTC = *v
		}
		if v := parse(end); v != nil {
			r.EndUTC = *v
		}
		r.Snippet = textutil.Snippet(r.BodyText, f.Query)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) CalendarEvent(ctx context.Context, id string) (domain.CalendarEvent, error) {
	var e domain.CalendarEvent
	var rowID int64
	var start, end, created, modified, deleted, raw string
	err := s.DB.QueryRowContext(ctx, `SELECT row_id,id,calendar_id,ical_uid,series_master_id,event_type,subject,body_html,body_text,body_preview,start_utc,end_utc,start_timezone,end_timezone,original_start_timezone,original_end_timezone,is_all_day,is_cancelled,is_online_meeting,is_organizer,is_draft,has_attachments,organizer_name,organizer_address,response,teams_join_url,web_url,sensitivity,show_as,importance,created_at,modified_at,deleted_at,raw_json FROM calendar_events WHERE id=? LIMIT 1`, id).Scan(&rowID, &e.ID, &e.CalendarID, &e.ICalUID, &e.SeriesMasterID, &e.Type, &e.Subject, &e.BodyHTML, &e.BodyText, &e.BodyPreview, &start, &end, &e.StartTimezone, &e.EndTimezone, &e.OriginalStartTimezone, &e.OriginalEndTimezone, &e.AllDay, &e.Cancelled, &e.OnlineMeeting, &e.Organizer, &e.Draft, &e.HasAttachments, &e.OrganizerName, &e.OrganizerAddress, &e.Response, &e.TeamsJoinURL, &e.WebURL, &e.Sensitivity, &e.ShowAs, &e.Importance, &created, &modified, &deleted, &raw)
	if err != nil {
		return e, err
	}
	if v := parse(start); v != nil {
		e.StartUTC = *v
	}
	if v := parse(end); v != nil {
		e.EndUTC = *v
	}
	e.CreatedAt, e.ModifiedAt, e.DeletedAt = parse(created), parse(modified), parse(deleted)
	e.RawJSON = []byte(raw)
	attendees, err := s.DB.QueryContext(ctx, `SELECT attendee_type,name,address,response FROM calendar_attendees WHERE event_row_id=?`, rowID)
	if err != nil {
		return e, err
	}
	for attendees.Next() {
		var a domain.CalendarAttendee
		if err = attendees.Scan(&a.Type, &a.Name, &a.Address, &a.Response); err != nil {
			attendees.Close()
			return e, err
		}
		e.Attendees = append(e.Attendees, a)
	}
	if err = attendees.Close(); err != nil {
		return e, err
	}
	locations, err := s.DB.QueryContext(ctx, `SELECT name,address,location_type,unique_id,unique_id_type FROM calendar_locations WHERE event_row_id=?`, rowID)
	if err != nil {
		return e, err
	}
	for locations.Next() {
		var l domain.CalendarLocation
		if err = locations.Scan(&l.Name, &l.Address, &l.LocationType, &l.UniqueID, &l.UniqueIDType); err != nil {
			locations.Close()
			return e, err
		}
		e.Locations = append(e.Locations, l)
	}
	if err = locations.Close(); err != nil {
		return e, err
	}
	categories, err := s.DB.QueryContext(ctx, `SELECT category FROM calendar_categories WHERE event_row_id=?`, rowID)
	if err != nil {
		return e, err
	}
	for categories.Next() {
		var c string
		if err = categories.Scan(&c); err != nil {
			categories.Close()
			return e, err
		}
		e.Categories = append(e.Categories, c)
	}
	if err = categories.Close(); err != nil {
		return e, err
	}
	attachments, err := s.DB.QueryContext(ctx, `SELECT id,name,content_type,size,is_inline,raw_json FROM calendar_attachments WHERE event_row_id=?`, rowID)
	if err != nil {
		return e, err
	}
	for attachments.Next() {
		var a domain.CalendarAttachment
		if err = attachments.Scan(&a.ID, &a.Name, &a.ContentType, &a.Size, &a.Inline, &a.RawJSON); err != nil {
			attachments.Close()
			return e, err
		}
		e.Attachments = append(e.Attachments, a)
	}
	return e, attachments.Close()
}
func (s *Store) CalendarStats(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for key, q := range map[string]string{"calendars": "SELECT count(*) FROM calendars", "events": "SELECT count(*) FROM calendar_events WHERE deleted_at IS NULL OR deleted_at=''"} {
		var n int
		if err := s.DB.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}

func (s *Store) EnsureCalendarWindow(ctx context.Context, w domain.CalendarSyncWindow) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_sync_windows(calendar_id,window_start_utc,window_end_utc,consecutive_failures) VALUES(?,?,?,0)`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) CalendarSyncWindows(ctx context.Context, calendarID string) ([]domain.CalendarSyncWindow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT calendar_id,window_start_utc,window_end_utc,COALESCE(next_link,''),COALESCE(delta_link,''),COALESCE(last_attempt_at,''),COALESCE(last_success_at,''),COALESCE(last_error,''),consecutive_failures FROM calendar_sync_windows WHERE calendar_id=? ORDER BY window_start_utc`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CalendarSyncWindow
	for rows.Next() {
		var w domain.CalendarSyncWindow
		var start, end, attempt, success string
		if err = rows.Scan(&w.CalendarID, &start, &end, &w.NextLink, &w.DeltaLink, &attempt, &success, &w.LastError, &w.ConsecutiveFailures); err != nil {
			return nil, err
		}
		if v := parse(start); v != nil {
			w.StartUTC = *v
		}
		if v := parse(end); v != nil {
			w.EndUTC = *v
		}
		w.LastAttemptAt, w.LastSuccessAt = parse(attempt), parse(success)
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) RecordCalendarWindowProgress(ctx context.Context, w domain.CalendarSyncWindow, next string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET next_link=?,last_attempt_at=? WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, next, stamp(at), w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) RecordCalendarWindowSuccess(ctx context.Context, w domain.CalendarSyncWindow, delta string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET next_link='',delta_link=?,last_attempt_at=?,last_success_at=?,last_error='',consecutive_failures=0 WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, delta, stamp(at), stamp(at), w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) RecordCalendarWindowFailure(ctx context.Context, w domain.CalendarSyncWindow, at time.Time, syncErr error) error {
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET last_attempt_at=?,last_error=?,consecutive_failures=consecutive_failures+1 WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, stamp(at), message, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) ResetCalendarWindowToken(ctx context.Context, w domain.CalendarSyncWindow) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET next_link='',delta_link='' WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) LinkCalendarWindowEvent(ctx context.Context, w domain.CalendarSyncWindow, eventID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_window_events(calendar_id,window_start_utc,window_end_utc,event_id) VALUES(?,?,?,?)`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC), eventID)
	return err
}
func (s *Store) CalendarWindowEventIDs(ctx context.Context, w domain.CalendarSyncWindow) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT event_id FROM calendar_window_events WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) ClearCalendarWindowSeen(ctx context.Context, w domain.CalendarSyncWindow) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM calendar_window_seen WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	return err
}
func (s *Store) MarkCalendarWindowSeen(ctx context.Context, w domain.CalendarSyncWindow, eventID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_window_seen(calendar_id,window_start_utc,window_end_utc,event_id) VALUES(?,?,?,?)`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC), eventID)
	return err
}
func (s *Store) CalendarWindowSeenIDs(ctx context.Context, w domain.CalendarSyncWindow) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT event_id FROM calendar_window_seen WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) RemoveCalendarWindowEvent(ctx context.Context, w domain.CalendarSyncWindow, eventID string, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM calendar_window_events WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=? AND event_id=?`, w.CalendarID, stamp(w.StartUTC), stamp(w.EndUTC), eventID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM calendar_window_events WHERE calendar_id=? AND event_id=?`, w.CalendarID, eventID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return tx.Commit()
	}
	var rowID int64
	err = tx.QueryRowContext(ctx, `SELECT row_id FROM calendar_events WHERE calendar_id=? AND id=?`, w.CalendarID, eventID).Scan(&rowID)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE calendar_events SET subject='',body_html='',body_text='',body_preview='',deleted_at=? WHERE row_id=?`, stamp(at), rowID); err != nil {
		return err
	}
	for _, table := range []string{"calendar_attendees", "calendar_locations", "calendar_categories", "calendar_attachments", "calendar_fts"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE event_row_id=?`, rowID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
