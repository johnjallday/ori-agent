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
