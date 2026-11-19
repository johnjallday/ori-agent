package workspacerun

import (
	"encoding/json"
	"strings"
	"testing"
)

// A run's context plan used to say whether files wired to a ticket on the
// workspace canvas were injected into its prompt. The canvas is retired and
// nothing is injected, so the flag is gone from the plan. Runs stored before
// that still carry it, and they must keep loading.
func TestRunStoredWithTheRetiredAttachedFilesFlagStillLoads(t *testing.T) {
	db := openTestDB(t, t.Context())
	createTestWorkspace(t, t.Context(), db, "workspace")
	store := NewSQLiteStore(db)

	run := &Run{
		ID: "run", WorkspaceID: "workspace", ProfileID: ProfileGeneral,
		Executor: Executor{Kind: ExecutorKindOriAgent}, Status: RunStatusPending,
		ContextPlan: DefaultTaskContextPlan(),
	}
	if err := store.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}

	// Rewrite the stored plan exactly as an older build wrote it.
	legacy := `{"strategy":"task_default","include_workspace_snapshot":true,"include_attached_files":true,"expose_workspace_tools":true}`
	if _, err := db.ExecContext(t.Context(),
		`UPDATE workspace_runs SET context_plan_json = ? WHERE id = ? AND workspace_id = ?`,
		legacy, "run", "workspace"); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetRun(t.Context(), "workspace", "run")
	if err != nil {
		t.Fatalf("a run stored with the retired flag no longer loads: %v", err)
	}
	want := ContextPlan{Strategy: "task_default", IncludeWorkspaceSnapshot: true, ExposeWorkspaceTools: true}
	if got.ContextPlan != want {
		t.Fatalf("context plan = %+v, want %+v", got.ContextPlan, want)
	}

	// The plan a new run writes no longer names the flag.
	encoded, err := json.Marshal(DefaultTaskContextPlan())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "include_attached_files") {
		t.Fatalf("the default task plan still names the retired flag: %s", encoded)
	}
}
