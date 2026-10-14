package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementEvidence_ExactManagerReadsSavedMetadataWithoutRescan(t *testing.T) {
	f := newRunFixture(t)
	authority := ManagerAuthority{HomeID: f.scope.HomeID, AgentName: managerBinding.AgentName, AgentInstanceID: managerBinding.AgentInstanceID}
	before := f.doc(t)
	// Deleting the physical tree must not trigger resolution/discovery. The
	// canonical record remains an explicitly dated observation, not live truth.
	if err := os.RemoveAll(f.tree.root); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.library.ArrangementEvidence(t.Context(), f.scope.OwnerUserID, authority)
	if err != nil || len(snapshot.Items) != len(before.Entries) || len(snapshot.Items) == 0 {
		t.Fatalf("saved evidence: %+v %v", snapshot, err)
	}
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.Overview(t.Context()); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{f.tree.root, "absolute_path", "relative_folder", "root_identity", "song_facts", "collaborators"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("private %q escaped projection", forbidden)
		}
	}
	for _, mutate := range []func(*ManagerAuthority){
		func(a *ManagerAuthority) { a.AgentName = "Personal Assistant" },
		func(a *ManagerAuthority) { a.AgentInstanceID = "replacement" },
		func(a *ManagerAuthority) { a.HomeID = "other-home" },
	} {
		other := authority
		mutate(&other)
		if _, err := f.library.ArrangementEvidence(t.Context(), f.scope.OwnerUserID, other); !errors.Is(err, workspacearrangement.ErrUnavailable) {
			t.Fatal("foreign actor accessed Home evidence")
		}
	}
	if _, err := f.library.ArrangementEvidence(t.Context(), "another-user", authority); !errors.Is(err, workspacearrangement.ErrUnavailable) {
		t.Fatal("foreign owner accessed Home evidence")
	}
	f.available.Store(false)
	if _, err := f.library.ArrangementEvidence(t.Context(), f.scope.OwnerUserID, authority); !errors.Is(err, workspacearrangement.ErrUnavailable) {
		t.Fatal("missing provider served cached evidence")
	}
	f.available.Store(true)
	after := f.doc(t)
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	if string(left) != string(right) {
		t.Fatal("planning read mutated canonical library")
	}
}
