package workspace

import (
	"errors"
	"testing"
)

type legacySummarySource struct {
	listed []*Workspace
	err    error
	calls  int
}

func (s *legacySummarySource) ListActive() ([]*Workspace, error) {
	s.calls++
	return s.listed, s.err
}

func TestListActiveSummaries_LegacyBridgeProjectsWithoutChangingRecords(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Legacy"})
	ws.Tasks = []Task{{ID: "task"}}
	ws.SharedData = map[string]any{"secret-shaped-fixture": "not listing metadata"}
	source := &legacySummarySource{listed: []*Workspace{nil, ws}}
	listed, err := ListActiveSummaries(source)
	if err != nil || len(listed) != 1 || listed[0].ID != ws.ID || source.calls != 1 {
		t.Fatalf("legacy listing projection: %+v, err=%v, calls=%d", listed, err, source.calls)
	}
	listed[0].Name = "uncommitted edit"
	if ws.Name != "Legacy" || len(ws.Tasks) != 1 || len(ws.SharedData) != 1 {
		t.Fatal("legacy bridge changed its source record")
	}
}

func TestListActiveSummaries_LegacyErrorsAndEmptySources(t *testing.T) {
	failure := errors.New("listing failed")
	if _, err := ListActiveSummaries(&legacySummarySource{err: failure}); !errors.Is(err, failure) {
		t.Fatalf("legacy error = %v, want %v", err, failure)
	}
	if _, err := ListActiveSummaries(nil); err == nil {
		t.Fatal("nil source should fail")
	}
	listed, err := ListActiveSummaries(&legacySummarySource{})
	if err != nil || listed == nil || len(listed) != 0 {
		t.Fatalf("empty listing = %#v, err=%v", listed, err)
	}
}

func TestSyncStoreSummaryListing_LegacyPrimaryCompatibility(t *testing.T) {
	primary := NewInMemoryStore()
	seed := NewWorkspace(CreateWorkspaceParams{Name: "Legacy Primary"})
	if err := primary.Save(seed); err != nil {
		t.Fatal(err)
	}
	// Embedding the legacy interface hides the primary's optional summary API.
	wrapped := struct{ Store }{primary}
	listed, err := NewSyncStore(wrapped, nil).ListActiveSummaries()
	if err != nil || len(listed) != 1 || listed[0].ID != seed.ID {
		t.Fatalf("legacy primary summary listing: %+v, err=%v", listed, err)
	}
}
