package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type MemoryRow struct {
	ID         string
	UserID     string
	SessionID  string
	TurnID     string
	Slot       string
	EntityKey  sql.NullString
	Value      string
	Confidence float64
	Evidence   sql.NullString
	Active     bool
	Supersedes sql.NullString
	Mutation   string
	StatedAt   string
	ValidFrom  sql.NullString
	ValidUntil sql.NullString
	CreatedAt  string
	UpdatedAt  string
}

type MemoryInsert struct {
	ID         string
	UserID     string
	SessionID  string
	TurnID     string
	Slot       string
	EntityKey  string
	Value      string
	Confidence float64
	Evidence   string
	Active     bool
	Supersedes *string
	Mutation   string
	StatedAt   string
	ValidFrom  *string
	ValidUntil *string
	CreatedAt  string
	UpdatedAt  string
}

func InsertMemory(ctx context.Context, tx *sql.Tx, m *MemoryInsert) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory
			(id, user_id, session_id, turn_id, slot, entity_key, value,
			 confidence, evidence, active, supersedes, mutation,
			 stated_at, valid_from, valid_until, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.UserID, m.SessionID, m.TurnID, m.Slot, nullStr(m.EntityKey), m.Value,
		m.Confidence, nullStr(m.Evidence), boolInt(m.Active), m.Supersedes, m.Mutation,
		m.StatedAt, m.ValidFrom, m.ValidUntil, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

func DeactivateMemory(ctx context.Context, tx *sql.Tx, id, validUntil, updatedAt string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE memory SET active=0, valid_until=?, updated_at=? WHERE id=?`,
		validUntil, updatedAt, id,
	)
	if err != nil {
		return fmt.Errorf("deactivate memory: %w", err)
	}
	return nil
}

func GetActiveSingleton(ctx context.Context, tx *sql.Tx, userID, slot string) (*MemoryRow, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, user_id, session_id, turn_id, slot, entity_key, value,
		       confidence, evidence, active, supersedes, mutation,
		       stated_at, valid_from, valid_until, created_at, updated_at
		FROM memory
		WHERE user_id=? AND slot=? AND active=1
		LIMIT 1`, userID, slot)
	return scanMemoryRow(row)
}

func GetActiveCollection(ctx context.Context, tx *sql.Tx, userID, slot string) ([]MemoryRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, session_id, turn_id, slot, entity_key, value,
		       confidence, evidence, active, supersedes, mutation,
		       stated_at, valid_from, valid_until, created_at, updated_at
		FROM memory
		WHERE user_id=? AND slot=? AND active=1`, userID, slot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

func GetActiveProfileMemories(ctx context.Context, db *sql.DB, userID string) ([]MemoryRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, session_id, turn_id, slot, entity_key, value,
		       confidence, evidence, active, supersedes, mutation,
		       stated_at, valid_from, valid_until, created_at, updated_at
		FROM memory
		WHERE user_id=? AND active=1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

func GetUserContextSummary(ctx context.Context, tx *sql.Tx, userID string) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT slot, entity_key, value FROM memory
		WHERE user_id=? AND active=1
		ORDER BY updated_at DESC
		LIMIT 30`, userID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var slot, entityKey, value sql.NullString
		if err := rows.Scan(&slot, &entityKey, &value); err != nil {
			return "", err
		}
		line := slot.String + ": " + value.String
		if entityKey.Valid && entityKey.String != "" {
			line = slot.String + "/" + entityKey.String + ": " + value.String
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return "(none yet)", nil
	}
	return strings.Join(parts, "\n"), rows.Err()
}

func MemoryRowID(ctx context.Context, tx *sql.Tx, memoryID string) (int64, error) {
	var rowid int64
	err := tx.QueryRowContext(ctx, `SELECT rowid FROM memory WHERE id=?`, memoryID).Scan(&rowid)
	return rowid, err
}

func ListMemoriesByUser(ctx context.Context, db *sql.DB, userID string) ([]MemoryRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, session_id, turn_id, slot, entity_key, value,
		       confidence, evidence, active, supersedes, mutation,
		       stated_at, valid_from, valid_until, created_at, updated_at
		FROM memory
		WHERE user_id = ?
		ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

func DeleteMemoriesByUser(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM memory WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete memories: %w", err)
	}
	return nil
}

func scanMemoryRow(row *sql.Row) (*MemoryRow, error) {
	var m MemoryRow
	var active int
	err := row.Scan(
		&m.ID, &m.UserID, &m.SessionID, &m.TurnID, &m.Slot, &m.EntityKey, &m.Value,
		&m.Confidence, &m.Evidence, &active, &m.Supersedes, &m.Mutation,
		&m.StatedAt, &m.ValidFrom, &m.ValidUntil, &m.CreatedAt, &m.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Active = active == 1
	return &m, nil
}

func scanMemoryRows(rows *sql.Rows) ([]MemoryRow, error) {
	var out []MemoryRow
	for rows.Next() {
		var m MemoryRow
		var active int
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.SessionID, &m.TurnID, &m.Slot, &m.EntityKey, &m.Value,
			&m.Confidence, &m.Evidence, &active, &m.Supersedes, &m.Mutation,
			&m.StatedAt, &m.ValidFrom, &m.ValidUntil, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.Active = active == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
