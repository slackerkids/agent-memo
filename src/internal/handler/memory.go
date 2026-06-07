package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/slackerkids/agent-memo.git/internal/repository"
)

type UserMemoriesResponse struct {
	Memories []MemoryRecord `json:"memories"`
}

type MemoryRecord struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Key           string  `json:"key"`
	Value         string  `json:"value"`
	Confidence    float64 `json:"confidence"`
	SourceSession string  `json:"source_session"`
	SourceTurn    string  `json:"source_turn"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	Supersedes    *string `json:"supersedes"`
	Active        bool    `json:"active"`
	Slot          string  `json:"slot"`
	EntityKey     string  `json:"entity_key"`
	Evidence      string  `json:"evidence"`
}

type Memories struct {
	DB *sql.DB
}

func (h *Memories) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	userID := r.PathValue("user_id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, "user_id required")
		return
	}

	rows, err := repository.ListMemoriesByUser(r.Context(), h.DB, userID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	memories := make([]MemoryRecord, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, memoryRowToRecord(row))
	}

	WriteJSON(w, http.StatusOK, UserMemoriesResponse{Memories: memories})
}

func memoryRowToRecord(row repository.MemoryRow) MemoryRecord {
	rec := MemoryRecord{
		ID:            row.ID,
		Type:          slotToType(row.Slot),
		Key:           row.Slot,
		Value:         row.Value,
		Confidence:    row.Confidence,
		SourceSession: row.SessionID,
		SourceTurn:    row.TurnID,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Active:        row.Active,
		Slot:          row.Slot,
	}
	if row.EntityKey.Valid {
		rec.EntityKey = row.EntityKey.String
	}
	if row.Evidence.Valid {
		rec.Evidence = row.Evidence.String
	}
	if row.Supersedes.Valid {
		s := row.Supersedes.String
		rec.Supersedes = &s
	}
	return rec
}

func slotToType(slot string) string {
	switch {
	case strings.HasPrefix(slot, "opinion."):
		return "opinion"
	case strings.HasPrefix(slot, "preference."):
		return "preference"
	case strings.HasPrefix(slot, "event."):
		return "event"
	default:
		return "fact"
	}
}
