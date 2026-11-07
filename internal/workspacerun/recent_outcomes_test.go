package workspacerun

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestRecentOutcomesAreBoundedMetadataForTheSelectedWindow(t *testing.T) {
	db := openTestDB(t, t.Context())
	for _, id := range []string{"workspace", "foreign"} {
		createTestWorkspace(t, t.Context(), db, id)
	}
	for name, store := range map[string]Store{"sqlite": NewSQLiteStore(db), "memory": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			for n := range 30 {
				start, finish := now.Add(-time.Duration(n+2)*time.Minute), now.Add(-time.Duration(n+1)*time.Minute)
				status := RunStatusSucceeded
				if n%2 == 0 {
					status = RunStatusFailed
				}
				run := &Run{ID: fmt.Sprintf("r%02d", n), WorkspaceID: "workspace", ProfileID: ProfileGeneral,
					Status: status, StartedAt: &start, FinishedAt: &finish, Error: "private diagnostic"}
				switch n {
				case 0:
					run.WorkspaceID = "foreign"
				case 1:
					run.StartedAt = nil // old unknown execution is not backfilled
				case 2:
					run.Status = RunStatusExecuting
				case 3:
					run.FinishedAt = nil
				}
				if err := store.CreateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			}
			reader := store.(RecentOutcomeReader)
			got, err := reader.RecentOutcomes(t.Context(), "workspace", now.Add(-time.Hour), now, 20)
			if err != nil || len(got) != 20 || got[0] != RunStatusFailed || got[19] != RunStatusSucceeded {
				t.Fatalf("bounded page: %+v %v", got, err)
			}
			got, err = reader.RecentOutcomes(t.Context(), "workspace", now.Add(-7*time.Minute), now, 20)
			if err != nil || !slices.Equal(got, []RunStatus{RunStatusFailed, RunStatusSucceeded}) {
				t.Fatalf("consent window: %+v %v", got, err)
			}
			got, err = reader.RecentOutcomes(t.Context(), "missing", now.Add(-time.Hour), now, 20)
			if err != nil || len(got) != 0 {
				t.Fatalf("foreign metadata: %+v %v", got, err)
			}
			for _, limit := range []int{0, -1, 101} {
				if _, err := reader.RecentOutcomes(t.Context(), "workspace", now.Add(-time.Hour), now, limit); err == nil {
					t.Fatal("accepted unbounded request")
				}
			}
		})
	}
}
