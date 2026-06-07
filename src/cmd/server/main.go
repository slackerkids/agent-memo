package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
	"github.com/slackerkids/agent-memo.git/internal/app"
	"github.com/slackerkids/agent-memo.git/internal/db"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-health-check" {
		os.Exit(runHealthCheck())
	}

	_ = godotenv.Load()

	var cfg app.Config
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.OpenAIAPIKey == "" {
		log.Println("WARN: OPENAI_API_KEY not set — vector recall channel disabled")
	}

	if err := os.MkdirAll(dirOf(cfg.DBPath), 0o755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	application := app.New(&cfg, database)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      application.Router(),
		ReadTimeout:  65 * time.Second,
		WriteTimeout: 65 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

func runHealthCheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
