package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/slackerkids/agent-memo.git/internal/contract"
	"github.com/slackerkids/agent-memo.git/internal/repository"
	"golang.org/x/sync/errgroup"
)

type EmbedderProvider string

const (
	ProviderOpenAI EmbedderProvider = "openai"
	ProviderFake   EmbedderProvider = "fake"
)

type Embedder struct {
	provider EmbedderProvider
	model    string
	dim      int
	apiKey   string
	baseURL  string
	enabled  bool
	client   *http.Client
}

func NewEmbedder(apiKey, baseURL string) *Embedder {
	provider := EmbedderProvider(os.Getenv("EMBEDDINGS_PROVIDER"))
	if provider == "" {
		provider = ProviderOpenAI
	}

	dim := 1536
	if provider == ProviderFake {
		dim = 64
	}
	if d := os.Getenv("EMBEDDING_DIM"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			dim = n
		}
	}

	enabled := apiKey != "" || provider == ProviderFake
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	return &Embedder{
		provider: provider,
		model:    "text-embedding-3-small",
		dim:      dim,
		apiKey:   apiKey,
		baseURL:  strings.TrimRight(baseURL, "/"),
		enabled:  enabled,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *Embedder) Enabled() bool { return e.enabled }
func (e *Embedder) Dim() int      { return e.dim }

func (e *Embedder) Embed(ctx context.Context, text string) []float32 {
	if !e.enabled || strings.TrimSpace(text) == "" {
		return nil
	}
	switch e.provider {
	case ProviderFake:
		return fakeEmbed(text, e.dim)
	default:
		vecs, err := e.openAIEmbedBatch(ctx, []string{text})
		if err != nil || len(vecs) == 0 || vecs[0] == nil {
			return nil
		}
		return vecs[0]
	}
}

func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) [][]float32 {
	results := make([][]float32, len(texts))
	if !e.enabled {
		return results
	}

	if e.provider == ProviderFake {
		for i, t := range texts {
			if strings.TrimSpace(t) != "" {
				results[i] = fakeEmbed(t, e.dim)
			}
		}
		return results
	}

	vecs, err := e.openAIEmbedBatch(ctx, texts)
	if err != nil {
		log.Printf("WARN: batched embedding failed, falling back to per-item: %v", err)
		for i, t := range texts {
			results[i] = e.Embed(ctx, t)
		}
		return results
	}
	return vecs
}

func (e *Embedder) EmbedMemories(ctx context.Context, tx *sql.Tx, memories []contract.InsertedMemory) error {
	if !e.enabled || len(memories) == 0 {
		return nil
	}

	texts := make([]string, len(memories))
	for i, m := range memories {
		texts[i] = m.Slot + ": " + m.Value
	}

	var vecs [][]float32
	if len(texts) == 1 {
		vecs = e.EmbedBatch(ctx, texts)
	} else {
		g, gctx := errgroup.WithContext(ctx)
		vecs = make([][]float32, len(texts))
		for i, t := range texts {
			i, t := i, t
			g.Go(func() error {
				vecs[i] = e.Embed(gctx, t)
				return nil
			})
		}
		_ = g.Wait()
	}

	for i, m := range memories {
		vec := vecs[i]
		if vec == nil {
			continue
		}
		rowid, err := repository.MemoryRowID(ctx, tx, m.ID)
		if err != nil {
			log.Printf("WARN: rowid lookup failed for %s: %v", m.ID, err)
			continue
		}
		blob, err := sqlite_vec.SerializeFloat32(vec)
		if err != nil {
			log.Printf("WARN: serialize vector failed for %s: %v", m.ID, err)
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO memory_vec(memory_rowid, embedding) VALUES (?,?)`,
			rowid, blob,
		); err != nil {
			log.Printf("WARN: vector write failed for %s: %v", m.ID, err)
		}
	}
	return nil
}

func (e *Embedder) openAIEmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{
		"model": e.model,
		"input": texts,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai embeddings %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		vec := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			vec[j] = float32(v)
		}
		out[i] = vec
	}
	return out, nil
}

func fakeEmbed(text string, dim int) []float32 {
	h := sha256.Sum256([]byte(text))
	seed := binary.LittleEndian.Uint64(h[:8])
	vec := make([]float32, dim)
	var norm float64
	for i := range vec {
		seed = seed*6364136223846793005 + 1442695040888963407
		v := float32(int64(seed>>33)) / float32(1<<31)
		vec[i] = v
		norm += float64(v * v)
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}
