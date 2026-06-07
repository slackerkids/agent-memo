package app

import (
	"database/sql"
	"net/http"

	"github.com/slackerkids/agent-memo.git/internal/gateway"
	"github.com/slackerkids/agent-memo.git/internal/handler"
	"github.com/slackerkids/agent-memo.git/internal/service"
)

type App struct {
	cfg *Config
	db  *sql.DB
}

func New(cfg *Config, db *sql.DB) *App {
	return &App{cfg: cfg, db: db}
}

func (a *App) Router() http.Handler {
	extractor := gateway.NewExtractor(a.cfg.DeepseekAPIKey, a.cfg.DeepseekBaseURL)
	embedder := gateway.NewEmbedder(a.cfg.OpenAIAPIKey, a.cfg.OpenAIBaseURL)
	memorySvc := &service.MemoryService{}
	turnSvc := &service.TurnService{
		DB:        a.db,
		Extractor: extractor,
		Embedder:  embedder,
		Memory:    memorySvc,
	}

	mux := http.NewServeMux()

	mux.Handle("GET /health", &handler.Health{})
	mux.Handle("POST /turns", &handler.Turn{Service: turnSvc})
	mux.Handle("POST /recall", &handler.Recall{})
	mux.Handle("POST /search", &handler.Search{})
	mux.Handle("GET /users/{user_id}/memories", &handler.Memories{DB: a.db})
	mux.Handle("DELETE /sessions/{session_id}", &handler.DeleteSession{DB: a.db})
	mux.Handle("DELETE /users/{user_id}", &handler.DeleteUser{DB: a.db})

	var h http.Handler = mux
	h = bodyLimitMiddleware(h)
	h = authMiddleware(h, a.cfg.MemoryAuthToken)
	h = recoveryMiddleware(h)
	return h
}
