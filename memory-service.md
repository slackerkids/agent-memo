# Higgsfield Memory Service — Project Tracker

## Stack

| Component | Choice |
|---|---|
| Language | Go 1.26 |
| HTTP Router | stdlib `net/http` (Go 1.22+ path params) |
| SQLite Driver | `mattn/go-sqlite3` (CGO) |
| Vector Search | `sqlite-vec` CGO binding |
| FTS Keyword Search | SQLite FTS5, Porter stemmer |
| DB Migrations | `pressly/goose` |
| Config / Env | `joho/godotenv` + `caarlos0/env` |
| LLM Extraction | Deepseek API (OpenAI-compatible) |
| Embeddings | OpenAI `text-embedding-3-small` (1536-dim) |
| Architecture | Monolith, single container |
| Persistence | Docker named volume → `/data/memory.db` |

**Docker:** Builder `golang:1.26-alpine` + gcc + musl-dev → static binary via
`-extldflags "-static"` → runtime `scratch`. CA certs + tzdata copied across.
Image ≈ 20-30MB.

---

## File Structure

```
memory-service/
├── README.md
├── CHANGELOG.md
├── Dockerfile
├── docker-compose.yml
├── .env.example
└── src/
    ├── go.mod
    ├── go.sum
    ├── cmd/
    │   └── server/
    │       └── main.go          -- signal handling, graceful shutdown, server boot
    ├── internal/
    │   ├── app/
    │   │   └── app.go           -- deps wiring, router setup, middleware chain
    │   ├── handler/
    │   │   ├── health.go
    │   │   ├── turn.go
    │   │   ├── recall.go
    │   │   ├── search.go
    │   │   ├── memory.go
    │   │   ├── cleanup.go
    │   │   └── error.go         -- shared JSON error helper {"error":"msg"}
    │   ├── service/
    │   │   ├── slots.go         -- slot catalog definitions
    │   │   ├── turn.go          -- ingestion + extraction orchestration
    │   │   ├── recall.go        -- hybrid retrieval + RRF + context assembly
    │   │   └── memory.go        -- slot logic, fact evolution, supersession
    │   ├── repository/
    │   │   ├── turn.go
    │   │   ├── memory.go
    │   │   └── graph.go         -- entity + edge queries, recursive CTEs
    │   ├── gateway/
    │   │   ├── extractor.go     -- Deepseek API client + extraction prompt
    │   │   └── embedder.go      -- OpenAI embedding API client
    │   └── db/
    │       └── db.go            -- sql.Open, pragmas, goose migrations, connection pool
    ├── migrations/
    │   ├── 001_initial.sql      -- all tables + virtual tables
    │   └── 002_indexes.sql      -- all indexes
    ├── tests/
    │   ├── contract_test.go     -- endpoint shapes, status codes
    │   ├── persistence_test.go  -- write → restart → read
    │   ├── concurrent_test.go   -- two users, no bleed
    │   ├── malformed_test.go    -- bad JSON, missing fields, unicode
    │   └── quality_test.go      -- fixture runner, recall score
    └── fixtures/
        └── conversations.yaml   -- 8 scenarios, 20+ probes
```

---

## HTTP Contract

| Method | Path | Status |
|---|---|---|
| GET | /health | 200 |
| POST | /turns | 201 `{"id":"..."}` |
| POST | /recall | 200 `{"context":"...","citations":[...]}` |
| POST | /search | 200 `{"results":[...]}` |
| GET | /users/{user_id}/memories | 200 `{"memories":[...]}` |
| DELETE | /sessions/{session_id} | 204 |
| DELETE | /users/{user_id} | 204 |

**Error shape** (all 4xx/5xx): `{"error": "human readable message"}`

**Auth middleware:** if `MEMORY_AUTH_TOKEN` is set → validate
`Authorization: Bearer <token>` → 401 on mismatch. If unset → no-op.

**Session vs user delete:**
- `/sessions/{id}` — removes turns only. Memories are user-scoped, persist.
- `/users/{id}` — removes everything: turns, memories, entities, edges.

**Request timeouts:** `http.Server` with `ReadTimeout: 65s`, `WriteTimeout: 65s`
(eval harness has 60s timeout on `/turns`). Per-handler context passed to
gateway calls so hung LLM calls don't block forever.

---

## SQLite Setup (db/db.go)

```go
db, _ := sql.Open("sqlite3", "file:/data/memory.db?_busy_timeout=5000")

// Run once after open — WAL persists in file header
db.Exec(`PRAGMA journal_mode = WAL`)
db.Exec(`PRAGMA synchronous = normal`)   // safe in WAL, much faster than full
db.Exec(`PRAGMA temp_store = memory`)
db.Exec(`PRAGMA cache_size = -32000`)    // 32MB page cache
db.Exec(`PRAGMA foreign_keys = ON`)

// Single writer to avoid SQLITE_BUSY on concurrent writes
db.SetMaxOpenConns(1)

// Run goose migrations
goose.SetBaseFS(migrationsFS)
goose.Up(db, ".")
```

WAL mode: readers never block writers, writers never block readers.
`_busy_timeout=5000` — wait up to 5s on a locked write before returning error.

---

## Database Schema (migrations/001_initial.sql)

```sql
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
  slot, value, evidence,
  content='memory',
  content_rowid='rowid',
  tokenize='porter unicode61'
);

CREATE VIRTUAL TABLE memory_vec USING vec0(
  memory_rowid INTEGER PRIMARY KEY,
  embedding FLOAT[1536]
);
```

**FTS5 sync — handled by SQLite triggers in `001_initial.sql`:**

FTS5 `content=` tables are NOT auto-synced by default. Instead of manual
dual-write in Go code, use `AFTER INSERT/DELETE/UPDATE` triggers defined
in `migrations/001_initial.sql`. The triggers fire inside the same
transaction as the memory write — atomically, automatically, with zero
Go code that can forget to sync.

```sql
CREATE TRIGGER IF NOT EXISTS memory_ai
    AFTER INSERT ON memory BEGIN
        INSERT INTO memory_fts(rowid, slot, value, evidence)
        VALUES (new.rowid, new.slot, new.value, new.evidence);
    END;

CREATE TRIGGER IF NOT EXISTS memory_ad
    AFTER DELETE ON memory BEGIN
        INSERT INTO memory_fts(memory_fts, rowid, slot, value, evidence)
        VALUES ('delete', old.rowid, old.slot, old.value, old.evidence);
    END;

CREATE TRIGGER IF NOT EXISTS memory_au
    AFTER UPDATE ON memory BEGIN
        INSERT INTO memory_fts(memory_fts, rowid, slot, value, evidence)
        VALUES ('delete', old.rowid, old.slot, old.value, old.evidence);
        INSERT INTO memory_fts(rowid, slot, value, evidence)
        VALUES (new.rowid, new.slot, new.value, new.evidence);
    END;
```

`DeactivateMemory` is now just an `UPDATE memory SET active=0` — the
`memory_au` trigger automatically re-syncs `memory_fts`. No rowid fetch,
no manual FTS delete in Go. `InsertMemory` is a plain INSERT.

`memory_fts` also indexes `evidence` (verbatim quote) — enables keyword
search on the exact phrase the user said, not just the extracted value.

---

**Synchronous guarantee — explicit rule:**

The challenge states: *"After `POST /turns` returns, the ingested data and
extracted memories must be immediately available via `/recall`."*

Everything completes inside one transaction before the 201 is written:

```
receive POST /turns
    → validate request
    → open single tx
    → store raw turn
    → call Deepseek extraction (blocking, within tx context)
    → for each memory candidate:
        → supersede old memory if exists (UPDATE → memory_au trigger fires)
        → insert new memory (INSERT → memory_ai trigger fires)
        → insert entity + edge rows
    → call OpenAI EmbedBatch (batched, blocking, one API call)
    → write all vectors to memory_vec
    → tx.Commit()   ← single commit, all or nothing
    → write 201 {"id": "..."}   ← nothing async after this point
```

Zero goroutines that outlive the request. The batched embed call is
blocking. If embedding fails, log and skip — the memory row is still
committed and queryable via BM25/graph.

---

## Indexes (migrations/002_indexes.sql)

```sql
-- +goose Up
CREATE INDEX idx_memory_user_active   ON memory(user_id, active);
CREATE INDEX idx_memory_user_slot     ON memory(user_id, slot, active);
CREATE INDEX idx_memory_user_entity   ON memory(user_id, slot, entity_key, active);
CREATE INDEX idx_memory_turn          ON memory(turn_id);
CREATE INDEX idx_memory_valid_until   ON memory(valid_until);

CREATE INDEX idx_turn_session         ON turn(session_id);
CREATE INDEX idx_turn_user            ON turn(user_id);

CREATE INDEX idx_entity_user_name     ON entity(user_id, name);
CREATE INDEX idx_entity_user_type     ON entity(user_id, type);

CREATE INDEX idx_edge_source          ON edge(source_id, relation);
CREATE INDEX idx_edge_target          ON edge(target_id, relation);
CREATE INDEX idx_edge_user_valid      ON edge(user_id, valid_until);
```

---

## Slot Catalog

Defined as `SlotDef` struct in `internal/service/slots.go` with `PreviousSlot`
field encoding the auto-chain relationship directly on the definition.

**Singletons** — one active row per user, new value supersedes old:

| Slot | Auto-chains to on upsert |
|---|---|
| identity.name | — |
| identity.age | — |
| identity.pronouns | — |
| location.current | location.previous |
| location.previous | — |
| location.hometown | — |
| employment.current_company | employment.previous_company |
| employment.current_role | — |
| employment.previous_company | — |
| relationship.partner | — |
| preference.response_style | — |
| preference.communication_style | — |
| preference.diet | — |

**Collections** — multiple rows per user, keyed by `entity_key`:

| Slot | Example entity_key | Example value |
|---|---|---|
| pet | biscuit | golden retriever |
| family_member | mom | lives in Almaty |
| restriction.allergy | shellfish | causes anaphylaxis |
| skill.using | go | backend services |
| preference.food | sushi | loves it |
| opinion.topic | typescript | finds generics annoying |
| project.current | reelka | AI video platform |
| event.upcoming | wedding | sister's wedding in June |

**Unstructured:** slot name `"unstructured"` — escape hatch, always inserted,
FTS-indexed, no supersession logic.

**Correction semantics (`mutation=replace`):** old row → `active=0,
valid_until=now`. Auto-chain to `.previous` does NOT fire.
Wrong facts are not history.

---

## Temporal Model

Three timestamps on every memory and edge:

- `stated_at` — from the turn timestamp. When the user said it.
- `valid_from` — when the fact became true. LLM populates from evidence
  ("moved last month" → ~30 days before `stated_at`). Null if unknown.
- `valid_until` — set on supersession. Null = currently active.

---

## Extraction Pipeline

**Flow:**
1. `handler/turn.go` receives POST, validates shape, calls `service/turn.go`
2. `service/turn.go` opens a single transaction, persists raw turn
3. `gateway/extractor.go` — blocking Deepseek call → JSON memory candidates
4. For each candidate: slot validation → fact evolution check → DB write
   (FTS5 sync handled automatically by SQLite triggers)
5. `gateway/embedder.go` — batched/parallel OpenAI embed `slot: value` →
   store in `memory_vec`
6. Transaction commits, then 201 response

**Concurrency inside `/turns`:**
Extraction (Deepseek) is the slow call. Once candidates come back, embed each
one concurrently using an `errgroup` — N embedding calls in parallel, then
batch-write all vectors. Cuts embedding latency from serial to parallel.

**LLM output per fact:**
```json
{
  "slot": "location.current",
  "entity_key": null,
  "value": "Berlin",
  "confidence": 0.95,
  "mutation": "upsert",
  "valid_from": "2025-02-10",
  "evidence": "I moved to Berlin from NYC last month",
  "entities": [{"name": "Berlin", "type": "location"}],
  "edges": [{"source": "user", "relation": "LIVES_IN",
             "target": "Berlin", "valid_from": "2025-02-10"}]
}
```

**Entity dedup for collections:** before inserting, fetch active entity_keys
for `(user_id, slot)`, compare via normalized string match (lowercase+trim +
optional Levenshtein). Match → update existing row, preserve canonical key.

---

## Recall Strategy (POST /recall)

**Four channels:**

**A. Profile facts** — active singleton slots loaded unconditionally.
Never cut by token budget. Rendered first.

**B. FTS5 BM25** — Porter-stemmed, OR-joined query → up to 20 rows.

**C. Vector KNN** — query embedded → sqlite-vec KNN → up to 20 rows.
If `OPENAI_API_KEY` absent: warn at startup, skip channel silently.

**D. Graph recursive CTE** — 2-hop traversal, resolves multi-hop in SQL:
```sql
WITH RECURSIVE traverse(entity_id, depth) AS (
  SELECT target_id, 1 FROM edge
  WHERE source_id = (SELECT id FROM entity WHERE user_id=? AND name LIKE ?)
    AND valid_until IS NULL
  UNION ALL
  SELECT e.target_id, t.depth + 1
  FROM edge e JOIN traverse t ON e.source_id = t.entity_id
  WHERE t.depth < 2 AND e.valid_until IS NULL
)
SELECT name, type FROM entity WHERE id IN (SELECT entity_id FROM traverse);
```

**Fusion:** RRF (k=60) over B+C+D, then:
```
final = 0.65 * rrf_norm + 0.20 * recency + 0.10 * confidence + 0.05 * active_boost
```

**Token budget triage:**
1. Profile facts — never cut
2. RRF-ranked memories — cut from bottom
3. Graph context
4. Recent session turns — cut first

**Noise guard:** empty context if no profile facts AND top score < 0.15.
Token approximation: `len(text) / 4`. Hard cap at `max_tokens`.

---

## Config (.env.example)

```bash
PORT=8080
DB_PATH=/data/memory.db

# Required for LLM extraction
DEEPSEEK_API_KEY=sk-...
DEEPSEEK_BASE_URL=https://api.deepseek.com

# Optional — disables vector channel if absent (logged as warning at startup)
OPENAI_API_KEY=sk-...

# Optional — skips auth entirely if unset
MEMORY_AUTH_TOKEN=
```

---

## Graceful Shutdown (main.go)

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{
    Addr:         fmt.Sprintf(":%d", cfg.Port),
    Handler:      app.Router(),
    ReadTimeout:  65 * time.Second,
    WriteTimeout: 65 * time.Second,
    IdleTimeout:  120 * time.Second,
}

go srv.ListenAndServe()
<-ctx.Done()

shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)
db.Close()
```

---

## Concurrency Strategy

| Where | What | Approach |
|---|---|---|
| `/turns` extraction | Deepseek call | single call, wait for candidates |
| `/turns` embedding | N embeddings after extraction | `errgroup` parallel calls |
| `/recall` read channels | FTS5 + vector KNN | concurrent goroutines, merge results |
| `/recall` graph | CTE (needs entity names from above) | after first two channels |
| Concurrent sessions | multiple users hitting `/turns` | SQLite WAL handles read concurrency; writes serialize via `MaxOpenConns(1)` |

---

## Build Order

1. Shell scaffold: create all dirs and stub files
2. `go.mod` init, add all dependencies
3. Dockerfile + docker-compose verified: `/health` returns 200
4. `db/db.go`: sql.Open, pragmas, WAL, goose migrations
5. `migrations/001_initial.sql` + `002_indexes.sql`
6. Config struct (`caarlos0/env` + `godotenv`)
7. Auth middleware
8. `POST /turns` → store raw turn only
9. `GET /users/{user_id}/memories` → empty list
10. `gateway/extractor.go` → Deepseek client + prompt
11. `service/memory.go` → slot validation, fact evolution, supersession, temporal fields
12. `gateway/embedder.go` → OpenAI client, startup warning if key absent
13. FTS5 triggers verified (automatic sync on memory insert/update)
14. sqlite-vec write on memory insert
15. Knowledge graph: entity + edge write
16. `POST /recall` → channels A+B+C+D → RRF → token budget → format
17. `POST /search`
18. `DELETE` endpoints
19. Graceful shutdown wiring
20. Tests + fixture runner (run from step 10 onward)
21. README + CHANGELOG polish

---

## Eval Checklist

- [ ] Recall quality
- [ ] Fact evolution: supersession, valid_until, auto-chain, correction
- [ ] Multi-hop: graph recursive CTE
- [ ] Noise resistance: noise guard, cold sessions
- [ ] Extraction quality: typed slots, implied facts
- [ ] Persistence: named volume, WAL, graceful shutdown
- [ ] Cross-session scoping: session delete ≠ user delete
- [ ] Robustness: malformed input, missing keys, graceful degradation
- [ ] Synchronous correctness: after /turns returns, data queryable immediately
- [ ] Contract compliance: 7 endpoints, shapes, status codes
- [ ] Error responses: consistent `{"error":"..."}` shape