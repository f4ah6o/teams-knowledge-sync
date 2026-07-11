package outlookstore

import (
	"context"
	"database/sql"
	"time"
)

type MailSyncState struct {
	FolderID, NextLink, DeltaLink, LastError     string
	LastAttemptAt, LastSuccessAt, LastFullSyncAt *time.Time
	ConsecutiveFailures                          int
}

func (s *Store) GetMailSyncState(ctx context.Context, folderID string) (MailSyncState, error) {
	st := MailSyncState{FolderID: folderID}
	var attempt, success, full string
	err := s.DB.QueryRowContext(ctx, `SELECT next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures FROM mail_sync_states WHERE folder_id=?`, folderID).Scan(&st.NextLink, &st.DeltaLink, &attempt, &success, &full, &st.LastError, &st.ConsecutiveFailures)
	if err == sql.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.LastAttemptAt, st.LastSuccessAt, st.LastFullSyncAt = parse(attempt), parse(success), parse(full)
	return st, nil
}

// SaveMailNextLink checkpoints paging progress so an interrupted delta walk
// resumes at the last unapplied page.
func (s *Store) SaveMailNextLink(ctx context.Context, folderID, nextLink string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mail_sync_states(folder_id,next_link,last_attempt_at) VALUES(?,?,?) ON CONFLICT(folder_id) DO UPDATE SET next_link=excluded.next_link,last_attempt_at=excluded.last_attempt_at`, folderID, nextLink, stamp(time.Now()))
	return err
}

// CommitMailDeltaLink stores the folder's new delta link after every page of
// the walk has been applied, clearing the paging checkpoint.
func (s *Store) CommitMailDeltaLink(ctx context.Context, folderID, deltaLink string, startedAt, completedAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mail_sync_states(folder_id,next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures) VALUES(?,'',?,?,?,?, '',0) ON CONFLICT(folder_id) DO UPDATE SET next_link='',delta_link=excluded.delta_link,last_attempt_at=excluded.last_attempt_at,last_success_at=excluded.last_success_at,last_full_sync_at=COALESCE(mail_sync_states.last_full_sync_at,excluded.last_full_sync_at),last_error='',consecutive_failures=0`, folderID, deltaLink, stamp(completedAt), stamp(startedAt), stamp(startedAt))
	return err
}
func (s *Store) RecordMailSyncFailure(ctx context.Context, folderID string, attemptedAt time.Time, syncErr error) error {
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mail_sync_states(folder_id,last_attempt_at,last_error,consecutive_failures) VALUES(?,?,?,1) ON CONFLICT(folder_id) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,last_error=excluded.last_error,consecutive_failures=mail_sync_states.consecutive_failures+1`, folderID, stamp(attemptedAt), message)
	return err
}

// ResetMailSyncState drops stored links so the folder's next sync starts a
// full delta initialization (used on token expiry and --full).
func (s *Store) ResetMailSyncState(ctx context.Context, folderID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE mail_sync_states SET next_link='',delta_link='',last_full_sync_at='' WHERE folder_id=?`, folderID)
	return err
}
func (s *Store) ListMailSyncStates(ctx context.Context) ([]MailSyncState, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT folder_id,next_link,delta_link,last_attempt_at,last_success_at,last_full_sync_at,last_error,consecutive_failures FROM mail_sync_states ORDER BY folder_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailSyncState
	for rows.Next() {
		var st MailSyncState
		var attempt, success, full string
		if err = rows.Scan(&st.FolderID, &st.NextLink, &st.DeltaLink, &attempt, &success, &full, &st.LastError, &st.ConsecutiveFailures); err != nil {
			return nil, err
		}
		st.LastAttemptAt, st.LastSuccessAt, st.LastFullSyncAt = parse(attempt), parse(success), parse(full)
		out = append(out, st)
	}
	return out, rows.Err()
}
