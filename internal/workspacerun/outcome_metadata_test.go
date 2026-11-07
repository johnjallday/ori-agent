package workspacerun

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOutcomeMetadataUsesOnlyExactScopedMetadata(t *testing.T) {
	db := openTestDB(t, t.Context())
	createTestWorkspace(t, t.Context(), db, "workspace")
	for name, store := range map[string]Store{"sqlite": NewSQLiteStore(db), "memory": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			start, finish := time.Now().UTC().Add(-time.Minute), time.Now().UTC()
			run := &Run{ID: "run", WorkspaceID: "workspace", ProfileID: ProfileGeneral, Status: RunStatusSucceeded,
				Scope: Scope{TargetTaskID: "ticket"}, Executor: Executor{Kind: ExecutorKindOriAgent}, StartedAt: &start, FinishedAt: &finish,
				Prompt: "private prompt", Error: "private error"}
			if err := store.CreateRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AddArtifact(t.Context(), "workspace", "run", Artifact{Kind: ArtifactFile, Inline: []byte("private result"), Metadata: map[string]any{"role": "task_result"}}); err != nil {
				t.Fatal(err)
			}
			if name == "sqlite" {
				if _, err := db.ExecContext(t.Context(), `UPDATE workspace_runs SET profile_snapshot_json='invalid-json',prepared_context_json='invalid-json' WHERE id='run'`); err != nil {
					t.Fatal(err)
				}
			}
			reader := store.(OutcomeMetadataReader)
			out, err := reader.OutcomeMetadata(t.Context(), "workspace", "run")
			if err != nil || out.TicketID != "ticket" || out.Executor != ExecutorKindOriAgent || out.Status != RunStatusSucceeded || out.ResultCount != 1 || out.ResultBytes != int64(len("private result")) || out.StartedAt == nil {
				t.Fatalf("metadata: %+v %v", out, err)
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private") {
				t.Fatal("read private Run/artifact contents")
			}
			*out.StartedAt = time.Time{}
			again, err := reader.OutcomeMetadata(t.Context(), "workspace", "run")
			if err != nil || again.StartedAt.IsZero() {
				t.Fatal("projection mutated canonical times")
			}
			if _, err := reader.OutcomeMetadata(t.Context(), "foreign", "run"); err == nil {
				t.Fatal("foreign metadata exposed")
			}
			if _, err := store.AddArtifact(t.Context(), "workspace", "run", Artifact{Kind: ArtifactFile, Inline: []byte("ambiguous"), Metadata: map[string]any{"role": "task_result"}}); err != nil {
				t.Fatal(err)
			}
			out, err = reader.OutcomeMetadata(t.Context(), "workspace", "run")
			if err != nil || out.ResultCount != 2 {
				t.Fatal("ambiguity hidden")
			}
		})
	}
}
