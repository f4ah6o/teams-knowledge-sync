package outlookstore

import (
	"context"
	"database/sql"
	"time"
)

type CalendarWindowState struct {
	CalendarID                                   string
	WindowStart, WindowEnd                       time.Time
	NextLink, DeltaLink, LastError               string
	LastAttemptAt, LastSuccessAt, LastFullSyncAt *time.Time
	ConsecutiveFailures                          int
}

// EnsureCalendarWindow creates the window's state row if it does not exist,
// preserving any existing delta state.
func (s *Store) EnsureCalendarWindow(ctx context.Context, calendarID string, start, end time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_sync_windows(calendar_id,window_start_utc,window_end_utc) VALUES(?,?,?)`, calendarID, stamp(start), stamp(end))
	return err
}
func (s *Store) GetCalendarWindowState(ctx context.Context, calendarID string, start, end time.Time) (CalendarWindowState, error) {
	st := CalendarWindowState{CalendarID: calendarID, WindowStart: start, WindowEnd: end}
	var attempt, success, full string
	err := s.DB.QueryRowContext(ctx, `SELECT next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures FROM calendar_sync_windows WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, calendarID, stamp(start), stamp(end)).Scan(&st.NextLink, &st.DeltaLink, &attempt, &success, &full, &st.LastError, &st.ConsecutiveFailures)
	if err == sql.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.LastAttemptAt, st.LastSuccessAt, st.LastFullSyncAt = parse(attempt), parse(success), parse(full)
	return st, nil
}
func (s *Store) SaveCalendarWindowNextLink(ctx context.Context, calendarID string, start, end time.Time, nextLink string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO calendar_sync_windows(calendar_id,window_start_utc,window_end_utc,next_link,last_attempt_at) VALUES(?,?,?,?,?) ON CONFLICT(calendar_id,window_start_utc,window_end_utc) DO UPDATE SET next_link=excluded.next_link,last_attempt_at=excluded.last_attempt_at`, calendarID, stamp(start), stamp(end), nextLink, stamp(time.Now()))
	return err
}
func (s *Store) CommitCalendarWindowDeltaLink(ctx context.Context, calendarID string, start, end time.Time, deltaLink string, startedAt, completedAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO calendar_sync_windows(calendar_id,window_start_utc,window_end_utc,next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures) VALUES(?,?,?, '',?,?,?,?, '',0) ON CONFLICT(calendar_id,window_start_utc,window_end_utc) DO UPDATE SET next_link='',delta_link=excluded.delta_link,last_attempt_at=excluded.last_attempt_at,last_success_at=excluded.last_success_at,last_full_sync_at=COALESCE(NULLIF(calendar_sync_windows.last_full_sync_at,''),excluded.last_full_sync_at),last_error='',consecutive_failures=0`, calendarID, stamp(start), stamp(end), deltaLink, stamp(completedAt), stamp(startedAt), stamp(startedAt))
	return err
}
func (s *Store) RecordCalendarWindowFailure(ctx context.Context, calendarID string, start, end time.Time, attemptedAt time.Time, syncErr error) error {
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO calendar_sync_windows(calendar_id,window_start_utc,window_end_utc,last_attempt_at,last_error,consecutive_failures) VALUES(?,?,?,?,?,1) ON CONFLICT(calendar_id,window_start_utc,window_end_utc) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,last_error=excluded.last_error,consecutive_failures=calendar_sync_windows.consecutive_failures+1`, calendarID, stamp(start), stamp(end), stamp(attemptedAt), message)
	return err
}

// ResetCalendarWindowState drops the window's stored links so its next sync
// starts a full delta initialization.
func (s *Store) ResetCalendarWindowState(ctx context.Context, calendarID string, start, end time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET next_link='',delta_link='',last_full_sync_at='' WHERE calendar_id=? AND window_start_utc=? AND window_end_utc=?`, calendarID, stamp(start), stamp(end))
	return err
}
func (s *Store) ResetCalendarWindows(ctx context.Context, calendarID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE calendar_sync_windows SET next_link='',delta_link='',last_full_sync_at='' WHERE calendar_id=?`, calendarID)
	return err
}
func (s *Store) ListCalendarWindowStates(ctx context.Context) ([]CalendarWindowState, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT calendar_id,window_start_utc,window_end_utc,next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures FROM calendar_sync_windows ORDER BY calendar_id,window_start_utc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarWindowState
	for rows.Next() {
		var st CalendarWindowState
		var start, end, attempt, success, full string
		if err = rows.Scan(&st.CalendarID, &start, &end, &st.NextLink, &st.DeltaLink, &attempt, &success, &full, &st.LastError, &st.ConsecutiveFailures); err != nil {
			return nil, err
		}
		if t := parse(start); t != nil {
			st.WindowStart = *t
		}
		if t := parse(end); t != nil {
			st.WindowEnd = *t
		}
		st.LastAttemptAt, st.LastSuccessAt, st.LastFullSyncAt = parse(attempt), parse(success), parse(full)
		out = append(out, st)
	}
	return out, rows.Err()
}

// EventBounds returns the stored start/end of an event, used to judge whether
// an out-of-range delta removal belongs to the window being synced.
func (s *Store) EventBounds(ctx context.Context, calendarID, id string) (time.Time, time.Time, bool, error) {
	var start, end string
	err := s.DB.QueryRowContext(ctx, `SELECT start_utc,end_utc FROM calendar_events WHERE calendar_id=? AND id=?`, calendarID, id).Scan(&start, &end)
	if err == sql.ErrNoRows {
		return time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	var s0, e0 time.Time
	if t := parse(start); t != nil {
		s0 = *t
	}
	if t := parse(end); t != nil {
		e0 = *t
	}
	return s0, e0, true, nil
}

// TombstoneCalendarEvent clears the event's details and index while keeping
// its ID and deletion time.
func (s *Store) TombstoneCalendarEvent(ctx context.Context, calendarID, id string, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE calendar_events SET body_html=NULL,body_text=NULL,body_preview=NULL,location_name='',online_meeting_url='',join_url='',deleted_at=? WHERE calendar_id=? AND id=?`, stamp(at), calendarID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit()
	}
	var row int64
	if err = tx.QueryRowContext(ctx, `SELECT row_id FROM calendar_events WHERE calendar_id=? AND id=?`, calendarID, id).Scan(&row); err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM calendar_attendees WHERE event_row_id=?", "DELETE FROM calendar_locations WHERE event_row_id=?", "DELETE FROM calendar_categories WHERE event_row_id=?", "DELETE FROM calendar_attachments WHERE event_row_id=?", "DELETE FROM calendar_fts WHERE event_row_id=?"} {
		if _, err = tx.ExecContext(ctx, q, row); err != nil {
			return err
		}
	}
	return tx.Commit()
}
