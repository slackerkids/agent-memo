package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/pressly/goose/v3"
	"github.com/slackerkids/agent-memo.git/migrations"

	_ "github.com/mattn/go-sqlite3"
)

func Open(path string) (*sql.DB, error) {
	sqlite_vec.Auto()

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", path)
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = normal",
		"PRAGMA temp_store = memory",
		"PRAGMA cache_size = -32000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := database.Exec(p); err != nil {
			database.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	database.SetMaxOpenConns(1)

	if err := verifyVec(database); err != nil {
		log.Printf("WARN: sqlite-vec not available: %v — vector recall channel disabled", err)
	}

	if err := runMigrations(database); err != nil {
		database.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return database, nil
}

func verifyVec(database *sql.DB) error {
	var version string
	return database.QueryRow("SELECT vec_version()").Scan(&version)
}

func runMigrations(database *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(database, ".")
}

// VecEnabled reports whether sqlite-vec functions are available.
func VecEnabled(database *sql.DB) bool {
	return verifyVec(database) == nil
}

// EnsureDBDir is a no-op helper for callers that want to mkdir parent paths.
func EnsureDBDir(path string) error {
	_ = path
	return nil
}

// SubFS is unused but kept for tests that may need filtered migration FS.
func SubFS(fsys fs.FS, dir string) (fs.FS, error) {
	return fs.Sub(fsys, dir)
}
