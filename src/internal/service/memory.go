package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/slackerkids/agent-memo.git/internal/contract"
	"github.com/slackerkids/agent-memo.git/internal/repository"
	"github.com/slackerkids/agent-memo.git/internal/slots"
)

type MemoryService struct{}

type ApplyInput struct {
	UserID    string
	SessionID string
	TurnID    string
	StatedAt  string
	Memories  []contract.ExtractedMemory
	Entities  []contract.ExtractedEntity
	Edges     []contract.ExtractedEdge
}

func (s *MemoryService) ApplyMemories(ctx context.Context, tx *sql.Tx, input *ApplyInput) ([]contract.InsertedMemory, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	validUntil := input.StatedAt
	if validUntil == "" {
		validUntil = now
	}

	var inserted []contract.InsertedMemory

	for _, m := range input.Memories {
		if m.Confidence < LowConfidenceThreshold {
			continue
		}
		m.Slot = slots.Normalize(m.Slot)
		if m.Mutation == "" {
			m.Mutation = "upsert"
		}

		var ids []string
		var err error

		switch {
		case m.Mutation == "negate":
			ids, err = s.negateMemory(ctx, tx, input, m, validUntil, now)
		case slots.IsSingleton(m.Slot):
			ids, err = s.applySingleton(ctx, tx, input, m, validUntil, now)
		case slots.IsCollection(m.Slot):
			ids, err = s.applyCollection(ctx, tx, input, m, validUntil, now)
		default:
			ids, err = s.insertOne(ctx, tx, input, m, nil, now)
		}
		if err != nil {
			return inserted, err
		}
		for _, id := range ids {
			inserted = append(inserted, contract.InsertedMemory{ID: id, Slot: m.Slot, Value: m.Value})
		}
	}

	userEntityID, err := repository.EnsureUserEntity(ctx, tx, input.UserID, now)
	if err != nil {
		return inserted, err
	}

	for _, e := range input.Entities {
		if _, err := repository.UpsertEntity(ctx, tx, input.UserID, e.Name, e.Type, now); err != nil {
			return inserted, err
		}
	}

	for _, edge := range input.Edges {
		sourceID := userEntityID
		if strings.ToLower(edge.Source) != "user" {
			var err error
			sourceID, err = repository.UpsertEntity(ctx, tx, input.UserID, edge.Source, "concept", now)
			if err != nil {
				continue
			}
		}
		targetID, err := repository.UpsertEntity(ctx, tx, input.UserID, edge.Target, "concept", now)
		if err != nil {
			continue
		}
		if err := repository.InsertEdge(ctx, tx, input.UserID, sourceID, targetID, edge.Relation, input.StatedAt, edge.ValidFrom, now); err != nil {
			return inserted, err
		}
	}

	return inserted, nil
}

func (s *MemoryService) applySingleton(
	ctx context.Context, tx *sql.Tx, input *ApplyInput,
	m contract.ExtractedMemory, validUntil, now string,
) ([]string, error) {
	existing, err := repository.GetActiveSingleton(ctx, tx, input.UserID, m.Slot)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		if strings.EqualFold(strings.TrimSpace(existing.Value), strings.TrimSpace(m.Value)) {
			return nil, nil
		}
		if err := repository.DeactivateMemory(ctx, tx, existing.ID, validUntil, now); err != nil {
			return nil, err
		}
		if prevSlot, ok := slots.GetPreviousSlot(m.Slot); ok && m.Mutation != "replace" {
			prev := contract.ExtractedMemory{
				Slot:       prevSlot,
				Value:      existing.Value,
				Confidence: 0.95,
				Evidence:   fmt.Sprintf("auto-set from %s update", m.Slot),
				Mutation:   "upsert",
			}
			if _, err := s.insertOne(ctx, tx, input, prev, nil, now); err != nil {
				return nil, err
			}
		}
	}

	var supersedes *string
	if existing != nil {
		supersedes = &existing.ID
	}
	return s.insertOne(ctx, tx, input, m, supersedes, now)
}

func (s *MemoryService) applyCollection(
	ctx context.Context, tx *sql.Tx, input *ApplyInput,
	m contract.ExtractedMemory, validUntil, now string,
) ([]string, error) {
	incomingKey := strings.ToLower(strings.TrimSpace(m.EntityKey))
	if incomingKey == "" {
		incomingKey = "unknown"
	}

	existing, err := repository.GetActiveCollection(ctx, tx, input.UserID, m.Slot)
	if err != nil {
		return nil, err
	}

	resolvedKey := incomingKey
	var supersedes *string

	for _, row := range existing {
		rowKey := ""
		if row.EntityKey.Valid {
			rowKey = row.EntityKey.String
		}
		if rowKey == incomingKey || fuzzyMatch(incomingKey, rowKey, EntityFuzzyThreshold) {
			if strings.EqualFold(strings.TrimSpace(row.Value), strings.TrimSpace(m.Value)) {
				return nil, nil
			}
			if err := repository.DeactivateMemory(ctx, tx, row.ID, validUntil, now); err != nil {
				return nil, err
			}
			supersedes = &row.ID
			resolvedKey = rowKey
			break
		}
	}

	m.EntityKey = resolvedKey
	return s.insertOne(ctx, tx, input, m, supersedes, now)
}

func (s *MemoryService) negateMemory(
	ctx context.Context, tx *sql.Tx, input *ApplyInput,
	m contract.ExtractedMemory, validUntil, now string,
) ([]string, error) {
	if slots.IsSingleton(m.Slot) {
		existing, err := repository.GetActiveSingleton(ctx, tx, input.UserID, m.Slot)
		if err != nil || existing == nil {
			return nil, err
		}
		return nil, repository.DeactivateMemory(ctx, tx, existing.ID, validUntil, now)
	}

	existing, err := repository.GetActiveCollection(ctx, tx, input.UserID, m.Slot)
	if err != nil {
		return nil, err
	}
	incomingKey := strings.ToLower(strings.TrimSpace(m.EntityKey))
	for _, row := range existing {
		rowKey := ""
		if row.EntityKey.Valid {
			rowKey = row.EntityKey.String
		}
		if rowKey == incomingKey || fuzzyMatch(incomingKey, rowKey, EntityFuzzyThreshold) {
			return nil, repository.DeactivateMemory(ctx, tx, row.ID, validUntil, now)
		}
	}
	return nil, nil
}

func (s *MemoryService) insertOne(
	ctx context.Context, tx *sql.Tx, input *ApplyInput,
	m contract.ExtractedMemory, supersedes *string, now string,
) ([]string, error) {
	id := uuid.New().String()
	rec := &repository.MemoryInsert{
		ID:         id,
		UserID:     input.UserID,
		SessionID:  input.SessionID,
		TurnID:     input.TurnID,
		Slot:       m.Slot,
		EntityKey:  m.EntityKey,
		Value:      m.Value,
		Confidence: m.Confidence,
		Evidence:   m.Evidence,
		Active:     true,
		Supersedes: supersedes,
		Mutation:   m.Mutation,
		StatedAt:   input.StatedAt,
		ValidFrom:  m.ValidFrom,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := repository.InsertMemory(ctx, tx, rec); err != nil {
		return nil, err
	}
	return []string{id}, nil
}

func fuzzyMatch(a, b string, threshold int) bool {
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == b {
		return true
	}

	aSet := toSet(strings.Fields(a))
	bSet := toSet(strings.Fields(b))
	if len(aSet) == 0 || len(bSet) == 0 {
		return false
	}

	intersection := 0
	for w := range aSet {
		if bSet[w] {
			intersection++
		}
	}
	union := len(aSet) + len(bSet) - intersection
	score := int(float64(intersection) / float64(union) * 100)
	return score >= threshold
}

func toSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
