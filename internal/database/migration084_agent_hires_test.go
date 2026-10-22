package database

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration084AgentHiresIsIdempotentBoundedAndResetInventoried(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "hires.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 2 {
		if err := db.migration084AgentHires(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	insert := `INSERT INTO agent_hires (id, user_id, request_id, agent_name, workspace_id, brief, role, blueprint, state, created_at, updated_at)
		VALUES (?, 'local', ?, 'Reviewer', 'ws-1', 'Check the work.', 'validator', 'blank', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	if _, err := db.ExecContext(t.Context(), insert, "hire-1", "req-1", "looking"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), insert, "hire-2", "req-1", "looking"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("a reused request id was stored: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), insert, "hire-3", "req-3", "hired"); err == nil {
		t.Fatal("a state outside the hire's states was stored")
	}

	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified hire state: %v %v", err, inspection.Problems)
	}
	if count := inspection.Counts["agent_hires"]; count == nil || *count != 1 {
		t.Fatalf("agent_hires missing from the reset inventory: %v", count)
	}
}
