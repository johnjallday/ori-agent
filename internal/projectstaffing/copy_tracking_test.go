package projectstaffing

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// writeSpy fails the test on any workspace write: CopyTracking is an
// inspection read and must never carry, heal or drop anything.
type writeSpy struct {
	workspace.Store
	t *testing.T
}

func (s writeSpy) Save(ws *workspace.Workspace) error {
	s.t.Errorf("CopyTracking saved workspace %s", ws.ID)
	return nil
}

func (s writeSpy) Update(id string, fn func(*workspace.Workspace) error) error {
	s.t.Errorf("CopyTracking updated workspace %s", id)
	return nil
}

func (s writeSpy) SaveWorkspaceAgent(workspaceID, agentName string, _ *agent.Agent) error {
	s.t.Errorf("CopyTracking wrote %s's copy in %s", agentName, workspaceID)
	return nil
}

func trackingOf(t *testing.T, st workspace.Store, roster definitions, songID string) string {
	t.Helper()
	service := New(writeSpy{Store: st, t: t}, roster)
	library, _ := roster.GetAgent("REAPER Assistant")
	state, _ := service.CopyTracking("REAPER Assistant", songID, library, copyOf(t, st, songID))
	return state
}

func TestCopyTrackingReportsInStepPendingAndCustomizedWithoutWriting(t *testing.T) {
	st, _, roster := sharedFixture(t)

	// song-b is changed in its project; song-c already holds a later edit.
	mine := copyOf(t, st, "song-b")
	mine.Settings.SystemPrompt = "Only for song B."
	if err := st.SaveWorkspaceAgent("song-b", "REAPER Assistant", mine); err != nil {
		t.Fatal(err)
	}
	if got := trackingOf(t, st, roster, "song-a"); got != CopyInStep {
		t.Fatalf("song-a = %s, want in_step", got)
	}
	if got := trackingOf(t, st, roster, "song-b"); got != CopyCustomized {
		t.Fatalf("song-b = %s, want customized", got)
	}

	// The library moves on and no carry has run yet: tracked, unchanged copies
	// are pending, not customised.
	roster["REAPER Assistant"].Settings.Model = "terra"
	if got := trackingOf(t, st, roster, "song-a"); got != CopyPending {
		t.Fatalf("song-a after a library edit = %s, want pending", got)
	}
	if got := trackingOf(t, st, roster, "song-b"); got != CopyCustomized {
		t.Fatalf("song-b after a library edit = %s, want customized", got)
	}

	// A copy that already holds the library's settings is in step even when
	// its recorded digest is stale (Carry heals it rather than skipping it).
	healed := copyOf(t, st, "song-c")
	healed.Settings.Model = "terra"
	if err := st.SaveWorkspaceAgent("song-c", "REAPER Assistant", healed); err != nil {
		t.Fatal(err)
	}
	if got := trackingOf(t, st, roster, "song-c"); got != CopyInStep {
		t.Fatalf("song-c = %s, want in_step", got)
	}
}

func TestCopyTrackingNeverInfersTrackingFromEqualValues(t *testing.T) {
	st, _, roster := sharedFixture(t)
	// A plain workspace holding a copy equal to the library is independent.
	plain := &workspace.Workspace{ID: "plain", Name: "Plain", Status: workspace.StatusActive}
	if err := st.Save(plain); err != nil {
		t.Fatal(err)
	}
	library, _ := roster.GetAgent("REAPER Assistant")
	if err := st.SaveWorkspaceAgent("plain", "REAPER Assistant", library); err != nil {
		t.Fatal(err)
	}
	service := New(writeSpy{Store: st, t: t}, roster)
	state, err := service.CopyTracking("REAPER Assistant", "plain", library, library)
	if err != nil || state != CopyUntracked {
		t.Fatalf("equal-valued unlinked copy = %s %v, want untracked", state, err)
	}
	// Another agent in a tracked song has no record of its own.
	other := &agent.Agent{Settings: types.Settings{Model: "luna"}}
	state, err = service.CopyTracking("Mix Helper", "song-a", other, other)
	if err != nil || state != CopyUntracked {
		t.Fatalf("unrecorded agent = %s %v, want untracked", state, err)
	}
}

// End to end: the real projection with the real tracking reader reports
// in-step, pending and customized copies and the matching library impact,
// and writes nothing.
func TestSettingsOwnershipWithRealCopyTracking(t *testing.T) {
	st, _, roster := sharedFixture(t)
	mine := copyOf(t, st, "song-b")
	mine.Settings.SystemPrompt = "Only for song B."
	if err := st.SaveWorkspaceAgent("song-b", "REAPER Assistant", mine); err != nil {
		t.Fatal(err)
	}
	roster["REAPER Assistant"].Settings.Model = "terra" // no carry has run yet
	spy := writeSpy{Store: st, t: t}
	service := New(spy, roster)
	src := workspace.SettingsOwnershipSources{
		Workspaces: spy,
		Library: func(name string) store.LibraryRecord {
			ag, ok := roster.GetAgent(name)
			if !ok {
				return store.LibraryRecord{State: store.LibraryAbsent}
			}
			return store.LibraryRecord{Definition: ag, State: store.LibraryPresent, Owner: store.LibraryOwnerLibrary}
		},
		Tracking: service.CopyTracking,
	}
	for song, want := range map[string]string{"song-a": CopyPending, "song-b": CopyCustomized, "song-c": CopyPending} {
		out, err := workspace.ProjectSettingsOwnership(src, workspace.SettingsOwnershipRequest{Agent: "REAPER Assistant", WorkspaceID: song})
		if err != nil {
			t.Fatal(err)
		}
		if out.Tracking.State != want {
			t.Errorf("%s tracking = %s, want %s", song, out.Tracking.State, want)
		}
	}
	lib, err := workspace.ProjectSettingsOwnership(src, workspace.SettingsOwnershipRequest{Agent: "REAPER Assistant", IncludeImpact: true})
	if err != nil {
		t.Fatal(err)
	}
	effects := map[string]string{}
	for _, c := range lib.Impact.Copies {
		effects[c.WorkspaceID] = c.Effect
	}
	if effects["song-a"] != workspace.ImpactPending || effects["song-b"] != workspace.ImpactCustomized {
		t.Fatalf("impact = %+v", lib.Impact.Copies)
	}
}

func TestCopyTrackingIsUnknownWhenTheRecordCannotBeConfirmed(t *testing.T) {
	st, _, roster := sharedFixture(t)
	if err := st.Update("song-c", func(song *workspace.Workspace) error {
		song.AgentInstances = nil
		link := song.GetAssistantProjectLink()
		link.ProjectBindings.Bindings = nil
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := trackingOf(t, st, roster, "song-c"); got != CopyUnknown {
		t.Fatalf("role no longer filled = %s, want unknown", got)
	}

	service := New(writeSpy{Store: st, t: t}, roster)
	if state, err := service.CopyTracking("REAPER Assistant", "missing", nil, &agent.Agent{}); state != CopyUnknown || err == nil {
		t.Fatalf("missing workspace = %s %v, want unknown with a reason", state, err)
	}
	// Tracked and unchanged, but no library profile to compare with.
	if state, err := service.CopyTracking("REAPER Assistant", "song-a", nil, copyOf(t, st, "song-a")); state != CopyUnknown || err == nil {
		t.Fatalf("no library = %s %v, want unknown", state, err)
	}
}
