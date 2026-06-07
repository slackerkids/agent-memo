package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Entity struct {
	ID        string
	UserID    string
	Name      string
	Type      string
	CreatedAt string
}

func UpsertEntity(ctx context.Context, tx *sql.Tx, userID, name, entityType, now string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty entity name")
	}

	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM entity WHERE user_id=? AND lower(name)=lower(?)`,
		userID, name,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	id = uuid.New().String()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO entity (id, user_id, name, type, created_at) VALUES (?,?,?,?,?)`,
		id, userID, name, entityType, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert entity: %w", err)
	}
	return id, nil
}

func InsertEdge(ctx context.Context, tx *sql.Tx, userID, sourceID, targetID, relation, statedAt string, validFrom *string, now string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO edge (id, user_id, source_id, target_id, relation, stated_at, valid_from, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		uuid.New().String(), userID, sourceID, targetID, relation, statedAt, validFrom, now,
	)
	if err != nil {
		return fmt.Errorf("insert edge: %w", err)
	}
	return nil
}

func TraverseGraph(ctx context.Context, db *sql.DB, userID string, seedNames []string, maxDepth int) ([]string, error) {
	if len(seedNames) == 0 {
		return nil, nil
	}

	seen := map[string]bool{}
	var results []string

	for _, seed := range seedNames {
		seed = strings.TrimSpace(seed)
		if seed == "" {
			continue
		}
		rows, err := db.QueryContext(ctx, graphQuery, userID, "%"+seed+"%", maxDepth, userID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				rows.Close()
				return nil, err
			}
			key := name + "|" + typ
			if !seen[key] {
				seen[key] = true
				results = append(results, name+" ("+typ+")")
			}
		}
		rows.Close()
	}
	return results, nil
}

const graphQuery = `
WITH RECURSIVE traverse(entity_id, depth) AS (
    SELECT e2.id, 1
    FROM entity e1
    JOIN edge ed ON ed.source_id = e1.id
    JOIN entity e2 ON ed.target_id = e2.id
    WHERE e1.user_id = ?
      AND lower(e1.name) LIKE lower(?)
      AND (ed.valid_until IS NULL OR ed.valid_until = '')
    UNION ALL
    SELECT e2.id, t.depth + 1
    FROM edge ed
    JOIN traverse t ON ed.source_id = t.entity_id
    JOIN entity e2 ON ed.target_id = e2.id
    WHERE t.depth < ?
      AND (ed.valid_until IS NULL OR ed.valid_until = '')
)
SELECT DISTINCT e.name, e.type
FROM entity e
WHERE e.id IN (SELECT entity_id FROM traverse)
  AND e.user_id = ?`

func DeleteGraphByUser(ctx context.Context, db *sql.DB, userID string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM edge WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete edges: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM memory_vec WHERE memory_rowid IN (
		SELECT rowid FROM memory WHERE user_id = ?)`, userID); err != nil {
		// vec table may be empty
		_ = err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM entity WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete entities: %w", err)
	}
	return nil
}

func EnsureUserEntity(ctx context.Context, tx *sql.Tx, userID, now string) (string, error) {
	return UpsertEntity(ctx, tx, userID, "user", "person", now)
}

func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
