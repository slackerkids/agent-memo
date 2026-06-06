# CHANGELOG

All architecture and design decisions made before writing a line of code.
Each entry records what was decided, why, and what was ruled out.

---

## v0 — Initial stack decision

**Date:** 2025-06-06

**Decisions:**

Go as the implementation language. Commercial experience with it, backend-native,
strong concurrency primitives, compiles to a single static binary. No new language
overhead during a 2-day timebox.

SQLite as the backing store. Single file, no external process, no Docker
service dependency. WAL mode gives reliable concurrent reads with serialized
writes — correct behavior for a memory sidecar. Fits the single-container
eval setup exactly.

`mattn/go-sqlite3` as the SQLite driver over the alternatives:
- `ncruces/go-sqlite3` (WASM): no CGO, cleaner build, but WASM overhead
  and less mature ecosystem.
- `modernc.org/sqlite` (pure Go transpile): no CGO, but confirmed incompatible
  with `sqlite-vec` extension.
- Decision: CGO with `mattn/go-sqlite3`. Builder image `golang:1.22` has gcc.
  Runtime image `debian:bookworm-slim`. Slightly larger image, zero other friction.

`sqlite-vec` for vector search. Loaded as a CGO extension alongside mattn.
Gives KNN vector search inside the same SQLite file. No external vector DB needed.

SQLite FTS5 for keyword search. Built into SQLite, zero extra deps. Porter
stemmer tokenizer (`tokenize='porter unicode61'`) chosen over default so that
inflected forms ("pets" matches "pet", "remotely" matches "remote") work.

`pressly/goose` for migrations. SQL-file-based, simple, no ORM.
Migrations live in `src/migrations/`, run at startup.

`joho/godotenv` + `caarlos0/env` for config. godotenv loads `.env` in dev
(no-op in prod where env vars are injected). caarlos0/env parses env vars into
a typed Go struct with required/default tags. Ruled out Viper as overkill for
this scope.

OpenAI `text-embedding-3-small` for embeddings. 1536 dimensions, $0.02/1M tokens,
industry default, well-documented SDK, graceful no-op when `OPENAI_API_KEY` absent.

Deepseek API for LLM extraction. OpenAI-compatible endpoint, cheap, fast JSON
output. Primary extraction LLM. No local model (Ollama) to avoid Docker image
weight and cold-start latency.

Stdlib `net/http` (Go 1.22+) as the HTTP router. Path parameters built in since
1.22, zero external deps, sufficient for 7 endpoints.

No OpenAPI codegen. Adds setup friction with no quality benefit at this scope.
Handlers written by hand.

**Ruled out:**
- Postgres + pgvector: external process, extra container, network hop. Overkill
  for single-container eval.
- Dedicated vector DB (Qdrant, Weaviate): same problem, plus another service.
- Python/FastAPI: slower than Go for this workload; differentiating on stack
  is a small signal but worth it.

---

## v1 — Architecture: monolith with layered internal structure

**Date:** 2025-06-06

**Decision:**

Single container, single binary, monolith. No microservices.
The eval is a single-container setup; splitting services adds ops complexity
with zero correctness benefit.

Internal layered structure chosen over flat package layout:

```
src/
  cmd/server/main.go
  internal/
    handler/     -- HTTP layer, request parsing, response shaping
    service/     -- business logic, orchestration
    repository/  -- DB queries only, no logic
    gateway/     -- external API clients (Deepseek, OpenAI)
    app/         -- dependency injection, router wiring, middleware
  migrations/
  tests/
  fixtures/
```

`gateway/` chosen over `extractor/` + `embedder/` flat packages. Gateway is
a conventional term for external API client wrappers. Keeps external
dependencies in one named place. The service layer calls gateway clients;
gateway packages contain no business logic.

`repository/graph.go` added for knowledge graph queries (recursive CTEs).
Kept in repository layer since it is pure DB access, not service logic.

Singular package names throughout (`handler` not `handlers`, `service` not
`services`) — idiomatic Go convention.

**README and CHANGELOG at repo root.** Code lives under `src/`.
Dockerfile, docker-compose.yml, .env.example also at root.

---

## v2 — Memory model: slot catalog + knowledge graph hybrid

**Date:** 2025-06-06

**Problem with free-form key field:**

A free-form `key TEXT` field means the LLM can output "job", "employment",
"work", "company" for the same concept across different turns. Supersession
logic then requires fuzzy matching to detect sameness, which is fragile and
hard to test.

**Decision: predefined slot catalog**

Adopted a typed slot catalog (inspired by typed slot catalog patterns in
memory-system literature)
with three tiers:

- **Singletons** (one active row per user): name, location.current,
  location.previous, employment.company, employment.role,
  employment.previous_company, relationship.status, diet,
  response_style, language.
- **Collections** (multiple rows, keyed by `entity_key`): pet, allergy,
  family_member, hobby, opinion.
- **Unstructured** (`misc`): escape hatch for anything that doesn't fit.

LLM extraction prompt provides the catalog. Model picks a slot, not a free
key. Supersession logic is deterministic: same `user_id + slot + entity_key`
= same fact.

**Auto-chain on update:** writing a new `location.current` automatically
copies the old value to `location.previous`. Same for
`employment.company → employment.previous_company`.

**Correction semantics (`mutation=replace`):** when a user explicitly
retracts a wrong fact ("not Berlin — Munich"), auto-chain does NOT fire.
The wrong value was never historical. Old row gets `active=0, valid_until=now`
but is NOT written to `.previous`. The corrected value becomes the only
active row.

**Knowledge graph added alongside slot catalog:**

Two extra tables: `entity` (named nodes) and `edge` (typed relations with
temporal validity). Rationale: multi-hop recall is an explicit eval category.
The slot model alone cannot resolve "what city does the user with the dog
named Biscuit live in?" without a secondary retrieval pass.

With a graph, this is a recursive CTE in pure SQL — one query, no extra
retrieval round-trip. The implementation cost is low (2 tables + CTE query)
since SQLite already supports recursive CTEs.

Hybrid chosen (slots + graph) rather than graph-only because:
- Singleton slots give fast, deterministic profile fact retrieval with no
  graph traversal needed.
- Graph handles entity relationships and multi-hop queries naturally.
- Not either/or — they serve different retrieval paths.

**Ruled out:**
- Graph-only (e.g. all facts as edges): adds traversal cost to simple profile
  lookups that don't need it.
- Pure slot model without graph: requires secondary retrieval pass for
  multi-hop, which is slower and less clean.

---

## v3 — Temporal model: valid_from / valid_until / stated_at

**Date:** 2025-06-06

**Problem:**

`active=0` on superseded rows loses when the fact stopped being true.
You cannot reconstruct a timeline from a boolean.

**Decision:**

Three timestamps on every memory and edge:

- `stated_at` — when the user made the statement (from turn timestamp).
  Never changes.
- `valid_from` — when the fact became true. May differ from `stated_at`:
  "I moved here last month" → `valid_from` is ~30 days before `stated_at`.
  Populated by the LLM from the evidence text when possible.
- `valid_until` — when the fact stopped being true. Set on supersession to
  the timestamp of the new turn. NULL means currently active.

This gives a reconstructable user history timeline. "User lived in NYC
(valid_from=?, valid_until=2025-03-10). Moved to Berlin (valid_from=2025-02-10,
stated_at=2025-03-10, valid_until=NULL)."

Opinion arc: two opinion rows for the same topic are both preserved with
their own `valid_from / valid_until`. The arc is queryable without additional
schema. This closes the gap that a boolean-only active flag leaves open.

---

## v4 — Retrieval: hybrid BM25 + vector + graph with RRF

**Date:** 2025-06-06

**Decision:**

Four retrieval channels fused via Reciprocal Rank Fusion (RRF, k=60):

- **A. Profile facts** — unconditional load of active singleton slots.
  Always rendered first. Never cut by token budget.
- **B. FTS5 BM25** — keyword match with Porter stemmer. Catches exact names
  ("Biscuit", "Notion"), handles inflection.
- **C. Vector KNN** — semantic similarity via sqlite-vec. Catches paraphrase
  ("significant other" matches "partner"), semantic relatedness.
- **D. Graph recursive CTE** — 2-hop traversal. Multi-hop queries resolved
  in SQL, no second retrieval pass.

RRF chosen over weighted score averaging because it requires no calibration
of score scales across channels and degrades cleanly: when a channel is empty
(missing API key, extension not loaded), fusion over remaining channels is
mathematically equivalent to ranking by those channels alone. Service never
crashes on missing keys.

Final score formula:
```
final = 0.65 * rrf_norm + 0.20 * recency + 0.10 * confidence + 0.05 * active_boost
```
Recency decays linearly over 90 days.

Noise guard: return empty context when no profile facts exist AND top
retrieval score < 0.15. Prevents hallucinated context on cold sessions.

**Token budget triage (tight budget):**
1. Profile facts — never cut
2. Query-relevant memories by RRF score — cut from bottom up
3. Graph context
4. Recent session turns — dropped first

Token approximation: `len(text) / 4`. Hard cap at `max_tokens`.

**Ruled out:**
- Vanilla cosine-only: explicitly called out in challenge as insufficient.
  Misses keyword-heavy queries like "what's the dog's name?".
- BM25-only: misses paraphrase and semantic queries entirely.
- Secondary retrieval pass for multi-hop: works but adds latency and code
  complexity. Graph CTE is cleaner.

---

## v5 — Infrastructure: indexes, goose, auth middleware

**Date:** 2025-06-06

**Indexes:**

All indexes defined upfront in `migrations/002_indexes.sql`. High query volume
expected on recall path. Key indexes:

- `memory(user_id, active)` — primary recall filter
- `memory(user_id, slot, active)` — singleton lookup
- `memory(user_id, slot, entity_key, active)` — collection slot lookup
- `memory(valid_until)` — timeline queries
- `edge(source_id, relation)` + `edge(target_id, relation)` — graph traversal
- `entity(user_id, name)` — entity lookup by name

**Migrations (goose):**

`pressly/goose` runs SQL migrations at service startup. Two files:
`001_initial.sql` creates all tables and virtual tables.
`002_indexes.sql` creates all indexes.
SQL-file-based, no ORM dependency, easy to review and extend.

**Auth middleware:**

Optional Bearer token auth. If `MEMORY_AUTH_TOKEN` env var is set, middleware
validates `Authorization: Bearer <token>` header on every request and returns
401 on mismatch. If env var is empty or unset, middleware is a no-op.
Implemented in `app/app.go` as a wrapping handler. ~15 lines.
Documented in `.env.example` as an optional, commented-out variable.

**Collection entity dedup:**

Before inserting a collection memory (pet, family_member, etc.), fetch all
active `entity_key` values for that `(user_id, slot)` and compare via
normalized string matching (lowercase, trim, optional Levenshtein distance
for typo tolerance). If match found, treat as update to existing entity,
preserve canonical key. Prevents "mylo", "mylo the cat", "my cat mylo"
from creating three separate rows for one cat.

---

## v6 — SQLite tuning: WAL mode, pragmas, connection pool

**Date:** 2025-06-06

**Problem:**

Default SQLite journal mode (DELETE) causes readers to block writers and
vice versa. With concurrent HTTP requests this creates contention on every
recall request that arrives while a turn is being ingested.

**Decision:**

Enable WAL mode and set pragmas on every DB open in `db/db.go`:

```
PRAGMA journal_mode = WAL      -- persistent in file header after first set
PRAGMA synchronous = normal    -- safe in WAL, avoids fsync on every commit
PRAGMA temp_store = memory     -- temp tables in RAM
PRAGMA cache_size = -32000     -- 32MB page cache
PRAGMA foreign_keys = ON
_busy_timeout=5000             -- in DSN: wait up to 5s on locked write
```

`db.SetMaxOpenConns(1)` to serialize writes and prevent SQLITE_BUSY.
WAL allows unlimited concurrent readers alongside the single writer.

---

## v7 — Graceful shutdown + request timeouts

**Date:** 2025-06-06

**Problem:**

`docker compose down` sends SIGTERM. If a write is in progress, an abrupt
exit risks a corrupt WAL. The eval harness also has a 60-second timeout
on `/turns` — a hung Deepseek call must not block the process.

**Decision:**

`signal.NotifyContext` in `main.go` catches SIGTERM/SIGINT, triggers
`srv.Shutdown(10s context)` which drains in-flight requests, then closes
the DB cleanly. `http.Server` configured with `ReadTimeout: 65s` and
`WriteTimeout: 65s`. Per-handler context passed to all gateway calls
(Deepseek, OpenAI) so hung external calls time out cleanly.

---

## v8 — Concurrency: parallel embeddings, concurrent recall channels

**Date:** 2025-06-06

**Problem:**

`/turns` calls Deepseek (extraction) then embeds each extracted memory
sequentially. With 5+ memories per turn, serial embedding adds meaningful
latency within the 60s window.

`/recall` runs FTS5 and vector KNN sequentially even though they are
independent read-only queries.

**Decision:**

Inside `/turns`: after extraction returns candidates, embed each one
concurrently using `golang.org/x/sync/errgroup`. Batch-write all vectors
after all embeddings complete. Reduces embedding latency from O(N) serial
to O(1) parallel.

Inside `/recall`: FTS5 query and vector KNN query run in separate goroutines,
results merged via channel. Graph CTE runs after (needs entity names from
first-pass results). WAL mode makes concurrent reads safe.

---

## v9 — Error handling, FTS5 sync, embedding fallback logging

**Date:** 2025-06-06

**Problem:**

Three small things that get noticed in review:
1. Inconsistent error response shapes across handlers.
2. FTS5 content tables don't auto-sync — manual dual-write required.
3. When `OPENAI_API_KEY` is absent, vector channel silently degrades
   with no indication to the operator.

**Decision:**

1. `handler/error.go` — shared helper that writes `{"error":"msg"}` with
   the correct status code. All handlers use it. No handler writes raw
   `http.Error()` directly.
2. Every `INSERT`/`UPDATE` on `memory` explicitly inserts into `memory_fts`.
   Encapsulated in `repository/memory.go` so no caller can forget.
3. At startup, if `OPENAI_API_KEY` is empty, log
   `WARN: OPENAI_API_KEY not set — vector recall channel disabled`.
   Vector channel skips silently at runtime with no error.

---

## v10 — Healthcheck, request body limit, UUID, cross-session recall
 
**Date:** 2025-06-07
 
**Healthcheck in scratch:**
 
`scratch` has no curl, wget, or shell. The main binary accepts a
`-health-check` CLI flag: it GETs `http://localhost:8080/health` and exits
0 on 200, 1 otherwise. `docker-compose.yml` uses exec form (no shell):
`CMD ["/memory-service", "-health-check"]`. One binary, no sidecar.
 
**Request body size limit:**
 
Every handler wraps the request body with `http.MaxBytesReader(w, r.Body, 1<<20)`
(1MB). An oversized payload returns 413 rather than potentially OOMing the
process. Encapsulated in a middleware so no handler forgets.
 
**UUID for all IDs:**
 
All primary keys (turn.id, memory.id, entity.id, edge.id) generated via
`github.com/google/uuid`. Deterministic, unique, no collision risk.
Do not use timestamps or math/rand as IDs.
 
**Cross-session recall:**
 
Memories are user-scoped, not session-scoped. `/recall` filters by `user_id`
only, not `session_id`. This is required: the eval smoke test ingests on
`session_id: smoke-1` and recalls on `session_id: smoke-2`. If recall filters
by session, the smoke test fails immediately. Documented in README.
 
**`stated_at` from turn timestamp:**
 
All memories extracted from a turn use the turn's `timestamp` field as
`stated_at`, not `time.Now()`. This preserves the user's actual timeline
even if extraction runs with latency.
 
**`/users/{user_id}/memories` returns all rows including superseded:**
 
`active` filter NOT applied on this endpoint. Returns full history with
`active: true/false` on each row. Required for eval to inspect supersession
chain. `/recall` and `/search` still filter `active=1` only.
 
**`/search` vs `/recall` formatters are separate:**
 
`/recall` returns formatted prose + citations (for agent prompt injection).
`/search` returns structured result array (for agent tool call).
They share the retrieval pipeline but have separate response formatters.
`max_tokens` on `/recall` is enforced — small values (256, 512) tested by eval.
 
---
 
## v11 — Go 1.26, FTS5 dual-write encapsulated
 
**Date:** 2025-06-07
 
**Go version:** Updated to Go 1.26 (released February 2026, current stable).
Dockerfile builder stage: `golang:1.26-alpine`. go.mod: `go 1.26`.

**FTS5 dual-write encapsulated in repository layer:**
 
FTS5 content tables do not auto-sync. Every INSERT on `memory` must be
accompanied by an INSERT on `memory_fts` in the same transaction, and every
supersession (marking `active=0`) must DELETE from `memory_fts`.
 
All memory writes are funneled through a single `repository.InsertMemory`
function that wraps both writes in one transaction. No handler or service
code writes to `memory` directly. This makes the dual-write structurally
impossible to forget — if you call the wrong thing, it won't compile.
 
**Synchronous guarantee formally documented:**
 
The full pipeline — extraction, supersession, memory insert, FTS5 sync,
embedding, vector write — must complete before writing the 201 response.
No goroutines that outlive the request. Parallel embedding calls via
`errgroup` are allowed (they block the handler until all complete).
The rule: if it happens after `w.WriteHeader(201)`, it violates the guarantee.

---

## v12 — Pre-build alignment: triggers, healthcheck, schema canonicalization

**Date:** 2025-06-07

**What changed:**

1. **FTS5 sync via SQLite triggers** — supersedes v9/v11 manual dual-write in Go.
   `AFTER INSERT/UPDATE/DELETE` triggers in `001_initial.sql` keep `memory_fts`
   in sync atomically. `InsertMemory` is a plain INSERT; `DeactivateMemory` is
   a plain UPDATE. `evidence` column added to `memory` and indexed in FTS5.

2. **`memory_vec` schema** — `memory_rowid INTEGER PRIMARY KEY` joins on
   `memory.rowid` instead of `memory_id TEXT`.

3. **Healthcheck** — `-health-check` CLI flag on main binary (see v10 update).
   No separate `httpget` sidecar.

4. **RRF weights canonicalized** — `0.65 / 0.20 / 0.10 / 0.05` (CHANGELOG v4).
   PLAN.md code blocks updated to match.

5. **Go version aligned** — `go 1.26.3` in go.mod, `golang:1.26-alpine` in
   Dockerfile.

**Next:** Implement per PLAN.md build order starting at `db/db.go` + `main.go`.
