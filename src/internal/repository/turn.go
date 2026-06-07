package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type Turn struct {
	ID        string
	SessionID string
	UserID    string
	Messages  string
	Timestamp string
	Metadata  string
	CreatedAt string
}

func InsertTurn(ctx context.Context, tx *sql.Tx, t *Turn) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO turn (id, session_id, user_id, messages, timestamp, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SessionID, t.UserID, t.Messages, t.Timestamp, t.Metadata, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert turn: %w", err)
	}
	return nil
}

func DeleteTurnsBySession(ctx context.Context, db *sql.DB, sessionID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM turn WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete turns by session: %w", err)
	}
	return nil
}

func DeleteTurnsByUser(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM turn WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete turns by user: %w", err)
	}
	return nil
}
