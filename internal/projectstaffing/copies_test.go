package projectstaffing

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// definitions is a roster that holds real agent definitions.
type definitions map[string]*agent.Agent

func (d definitions) ListAgents() []string {
	names := make([]string, 0, len(d))
	for name := range d {
		names = append(names, name)
	}
	return names
}

func (d definitions) GetAgent(name string) (*agent.Agent, bool) {
	ag, found := d[name]
	if !found {
		return nil, false
	}
	clone := *ag
	return &clone, true
}

func (d definitions) AgentOrigin(name string) (store.AgentOrigin, bool) {
	_, found := d[name]
	return store.AgentOrigin{Source: store.SourceRoster}, found
}

func addSong(t *testing.T, st workspace.Store, id string) {
	t.Helper()
	song := &workspace.Workspace{ID: id, Name: id, Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	song.SetAssistantProjectLink(&workspace.AssistantProjectLink{
		StationWorkspaceID: "home", ProjectProvider: provider(digest),
		ProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true}},
	})
	if err := st.Save(song); err != nil {
		t.Fatal(err)
	}
}

// staff does what a create or bind does in a song: it writes the agent's current
// definition as the song's own copy and binds the role, then settles.
func staff(t *testing.T, st workspace.Store, service *Service, roster definitions, songID string) {
	t.Helper()
	fills, err := service.Fill(songID, []string{"reaper-assistant"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer})
	if err != nil || len(fills) != 1 {
		t.Fatalf("fill %s = %+v %v", songID, fills, err)
	}
	source := workspace.RoleSourceAssigned
	if fills[0].Mode == ModeCreate {
		roster[fills[0].Name] = &agent.Agent{Role: types.RoleGeneral, Settings: types.Settings{Provider: "codex", Model: "luna", SystemPrompt: "Help with the song."}}
		source = workspace.RoleSourceCreated
	}
	definition, _ := roster.GetAgent(fills[0].Name)
	definition.Statistics = &types.AgentStatistics{MessageCount: 7}
	if err := st.SaveWorkspaceAgent(songID, fills[0].Name, definition); err != nil {
		t.Fatal(err)
	}
	bindInSong(t, st, songID, fills[0].Name, source)
	if err := service.Settle(songID); err != nil {
		t.Fatal(err)
	}
}

func copyOf(t *testing.T, st workspace.Store, songID string) *agent.Agent {
	t.Helper()
	ag, found, err := st.GetWorkspaceAgent(songID, "REAPER Assistant")
	if err != nil || !found {
		t.Fatalf("%s has no copy: %v", songID, err)
	}
	clone := *ag
	return &clone
}

func names(refs []workspace.WorkspaceRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.ID)
	}
	return out
}

func sharedFixture(t *testing.T) (workspace.Store, *Service, definitions) {
	t.Helper()
	st := workspace.NewInMemoryStore()
	roster := sharedOn(t, st)
	return st, New(st, roster), roster
}

// sharedOn seeds a Home with three songs on st, all staffed by one shared
// assistant under the Home's consent.
func sharedOn(t *testing.T, st workspace.Store) definitions {
	t.Helper()
	seedHome(t, st)
	addSong(t, st, "song-c")
	roster := definitions{}
	service := New(st, roster)
	for _, song := range []string{"song-a", "song-b", "song-c"} {
		staff(t, st, service, roster, song)
	}
	return roster
}

// What the carry reads after a restart is all on disk: each song's copy in its
// folder and the digests in the Home's workspace.json.
func TestCarrySurvivesARestart(t *testing.T) {
	root := t.TempDir()
	files, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	roster := sharedOn(t, files)
	mine := copyOf(t, files, "song-b")
	mine.Settings.SystemPrompt = "Only for song B."
	if err := files.SaveWorkspaceAgent("song-b", "REAPER Assistant", mine); err != nil {
		t.Fatal(err)
	}

	reopened, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	roster["REAPER Assistant"].Settings.SystemPrompt = "Help with every song."
	report, err := New(reopened, roster).Carry("REAPER Assistant")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(report.Updated); len(got) != 2 || got[0] != "song-a" || got[1] != "song-c" {
		t.Fatalf("updated after a restart = %v", got)
	}
	if got := names(report.Customised); len(got) != 1 || got[0] != "song-b" {
		t.Fatalf("customised after a restart = %v", got)
	}
	if copied := copyOf(t, reopened, "song-c"); copied.Settings.SystemPrompt != "Help with every song." {
		t.Fatalf("song C on disk = %+v", copied.Settings)
	}
}

// D10: an edit to the shared assistant reaches every song it works on, except a
// song where the user changed that song's own copy.
func TestCarryReachesBoundSongsAndKeepsACustomisedCopy(t *testing.T) {
	st, service, roster := sharedFixture(t)
	consent, _ := service.Consents().Read("home")
	if got := consent.Roles[0].Copies; len(got) != 3 {
		t.Fatalf("settle tracked %d copies: %+v", len(got), got)
	}

	// The user changes the prompt inside Song B only.
	mine := copyOf(t, st, "song-b")
	mine.Settings.SystemPrompt = "Only for song B."
	if err := st.SaveWorkspaceAgent("song-b", "REAPER Assistant", mine); err != nil {
		t.Fatal(err)
	}
	// Then edits the shared assistant's model and prompt.
	roster["REAPER Assistant"].Settings.Model = "terra"
	roster["REAPER Assistant"].Settings.SystemPrompt = "Help with every song."

	report, err := service.Carry("REAPER Assistant")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(report.Updated); len(got) != 2 || got[0] != "song-a" || got[1] != "song-c" {
		t.Fatalf("updated = %v", got)
	}
	if got := names(report.Customised); len(got) != 1 || got[0] != "song-b" {
		t.Fatalf("customised = %v", got)
	}
	for _, song := range []string{"song-a", "song-c"} {
		copied := copyOf(t, st, song)
		if copied.Settings.Model != "terra" || copied.Settings.SystemPrompt != "Help with every song." {
			t.Fatalf("%s did not get the edit: %+v", song, copied.Settings)
		}
		if copied.Statistics == nil || copied.Statistics.MessageCount != 7 || copied.Role != types.RoleGeneral {
			t.Fatalf("%s lost what the edit does not carry: %+v", song, copied)
		}
	}
	if kept := copyOf(t, st, "song-b"); kept.Settings.SystemPrompt != "Only for song B." || kept.Settings.Model != "luna" {
		t.Fatalf("song B's own copy was overwritten: %+v", kept.Settings)
	}

	// Carrying again changes nothing and still names the customised song.
	again, err := service.Carry("REAPER Assistant")
	if err != nil || len(again.Updated) != 0 || len(again.Customised) != 1 {
		t.Fatalf("second carry = %+v %v", again, err)
	}
	consent, _ = service.Consents().Read("home")
	want := CopyDigest(roster["REAPER Assistant"])
	for _, entry := range consent.Roles[0].Copies {
		if (entry.WorkspaceID == "song-b") == (entry.Digest == want) {
			t.Fatalf("recorded digests = %+v", consent.Roles[0].Copies)
		}
	}
}

// A copy that already has the edit (a carry whose record write was lost) is
// current, not customised: the record is healed.
func TestCarryHealsARecordThatMissedAWrite(t *testing.T) {
	st, service, roster := sharedFixture(t)
	roster["REAPER Assistant"].Settings.SystemPrompt = "New."
	updated := copyOf(t, st, "song-a")
	updated.Settings.SystemPrompt = "New."
	if err := st.SaveWorkspaceAgent("song-a", "REAPER Assistant", updated); err != nil {
		t.Fatal(err)
	}
	report, err := service.Carry("REAPER Assistant")
	if err != nil || len(report.Customised) != 0 || len(report.Updated) != 2 {
		t.Fatalf("carry = %+v %v", report, err)
	}
	consent, _ := service.Consents().Read("home")
	for _, entry := range consent.Roles[0].Copies {
		if entry.Digest != CopyDigest(roster["REAPER Assistant"]) {
			t.Fatalf("not healed: %+v", consent.Roles[0].Copies)
		}
	}
}

// A song whose role was cleared, or given to another agent, is no longer the
// shared assistant's: it is dropped and never written.
func TestCarryDropsASongThatNoLongerHasTheAgent(t *testing.T) {
	st, service, roster := sharedFixture(t)
	if err := st.Update("song-c", func(song *workspace.Workspace) error {
		song.AgentInstances = nil
		link := song.GetAssistantProjectLink()
		link.ProjectBindings.Bindings = nil
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	roster["REAPER Assistant"].Settings.Model = "terra"
	report, err := service.Carry("REAPER Assistant")
	if err != nil || len(report.Updated) != 2 {
		t.Fatalf("carry = %+v %v", report, err)
	}
	if copied := copyOf(t, st, "song-c"); copied.Settings.Model != "luna" {
		t.Fatalf("a song without the agent was written: %+v", copied.Settings)
	}
	consent, _ := service.Consents().Read("home")
	for _, entry := range consent.Roles[0].Copies {
		if entry.WorkspaceID == "song-c" {
			t.Fatalf("song C is still tracked: %+v", consent.Roles[0].Copies)
		}
	}
}

// A copy still as Ori wrote it is the agent itself; one changed in its song is
// the only customisation.
func TestInStepNamesOnlyUnchangedCopies(t *testing.T) {
	st, service, _ := sharedFixture(t)
	mine := copyOf(t, st, "song-b")
	mine.Settings.Model = "sol"
	if err := st.SaveWorkspaceAgent("song-b", "REAPER Assistant", mine); err != nil {
		t.Fatal(err)
	}
	inStep := service.InStep("REAPER Assistant")
	if len(inStep) != 2 || !inStep["song-a"] || !inStep["song-c"] || inStep["song-b"] {
		t.Fatalf("in step = %v", inStep)
	}
}

// Only the consent's own agent is carried; any other agent's edit is not.
func TestCarryIgnoresAnAgentNoConsentRecords(t *testing.T) {
	st, service, roster := sharedFixture(t)
	roster["Mix Helper"] = &agent.Agent{Settings: types.Settings{Model: "terra"}}
	if err := st.SaveWorkspaceAgent("song-a", "Mix Helper", &agent.Agent{Settings: types.Settings{Model: "luna"}}); err != nil {
		t.Fatal(err)
	}
	report, err := service.Carry("Mix Helper")
	if err != nil || len(report.Updated)+len(report.Customised) != 0 {
		t.Fatalf("carry = %+v %v", report, err)
	}
}
