package handler

import "net/http"

type SearchRequest struct {
	Query     string  `json:"query"`
	SessionID *string `json:"session_id"`
	UserID    *string `json:"user_id"`
	Limit     int     `json:"limit"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	Content   string         `json:"content"`
	Score     float64        `json:"score"`
	SessionID string         `json:"session_id"`
	Timestamp string         `json:"timestamp"`
	Metadata  map[string]any `json:"metadata"`
}

type Search struct{}

func (h *Search) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	WriteJSON(w, http.StatusOK, SearchResponse{Results: []SearchResult{}})
}
