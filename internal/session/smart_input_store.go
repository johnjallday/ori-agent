package session

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

type SmartInputOverrideLog struct {
	ID                string
	WorkspaceID       string
	Input             string
	PredictedDecision string
	SelectedDecision  string
	Method            string
	Confidence        float64
	CreatedAt         time.Time
}

type SmartInputOverrideStore struct {
	db *database.DB
}

func NewSmartInputOverrideStore(db *database.DB) *SmartInputOverrideStore {
	return &SmartInputOverrideStore{db: db}
}

func (s *SmartInputOverrideStore) LogOverride(ctx context.Context, override *SmartInputOverrideLog) error {
	if override.ID == "" {
		override.ID = uuid.New().String()
	}
	if override.CreatedAt.IsZero() {
		override.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO smart_input_overrides (
			id, workspace_id, input, predicted_decision, selected_decision,
			method, confidence, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		override.ID,
		override.WorkspaceID,
		override.Input,
		override.PredictedDecision,
		override.SelectedDecision,
		override.Method,
		override.Confidence,
		override.CreatedAt,
	)
	return err
}
