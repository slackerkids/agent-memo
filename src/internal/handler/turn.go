package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/slackerkids/agent-memo.git/internal/contract"
	"github.com/slackerkids/agent-memo.git/internal/service"
)

type TurnResponse struct {
	ID string `json:"id"`
}

type Turn struct {
	Service *service.TurnService
}

func (h *Turn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req contract.TurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, "session_id required")
		return
	}
	if len(req.Messages) == 0 {
		WriteError(w, http.StatusBadRequest, "messages required")
		return
	}
	if req.Timestamp == "" {
		WriteError(w, http.StatusBadRequest, "timestamp required")
		return
	}

	result, err := h.Service.Ingest(r.Context(), &req)
	if err != nil {
		log.Printf("WARN: turn ingest failed: %v", err)
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	WriteJSON(w, http.StatusCreated, TurnResponse{ID: result.TurnID})
}
