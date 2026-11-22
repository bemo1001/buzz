package buzz

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SQLStore persists buzz events in the buzz_events table.
type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) Save(ctx context.Context, ev *Event) error {
	ms := ev.CreatedAt.UnixMilli()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO buzz_events(id, sender_id, recipient_id, level, note, status, app_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		ev.ID, ev.SenderID, ev.RecipientID, int(ev.Level), ev.Note, string(ev.Status), ev.AppID, ms, ms)
	return err
}

func (s *SQLStore) Get(ctx context.Context, id string) (*Event, bool, error) {
	var ev Event
	var lvl int
	var st string
	var created int64
	err := s.DB.QueryRowContext(ctx, `SELECT id, sender_id, recipient_id, level, note, status, app_id, created_at FROM buzz_events WHERE id = ?`, id).
		Scan(&ev.ID, &ev.SenderID, &ev.RecipientID, &lvl, &ev.Note, &st, &ev.AppID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	ev.Level, ev.Status, ev.CreatedAt = Level(lvl), Status(st), time.UnixMilli(created).UTC()
	return &ev, true, nil
}

func (s *SQLStore) SetStatus(ctx context.Context, id string, st Status) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE buzz_events SET status = ?, updated_at = ? WHERE id = ?`, string(st), time.Now().UnixMilli(), id)
	return err
}
