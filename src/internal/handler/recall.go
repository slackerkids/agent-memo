package handler

import (
	"net/http"
)

type RecallRequest struct {
	Query     string  `json:"query"`
	SessionID string  `json:"session_id"`
	UserID    *string `json:"user_id"`
	MaxTokens int     `json:"max_tokens"`
}

type RecallResponse struct {
	Context   string     `json:"context"`
	Citations []Citation `json:"citations"`
}

type Citation struct {
	TurnID  string  `json:"turn_id"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

type Recall struct{}

func (h *Recall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	WriteJSON(w, http.StatusOK, RecallResponse{
		Context:   "",
		Citations: []Citation{},
	})
}
