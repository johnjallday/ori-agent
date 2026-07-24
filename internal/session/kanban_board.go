package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const workspaceSharedDataKanbanBoardKey = "kanban_board"

// kanbanBoardConfigVersion is the current board-config schema version (PRD
// workspace-backlog FR40-44, 92-98). Version 1 boards defaulted to a
// "backlog"-ID first column, which now collides with the canonical Backlog
// lifecycle stage (workspace.TaskStatusBacklog); version 2 boards begin at
// Ready instead. See MigrateLegacyKanbanBoardConfig.
const kanbanBoardConfigVersion = 2

// legacyBacklogColumnID/legacyBacklogColumnName are the version-1 default
// first column's identifier and label, kept only so migration can recognize
// an untouched legacy default and rename it to Ready.
const legacyBacklogColumnID = "backlog"
const legacyBacklogColumnName = "Todo"

// legacyHyphenInProgressID is a second, inconsistent in-progress column ID
// seen in one JS default-board implementation (FR98); normalized alongside
// the backlog rename.
const legacyHyphenInProgressID = "in-progress"

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
		Version: kanbanBoardConfigVersion,
		Columns: []KanbanBoardColumn{
			{ID: "ready", Name: "Ready", Order: 1},
			{ID: "in_progress", Name: "In Progress", Order: 2},
			{ID: "review", Name: "Review", Order: 3},
			{ID: "done", Name: "Done", Order: 4},
		},
	}
}

// MigrateLegacyKanbanBoardConfig normalizes a possibly-legacy board config to
// the current version (FR92, 98-99): it renames a column whose ID is the old
// "backlog" default to "ready" (preserving a user-customized name, or
// applying "Ready" when the name was still the untouched legacy "Todo"
// default), normalizes the hyphenated "in-progress" identifier to
// "in_progress", and bumps Version. User-created columns and their order are
// left untouched. Safe to call repeatedly: a config already at the current
// version is returned unchanged, and re-running finds no more legacy IDs to
// rename.
func MigrateLegacyKanbanBoardConfig(cfg KanbanBoardConfig) KanbanBoardConfig {
	if cfg.Version >= kanbanBoardConfigVersion {
		return cfg
	}
	for i := range cfg.Columns {
		switch cfg.Columns[i].ID {
		case legacyBacklogColumnID:
			cfg.Columns[i].ID = "ready"
			if cfg.Columns[i].Name == legacyBacklogColumnName || cfg.Columns[i].Name == "Backlog" {
				cfg.Columns[i].Name = "Ready"
			}
		case legacyHyphenInProgressID:
			cfg.Columns[i].ID = "in_progress"
		}
	}
	cfg.Version = kanbanBoardConfigVersion
	return cfg
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

	cfg = MigrateLegacyKanbanBoardConfig(cfg)

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

	cfg = MigrateLegacyKanbanBoardConfig(cfg)

	normalized, err := NormalizeKanbanBoardConfig(cfg)
	if err != nil {
		return err
	}

	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
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
