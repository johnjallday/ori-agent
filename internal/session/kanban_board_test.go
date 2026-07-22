package session

import "testing"

func TestNormalizeKanbanBoardConfig_ValidatesColumns(t *testing.T) {
	t.Run("requires columns", func(t *testing.T) {
		_, err := NormalizeKanbanBoardConfig(KanbanBoardConfig{})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("requires id and name", func(t *testing.T) {
		_, err := NormalizeKanbanBoardConfig(KanbanBoardConfig{Columns: []KanbanBoardColumn{{ID: "", Name: "Todo"}}})
		if err == nil {
			t.Fatalf("expected error")
		}
		_, err = NormalizeKanbanBoardConfig(KanbanBoardConfig{Columns: []KanbanBoardColumn{{ID: "todo", Name: ""}}})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("rejects duplicate ids", func(t *testing.T) {
		_, err := NormalizeKanbanBoardConfig(KanbanBoardConfig{Columns: []KanbanBoardColumn{{ID: "todo", Name: "Todo"}, {ID: "todo", Name: "Todo2"}}})
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

// TestDefaultKanbanBoardConfig_BeginsAtReady covers task-list 1.11/1.41: new
// workspaces must get Ready/InProgress/Review/Done, with no Backlog column
// (Backlog now lives in its own dedicated panel, not the task board).
func TestDefaultKanbanBoardConfig_BeginsAtReady(t *testing.T) {
	cfg := DefaultKanbanBoardConfig()
	if cfg.Version != kanbanBoardConfigVersion {
		t.Fatalf("Version = %d, want %d", cfg.Version, kanbanBoardConfigVersion)
	}
	if len(cfg.Columns) == 0 || cfg.Columns[0].ID != "ready" {
		t.Fatalf("first column = %+v, want id=ready", cfg.Columns)
	}
	for _, c := range cfg.Columns {
		if c.ID == "backlog" {
			t.Fatalf("default board must not contain a backlog column: %+v", cfg.Columns)
		}
	}
}

// TestMigrateLegacyKanbanBoardConfig covers task-list 1.13/1.15/1.44/1.92-98:
// legacy backlog/Todo and hyphenated in-progress columns are renamed, custom
// columns and order are preserved, and re-running is a no-op.
func TestMigrateLegacyKanbanBoardConfig(t *testing.T) {
	t.Run("renames untouched legacy default", func(t *testing.T) {
		legacy := KanbanBoardConfig{
			Version: 1,
			Columns: []KanbanBoardColumn{
				{ID: "backlog", Name: "Todo", Order: 1},
				{ID: "in_progress", Name: "In Progress", Order: 2},
				{ID: "review", Name: "Review", Order: 3},
				{ID: "done", Name: "Done", Order: 4},
			},
		}
		got := MigrateLegacyKanbanBoardConfig(legacy)
		if got.Version != kanbanBoardConfigVersion {
			t.Fatalf("Version = %d, want %d", got.Version, kanbanBoardConfigVersion)
		}
		if got.Columns[0].ID != "ready" || got.Columns[0].Name != "Ready" {
			t.Fatalf("column[0] = %+v, want id=ready name=Ready", got.Columns[0])
		}
		// Order and remaining columns untouched.
		if got.Columns[1].ID != "in_progress" || got.Columns[2].ID != "review" || got.Columns[3].ID != "done" {
			t.Fatalf("unexpected columns after migration: %+v", got.Columns)
		}
	})

	t.Run("preserves a user-renamed backlog column's label", func(t *testing.T) {
		legacy := KanbanBoardConfig{
			Version: 1,
			Columns: []KanbanBoardColumn{
				{ID: "backlog", Name: "Someday Maybe", Order: 1},
				{ID: "done", Name: "Done", Order: 2},
			},
		}
		got := MigrateLegacyKanbanBoardConfig(legacy)
		if got.Columns[0].ID != "ready" || got.Columns[0].Name != "Someday Maybe" {
			t.Fatalf("column[0] = %+v, want id=ready with custom name preserved", got.Columns[0])
		}
	})

	t.Run("normalizes hyphenated in-progress id", func(t *testing.T) {
		legacy := KanbanBoardConfig{
			Version: 1,
			Columns: []KanbanBoardColumn{
				{ID: "backlog", Name: "Backlog", Order: 1},
				{ID: "in-progress", Name: "In Progress", Order: 2},
				{ID: "done", Name: "Done", Order: 3},
			},
		}
		got := MigrateLegacyKanbanBoardConfig(legacy)
		if got.Columns[1].ID != "in_progress" {
			t.Fatalf("column[1].ID = %q, want in_progress", got.Columns[1].ID)
		}
	})

	t.Run("preserves user-created custom columns and order", func(t *testing.T) {
		legacy := KanbanBoardConfig{
			Version: 1,
			Columns: []KanbanBoardColumn{
				{ID: "backlog", Name: "Todo", Order: 1},
				{ID: "design", Name: "Design", Order: 2},
				{ID: "in_progress", Name: "In Progress", Order: 3},
				{ID: "qa", Name: "QA", Order: 4},
				{ID: "done", Name: "Done", Order: 5},
			},
		}
		got := MigrateLegacyKanbanBoardConfig(legacy)
		if len(got.Columns) != 5 {
			t.Fatalf("expected all 5 columns preserved, got %d", len(got.Columns))
		}
		if got.Columns[1].ID != "design" || got.Columns[3].ID != "qa" {
			t.Fatalf("custom columns were altered: %+v", got.Columns)
		}
	})

	t.Run("idempotent: already-current config is unchanged", func(t *testing.T) {
		current := DefaultKanbanBoardConfig()
		got := MigrateLegacyKanbanBoardConfig(current)
		if len(got.Columns) != len(current.Columns) {
			t.Fatalf("re-migrating a current config changed it: %+v", got)
		}
		for i := range got.Columns {
			if got.Columns[i] != current.Columns[i] {
				t.Fatalf("column[%d] changed on idempotent re-run: %+v vs %+v", i, got.Columns[i], current.Columns[i])
			}
		}
	})
}

// TestGetWorkspaceKanbanBoardConfig_MigratesPersistedLegacyConfig covers the
// end-to-end read path: a workspace whose SharedData still has a version-1
// board config gets the migrated (Ready-first) columns back, without a
// separate write-back migration step required.
func TestGetWorkspaceKanbanBoardConfig_MigratesPersistedLegacyConfig(t *testing.T) {
	ws := &Workspace{
		SharedData: map[string]any{
			workspaceSharedDataKanbanBoardKey: map[string]any{
				"version": 1,
				"columns": []map[string]any{
					{"id": "backlog", "name": "Todo", "order": 1},
					{"id": "in_progress", "name": "In Progress", "order": 2},
					{"id": "done", "name": "Done", "order": 3},
				},
			},
		},
	}

	cfg, found := GetWorkspaceKanbanBoardConfig(ws)
	if !found {
		t.Fatalf("expected an existing config to be found")
	}
	if cfg.Version != kanbanBoardConfigVersion {
		t.Fatalf("Version = %d, want %d", cfg.Version, kanbanBoardConfigVersion)
	}
	if cfg.Columns[0].ID != "ready" || cfg.Columns[0].Name != "Ready" {
		t.Fatalf("column[0] = %+v, want migrated Ready column", cfg.Columns[0])
	}
}

// TestGetWorkspaceKanbanBoardConfig_NoConfigUsesReadyFirstDefault covers a
// brand-new workspace with no persisted board config at all (task-list 1.11).
func TestGetWorkspaceKanbanBoardConfig_NoConfigUsesReadyFirstDefault(t *testing.T) {
	ws := &Workspace{}
	cfg, found := GetWorkspaceKanbanBoardConfig(ws)
	if found {
		t.Fatalf("expected no existing config")
	}
	if cfg.Columns[0].ID != "ready" {
		t.Fatalf("column[0] = %+v, want id=ready", cfg.Columns[0])
	}
}
