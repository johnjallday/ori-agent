package projectconnection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// A project connected with a goal is created with it as its workspace goal,
// its first goal run left pending; the goal is part of the reviewed input, so
// a commit with another goal is a changed review.
func TestAttachExistingCreatesTheProjectWithItsReviewedGoal(t *testing.T) {
	service, store, selections := connectionService(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "Song.rpp"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-goal", Template: connectionTemplate(t)}
	request := Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Song",
		Goal: "  Finish the\n bridge. ",
	}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	other := request
	other.Goal = "Something else."
	if _, err := service.Commit(context.Background(), scope, other, preview.InputDigest, preview.OwnerDigest); !errors.Is(err, ErrChanged) {
		t.Fatalf("a goal the review did not show: %v", err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Get(result.ProjectWorkspaceID)
	if err != nil || child.Mission != "Finish the bridge." || !child.GoalFirstRunPending {
		t.Fatalf("child goal = %q pending=%v err=%v", child.Mission, child.GoalFirstRunPending, err)
	}
}

func TestAttachExistingWithoutAGoalLeavesTheProjectWithout(t *testing.T) {
	service, store, selections := connectionService(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "Song.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(external)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-plain", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Song"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if child, _ := store.Get(result.ProjectWorkspaceID); child.Mission != "" || child.GoalFirstRunPending {
		t.Fatalf("child goal = %q pending=%v", child.Mission, child.GoalFirstRunPending)
	}
}

func TestPreviewRefusesAGoalThatIsNotOneBoundedLine(t *testing.T) {
	service, _, selections := connectionService(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "Song.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(external)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-bad", Template: connectionTemplate(t)}
	for _, goal := range []string{strings.Repeat("a", maxGoalRunes+1), "Finish\x00it"} {
		request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Song", Goal: goal}
		if _, err := service.Preview(context.Background(), scope, request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("goal %q: err = %v", goal[:min(len(goal), 12)], err)
		}
	}
}
