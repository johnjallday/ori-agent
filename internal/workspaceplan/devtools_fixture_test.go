package workspaceplan

import (
	"bytes"
	"os"
	"testing"
)

// This producer-owned fixture is the cross-repository artifact contract. The
// external devtools consumer must test these bytes rather than import Ori's
// internal renderer or carry its own copy of application rendering code.
func TestApprovedTaskListMatchesDevtoolsFixture(t *testing.T) {
	t.Parallel()
	plan := &Plan{ID: "plan-1", WorkspaceID: "ws-1", Title: "Bridge"}
	artifact := ProposedArtifact{Kind: ArtifactTaskList, Title: "Tasks: Bridge"}
	version := &Version{
		Number: 1,
		Title:  "Bridge",
		Content: PlanContent{Groups: []TaskGroup{{
			ID: "group-1", Title: "First slice",
			Items: []TaskItem{{ID: "item-1", Description: "Wire the approved path"}},
		}}},
	}
	got, err := (DefaultArtifactRenderer{}).Render(artifact, plan, version)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/devtools-approved-tasks.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("approved task artifact changed; review producer/consumer compatibility before updating the fixture\ngot:\n%s\nwant:\n%s", got, want)
	}
}
