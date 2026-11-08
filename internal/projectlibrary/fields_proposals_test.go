package projectlibrary

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func strPtr(value string) *string { return &value }

func listPtr(values ...string) *[]string { return &values }

func fieldsWords() Words {
	return Words{Source: WordsSourcePackage, Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs and assistants",
		Fields: []WordsField{{ID: "key", Label: "Key", Kind: "text"}, {ID: "references", Label: "References", Kind: "list"}}}
}

// newFieldsFixture is the run fixture with the Home's words snapshot of a
// package that declares "key" (text) and "references" (list).
func newFieldsFixture(t *testing.T) *runFixture {
	t.Helper()
	f := newRunFixture(t)
	if err := f.file.Update(f.scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectCardWords = &workspace.ProjectCardWords{PluginID: "music", Version: "0.5.0", Noun: "Song",
			NounPlural: "Songs", RosterTitle: "Songs and assistants", Fields: []workspace.ProjectCardWordsField{
				{ID: "key", Label: "Key", Kind: "text"}, {ID: "references", Label: "References", Kind: "list"}}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func fieldsRevisionOf(t *testing.T, f *runFixture, entryID string) int64 {
	t.Helper()
	entry := sessionEntry(f.doc(t), entryID)
	if entry == nil {
		t.Fatalf("no entry %s", entryID)
	}
	return entry.Fields.Revision
}

func proposalStatus(t *testing.T, f *runFixture, proposalID string) string {
	t.Helper()
	page, err := f.library.ListManagerProposals(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Proposal.ID == proposalID {
			return row.Status
		}
	}
	return "missing"
}

func TestFieldsProposal_SaveReplayConflictAndBounds(t *testing.T) {
	f := newFieldsFixture(t)
	authority := managerAuthorityFor(f)
	rev := fieldsRevisionOf(t, f, "single")
	proposed := ProposedFields{NextAction: strPtr("Send stems to the mixer"), Deliverables: listPtr("Stems v2"),
		Extra: map[string][]string{"key": {"F minor"}}}
	saved, replay, err := f.library.ProposeFieldsAsManager(authority, "single", rev, proposed, "Bounced stems", "fields-1")
	if err != nil || replay || saved.Kind != ProposalKindFields || saved.ProposerKind != ProposerManager ||
		saved.ProposerWorkspaceID != f.scope.HomeID || saved.AgentName != "Manager" ||
		saved.ExpiresAt.Sub(saved.CreatedAt) != 24*time.Hour || saved.NextAction != "" {
		t.Fatalf("save: %+v %v %v", saved, replay, err)
	}
	if again, replay, err := f.library.ProposeFieldsAsManager(authority, "single", rev, proposed, "Bounced stems", "fields-1"); err != nil || !replay || again.ID != saved.ID {
		t.Fatalf("exact replay: %+v %v %v", again, replay, err)
	}
	changed := proposed
	changed.NextAction = strPtr("Something else")
	if _, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, changed, "Bounced stems", "fields-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a changed patch reused the request key: %v", err)
	}
	if _, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev+1, proposed, "", "fields-stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale fields revision was saved: %v", err)
	}
	if status := proposalStatus(t, f, saved.ID); status != "ready" {
		t.Fatalf("status = %s", status)
	}
	for name, bad := range map[string]ProposedFields{
		"empty":           {},
		"empty next":      {NextAction: strPtr("")},
		"long next":       {NextAction: strPtr(strings.Repeat("a", 241))},
		"17 blockers":     {Blockers: listPtr(strings.Split(strings.Repeat("x,", 17)[:33], ",")...)},
		"blank blocker":   {Blockers: listPtr("")},
		"empty extra map": {Extra: map[string][]string{}},
		"bad milestone":   {Milestones: &[]Milestone{{ID: "", Label: "x"}}},
	} {
		if _, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, bad, "", "bad-"+name); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, ProposedFields{Extra: map[string][]string{"mood": {"dark"}}},
		"", "undeclared"); !errors.Is(err, ErrUndeclaredField) {
		t.Fatalf("an undeclared plugin field was saved: %v", err)
	}
	if _, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, ProposedFields{Extra: map[string][]string{"key": {"a", "b"}}},
		"", "two-line-text"); !errors.Is(err, ErrUndeclaredField) {
		t.Fatalf("a two-line text field was saved: %v", err)
	}
	// Clearing a list is a real suggestion ("blockers resolved").
	if _, _, err := f.library.ProposeFieldsAsManager(authority, "alternates", fieldsRevisionOf(t, f, "alternates"),
		ProposedFields{Blockers: listPtr()}, "", "clear-blockers"); err != nil {
		t.Fatalf("clearing blockers: %v", err)
	}
	// A different Home Manager identity is refused.
	stranger := authority
	stranger.AgentName = "Someone"
	if _, _, err := f.library.ProposeFieldsAsManager(stranger, "single", rev, proposed, "", "stranger"); err == nil {
		t.Fatal("an unbound agent saved a suggestion")
	}
}

func TestFieldsProposal_NewerSuggestionWithdrawsTheWaitingOne(t *testing.T) {
	f := newFieldsFixture(t)
	authority := managerAuthorityFor(f)
	rev := fieldsRevisionOf(t, f, "single")
	first, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, ProposedFields{NextAction: strPtr("One")}, "", "w1")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := f.library.ProposeFieldsAsManager(authority, "alternates", fieldsRevisionOf(t, f, "alternates"),
		ProposedFields{NextAction: strPtr("Other song")}, "", "w-other")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, ProposedFields{NextAction: strPtr("Two")}, "", "w2")
	if err != nil {
		t.Fatal(err)
	}
	if got := proposalStatus(t, f, first.ID); got != "withdrawn" {
		t.Fatalf("first = %s", got)
	}
	if proposalStatus(t, f, second.ID) != "ready" || proposalStatus(t, f, other.ID) != "ready" {
		t.Fatal("the newer suggestion and another song's must stay ready")
	}
	// A withdrawn suggestion is not a dismissal and cannot be dismissed or accepted.
	doc := f.doc(t)
	if len(doc.Dismissals) != 0 {
		t.Fatalf("withdrawal recorded a dismissal: %+v", doc.Dismissals)
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, first.ID, "dismiss-withdrawn"); err == nil {
		t.Fatal("a withdrawn suggestion was dismissed")
	}
	if _, _, err := f.library.ReviewProposal(f.scope, first.ID, f.scope.OwnerUserID, fieldsWords()); !errors.Is(err, ErrConflict) {
		t.Fatalf("a withdrawn suggestion was reviewed: %v", err)
	}
	summary, err := f.library.Summary(f.scope)
	if err != nil || summary.ReadyProposals != 2 {
		t.Fatalf("ready count = %d %v", summary.ReadyProposals, err)
	}
}

func TestFieldsProposal_ReviewAndCommitFromTheSavedSuggestion(t *testing.T) {
	f := newFieldsFixture(t)
	authority := managerAuthorityFor(f)
	doc := f.doc(t)
	if _, _, err := f.library.mutate(f.scope, doc.Revision, operation{key: "seed-extra", action: "fixture", digest: "fixture"},
		func(current *Document) (string, error) {
			entry := sessionEntry(*current, "single")
			entry.Fields.Extra = map[string][]string{"references": {"Old ref"}, "retired": {"kept"}}
			entry.Fields.Blockers = []string{"Old blocker"}
			entry.Fields.Revision++
			return "single", nil
		}); err != nil {
		t.Fatal(err)
	}
	rev := fieldsRevisionOf(t, f, "single")
	proposal, _, err := f.library.ProposeFieldsAsManager(authority, "single", rev, ProposedFields{
		NextAction: strPtr("Send stems"), Blockers: listPtr("Vocal comp"),
		Milestones: &[]Milestone{{ID: "m-new", Label: "Mix approved", DueDate: "2026-12-01"}},
		Extra:      map[string][]string{"key": {"F minor"}}}, "Stems bounced", "accept-1")
	if err != nil {
		t.Fatal(err)
	}
	review, kind, err := f.library.ReviewProposal(f.scope, proposal.ID, f.scope.OwnerUserID, fieldsWords())
	if err != nil || kind != ProposalKindFields || review.After.NextAction != "Send stems" ||
		len(review.After.Blockers) != 1 || review.After.Blockers[0] != "Vocal comp" || review.Before.Blockers[0] != "Old blocker" {
		t.Fatalf("review: %+v %s %v", review, kind, err)
	}
	// Extra merges: the proposed id is set, other declared and retired ids stay.
	if extra := review.After.Extra; extra["key"][0] != "F minor" || extra["references"][0] != "Old ref" || extra["retired"][0] != "kept" {
		t.Fatalf("merged extra: %+v", extra)
	}
	entry, replay, kind, err := f.library.CommitProposal(f.scope, proposal.ID, review.Token, "accept-commit", f.scope.OwnerUserID, fieldsWords())
	if err != nil || replay || kind != ProposalKindFields || entry.Fields.NextAction != "Send stems" ||
		entry.Fields.Source != "reviewed_user" || entry.Fields.Author != f.scope.OwnerUserID || entry.Fields.Milestones[0].ID != "m-new" {
		t.Fatalf("commit: %+v %v %s %v", entry.Fields, replay, kind, err)
	}
	// A lost reply retries with the same key and gets the same entry.
	again, replay, _, err := f.library.CommitProposal(f.scope, proposal.ID, review.Token, "accept-commit", f.scope.OwnerUserID, fieldsWords())
	if err != nil || !replay || again.Fields.Revision != entry.Fields.Revision {
		t.Fatalf("commit replay: %+v %v %v", again.Fields, replay, err)
	}
	if status := proposalStatus(t, f, proposal.ID); status != "stale" {
		t.Fatalf("an accepted suggestion must not stay actionable: %s", status)
	}
}

func TestFieldsProposal_StatusMatrixAndDeclarationRecheck(t *testing.T) {
	f := newFieldsFixture(t)
	authority := managerAuthorityFor(f)
	propose := func(entryID, key string, proposed ProposedFields) ManagerProposal {
		t.Helper()
		saved, _, err := f.library.ProposeFieldsAsManager(authority, entryID, fieldsRevisionOf(t, f, entryID), proposed, "", key)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	withKey := propose("single", "status-key", ProposedFields{Extra: map[string][]string{"key": {"F"}}})
	// The package stops declaring "key": the suggestion cannot be reviewed.
	if _, _, err := f.library.ReviewProposal(f.scope, withKey.ID, f.scope.OwnerUserID, NeutralWords()); !errors.Is(err, ErrConflict) {
		t.Fatalf("an undeclared plugin field was reviewed: %v", err)
	}
	if _, _, err := f.library.ReviewProposal(f.scope, withKey.ID, f.scope.OwnerUserID, fieldsWords()); err != nil {
		t.Fatalf("declared review: %v", err)
	}
	dismissed := propose("alternates", "status-dismiss", ProposedFields{NextAction: strPtr("Later")})
	if _, _, err := f.library.DismissManagerProposal(f.scope, dismissed.ID, "status-dismiss-key"); err != nil {
		t.Fatal(err)
	}
	if status := proposalStatus(t, f, dismissed.ID); status != "dismissed" {
		t.Fatalf("dismissed = %s", status)
	}
	// Manager unbound: unavailable, and the commit-time policy refuses.
	bindTestManager(t, f.file, f.scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole},
		[]workspace.AssistantRoleBinding{{RoleID: "manager", AgentInstanceID: "other-manager", AgentName: "Other"}})
	if status := proposalStatus(t, f, withKey.ID); status != "unavailable" {
		t.Fatalf("unbound Manager = %s", status)
	}
	// Expired.
	f.library.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if status := proposalStatus(t, f, withKey.ID); status != "expired" {
		t.Fatalf("expired = %s", status)
	}
}

func TestFieldsProposal_ScanTurnCapDedupeAndProvenance(t *testing.T) {
	f := newFieldsFixture(t)
	if _, claimed, err := f.library.claimProposalRun(f.scope, ProposalRun{ScanID: f.scanID, Status: runStarted,
		AgentName: "Manager", Model: "test-model", StartedAt: time.Now().UTC()}); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	authority := managerAuthorityFor(f)
	authority.Run = ManagerRunContext{ScanID: f.scanID, Model: "test-model"}
	propose := func(entryID, key string) (ManagerProposal, error) {
		saved, _, err := f.library.ProposeFieldsAsManager(authority, entryID, fieldsRevisionOf(t, f, entryID),
			ProposedFields{NextAction: strPtr("Listen " + entryID)}, "", key)
		return saved, err
	}
	first, err := propose("single", "scan-1")
	if err != nil || first.ScanID != f.scanID || first.Source != "manager_model" || first.Model != "test-model" {
		t.Fatalf("scan proposal provenance: %+v %v", first, err)
	}
	if _, err := propose("single", "scan-2"); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("a scan turn filed a second fields suggestion for the same entry: %v", err)
	}
	if _, err := propose("alternates", "scan-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := propose("unsupported", "scan-4"); err != nil {
		t.Fatal(err)
	}
	if _, err := propose("single", "scan-5"); !errors.Is(err, ErrLimit) && !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("a fourth scan suggestion was saved: %v", err)
	}
}

func TestFieldsProposal_DocumentRejectsForgedRows(t *testing.T) {
	f := newFieldsFixture(t)
	authority := managerAuthorityFor(f)
	saved, _, err := f.library.ProposeFieldsAsManager(authority, "single", fieldsRevisionOf(t, f, "single"),
		ProposedFields{NextAction: strPtr("Mix")}, "", "forge-base")
	if err != nil {
		t.Fatal(err)
	}
	doc := f.doc(t)
	for name, forge := range map[string]func(*ManagerProposal){
		"no patch":         func(p *ManagerProposal) { p.ProposedFields = nil },
		"stage smuggled":   func(p *ManagerProposal) { p.NextAction = "legacy next action" },
		"unknown proposer": func(p *ManagerProposal) { p.ProposerKind = "child" },
		"no workspace":     func(p *ManagerProposal) { p.ProposerWorkspaceID = "" },
		"keeper scan turn": func(p *ManagerProposal) {
			p.ProposerKind = ProposerKeeper
			p.ScanID = "scan"
			p.Source = "manager_model"
			p.Model = "m"
		},
		"next action kind":  func(p *ManagerProposal) { p.Kind = ""; p.NextAction = "x" },
		"withdrawn too old": func(p *ManagerProposal) { old := p.CreatedAt.Add(-time.Hour); p.WithdrawnAt = &old },
	} {
		forged := doc
		forged.Proposals = append([]ManagerProposal(nil), doc.Proposals...)
		for i := range forged.Proposals {
			if forged.Proposals[i].ID == saved.ID {
				forge(&forged.Proposals[i])
			}
		}
		if forged.valid(f.scope) {
			t.Errorf("%s: a forged fields row passed document validation", name)
		}
	}
	// The saved row round-trips through JSON as a valid document.
	encoded, _ := json.Marshal(doc)
	var decoded Document
	if err := json.Unmarshal(encoded, &decoded); err != nil || !decoded.valid(f.scope) {
		t.Fatalf("round trip: %v", err)
	}
}
