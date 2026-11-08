package projectlibrary

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// keeperFixture is the song-card fixture with provider evidence, a keeper
// instance ID on child-a ("Studio Keeper"), and a toggle for the provider.
func keeperFixture(t *testing.T) (*Store, Scope, *workspace.FileStore, *bool) {
	t.Helper()
	_, scope, file, _ := songCardFixture(t)
	bindTestManager(t, file, scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole},
		[]workspace.AssistantRoleBinding{managerBinding})
	if err := file.Update("child-a", func(child *workspace.Workspace) error {
		child.AgentInstances = []workspace.AgentInstance{{ID: "keeper-1", Name: "Studio Keeper", EntryPoint: true}}
		return child.SetEntryAgentName("Studio Keeper")
	}); err != nil {
		t.Fatal(err)
	}
	available := true
	store := NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return available })
	return store, scope, file, &available
}

var keeperA = KeeperAuthority{ChildWorkspaceID: "child-a", AgentInstanceID: "keeper-1", AgentName: "Studio Keeper"}

func TestKeeperPolicy_ProposesOnlyOnItsOwnConnectedSong(t *testing.T) {
	s, scope, _, _ := keeperFixture(t)
	next := "Send stems to the mixer"
	receipt, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next}, "Bounced stems", "keeper-1-key")
	if err != nil || receipt.Replay || receipt.ProposalID == "" || receipt.ExpiresAt.IsZero() {
		t.Fatalf("keeper save: %+v %v", receipt, err)
	}
	if again, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next}, "Bounced stems", "keeper-1-key"); err != nil || !again.Replay || again.ProposalID != receipt.ProposalID {
		t.Fatalf("keeper replay: %+v %v", again, err)
	}
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := savedProposal(doc, receipt.ProposalID)
	if saved.ProposerKind != ProposerKeeper || saved.ProposerWorkspaceID != "child-a" || saved.EntryID != "song-a" ||
		saved.AgentInstanceID != "keeper-1" || saved.ScanID != "" {
		t.Fatalf("saved keeper row: %+v", saved)
	}
	// The keeper's request key is namespaced under its song.
	for _, op := range doc.Operations {
		if op.ConsequenceID == saved.ID && !strings.HasPrefix(op.Key, "keeper:child-a:") {
			t.Fatalf("keeper key not namespaced: %q", op.Key)
		}
	}
	if status := s.managerProposalStatus(scope, doc, saved); status != "ready" {
		t.Fatalf("keeper suggestion status = %s", status)
	}
	if !s.KeeperEnabled(keeperA) {
		t.Fatal("the keeper is not enabled")
	}
	card, err := s.SongCard(scope, "child-a", NeutralWords())
	if err != nil || len(card.Suggestions) != 1 || card.Suggestions[0].ProposerKind != ProposerKeeper {
		t.Fatalf("card suggestions: %+v %v", card.Suggestions, err)
	}
}

func TestKeeperPolicy_AuthorityMatrix(t *testing.T) {
	next := "Mix"
	for name, tc := range map[string]struct {
		keeper KeeperAuthority
		setup  func(t *testing.T, file *workspace.FileStore, available *bool)
	}{
		"not the entry agent": {keeper: KeeperAuthority{ChildWorkspaceID: "child-a", AgentInstanceID: "other-1", AgentName: "Other"},
			setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
				updateChild(t, file, "child-a", func(child *workspace.Workspace) {
					child.AgentInstances = append(child.AgentInstances, workspace.AgentInstance{ID: "other-1", Name: "Other"})
				})
			}},
		"wrong instance id": {keeper: KeeperAuthority{ChildWorkspaceID: "child-a", AgentInstanceID: "forged", AgentName: "Studio Keeper"}},
		"disabled": {keeper: keeperA, setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
			if err := file.SaveWorkspaceAgent("child-a", "Studio Keeper", &agent.Agent{Status: types.AgentStatusDisabled}); err != nil {
				t.Fatal(err)
			}
		}},
		"diverged mirror": {keeper: keeperA, setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
			updateChild(t, file, "child-a", func(child *workspace.Workspace) {
				link := child.GetAssistantProjectLink()
				link.StateRevision = 2
				child.SetAssistantProjectLink(link)
			})
		}},
		"catalog-only entry": {keeper: KeeperAuthority{ChildWorkspaceID: "child-c", AgentInstanceID: "keeper-c", AgentName: "C Keeper"},
			setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
				updateChild(t, file, "child-c", func(child *workspace.Workspace) {
					child.AgentInstances = []workspace.AgentInstance{{ID: "keeper-c", Name: "C Keeper", EntryPoint: true}}
					_ = child.SetEntryAgentName("C Keeper")
				})
			}},
		"resource workspace": {keeper: keeperA, setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
			updateChild(t, file, "child-a", func(child *workspace.Workspace) {
				link := child.GetAssistantProjectLink()
				link.ProjectProvider = &workspace.AssistantProjectProviderOwner{WorkspaceKind: workspace.AssistantWorkspaceKindResource}
				child.SetAssistantProjectLink(link)
			})
		}},
		"foreign owner": {keeper: keeperA, setup: func(t *testing.T, file *workspace.FileStore, _ *bool) {
			updateChild(t, file, "child-a", func(child *workspace.Workspace) { child.OwnerUserID = "someone-else" })
		}},
		"home not writable":      {keeper: keeperA, setup: func(_ *testing.T, _ *workspace.FileStore, available *bool) { *available = false }},
		"missing song workspace": {keeper: KeeperAuthority{ChildWorkspaceID: "gone", AgentInstanceID: "keeper-1", AgentName: "Studio Keeper"}},
		"no workspace":           {keeper: KeeperAuthority{AgentInstanceID: "keeper-1", AgentName: "Studio Keeper"}},
	} {
		t.Run(name, func(t *testing.T) {
			s, scope, file, available := keeperFixture(t)
			if tc.setup != nil {
				tc.setup(t, file, available)
			}
			if s.KeeperEnabled(tc.keeper) {
				t.Fatal("the keeper is enabled")
			}
			if _, err := s.ProposeFieldsAsKeeper(tc.keeper, ProposedFields{NextAction: &next}, "", "matrix-"+strings.ReplaceAll(name, " ", "-")); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			if doc, err := s.Read(scope); err == nil && len(doc.Proposals) != 0 {
				t.Fatalf("a refused keeper saved %d suggestions", len(doc.Proposals))
			}
		})
	}
}

// The store keeps instance names unique, so the duplicate-name rule is checked
// on an in-memory song: two instances carrying the entry agent's name prove
// neither is the keeper.
func TestKeeperPolicy_InstanceMustBeTheOneEntryAgent(t *testing.T) {
	song := &workspace.Workspace{AgentInstances: []workspace.AgentInstance{
		{ID: "keeper-1", Name: "Studio Keeper", EntryPoint: true}}}
	if !keeperInstance(song, keeperA) {
		t.Fatal("the one entry agent is not the keeper")
	}
	song.AgentInstances = append(song.AgentInstances, workspace.AgentInstance{ID: "keeper-2", Name: "Studio Keeper"})
	if keeperInstance(song, keeperA) {
		t.Fatal("a duplicated entry agent name passed")
	}
	song.AgentInstances = song.AgentInstances[:1]
	if keeperInstance(song, KeeperAuthority{ChildWorkspaceID: "child-a", AgentInstanceID: "keeper-1", AgentName: "Other"}) {
		t.Fatal("a name that is not the entry agent passed")
	}
}

func updateChild(t *testing.T, file *workspace.FileStore, id string, change func(*workspace.Workspace)) {
	t.Helper()
	if err := file.Update(id, func(child *workspace.Workspace) error {
		change(child)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestKeeperPolicy_ReplacementAndUnavailableAfterTheKeeperLeaves(t *testing.T) {
	s, scope, file, _ := keeperFixture(t)
	one, two := "One", "Two"
	first, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &one}, "", "keeper-r1")
	if err != nil {
		t.Fatal(err)
	}
	// The Home Manager's suggestion for the same song is a different proposer
	// kind: neither withdraws the other.
	bindManagerOnHome := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: managerBinding.AgentInstanceID,
		AgentName: managerBinding.AgentName}
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	managerNext := "Manager's take"
	manager, _, err := s.ProposeFieldsAsManager(bindManagerOnHome, "song-a", sessionEntry(doc, "song-a").Fields.Revision,
		ProposedFields{NextAction: &managerNext}, "", "manager-coexist")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &two}, "", "keeper-r2")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, proposal := range doc.Proposals {
		statuses[proposal.ID] = s.managerProposalStatus(scope, doc, proposal)
	}
	if statuses[first.ProposalID] != "withdrawn" || statuses[second.ProposalID] != "ready" || statuses[manager.ID] != "ready" {
		t.Fatalf("statuses: %+v", statuses)
	}
	// The keeper leaves the song: its waiting suggestion can no longer be accepted.
	updateChild(t, file, "child-a", func(child *workspace.Workspace) {
		child.AgentInstances = []workspace.AgentInstance{{ID: "new-1", Name: "New Keeper", EntryPoint: true}}
		_ = child.SetEntryAgentName("New Keeper")
	})
	doc, _ = s.Read(scope)
	for _, proposal := range doc.Proposals {
		if proposal.ID == second.ProposalID {
			if status := s.managerProposalStatus(scope, doc, proposal); status != "unavailable" {
				t.Fatalf("after the keeper left: %s", status)
			}
		}
	}
	if _, _, err := s.ReviewProposal(scope, second.ProposalID, scope.OwnerUserID, NeutralWords()); !errors.Is(err, ErrConflict) {
		t.Fatalf("an unavailable keeper suggestion was reviewed: %v", err)
	}
}

// A looping keeper is bounded per song per day, so it cannot fill the Home's
// proposal cap; and its plugin fields are checked like the Manager's.
func TestKeeperPolicy_DailyCapAndDeclaredFields(t *testing.T) {
	s, scope, _, _ := keeperFixture(t)
	for i := 0; i < keeperSavesPerDay; i++ {
		next := "Try " + strings.Repeat("x", i+1)
		if _, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next}, "", "loop-"+next); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	next := "One more"
	if _, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next}, "", "loop-over"); !errors.Is(err, ErrLimit) {
		t.Fatalf("the keeper went past its daily cap: %v", err)
	}
	doc, _ := s.Read(scope)
	if len(doc.Proposals) != keeperSavesPerDay {
		t.Fatalf("rows = %d", len(doc.Proposals))
	}
	// After a day the withdrawn rows expire and the keeper may suggest again.
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next}, "", "next-day"); err != nil {
		t.Fatalf("next day: %v", err)
	}
	// Plugin fields: undeclared without a words snapshot, accepted with one.
	s.now = time.Now
	fresh, freshScope, freshFile, _ := keeperFixture(t)
	if _, err := fresh.ProposeFieldsAsKeeper(keeperA, ProposedFields{Extra: map[string][]string{"key": {"F"}}}, "", "extra-undeclared"); !errors.Is(err, ErrUndeclaredField) {
		t.Fatalf("an undeclared field was saved: %v", err)
	}
	if err := freshFile.Update(freshScope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectCardWords = &workspace.ProjectCardWords{Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs",
			Fields: []workspace.ProjectCardWordsField{{ID: "key", Label: "Key", Kind: "text"}}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ProposeFieldsAsKeeper(keeperA, ProposedFields{Extra: map[string][]string{"key": {"F"}}}, "", "extra-declared"); err != nil {
		t.Fatalf("a declared field was refused: %v", err)
	}
}

func TestKeeperPolicy_AcceptedSuggestionIsTheOwnersNote(t *testing.T) {
	s, scope, _, _ := keeperFixture(t)
	next := "Print stems"
	saved, err := s.ProposeFieldsAsKeeper(keeperA, ProposedFields{NextAction: &next, Deliverables: &[]string{"Stems v2"}}, "", "keeper-accept")
	if err != nil {
		t.Fatal(err)
	}
	review, kind, err := s.ReviewProposal(scope, saved.ProposalID, scope.OwnerUserID, NeutralWords())
	if err != nil || kind != ProposalKindFields || review.After.NextAction != next {
		t.Fatalf("review: %+v %v", review, err)
	}
	entry, _, _, err := s.CommitProposal(scope, saved.ProposalID, review.Token, "keeper-accept-commit", scope.OwnerUserID, NeutralWords())
	if err != nil || entry.Fields.NextAction != next || entry.Fields.Source != "reviewed_user" || entry.Fields.Author != scope.OwnerUserID {
		t.Fatalf("commit: %+v %v", entry.Fields, err)
	}
}
