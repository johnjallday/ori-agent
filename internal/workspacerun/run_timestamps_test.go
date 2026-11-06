package workspacerun

import "testing"

func TestRunStatusPersistsExecutionTimestampsOnce(t *testing.T) {
	db := openTestDB(t, t.Context())
	createTestWorkspace(t, t.Context(), db, "workspace")
	for name, store := range map[string]Store{"sqlite": NewSQLiteStore(db), "memory": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			run := &Run{ID: "run", WorkspaceID: "workspace", ProfileID: ProfileGeneral, Executor: Executor{Kind: ExecutorKindOriAgent}, Status: RunStatusPending}
			if err := store.CreateRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			for _, state := range []RunStatus{RunStatusPreparing, RunStatusPreparingContext} {
				if err := store.UpdateStatus(t.Context(), "workspace", "run", state, ""); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.GetRun(t.Context(), "workspace", "run")
			if err != nil || before.StartedAt != nil || before.FinishedAt != nil {
				t.Fatalf("preparation claimed execution: %+v %v", before, err)
			}
			if err := store.UpdateStatus(t.Context(), "workspace", "run", RunStatusExecuting, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateStatus(t.Context(), "workspace", "run", RunStatusSucceeded, ""); err != nil {
				t.Fatal(err)
			}
			first, err := store.GetRun(t.Context(), "workspace", "run")
			if err != nil || first.StartedAt == nil || first.FinishedAt == nil || first.FinishedAt.Before(*first.StartedAt) {
				t.Fatalf("missing execution times: %+v %v", first, err)
			}
			if err := store.UpdateStatus(t.Context(), "workspace", "run", RunStatusExecuting, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateStatus(t.Context(), "workspace", "run", RunStatusSucceeded, ""); err != nil {
				t.Fatal(err)
			}
			again, err := store.GetRun(t.Context(), "workspace", "run")
			if err != nil || !first.StartedAt.Equal(*again.StartedAt) || !first.FinishedAt.Equal(*again.FinishedAt) {
				t.Fatal("duplicate status moved timestamps")
			}
		})
	}
}
