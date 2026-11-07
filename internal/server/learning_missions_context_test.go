package server

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningMissionsDesignContextExcludesWorkspaceBodiesAndForeignActivity(t *testing.T) {
	source, instance, _ := learningSourceFixture(t)
	if err := source.workspaces.Update(instance.Definition.WorkspaceID, func(ws *workspace.Workspace) error {
		ws.SharedData = map[string]any{"secret": "DO_NOT_SEND_SHARED_DATA"}
		return ws.MutateTask(instance.Progress.TicketID, func(task *workspace.Task) error {
			task.Details, task.Result = "DO_NOT_SEND_TICKET_BODY", "DO_NOT_SEND_RESULT"
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	prefs := learningmissions.DefaultPreferences()
	prefs.WorkspaceIDs = []string{instance.Definition.WorkspaceID}
	prefs.ScopeBindings = map[string]string{instance.Definition.WorkspaceID: instance.Definition.Incarnation}
	prefs.Observe, prefs.ConsentVersion, prefs.ObservationSince = true, 1, time.Now().Add(-time.Hour).UnixMilli()
	owner := learningmissions.Owner{ID: "local", Preferences: prefs}
	projection, err := learningmissions.BuildContext(t.Context(), source, owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := projection.ModelJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"DO_NOT_SEND", "The exact original result", instance.Definition.WorkspaceID,
		instance.Progress.RunID, "incarnation", "scope_digest", "executor", "task_payload", "shared_data", "reviewed_input"} {
		if strings.Contains(string(prompt), forbidden) {
			t.Fatalf("prompt leaked %q: %s", forbidden, prompt)
		}
	}
	if len(projection.Data.Candidates) != 1 || projection.Data.Observations[0].Activity.Succeeded != 1 || projection.Data.Observations[0].Activity.Coverage != "partial" {
		t.Fatalf("metadata projection: %s", prompt)
	}
	owner.ID = "foreign"
	projection, err = learningmissions.BuildContext(t.Context(), source, owner, time.Now())
	if err != nil || len(projection.Data.Candidates) != 0 || projection.Data.Observations[0].Activity.Coverage != "unknown" {
		t.Fatalf("foreign context: %+v %v", projection, err)
	}
	owner.ID, owner.Preferences.Observe = "local", false
	projection, err = learningmissions.BuildContext(t.Context(), source, owner, time.Now())
	if err != nil || projection.Data.Observations[0].Activity.Coverage != "unknown" {
		t.Fatalf("observation without opt-in: %+v %v", projection, err)
	}
}
