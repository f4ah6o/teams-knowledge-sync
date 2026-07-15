package outlookstore

import (
	"context"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
)

func (s *Store) UpsertCalendar(ctx context.Context, c domain.Calendar) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO calendars(id,name,owner,color,is_default,can_edit,can_view_private,enabled,raw_json,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,owner=excluded.owner,color=excluded.color,is_default=excluded.is_default,can_edit=excluded.can_edit,can_view_private=excluded.can_view_private,enabled=excluded.enabled,raw_json=excluded.raw_json,updated_at=excluded.updated_at`, c.ID, c.Name, c.Owner, c.Color, c.IsDefault, c.CanEdit, c.CanViewPrivate, c.Enabled, string(c.RawJSON), stamp(time.Now()))
	return e
}
func (s *Store) ListCalendars(ctx context.Context) ([]domain.Calendar, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,owner,color,is_default,can_edit,can_view_private,enabled FROM calendars ORDER BY is_default DESC,name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.Calendar
	for rows.Next() {
		var c domain.Calendar
		var isDefault, canEdit, canPriv, enabled int
		if e = rows.Scan(&c.ID, &c.Name, &c.Owner, &c.Color, &isDefault, &canEdit, &canPriv, &enabled); e != nil {
			return nil, e
		}
		c.IsDefault, c.CanEdit, c.CanViewPrivate, c.Enabled = isDefault != 0, canEdit != 0, canPriv != 0, enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) GetCalendar(ctx context.Context, id string) (domain.Calendar, error) {
	var c domain.Calendar
	var isDefault, canEdit, canPriv, enabled int
	e := s.DB.QueryRowContext(ctx, `SELECT id,name,owner,color,is_default,can_edit,can_view_private,enabled FROM calendars WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Owner, &c.Color, &isDefault, &canEdit, &canPriv, &enabled)
	c.IsDefault, c.CanEdit, c.CanViewPrivate, c.Enabled = isDefault != 0, canEdit != 0, canPriv != 0, enabled != 0
	return c, e
}
func (s *Store) UpsertCalendarEvent(ctx context.Context, ev domain.CalendarEvent) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	_, e = tx.ExecContext(ctx, `INSERT INTO calendar_events(id,calendar_id,ical_uid,series_master_id,event_type,subject,body_html,body_text,body_preview,start_utc,end_utc,start_timezone,end_timezone,original_start,is_all_day,organizer_address,organizer_name,location_name,online_meeting_url,join_url,web_url,show_as,importance,sensitivity,response_status,is_cancelled,is_organizer,is_online_meeting,has_attachments,created_at,modified_at,deleted_at,etag,raw_json,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(calendar_id,id) DO UPDATE SET ical_uid=excluded.ical_uid,series_master_id=excluded.series_master_id,event_type=excluded.event_type,subject=excluded.subject,body_html=excluded.body_html,body_text=excluded.body_text,body_preview=excluded.body_preview,start_utc=excluded.start_utc,end_utc=excluded.end_utc,start_timezone=excluded.start_timezone,end_timezone=excluded.end_timezone,original_start=excluded.original_start,is_all_day=excluded.is_all_day,organizer_address=excluded.organizer_address,organizer_name=excluded.organizer_name,location_name=excluded.location_name,online_meeting_url=excluded.online_meeting_url,join_url=excluded.join_url,web_url=excluded.web_url,show_as=excluded.show_as,importance=excluded.importance,sensitivity=excluded.sensitivity,response_status=excluded.response_status,is_cancelled=excluded.is_cancelled,is_organizer=excluded.is_organizer,is_online_meeting=excluded.is_online_meeting,has_attachments=excluded.has_attachments,modified_at=excluded.modified_at,deleted_at=excluded.deleted_at,etag=excluded.etag,raw_json=excluded.raw_json,indexed_at=excluded.indexed_at`, ev.ID, ev.CalendarID, ev.ICalUID, ev.SeriesMasterID, ev.EventType, ev.Subject, ev.BodyHTML, ev.BodyText, ev.BodyPreview, stamp(ev.StartUTC), stamp(ev.EndUTC), ev.StartTimezone, ev.EndTimezone, ev.OriginalStart, ev.IsAllDay, ev.OrganizerAddress, ev.OrganizerName, ev.LocationName, ev.OnlineMeetingURL, ev.JoinURL, ev.WebURL, ev.ShowAs, ev.Importance, ev.Sensitivity, ev.ResponseStatus, ev.IsCancelled, ev.IsOrganizer, ev.IsOnlineMeeting, ev.HasAttachments, stampPtr(ev.CreatedAt), stampPtr(ev.ModifiedAt), stampPtr(ev.DeletedAt), ev.ETag, string(ev.RawJSON), now)
	if e != nil {
		return e
	}
	var row int64
	if e = tx.QueryRowContext(ctx, `SELECT row_id FROM calendar_events WHERE calendar_id=? AND id=?`, ev.CalendarID, ev.ID).Scan(&row); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM calendar_attendees WHERE event_row_id=?", "DELETE FROM calendar_locations WHERE event_row_id=?", "DELETE FROM calendar_categories WHERE event_row_id=?", "DELETE FROM calendar_attachments WHERE event_row_id=?", "DELETE FROM calendar_fts WHERE event_row_id=?"} {
		if _, e = tx.ExecContext(ctx, q, row); e != nil {
			return e
		}
	}
	for _, a := range ev.Attendees {
		if _, e = tx.ExecContext(ctx, `INSERT INTO calendar_attendees(event_row_id,attendee_type,address,display_name,response) VALUES(?,?,?,?,?)`, row, a.Type, a.Address, a.Name, a.Response); e != nil {
			return e
		}
	}
	for _, l := range ev.Locations {
		if _, e = tx.ExecContext(ctx, `INSERT INTO calendar_locations(event_row_id,name) VALUES(?,?)`, row, l); e != nil {
			return e
		}
	}
	for _, c := range ev.Categories {
		if _, e = tx.ExecContext(ctx, `INSERT INTO calendar_categories(event_row_id,category) VALUES(?,?)`, row, c); e != nil {
			return e
		}
	}
	for _, a := range ev.Attachments {
		if _, e = tx.ExecContext(ctx, `INSERT INTO calendar_attachments(event_row_id,id,name,content_type,size,is_inline) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, row, a.ID, a.Name, a.ContentType, a.Size, a.IsInline); e != nil {
			return e
		}
	}
	if ev.DeletedAt == nil && !(strings.EqualFold(ev.Sensitivity, "private") && ev.BodyText == "" && len(ev.Attendees) == 0) {
		parts := []string{ev.Subject, ev.BodyText, ev.OrganizerName, ev.OrganizerAddress, ev.LocationName}
		for _, a := range ev.Attendees {
			parts = append(parts, a.Name, a.Address)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO calendar_fts(event_row_id,content) VALUES(?,?)`, row, text.SearchTokens(strings.Join(parts, " "))); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) GetCalendarEvent(ctx context.Context, id string) (domain.CalendarEvent, error) {
	var ev domain.CalendarEvent
	var start, end, created, modified, deleted, raw string
	var allDay, cancelled, organizer, online, hasAtt int
	e := s.DB.QueryRowContext(ctx, `SELECT id,calendar_id,ical_uid,series_master_id,event_type,subject,COALESCE(body_html,''),COALESCE(body_text,''),COALESCE(body_preview,''),start_utc,end_utc,start_timezone,end_timezone,original_start,is_all_day,organizer_address,organizer_name,location_name,online_meeting_url,join_url,web_url,show_as,importance,sensitivity,response_status,is_cancelled,is_organizer,is_online_meeting,has_attachments,created_at,modified_at,deleted_at,etag,raw_json FROM calendar_events WHERE id=? LIMIT 1`, id).Scan(&ev.ID, &ev.CalendarID, &ev.ICalUID, &ev.SeriesMasterID, &ev.EventType, &ev.Subject, &ev.BodyHTML, &ev.BodyText, &ev.BodyPreview, &start, &end, &ev.StartTimezone, &ev.EndTimezone, &ev.OriginalStart, &allDay, &ev.OrganizerAddress, &ev.OrganizerName, &ev.LocationName, &ev.OnlineMeetingURL, &ev.JoinURL, &ev.WebURL, &ev.ShowAs, &ev.Importance, &ev.Sensitivity, &ev.ResponseStatus, &cancelled, &organizer, &online, &hasAtt, &created, &modified, &deleted, &ev.ETag, &raw)
	if e != nil {
		return ev, e
	}
	ev.IsAllDay, ev.IsCancelled, ev.IsOrganizer, ev.IsOnlineMeeting, ev.HasAttachments = allDay != 0, cancelled != 0, organizer != 0, online != 0, hasAtt != 0
	ev.RawJSON = []byte(raw)
	if t := parse(start); t != nil {
		ev.StartUTC = *t
	}
	if t := parse(end); t != nil {
		ev.EndUTC = *t
	}
	ev.CreatedAt, ev.ModifiedAt, ev.DeletedAt = parse(created), parse(modified), parse(deleted)
	var row int64
	if e = s.DB.QueryRowContext(ctx, `SELECT row_id FROM calendar_events WHERE id=? LIMIT 1`, id).Scan(&row); e != nil {
		return ev, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT attendee_type,address,display_name,response FROM calendar_attendees WHERE event_row_id=?`, row)
	if e != nil {
		return ev, e
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.Attendee
		if e = rows.Scan(&a.Type, &a.Address, &a.Name, &a.Response); e != nil {
			return ev, e
		}
		ev.Attendees = append(ev.Attendees, a)
	}
	return ev, rows.Err()
}

// EventsBetween returns non-cancelled, non-master events overlapping the
// half-open interval [from,to), ordered by start time.
func (s *Store) EventsBetween(ctx context.Context, from, to time.Time, calendarID string) ([]domain.CalendarEvent, error) {
	where := []string{"start_utc<?", "end_utc>?", "event_type<>'seriesMaster'", "is_cancelled=0", "(deleted_at IS NULL OR deleted_at='')"}
	args := []any{stamp(to), stamp(from)}
	if calendarID != "" {
		where = append(where, "calendar_id=?")
		args = append(args, calendarID)
	}
	return s.queryEvents(ctx, strings.Join(where, " AND ")+" ORDER BY start_utc", args)
}
func (s *Store) SearchEvents(ctx context.Context, f domain.EventSearchFilter) ([]domain.CalendarEvent, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	where := []string{"event_type<>'seriesMaster'", "(deleted_at IS NULL OR deleted_at='')"}
	args := []any{}
	if f.Query != "" {
		where = append(where, "(subject LIKE ? OR body_text LIKE ? OR organizer_name LIKE ? OR location_name LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q, q, q)
	}
	if f.CalendarID != "" {
		where = append(where, "calendar_id=?")
		args = append(args, f.CalendarID)
	}
	if f.From != nil {
		where = append(where, "end_utc>?")
		args = append(args, stamp(*f.From))
	}
	if f.To != nil {
		where = append(where, "start_utc<?")
		args = append(args, stamp(*f.To))
	}
	args = append(args, f.Limit)
	return s.queryEvents(ctx, strings.Join(where, " AND ")+" ORDER BY start_utc DESC LIMIT ?", args)
}
func (s *Store) queryEvents(ctx context.Context, whereOrder string, args []any) ([]domain.CalendarEvent, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,calendar_id,event_type,subject,start_utc,end_utc,start_timezone,end_timezone,is_all_day,organizer_name,location_name,join_url,web_url,sensitivity,is_cancelled FROM calendar_events WHERE `+whereOrder, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.CalendarEvent
	for rows.Next() {
		var ev domain.CalendarEvent
		var start, end string
		var allDay, cancelled int
		if e = rows.Scan(&ev.ID, &ev.CalendarID, &ev.EventType, &ev.Subject, &start, &end, &ev.StartTimezone, &ev.EndTimezone, &allDay, &ev.OrganizerName, &ev.LocationName, &ev.JoinURL, &ev.WebURL, &ev.Sensitivity, &cancelled); e != nil {
			return nil, e
		}
		ev.IsAllDay, ev.IsCancelled = allDay != 0, cancelled != 0
		if t := parse(start); t != nil {
			ev.StartUTC = *t
		}
		if t := parse(end); t != nil {
			ev.EndUTC = *t
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
func (s *Store) HasEvent(ctx context.Context, calendarID, id string) (bool, error) {
	var n int
	e := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM calendar_events WHERE calendar_id=? AND id=?`, calendarID, id).Scan(&n)
	return n > 0, e
}
func (s *Store) CalendarStats(ctx context.Context) (map[string]any, error) {
	stats := map[string]any{}
	for k, q := range map[string]string{
		"calendars":        "SELECT count(*) FROM calendars",
		"events":           "SELECT count(*) FROM calendar_events",
		"cancelled_events": "SELECT count(*) FROM calendar_events WHERE is_cancelled=1",
		"deleted_events":   "SELECT count(*) FROM calendar_events WHERE deleted_at IS NOT NULL AND deleted_at<>''",
	} {
		var n int
		if e := s.DB.QueryRowContext(ctx, q).Scan(&n); e != nil {
			return nil, e
		}
		stats[k] = n
	}
	return stats, nil
}
