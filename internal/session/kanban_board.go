package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const workspaceSharedDataKanbanBoardKey = "kanban_board"

type KanbanBoardColumn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

type KanbanBoardConfig struct {
	Version int                 `json:"version,omitempty"`
	Columns []KanbanBoardColumn `json:"columns"`
}

func DefaultKanbanBoardConfig() KanbanBoardConfig {
	return KanbanBoardConfig{
		Version: 1,
		Columns: []KanbanBoardColumn{
			{ID: "backlog", Name: "Todo", Order: 1},
			{ID: "in_progress", Name: "In Progress", Order: 2},
			{ID: "review", Name: "Review", Order: 3},
			{ID: "done", Name: "Done", Order: 4},
		},
	}
}

func GetWorkspaceKanbanBoardConfig(ws *Workspace) (KanbanBoardConfig, bool) {
	if ws == nil || ws.SharedData == nil {
		return DefaultKanbanBoardConfig(), false
	}

	raw, ok := ws.SharedData[workspaceSharedDataKanbanBoardKey]
	if !ok || raw == nil {
		return DefaultKanbanBoardConfig(), false
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return DefaultKanbanBoardConfig(), false
	}

	var cfg KanbanBoardConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultKanbanBoardConfig(), false
	}

	normalized, err := NormalizeKanbanBoardConfig(cfg)
	if err != nil {
		return DefaultKanbanBoardConfig(), false
	}

	return normalized, true
}

func SetWorkspaceKanbanBoardConfig(ws *Workspace, cfg KanbanBoardConfig) error {
	if ws == nil {
		return errors.New("workspace is nil")
	}

	normalized, err := NormalizeKanbanBoardConfig(cfg)
	if err != nil {
		return err
	}

	if ws.SharedData == nil {
		ws.SharedData = map[string]interface{}{}
	}

	ws.SharedData[workspaceSharedDataKanbanBoardKey] = normalized
	return nil
}

func NormalizeKanbanBoardConfig(cfg KanbanBoardConfig) (KanbanBoardConfig, error) {
	if len(cfg.Columns) == 0 {
		return KanbanBoardConfig{}, errors.New("board columns are required")
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}

	seen := make(map[string]struct{}, len(cfg.Columns))
	for i := range cfg.Columns {
		cfg.Columns[i].ID = strings.TrimSpace(cfg.Columns[i].ID)
		cfg.Columns[i].Name = strings.TrimSpace(cfg.Columns[i].Name)

		if cfg.Columns[i].ID == "" {
			return KanbanBoardConfig{}, fmt.Errorf("column[%d].id is required", i)
		}
		if cfg.Columns[i].Name == "" {
			return KanbanBoardConfig{}, fmt.Errorf("column[%d].name is required", i)
		}

		if _, ok := seen[cfg.Columns[i].ID]; ok {
			return KanbanBoardConfig{}, fmt.Errorf("duplicate column id: %s", cfg.Columns[i].ID)
		}
		seen[cfg.Columns[i].ID] = struct{}{}

		// Normalize order: if not set, use 1-based index.
		if cfg.Columns[i].Order <= 0 {
			cfg.Columns[i].Order = i + 1
		}
	}

	return cfg, nil
}
