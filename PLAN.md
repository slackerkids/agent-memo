# Implementation Plan

This document is the Cursor briefing. Read it before touching any file.

---

## Constants (internal/service/recall.go + memory.go)

```go
const (
    LowConfidenceThreshold = 0.45
    LowScoreThreshold      = 0.15
    EntityFuzzyThreshold   = 88   // normalized Levenshtein, 0-100
    RRFk                   = 60
    RecencyDecayDays       = 90
    VectorOverfetch        = 3    // fetch 3× limit from vec index, filter in Go
    MaxBodyBytes           = 1 << 20 // 1MB request body limit
)
```

---

## Structs / Types

### internal/handler/turn.go
```go
type TurnRequest struct {
    SessionID string            `json:"session_id"`
    UserID    *string           `json:"user_id"`
    Messages  []Message         `json:"messages"`
    Timestamp string            `json:"timestamp"` // ISO-8601
    Metadata  map[string]any    `json:"metadata"`
}

type Message struct {
    Role    string  `json:"role"`    // user | assistant | tool
    Content string  `json:"content"`
    Name    *string `json:"name"`    // tool messages only
}

type TurnResponse struct {
    ID string `json:"id"`
}
```

### internal/handler/recall.go
```go
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
```

### internal/handler/search.go
```go
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
```

### internal/handler/memory.go
```go
type UserMemoriesResponse struct {
    Memories []MemoryRecord `json:"memories"`
}

type MemoryRecord struct {
    ID            string   `json:"id"`
    Type          string   `json:"type"`
    Key           string   `json:"key"`           // = slot, for API compat
    Value         string   `json:"value"`
    Confidence    float64  `json:"confidence"`
    SourceSession string   `json:"source_session"`
    SourceTurn    string   `json:"source_turn"`
    CreatedAt     string   `json:"created_at"`
    UpdatedAt     string   `json:"updated_at"`
    Supersedes    *string  `json:"supersedes"`
    Active        bool     `json:"active"`
    Slot          string   `json:"slot"`
    EntityKey     string   `json:"entity_key"`
    Evidence      string   `json:"evidence"`
}
```

### internal/service/memory.go (core domain type)
```go
type Memory struct {
    ID            string
    UserID        string
    SessionID     string
    TurnID        string
    Type          string   // fact | preference | opinion | event
    Slot          string   // from slot catalog
    EntityKey     string   // for collection slots; "" for singletons
    Value         string
    Confidence    float64
    Evidence      string
    Mutation      string   // upsert | replace | negate
    Active        bool
    Supersedes    *string
    StatedAt      string   // from turn timestamp
    ValidFrom     *string  // LLM-populated when available
    ValidUntil    *string  // set on supersession
    CreatedAt     string
    UpdatedAt     string
}
```

### internal/gateway/extractor.go (LLM response)
```go
type ExtractionResult struct {
    Memories []ExtractedMemory `json:"memories"`
    Entities []ExtractedEntity `json:"entities"`
    Edges    []ExtractedEdge   `json:"edges"`
}

type ExtractedMemory struct {
    Type      string   `json:"type"`
    Slot      string   `json:"slot"`
    EntityKey string   `json:"entity_key"`
    Value     string   `json:"value"`
    Confidence float64 `json:"confidence"`
    Evidence  string   `json:"evidence"`
    Mutation  string   `json:"mutation"`   // upsert | replace | negate
    ValidFrom *string  `json:"valid_from"` // optional ISO date from LLM
}

type ExtractedEntity struct {
    Name string `json:"name"`
    Type string `json:"type"` // person | location | org | pet | concept
}

type ExtractedEdge struct {
    Source    string  `json:"source"` // entity name or "user"
    Relation  string  `json:"relation"`
    Target    string  `json:"target"`
    ValidFrom *string `json:"valid_from"`
}
```

---

## Slot Catalog (internal/service/slots.go)

Slot catalog definition. Use a `SlotDef` struct so the previous_slot relationship
lives on the definition itself — no separate map to keep in sync.

```go
type SlotTier string

const (
    TierSingleton    SlotTier = "singleton"
    TierCollection   SlotTier = "collection"
    TierUnstructured SlotTier = "unstructured"
)

type SlotDef struct {
    Slot         string
    Tier         SlotTier
    Description  string
    PreviousSlot string // non-empty only for singletons with auto-chain
}

// SlotCatalog is the single source of truth.
var SlotCatalog = []SlotDef{
    // ── Singletons ──────────────────────────────────────────────────────────
    {"identity.name",                   TierSingleton, "person's full name", ""},
    {"identity.age",                    TierSingleton, "person's age", ""},
    {"identity.pronouns",               TierSingleton, "preferred pronouns", ""},
    {"location.current",                TierSingleton, "current city/country of residence", "location.previous"},
    {"location.previous",               TierSingleton, "previous location (auto-set on supersession)", ""},
    {"location.hometown",               TierSingleton, "hometown", ""},
    {"employment.current_company",      TierSingleton, "current employer", "employment.previous_company"},
    {"employment.current_role",         TierSingleton, "current job title/role", ""},
    {"employment.previous_company",     TierSingleton, "previous employer (auto-set)", ""},
    {"relationship.partner",            TierSingleton, "romantic partner", ""},
    {"preference.response_style",       TierSingleton, "preferred response style (concise, detailed, etc.)", ""},
    {"preference.communication_style",  TierSingleton, "communication preference", ""},
    {"preference.diet",                 TierSingleton, "dietary preference (vegetarian, vegan, etc.)", ""},

    // ── Collections ──────────────────────────────────────────────────────────
    {"pet",                TierCollection, "pet (entity_key = pet name)", ""},
    {"family_member",      TierCollection, "family member (entity_key = relation or name)", ""},
    {"restriction.allergy",TierCollection, "food or other allergy (entity_key = allergen)", ""},
    {"skill.using",        TierCollection, "technology or skill the person uses", ""},
    {"preference.food",    TierCollection, "food preference (entity_key = food item)", ""},
    {"opinion.topic",      TierCollection, "opinion on a topic (entity_key = topic)", ""},
    {"project.current",    TierCollection, "current project (entity_key = project name)", ""},
    {"event.upcoming",     TierCollection, "upcoming event (entity_key = event description)", ""},
}

// Lookup indexes built at init time — fast path for hot recall loop
var (
    slotByName     = map[string]SlotDef{}
    singletonSlots = map[string]bool{}
    collectionSlots = map[string]bool{}

    // Profile slots loaded unconditionally on every recall (Tier-1 identity/location/work)
    profileSlots = map[string]bool{
        "identity.name": true,
        "identity.age": true,
        "identity.pronouns": true,
        "location.current": true,
        "location.previous": true,
        "location.hometown": true,
        "employment.current_company": true,
        "employment.current_role": true,
        "employment.previous_company": true,
        "relationship.partner": true,
        "preference.response_style": true,
        "preference.diet": true,
    }
)

func init() {
    for _, s := range SlotCatalog {
        slotByName[s.Slot] = s
        switch s.Tier {
        case TierSingleton:
            singletonSlots[s.Slot] = true
        case TierCollection:
            collectionSlots[s.Slot] = true
        }
    }
}

func IsSingleton(slot string) bool    { return singletonSlots[slot] }
func IsCollection(slot string) bool   { return collectionSlots[slot] }
func IsProfile(slot string) bool      { return profileSlots[slot] }
func IsValid(slot string) bool        { return slotByName[slot].Slot != "" || slot == "unstructured" }

// GetPreviousSlot returns the auto-chain target slot and whether one exists.
// The relationship is encoded on SlotDef — no separate map.
func GetPreviousSlot(slot string) (string, bool) {
    def, ok := slotByName[slot]
    if !ok || def.PreviousSlot == "" {
        return "", false
    }
    return def.PreviousSlot, true
}

// SlotListForPrompt is the canonical slot description sent to the LLM.
// Injected into extraction prompt via SlotListForPrompt().
func SlotListForPrompt() string {
    var sb strings.Builder
    for _, s := range SlotCatalog {
        sb.WriteString(fmt.Sprintf("  %s (%s): %s\n", s.Slot, s.Tier, s.Description))
    }
    sb.WriteString("  unstructured (unstructured): anything that doesn't fit above\n")
    return sb.String()
}
```

**Why `SlotDef` struct over separate maps:**
- `PreviousSlot` lives on the definition — can't get out of sync
- Adding a new slot with auto-chain is one line, not two places
- `SlotListForPrompt()` iterates the catalog directly — prompt always matches code
- `PreviousSlot` encoded on `SlotDef` — one definition, no separate chain map

---

## db/db.go

```go
func Open(path string) (*sql.DB, error) {
    // _busy_timeout=5000 in DSN: wait up to 5s on locked write
    dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", path)
    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return nil, err
    }

    // Pragmas — set on first connection
    pragmas := []string{
        "PRAGMA journal_mode = WAL",
        "PRAGMA synchronous = normal",
        "PRAGMA temp_store = memory",
        "PRAGMA cache_size = -32000",
        "PRAGMA foreign_keys = ON",
    }
    for _, p := range pragmas {
        if _, err := db.Exec(p); err != nil {
            return nil, fmt.Errorf("pragma %q: %w", p, err)
        }
    }

    db.SetMaxOpenConns(1) // single writer

    // Load sqlite-vec extension
    // Must happen before migrations
    if err := loadVecExtension(db); err != nil {
        log.Printf("WARN: sqlite-vec not loaded: %v — vector channel disabled", err)
    }

    // Run goose migrations
    if err := runMigrations(db); err != nil {
        return nil, err
    }

    return db, nil
}
```

---

## app/app.go

```go
type App struct {
    db        *sql.DB
    cfg       *Config
    extractor *gateway.Extractor
    embedder  *gateway.Embedder
}

func New(cfg *Config, db *sql.DB) *App { ... }

func (a *App) Router() http.Handler {
    mux := http.NewServeMux()

    // Register routes (Go 1.22 pattern syntax)
    mux.HandleFunc("GET /health",                      a.handleHealth)
    mux.HandleFunc("POST /turns",                      a.handleTurns)
    mux.HandleFunc("POST /recall",                     a.handleRecall)
    mux.HandleFunc("POST /search",                     a.handleSearch)
    mux.HandleFunc("GET /users/{user_id}/memories",    a.handleGetMemories)
    mux.HandleFunc("DELETE /sessions/{session_id}",    a.handleDeleteSession)
    mux.HandleFunc("DELETE /users/{user_id}",          a.handleDeleteUser)

    // Middleware chain (outermost first)
    var h http.Handler = mux
    h = bodyLimitMiddleware(h)   // 1MB body limit
    h = authMiddleware(h, cfg.MemoryAuthToken)
    h = recoveryMiddleware(h)    // catch panics → 500
    return h
}
```

---

## handler/turn.go — full pipeline (synchronous guarantee)

```go
func (a *App) handleTurns(w http.ResponseWriter, r *http.Request) {
    // 1. Parse + validate
    var req TurnRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON")
        return
    }
    if req.SessionID == "" {
        writeError(w, http.StatusBadRequest, "session_id required")
        return
    }

    userID := resolveUserID(req.UserID, req.SessionID)
    turnID := uuid.New().String()
    now := time.Now().UTC().Format(time.RFC3339)

    // 2. Everything in one transaction
    //    Raw turn + extracted memories + graph + FTS5 + vectors
    //    ALL committed before we write 201.
    tx, err := a.db.BeginTx(r.Context(), nil)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "db error")
        return
    }
    defer tx.Rollback() // no-op if committed

    // 2a. Store raw turn
    if err := repo.InsertTurn(r.Context(), tx, &repo.Turn{
        ID: turnID, SessionID: req.SessionID, UserID: userID,
        Messages: mustMarshal(req.Messages),
        Timestamp: req.Timestamp, Metadata: mustMarshal(req.Metadata),
        CreatedAt: now,
    }); err != nil {
        writeError(w, http.StatusInternalServerError, "db error")
        return
    }

    // 2b. Get user context summary for LLM prompt
    ctx := r.Context()
    userCtx, _ := repo.GetUserContextSummary(ctx, tx, userID)

    // 2c. Extract (blocking — must complete before 201)
    extracted, err := a.extractor.Extract(ctx, req.Messages, userCtx)
    if err != nil {
        log.Printf("WARN: extraction failed for turn %s: %v", turnID, err)
        extracted = &gateway.ExtractionResult{} // empty — don't crash
    }

    // 2d. Apply memories (supersession, graph — FTS5 synced by DB triggers)
    //     All within same tx — see service/memory.go
    if len(extracted.Memories) > 0 {
        if err := a.memorySvc.ApplyMemories(ctx, tx, &service.ApplyInput{
            UserID:    userID,
            SessionID: req.SessionID,
            TurnID:    turnID,
            StatedAt:  req.Timestamp,
            Memories:  extracted.Memories,
            Entities:  extracted.Entities,
            Edges:     extracted.Edges,
        }); err != nil {
            log.Printf("WARN: apply memories failed: %v", err)
            // don't abort — partial extraction is better than no turn stored
        }
    }

    // 2e. Generate embeddings (parallel errgroup — still blocking)
    //     Embedder writes vectors into tx via memory_vec
    if a.embedder.Enabled() && len(extracted.Memories) > 0 {
        if err := a.embedder.EmbedMemories(ctx, tx, extracted.Memories); err != nil {
            log.Printf("WARN: embedding failed: %v", err) // non-fatal
        }
    }

    // 2f. Commit — everything or nothing
    if err := tx.Commit(); err != nil {
        writeError(w, http.StatusInternalServerError, "commit failed")
        return
    }

    // 3. Write 201 — only AFTER commit
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(TurnResponse{ID: turnID})
}
```

---

## repository/memory.go — InsertMemory (triggers handle FTS5)

**FTS5 sync via SQLite triggers:** use `AFTER INSERT/DELETE/UPDATE` triggers
to sync `memory_fts` automatically. This is strictly better than manual
dual-write — no Go code can forget to sync FTS5, and `DeactivateMemory`
(an UPDATE) automatically triggers the delete+insert on `memory_fts`.

Triggers go in `migrations/001_initial.sql`:

```sql
-- FTS5 sync triggers — automatic, no manual dual-write needed in Go
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

`InsertMemory` is now just a plain INSERT — no rowid fetch, no FTS5 call:

```go
// InsertMemory inserts a memory row. FTS5 sync is handled by DB trigger.
// Still the only entry point for memory writes — keeps embedding logic centralised.
func InsertMemory(ctx context.Context, tx *sql.Tx, m *Memory) error {
    _, err := tx.ExecContext(ctx, `
        INSERT INTO memory
            (id, user_id, session_id, turn_id, slot, entity_key, value,
             confidence, evidence, active, supersedes, mutation,
             stated_at, valid_from, valid_until, created_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
        m.ID, m.UserID, m.SessionID, m.TurnID, m.Slot, m.EntityKey, m.Value,
        m.Confidence, m.Evidence, boolToInt(m.Active), m.Supersedes, m.Mutation,
        m.StatedAt, m.ValidFrom, m.ValidUntil, m.CreatedAt, m.UpdatedAt,
    )
    if err != nil {
        return fmt.Errorf("insert memory: %w", err)
    }
    return nil
}

// DeactivateMemory sets active=0 and valid_until on a memory row.
// The memory_au trigger automatically re-syncs memory_fts on UPDATE.
func DeactivateMemory(ctx context.Context, tx *sql.Tx, id string, validUntil string) error {
    _, err := tx.ExecContext(ctx,
        `UPDATE memory SET active=0, valid_until=?, updated_at=? WHERE id=?`,
        validUntil, time.Now().UTC().Format(time.RFC3339), id,
    )
    return err
}
```

**FTS5 virtual table declaration** — also include `evidence` column to match
triggers and enable keyword search on the verbatim quote field:

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    slot,
    value,
    evidence,
    content='memory',
    content_rowid='rowid',
    tokenize='porter unicode61'
);
```

**BM25 query updated** to MATCH against `memory_fts` which now has 3 columns.
FTS5 MATCH searches all columns by default — no query change needed.
But the JOIN remains the same: `memory_fts.rowid = m.rowid`.

---

## service/memory.go — ApplyMemories

```go
func (s *MemoryService) ApplyMemories(ctx context.Context, tx *sql.Tx, input *ApplyInput) error {
    now := time.Now().UTC().Format(time.RFC3339)

    for _, m := range input.Memories {
        if m.Confidence < LowConfidenceThreshold {
            continue // skip low-confidence extractions
        }

        switch {
        case m.Mutation == "negate":
            s.negateMemory(ctx, tx, input.UserID, m.Slot, m.EntityKey, now)

        case IsSingleton(m.Slot):
            s.applySingleton(ctx, tx, input, m, now)

        case IsCollection(m.Slot):
            s.applyCollection(ctx, tx, input, m, now)

        default: // misc / unstructured
            repo.InsertMemory(ctx, tx, buildMemory(input, m, now))
        }
    }

    // Apply graph entities and edges
    for _, e := range input.Entities {
        repo.UpsertEntity(ctx, tx, input.UserID, e, now)
    }
    for _, edge := range input.Edges {
        repo.InsertEdge(ctx, tx, input.UserID, edge, now)
    }

    return nil
}

func (s *MemoryService) applySingleton(
    ctx context.Context, tx *sql.Tx, input *ApplyInput,
    m gateway.ExtractedMemory, now string,
) {
    // 1. Check for existing active singleton
    existing, _ := repo.GetActiveSingleton(ctx, tx, input.UserID, m.Slot)

    if existing != nil {
        // Skip if same value
        if strings.EqualFold(strings.TrimSpace(existing.Value),
                             strings.TrimSpace(m.Value)) {
            return
        }

        // Deactivate old (sets valid_until, removes from FTS5)
        repo.DeactivateMemory(ctx, tx, existing.ID, now)

        // Auto-chain to .previous — only for genuine updates (not corrections)
        if prevSlot, ok := GetPreviousSlot(m.Slot); ok && m.Mutation != "replace" {
            prev := buildMemory(input, gateway.ExtractedMemory{
                Type:  "fact",
                Slot:  prevSlot,
                Value: existing.Value,
                Confidence: 0.95,
                Evidence: fmt.Sprintf("auto-set from %s update", m.Slot),
                Mutation: "upsert",
            }, now)
            prev.Supersedes = nil // chained value, not a supersession
            repo.InsertMemory(ctx, tx, prev)
        }
    }

    // Insert new memory
    mem := buildMemory(input, m, now)
    if existing != nil {
        mem.Supersedes = &existing.ID
    }
    repo.InsertMemory(ctx, tx, mem)
}

func (s *MemoryService) applyCollection(
    ctx context.Context, tx *sql.Tx, input *ApplyInput,
    m gateway.ExtractedMemory, now string,
) {
    incomingKey := strings.ToLower(strings.TrimSpace(m.EntityKey))
    if incomingKey == "" {
        incomingKey = "unknown"
    }

    // Fetch all active entity keys for this (user, slot)
    existing, _ := repo.GetActiveCollection(ctx, tx, input.UserID, m.Slot)

    resolvedKey := incomingKey
    var supersedes *string

    for _, row := range existing {
        // Exact match
        if row.EntityKey == incomingKey {
            if strings.EqualFold(strings.TrimSpace(row.Value),
                                  strings.TrimSpace(m.Value)) {
                return // same value, skip
            }
            repo.DeactivateMemory(ctx, tx, row.ID, now)
            supersedes = &row.ID
            break
        }
        // Fuzzy match (normalized Levenshtein ≥ 88)
        if fuzzyMatch(incomingKey, row.EntityKey, EntityFuzzyThreshold) {
            if strings.EqualFold(strings.TrimSpace(row.Value),
                                  strings.TrimSpace(m.Value)) {
                return
            }
            repo.DeactivateMemory(ctx, tx, row.ID, now)
            supersedes = &row.ID
            resolvedKey = row.EntityKey // preserve canonical key
            break
        }
    }

    mem := buildMemory(input, m, now)
    mem.EntityKey = resolvedKey
    mem.Supersedes = supersedes
    repo.InsertMemory(ctx, tx, mem)
}
```

---

## gateway/extractor.go — Deepseek call

```go
// Extraction prompt sent to Deepseek.
// The model must return ONLY valid JSON — no markdown fences, no preamble.
const systemPrompt = `You are a memory extraction engine for an AI assistant.
Extract structured facts from the conversation below.

SLOT CATALOG (use exactly these slot names — populated via SlotListForPrompt()):
Singletons (one fact per user):
  identity.name, identity.age, identity.pronouns
  location.current, location.previous, location.hometown
  employment.current_company, employment.current_role, employment.previous_company
  relationship.partner
  preference.response_style, preference.communication_style, preference.diet

Collections (multiple facts per user, each with an entity_key):
  pet/<entity_key>
  family_member/<entity_key>
  restriction.allergy/<entity_key>
  skill.using/<entity_key>
  preference.food/<entity_key>
  opinion.topic/<entity_key>
  project.current/<entity_key>
  event.upcoming/<entity_key>

Unstructured (anything that doesn't fit above):
  unstructured

NOTE: In code, inject this via SlotListForPrompt() so prompt always matches catalog.

MUTATION TYPES:
  upsert  — new or updated fact
  replace — explicit correction ("I meant X not Y" → the old value was wrong)
  negate  — explicit denial ("I don't have a dog")

EXTRACTION RULES:
- Extract implicit facts: "walking Biscuit" → pet/biscuit
- Extract temporal hints: "just got back from a year in Tokyo" → location.previous=Tokyo
  with valid_from approximately one year ago
- Extract implied location: "coffee shops in Berlin are amazing" → location.current=Berlin
  (only if confident)
- Partner: "my partner Alex" → relationship.status=in a relationship + family_member/alex
- Employment: "I work as PM at Notion" → employment.company=Notion, employment.role=PM
- Corrections: "Actually I meant Munich not Berlin" → mutation=replace

USER'S KNOWN FACTS (for context):
%s

Return ONLY this JSON structure (no markdown, no extra text):
{
  "memories": [
    {
      "type": "fact|preference|opinion|event",
      "slot": "<slot from catalog>",
      "entity_key": "<for collection slots, else empty string>",
      "value": "<extracted value>",
      "confidence": 0.0-1.0,
      "evidence": "<verbatim quote from conversation>",
      "mutation": "upsert|replace|negate",
      "valid_from": "<ISO date if known, else null>"
    }
  ],
  "entities": [
    {"name": "<entity name>", "type": "person|location|org|pet|concept"}
  ],
  "edges": [
    {"source": "user", "relation": "LIVES_IN|WORKS_AT|HAS_PET|KNOWS|PREFERS",
     "target": "<entity name>", "valid_from": "<ISO date or null>"}
  ]
}`

func (e *Extractor) Extract(ctx context.Context, messages []handler.Message, userCtx string) (*ExtractionResult, error) {
    // Build prompt
    prompt := fmt.Sprintf(systemPrompt, userCtx)

    // Format messages as readable text for the LLM
    var sb strings.Builder
    for _, m := range messages {
        sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
    }

    body := map[string]any{
        "model": "deepseek-chat",
        "messages": []map[string]string{
            {"role": "system", "content": prompt},
            {"role": "user", "content": sb.String()},
        },
        "temperature": 0.1, // low temp for consistent JSON output
        "response_format": map[string]string{"type": "json_object"},
    }

    // POST to Deepseek (OpenAI-compatible)
    // Parse response → ExtractionResult
    // Return empty result on any error, don't crash /turns
}
```

---

## gateway/embedder.go — provider pattern + serialize_f32

Embedding provider pattern. Two providers: `openai` (real) and `fake` (tests).
Selected via `EMBEDDINGS_PROVIDER` env var. `fake` uses deterministic
hash-seeded vectors — no API key, no semantic signal, but full mechanical
plumbing exercised.

```go
type EmbedderProvider string

const (
    ProviderOpenAI EmbedderProvider = "openai"
    ProviderFake   EmbedderProvider = "fake"
)

type Embedder struct {
    provider  EmbedderProvider
    model     string
    dim       int
    apiKey    string
    baseURL   string
    enabled   bool
}

func NewEmbedder(apiKey, baseURL string) *Embedder {
    provider := EmbedderProvider(os.Getenv("EMBEDDINGS_PROVIDER"))
    if provider == "" { provider = ProviderOpenAI }

    dim := 1536 // text-embedding-3-small default
    if provider == ProviderFake { dim = 64 }
    if d := os.Getenv("EMBEDDING_DIM"); d != "" {
        if n, err := strconv.Atoi(d); err == nil { dim = n }
    }

    enabled := apiKey != "" || provider == ProviderFake
    if !enabled {
        log.Println("WARN: OPENAI_API_KEY not set — vector recall channel disabled")
    }

    return &Embedder{
        provider: provider,
        model:    "text-embedding-3-small",
        dim:      dim,
        apiKey:   apiKey,
        baseURL:  baseURL,
        enabled:  enabled,
    }
}

func (e *Embedder) Enabled() bool { return e.enabled }
func (e *Embedder) Dim() int      { return e.dim }

// Embed returns a float32 embedding for text, or nil on failure.
// Never panics — callers treat nil as "degrade to BM25".
func (e *Embedder) Embed(ctx context.Context, text string) []float32 {
    if !e.enabled || strings.TrimSpace(text) == "" {
        return nil
    }
    switch e.provider {
    case ProviderFake:
        return fakeEmbed(text, e.dim)
    default:
        return e.openAIEmbed(ctx, text)
    }
}

// EmbedBatch embeds multiple texts in one API call (OpenAI), falls back to
// per-item if batched call fails. Fake provider always succeeds per-item.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) [][]float32 {
    results := make([][]float32, len(texts))
    if !e.enabled { return results }

    if e.provider == ProviderFake {
        for i, t := range texts {
            if strings.TrimSpace(t) != "" {
                results[i] = fakeEmbed(t, e.dim)
            }
        }
        return results
    }

    // OpenAI: try batched first
    vecs, err := e.openAIEmbedBatch(ctx, texts)
    if err != nil {
        log.Printf("WARN: batched embedding failed, falling back to per-item: %v", err)
        for i, t := range texts {
            results[i] = e.openAIEmbed(ctx, t)
        }
        return results
    }
    return vecs
}

// EmbedMemories embeds extracted memories in parallel and writes vectors to tx.
func (e *Embedder) EmbedMemories(ctx context.Context, tx *sql.Tx, memories []ExtractedMemory) error {
    if !e.enabled || len(memories) == 0 { return nil }

    // Build text inputs: "slot: value"
    texts := make([]string, len(memories))
    for i, m := range memories {
        texts[i] = m.Slot + ": " + m.Value
    }

    // Batched embed (one API call for all memories in this turn)
    vecs := e.EmbedBatch(ctx, texts)

    // Write vectors — get rowid for each memory, insert into memory_vec
    for i, m := range memories {
        vec := vecs[i]
        if vec == nil { continue }

        var rowid int64
        if err := tx.QueryRowContext(ctx,
            `SELECT rowid FROM memory WHERE id=?`, m.ID,
        ).Scan(&rowid); err != nil {
            log.Printf("WARN: rowid lookup failed for memory %s: %v", m.ID, err)
            continue
        }

        blob := SerializeF32(vec)
        if _, err := tx.ExecContext(ctx,
            `INSERT OR REPLACE INTO memory_vec(memory_rowid, embedding) VALUES (?,?)`,
            rowid, blob,
        ); err != nil {
            log.Printf("WARN: vector write failed for memory %s: %v", m.ID, err)
        }
    }
    return nil
}

// ── serialize helpers ──────────────────────────────────────────────────────────

// SerializeF32 converts []float32 to little-endian bytes for sqlite-vec.
// sqlite-vec expects IEEE 754 float32, little-endian packed.
// Must match deserialize exactly or KNN returns garbage silently.
func SerializeF32(vec []float32) []byte {
    buf := make([]byte, len(vec)*4)
    for i, f := range vec {
        binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
    }
    return buf
}

func DeserializeF32(b []byte) []float32 {
    vec := make([]float32, len(b)/4)
    for i := range vec {
        vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
    }
    return vec
}

// ── fake embedder (deterministic, for tests) ───────────────────────────────────

// fakeEmbed returns a deterministic unit-vector seeded by SHA-256 of text.
// Two identical texts → identical vectors. No semantic signal (fine for tests).
// Deterministic hash-seeded fake embedder for tests.
func fakeEmbed(text string, dim int) []float32 {
    h := sha256.Sum256([]byte(text))
    // Use hash bytes as seed for a simple LCG to fill the vector
    seed := binary.LittleEndian.Uint64(h[:8])
    vec := make([]float32, dim)
    var norm float64
    for i := range vec {
        // LCG: same sequence for same seed
        seed = seed*6364136223846793005 + 1442695040888963407
        v := float32(int64(seed>>33)) / float32(1<<31)
        vec[i] = v
        norm += float64(v * v)
    }
    // Normalize to unit vector
    if norm > 0 {
        norm = math.Sqrt(norm)
        for i := range vec { vec[i] = float32(float64(vec[i]) / norm) }
    }
    return vec
}

// ── OpenAI HTTP client ──────────────────────────────────────────────────────────

func (e *Embedder) openAIEmbed(ctx context.Context, text string) []float32 {
    vecs, err := e.openAIEmbedBatch(ctx, []string{text})
    if err != nil || vecs[0] == nil { return nil }
    return vecs[0]
}

func (e *Embedder) openAIEmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    // POST to OpenAI-compatible /v1/embeddings endpoint
    // Parse response data[].embedding arrays
    // Return [][]float32, one per input text
    // On HTTP error or JSON parse error, return nil, err
}
```

**Imports needed in embedder.go:**
```go
import (
    "crypto/sha256"
    "encoding/binary"
    "math"
    "encoding/json"
    "net/http"
    "bytes"
)
```

**memory_vec table declaration** (in migration, after VEC_ENABLED check):
```sql
CREATE VIRTUAL TABLE IF NOT EXISTS memory_vec
    USING vec0(memory_rowid INTEGER PRIMARY KEY, embedding FLOAT[1536]);
```
Note: `1536` must be replaced at runtime with the actual `EMBEDDING_DIM`
value. Use `fmt.Sprintf` in Go when running the migration if dim can vary
(for fake provider tests with dim=64). Or just hardcode 1536 for production
and use a separate test DB for the fake provider.

---

## service/recall.go — four-channel retrieval

### FTS query builder
```go
var stopwords = map[string]bool{
    "the": true, "a": true, "an": true, "is": true, "are": true,
    "was": true, "were": true, "be": true, "been": true, "do": true,
    "does": true, "did": true, "has": true, "have": true, "had": true,
    "i": true, "you": true, "we": true, "they": true, "it": true,
    "this": true, "that": true, "what": true, "who": true, "where": true,
    "when": true, "how": true, "which": true, "and": true, "or": true,
    "but": true, "in": true, "on": true, "at": true, "to": true,
    "for": true, "of": true, "with": true, "by": true, "from": true,
    "user": true, "their": true,
}

func buildFTSQuery(query string) string {
    words := tokenize(query) // split on non-word chars, lowercase
    var filtered []string
    for _, w := range words {
        if len(w) >= 2 && !stopwords[w] {
            filtered = append(filtered, w)
        }
    }
    if len(filtered) == 0 {
        filtered = words
    }
    if len(filtered) == 0 {
        return "*"
    }
    return strings.Join(filtered, " OR ")
}
```

### BM25 retrieval
```go
// Critical: JOIN on memories_fts.rowid = m.rowid
// bm25() returns negative values — ORDER BY bm25() ASC = best first
const bm25Query = `
    SELECT m.id, m.slot, m.entity_key, m.value,
           m.confidence, m.turn_id, m.updated_at, m.evidence, m.active,
           bm25(memory_fts) AS bm25_score
    FROM memory_fts
    JOIN memory m ON memory_fts.rowid = m.rowid
    WHERE memory_fts MATCH ?
      AND m.user_id = ?
      AND m.active = 1
    ORDER BY bm25(memory_fts)
    LIMIT ?`
```

### Vector KNN
```go
// Over-fetch by VectorOverfetch (3×) because vec0 index is global.
// Filter to user + active in Go after fetch.
const knnQuery = `
    SELECT memory_rowid, distance
    FROM memory_vec
    WHERE embedding MATCH ?
    ORDER BY distance
    LIMIT ?`
// Then JOIN memory table in Go for user_id + active filter
```

### RRF fusion
```go
func rrfFuse(bm25Ranked, vecRanked []MemoryRow, k int) map[string]float64 {
    scores := make(map[string]float64)
    for rank, row := range bm25Ranked {
        scores[row.ID] += 1.0 / float64(k+rank+1)
    }
    for rank, row := range vecRanked {
        scores[row.ID] += 1.0 / float64(k+rank+1)
    }
    return scores
}
```

### Final scoring
```go
func finalScore(rrfNorm, recency, confidence float64, active bool) float64 {
    activeboost := 0.0
    if active { activeboost = 1.0 }
    return 0.65*rrfNorm + 0.20*recency + 0.10*confidence + 0.05*activeboost
}

func recencyScore(updatedAt string) float64 {
    t, err := time.Parse(time.RFC3339, updatedAt)
    if err != nil { return 0.5 }
    daysOld := time.Since(t).Hours() / 24
    if daysOld < 0 { daysOld = 0 }
    score := 1.0 - daysOld/90.0
    if score < 0 { return 0.0 }
    return score
}
```

### Multi-hop
```go
func extractHopTerms(rows []MemoryRow) []string {
    terms := map[string]bool{}
    for i, row := range rows {
        if i >= 5 { break }
        if key := strings.TrimSpace(row.EntityKey); len(key) >= 2 {
            terms[key] = true
        }
        // Capitalized value tokens — names, places
        for _, word := range strings.Fields(row.Value) {
            cleaned := alphaOnly(word)
            if len(cleaned) >= 2 && len(cleaned) <= 20 && unicode.IsUpper(rune(cleaned[0])) {
                terms[strings.ToLower(cleaned)] = true
            }
        }
    }
    result := make([]string, 0, len(terms))
    for t := range terms { result = append(result, t) }
    return result
}

// Hop-2 penalty: cap RRF score at 0.5 so first-pass always ranks higher
func hopScore(rrfScore float64, recency, confidence float64) float64 {
    rrfNorm := math.Min(rrfScore*100, 0.5)
    return 0.65*rrfNorm + 0.20*recency + 0.10*confidence + 0.05*1.0
}
```

### Graph recursive CTE (repository/graph.go)
```go
const graphQuery = `
WITH RECURSIVE traverse(entity_id, depth) AS (
    SELECT e2.id, 1
    FROM entity e1
    JOIN edge ed ON ed.source_id = e1.id
    JOIN entity e2 ON ed.target_id = e2.id
    WHERE e1.user_id = ?
      AND e1.name LIKE ?
      AND ed.valid_until IS NULL
    UNION ALL
    SELECT ed.target_id, t.depth + 1
    FROM edge ed
    JOIN traverse t ON ed.source_id = t.entity_id
    WHERE t.depth < 2
      AND ed.valid_until IS NULL
)
SELECT DISTINCT e.name, e.type
FROM entity e
WHERE e.id IN (SELECT entity_id FROM traverse)
  AND e.user_id = ?`
```

### Token counting (approximation — no tiktoken in Go)
```go
// 4 chars ≈ 1 token — close enough without pulling in tiktoken
func countTokens(text string) int {
    return len(text) / 4
}
```

### Context assembly
```go
// Budget: soft cap at 2× max_tokens
// Priority: profile facts → RRF-ranked memories → graph context → recent turns
// Never cut profile facts regardless of budget
softCap := maxTokens * 2
```

---

## Context output format

Slot label map for `formatProfileSection`:

```go
var slotLabels = map[string]string{
    "identity.name":                  "Name",
    "identity.age":                   "Age",
    "identity.pronouns":              "Pronouns",
    "location.current":               "Lives in",
    "location.hometown":              "Hometown",
    "employment.current_company":     "Works at",
    "employment.current_role":        "Role",
    "relationship.partner":           "Partner",
    "preference.response_style":      "Prefers",
    "preference.communication_style": "Communication style",
    "preference.diet":                "Diet",
}
```

Augmentation rules:
- `location.current` → append `(previously {location.previous})` if exists
- `employment.current_company` → append `as {employment.current_role}` if exists,
  then `(previously at {employment.previous_company})` if exists

Output format:

```
## Known facts about this user
- Name: Alice.
- Lives in: Berlin (previously NYC). (updated 2025-03-10)
- Works at: Notion as PM (previously at Stripe). (updated 2025-03-10)
- Diet: vegetarian.

## Relevant from past conversations
- [2025-03-10] Has a dog named Biscuit (golden retriever)
- [2025-03-14] Finds TypeScript generics annoying [via related memory]
```

---

## Recall full flow (handler/recall.go)

```go
func (a *App) handleRecall(w http.ResponseWriter, r *http.Request) {
    var req RecallRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON")
        return
    }
    if req.MaxTokens <= 0 { req.MaxTokens = 1024 }

    userID := resolveUserID(req.UserID, req.SessionID)

    result, err := a.recallSvc.Recall(r.Context(), &service.RecallInput{
        Query:     req.Query,
        SessionID: req.SessionID,
        UserID:    userID,
        MaxTokens: req.MaxTokens,
    })
    if err != nil {
        // Never error on cold sessions — return empty
        result = &service.RecallResult{Context: "", Citations: nil}
    }

    writeJSON(w, http.StatusOK, RecallResponse{
        Context:   result.Context,
        Citations: result.Citations,
    })
}
```

---

## Middleware

### Body limit (app/app.go)
```go
func bodyLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
        next.ServeHTTP(w, r)
    })
}
```

### Auth (app/app.go)
```go
func authMiddleware(next http.Handler, token string) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if token == "" {
            next.ServeHTTP(w, r) // auth disabled
            return
        }
        auth := r.Header.Get("Authorization")
        if auth != "Bearer "+token {
            writeError(w, http.StatusUnauthorized, "unauthorized")
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### Recovery (app/app.go)
```go
func recoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                log.Printf("PANIC: %v\n%s", rec, debug.Stack())
                writeError(w, http.StatusInternalServerError, "internal error")
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

### Error helper (handler/error.go)
```go
func writeError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}
```

---

## main.go — graceful shutdown

```go
func main() {
    // Healthcheck mode for docker-compose (scratch has no curl/shell)
    if len(os.Args) > 1 && os.Args[1] == "-health-check" {
        os.Exit(runHealthCheck())
    }

    // Load .env (dev only — no-op if file missing)
    _ = godotenv.Load()

    var cfg Config
    if err := env.Parse(&cfg); err != nil {
        log.Fatalf("config: %v", err)
    }

    if cfg.OpenAIAPIKey == "" {
        log.Println("WARN: OPENAI_API_KEY not set — vector recall channel disabled")
    }

    db, err := db.Open(cfg.DBPath)
    if err != nil { log.Fatalf("db: %v", err) }
    defer db.Close()

    application := app.New(&cfg, db)

    srv := &http.Server{
        Addr:         fmt.Sprintf(":%d", cfg.Port),
        Handler:      application.Router(),
        ReadTimeout:  65 * time.Second,
        WriteTimeout: 65 * time.Second,
        IdleTimeout:  120 * time.Second,
    }

    ctx, stop := signal.NotifyContext(context.Background(),
        os.Interrupt, syscall.SIGTERM)
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
    srv.Shutdown(shutCtx)
    db.Close()
}
```

---

## Fuzzy entity matching (service/memory.go)

No rapidfuzz in Go. Use normalized Levenshtein distance:

```go
// fuzzyMatch returns true if the two strings match at threshold (0-100 scale).
// Uses token_set_ratio equivalent logic: compare sorted token sets.
// Handles "mylo" vs "mylo the cat" → 100, "mylo" vs "biscuit" → 0.
func fuzzyMatch(a, b string, threshold int) bool {
    if len(a) < 3 || len(b) < 3 { return false }
    a, b = strings.ToLower(a), strings.ToLower(b)
    if a == b { return true }

    // Token set ratio: if one is contained in the other, score is high
    aWords := strings.Fields(a)
    bWords := strings.Fields(b)
    aSet := toSet(aWords)
    bSet := toSet(bWords)

    intersection := 0
    for w := range aSet { if bSet[w] { intersection++ } }
    
    if len(aSet) == 0 || len(bSet) == 0 { return false }
    
    // Jaccard-like: intersection / union * 100
    union := len(aSet) + len(bSet) - intersection
    score := int(float64(intersection) / float64(union) * 100)
    return score >= threshold
}
```

---

## Design decisions and tradeoffs (document in README)

1. **Single transaction** for entire `/turns` pipeline — extraction, memories,
   FTS sync, embeddings, and vectors commit atomically. Mid-way failure rolls
   back cleanly with no orphan turns.

2. **Graph in SQLite** (recursive CTE) for multi-hop recall — one SQL query,
   no secondary retrieval round-trip.

3. **Temporal model** (`valid_from` / `valid_until`) — full timeline
   reconstructable. Opinion arcs preserved without extra schema.

4. **Go stdlib `net/http`** — synchronous per request, no event loop complexity.

5. **Custom token-set-ratio fuzzy matching** in Go (threshold 88) — no
   external fuzzy-match dependency.

6. **Embedding parallelism** via `errgroup` — N parallel embed calls after
   extraction, still blocking before 201 response.

7. **FTS5 sync via SQLite triggers** — no manual dual-write in Go code.

8. **CLI healthcheck** (`-health-check` flag) — works in `scratch` without
   curl or a sidecar binary.