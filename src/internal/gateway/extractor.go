package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/slackerkids/agent-memo.git/internal/contract"
	"github.com/slackerkids/agent-memo.git/internal/slots"
)

type Extractor struct {
	apiKey  string
	baseURL string
	client  *http.Client
	model   string
}

func NewExtractor(apiKey, baseURL string) *Extractor {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &Extractor{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 55 * time.Second},
		model:   "deepseek-chat",
	}
}

func (e *Extractor) Enabled() bool {
	return e.apiKey != ""
}

const extractionPromptTemplate = `You are a memory extraction engine for an AI assistant.
Extract structured facts from the conversation below.

SLOT CATALOG (use exactly these slot names):
%s

MUTATION TYPES:
  upsert  — new or updated fact
  replace — explicit correction ("I meant X not Y" → the old value was wrong)
  negate  — explicit denial ("I don't have a dog")

EXTRACTION RULES:
- Extract implicit facts: "walking Biscuit" → pet/biscuit
- Extract temporal hints: "moved last month" → set valid_from accordingly
- Employment: "PM at Notion" → employment.current_company=Notion, employment.current_role=PM
- Corrections: "Actually Munich not Berlin" → mutation=replace

USER'S KNOWN FACTS:
%s

Return ONLY valid JSON:
{
  "memories": [{"type":"fact|preference|opinion|event","slot":"...","entity_key":"...","value":"...","confidence":0.0,"evidence":"...","mutation":"upsert|replace|negate","valid_from":null}],
  "entities": [{"name":"...","type":"person|location|org|pet|concept"}],
  "edges": [{"source":"user","relation":"LIVES_IN|WORKS_AT|HAS_PET|KNOWS|PREFERS","target":"...","valid_from":null}]
}`

func (e *Extractor) Extract(ctx context.Context, messages []contract.Message, userCtx string) (*contract.ExtractionResult, error) {
	if !e.Enabled() {
		return &contract.ExtractionResult{}, nil
	}

	prompt := fmt.Sprintf(extractionPromptTemplate, slots.ListForPrompt(), userCtx)

	var sb strings.Builder
	for _, m := range messages {
		role := m.Role
		if m.Name != nil && *m.Name != "" {
			role += "(" + *m.Name + ")"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, m.Content))
	}

	body := map[string]any{
		"model": e.model,
		"messages": []map[string]string{
			{"role": "system", "content": prompt},
			{"role": "user", "content": sb.String()},
		},
		"temperature":     0.1,
		"response_format": map[string]string{"type": "json_object"},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
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
		return nil, fmt.Errorf("deepseek API %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, err
	}
	if len(chatResp.Choices) == 0 {
		return &contract.ExtractionResult{}, nil
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	var result contract.ExtractionResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return &contract.ExtractionResult{}, nil
	}
	return &result, nil
}
