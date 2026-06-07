package handler

import (
	"database/sql"
	"net/http"

	"github.com/slackerkids/agent-memo.git/internal/repository"
)

type DeleteSession struct {
	DB *sql.DB
}

func (h *DeleteSession) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, "session_id required")
		return
	}

	if err := repository.DeleteTurnsBySession(r.Context(), h.DB, sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type DeleteUser struct {
	DB *sql.DB
}

func (h *DeleteUser) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	userID := r.PathValue("user_id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, "user_id required")
		return
	}

	ctx := r.Context()
	if err := repository.DeleteMemoriesByUser(ctx, h.DB, userID); err != nil {
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := repository.DeleteGraphByUser(ctx, h.DB, userID); err != nil {
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := repository.DeleteTurnsByUser(ctx, h.DB, userID); err != nil {
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
