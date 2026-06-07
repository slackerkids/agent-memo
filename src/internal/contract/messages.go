package contract

type TurnRequest struct {
	SessionID string         `json:"session_id"`
	UserID    *string        `json:"user_id"`
	Messages  []Message      `json:"messages"`
	Timestamp string         `json:"timestamp"`
	Metadata  map[string]any `json:"metadata"`
}

type Message struct {
	Role    string  `json:"role"`
	Content string  `json:"content"`
	Name    *string `json:"name"`
}

func ResolveUserID(userID *string, sessionID string) string {
	if userID != nil && *userID != "" {
		return *userID
	}
	return sessionID
}
