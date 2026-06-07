package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/google/uuid"
	"github.com/slackerkids/agent-memo.git/internal/contract"
	"github.com/slackerkids/agent-memo.git/internal/gateway"
	"github.com/slackerkids/agent-memo.git/internal/repository"
)

type TurnService struct {
	DB        *sql.DB
	Extractor *gateway.Extractor
	Embedder  *gateway.Embedder
	Memory    *MemoryService
}

type IngestResult struct {
	TurnID string
}

func (s *TurnService) Ingest(ctx context.Context, req *contract.TurnRequest) (*IngestResult, error) {
	userID := contract.ResolveUserID(req.UserID, req.SessionID)
	turnID := uuid.New().String()
	now := repository.NowRFC3339()

	messagesJSON, err := json.Marshal(req.Messages)
	if err != nil {
		return nil, err
	}
	metadataJSON := []byte("{}")
	if req.Metadata != nil {
		metadataJSON, err = json.Marshal(req.Metadata)
		if err != nil {
			return nil, err
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := repository.InsertTurn(ctx, tx, &repository.Turn{
		ID:        turnID,
		SessionID: req.SessionID,
		UserID:    userID,
		Messages:  string(messagesJSON),
		Timestamp: req.Timestamp,
		Metadata:  string(metadataJSON),
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	userCtx, _ := repository.GetUserContextSummary(ctx, tx, userID)

	extracted, err := s.Extractor.Extract(ctx, req.Messages, userCtx)
	if err != nil {
		log.Printf("WARN: extraction failed for turn %s: %v", turnID, err)
		extracted = &contract.ExtractionResult{}
	}

	var inserted []contract.InsertedMemory
	if len(extracted.Memories) > 0 {
		inserted, err = s.Memory.ApplyMemories(ctx, tx, &ApplyInput{
			UserID:    userID,
			SessionID: req.SessionID,
			TurnID:    turnID,
			StatedAt:  req.Timestamp,
			Memories:  extracted.Memories,
			Entities:  extracted.Entities,
			Edges:     extracted.Edges,
		})
		if err != nil {
			log.Printf("WARN: apply memories failed: %v", err)
		}
	}

	if s.Embedder.Enabled() && len(inserted) > 0 {
		if err := s.Embedder.EmbedMemories(ctx, tx, inserted); err != nil {
			log.Printf("WARN: embedding failed: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &IngestResult{TurnID: turnID}, nil
}
