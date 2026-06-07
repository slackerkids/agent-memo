-- +goose Up
CREATE TABLE turn (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  user_id    TEXT,
  messages   TEXT NOT NULL,
  timestamp  TEXT NOT NULL,
  metadata   TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE memory (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  turn_id     TEXT NOT NULL REFERENCES turn(id),
  slot        TEXT NOT NULL,
  entity_key  TEXT,
  value       TEXT NOT NULL,
  confidence  REAL NOT NULL,
  evidence    TEXT,
  active      INTEGER NOT NULL DEFAULT 1,
  supersedes  TEXT REFERENCES memory(id),
  mutation    TEXT NOT NULL DEFAULT 'upsert',
  stated_at   TEXT NOT NULL,
  valid_from  TEXT,
  valid_until TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE entity (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE edge (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL,
  source_id   TEXT NOT NULL REFERENCES entity(id),
  target_id   TEXT NOT NULL REFERENCES entity(id),
  relation    TEXT NOT NULL,
  fact        TEXT,
  confidence  REAL,
  stated_at   TEXT NOT NULL,
  valid_from  TEXT,
  valid_until TEXT,
  created_at  TEXT NOT NULL
);

CREATE VIRTUAL TABLE memory_fts USING fts5(
  slot,
  value,
  evidence,
  content='memory',
  content_rowid='rowid',
  tokenize='porter unicode61'
);

CREATE VIRTUAL TABLE memory_vec USING vec0(
  memory_rowid INTEGER PRIMARY KEY,
  embedding FLOAT[1536]
);

-- FTS5 sync triggers — automatic, no manual dual-write in Go
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memory_ai
    AFTER INSERT ON memory BEGIN
        INSERT INTO memory_fts(rowid, slot, value, evidence)
        VALUES (new.rowid, new.slot, new.value, new.evidence);
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memory_ad
    AFTER DELETE ON memory BEGIN
        INSERT INTO memory_fts(memory_fts, rowid, slot, value, evidence)
        VALUES ('delete', old.rowid, old.slot, old.value, old.evidence);
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memory_au
    AFTER UPDATE ON memory BEGIN
        INSERT INTO memory_fts(memory_fts, rowid, slot, value, evidence)
        VALUES ('delete', old.rowid, old.slot, old.value, old.evidence);
        INSERT INTO memory_fts(rowid, slot, value, evidence)
        VALUES (new.rowid, new.slot, new.value, new.evidence);
    END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS memory_au;
DROP TRIGGER IF EXISTS memory_ad;
DROP TRIGGER IF EXISTS memory_ai;
DROP TABLE IF EXISTS memory_vec;
DROP TABLE IF EXISTS memory_fts;
DROP TABLE IF EXISTS edge;
DROP TABLE IF EXISTS entity;
DROP TABLE IF EXISTS memory;
DROP TABLE IF EXISTS turn;
