package assistantops

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// prepare asks for one task draft from a tab that chose to watch.
func (f *fixture) prepare(tab string, change func(*CreateRequest)) (*Snapshot, error) {
	f.t.Helper()
	req := CreateRequest{
		Owner: f.owner, Kind: KindTask, TabID: tab, Watch: true,
		Context: Context{WorkspaceID: "ws-album", AgentName: "mixer"},
		Actions: []Action{ActionOpenWorkspace, ActionOpenTaskDraft},
		Draft:   &Draft{Title: "  Mix the chorus  ", Brief: "Two passes: wide, then narrow."},
	}
	if change != nil {
		change(&req)
	}
	return f.service.Create(f.t.Context(), req)
}

func TestPrepareTask_IssuesOneDraftRevisionForTheRostersOwnAgent(t *testing.T) {
	f := newFixture(t)
	snapshot, err := f.prepare("tab-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != KindTask || snapshot.Status != StatusPrepared || snapshot.Task != nil {
		t.Fatalf("a prepared draft is not a task: %+v", snapshot)
	}
	draft := snapshot.Draft
	if draft == nil || draft.Revision != 1 || draft.Title != "Mix the chorus" || draft.Brief != "Two passes: wide, then narrow." {
		t.Fatalf("draft: %+v", draft)
	}
	// The assignment is the workspace's own instance, by canonical name and node.
	if draft.AgentName != "Mixer" || draft.AgentNodeID != "Mixer-node-1" || snapshot.Context.AgentNodeID != "Mixer-node-1" {
		t.Fatalf("assignment must come from the roster: %+v %+v", draft, snapshot.Context)
	}
	if len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Kind != ReceiptDraftPrepared {
		t.Fatalf("preparing is a server fact: %+v", snapshot.Receipts)
	}
	open, form := snapshot.Commands[0], snapshot.Commands[1]
	if open.Action != ActionOpenWorkspace || form.Action != ActionOpenTaskDraft || form.DraftRevision != 1 || form.AgentNodeID != "Mixer-node-1" {
		t.Fatalf("commands: %+v", snapshot.Commands)
	}
	// Another tab reads the same draft and is handed no command.
	other, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-b")
	if err != nil || other.Draft == nil || len(other.Commands) != 0 || other.Pending != 2 {
		t.Fatalf("second tab: %+v %v", other, err)
	}
}

func TestPrepareTask_RefusesWhatItCannotResolveOrBound(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		change func(*CreateRequest)
		want   error
	}{
		"agent not in this workspace": {func(r *CreateRequest) { r.Context.AgentName = "Editor" }, ErrInvalidated},
		"no agent at all":             {func(r *CreateRequest) { r.Context.AgentName = "" }, ErrInvalidated},
		"another instance's node":     {func(r *CreateRequest) { r.Context.AgentNodeID = "Mixer-node-9" }, ErrInvalidated},
		"no draft":                    {func(r *CreateRequest) { r.Draft = nil }, ErrInvalidCommand},
		"empty title":                 {func(r *CreateRequest) { r.Draft.Title = "   " }, ErrBounds},
		"title too long":              {func(r *CreateRequest) { r.Draft.Title = strings.Repeat("t", MaxDraftTitleRunes+1) }, ErrBounds},
		"brief too long":              {func(r *CreateRequest) { r.Draft.Brief = strings.Repeat("b", MaxDraftBriefRunes+1) }, ErrBounds},
		"a draft on a navigation":     {func(r *CreateRequest) { r.Kind = KindNavigation }, ErrInvalidCommand},
		"roster could not be read":    {func(r *CreateRequest) { f.guard.set(func(g *fakeGuard) { g.agentErr = errors.New("store locked") }) }, ErrUnavailable},
	}
	for name, tc := range cases {
		f.guard.set(func(g *fakeGuard) { g.agentErr = nil })
		if _, err := f.prepare("tab-a", tc.change); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if f.rows() != 0 {
		t.Fatalf("a refused draft stores nothing, found %d rows", f.rows())
	}
	// Opening a draft is not something a navigation can ask for.
	f.guard.set(func(g *fakeGuard) { g.agentErr = nil })
	if _, err := f.service.Create(t.Context(), CreateRequest{Owner: f.owner, Kind: KindNavigation, TabID: "tab-a", Watch: true,
		Context: Context{WorkspaceID: "ws-album"}, Actions: []Action{ActionOpenTaskDraft}}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("draft command without a draft: %v", err)
	}
}

func TestPrepareTask_TheFormIsAcknowledgedForItsRevisionOnly(t *testing.T) {
	f := newFixture(t)
	snapshot, err := f.prepare("tab-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := f.service.Acknowledge(t.Context(), Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID,
		Revision: snapshot.Revision, TabID: "tab-a", Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}})
	if err != nil {
		t.Fatal(err)
	}
	form := Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[1].ID, Revision: opened.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{DraftRevision: 7}}
	if _, err := f.service.Acknowledge(t.Context(), form); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("a form showing another revision is not this draft: %v", err)
	}
	form.Observed = &Observation{DraftRevision: 1}
	shown, err := f.service.Acknowledge(t.Context(), form)
	if err != nil || shown.Pending != 0 || shown.Status != StatusPrepared || shown.Task != nil {
		t.Fatalf("the form being shown changes no task state: %+v %v", shown, err)
	}
	kinds := []ReceiptKind{}
	for _, receipt := range shown.Receipts {
		kinds = append(kinds, receipt.Kind)
	}
	if fmt.Sprint(kinds) != fmt.Sprint([]ReceiptKind{ReceiptDraftPrepared, ReceiptViewOpened, ReceiptDraftOpened}) {
		t.Fatalf("a server fact and two view facts, kept distinct: %v", kinds)
	}
}

func TestPrepareTask_TwoDraftsAreIndependentAndANewerOneRetiresOnlyItsOwnTabsSteps(t *testing.T) {
	f := newFixture(t)
	first, err := f.prepare("tab-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := f.prepare("tab-b", func(r *CreateRequest) { r.Context.AgentName, r.Draft.Title = "Engineer", "Bounce stems" })
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.prepare("tab-a", func(r *CreateRequest) { r.Draft.Title = "Mix the bridge" })
	if err != nil {
		t.Fatal(err)
	}
	// The earlier draft in the same tab stops driving the view but keeps its
	// content: the user may still have it open in the form.
	old, err := f.service.Snapshot(t.Context(), first.ID, "tab-a")
	if err != nil || old.Pending != 0 || old.Status != StatusPrepared || old.Draft.Title != "Mix the chorus" {
		t.Fatalf("earlier draft: %+v %v", old, err)
	}
	other, err := f.service.Snapshot(t.Context(), elsewhere.ID, "tab-b")
	if err != nil || other.Pending != 2 || other.Draft.AgentName != "Engineer" || other.Draft.Title != "Bounce stems" {
		t.Fatalf("another tab's draft is untouched: %+v %v", other, err)
	}
	current, err := f.service.Snapshot(t.Context(), second.ID, "tab-a")
	if err != nil || current.Pending != 2 || current.Draft.Title != "Mix the bridge" {
		t.Fatalf("newer draft: %+v %v", current, err)
	}
	// A dismissed draft cannot be shown again.
	cancelled, err := f.service.Cancel(t.Context(), second.ID, "tab-a", current.Revision)
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("dismiss: %+v %v", cancelled, err)
	}
	if _, err := f.service.SetWatch(t.Context(), second.ID, "tab-a", cancelled.Revision, true, true); !errors.Is(err, ErrTransition) {
		t.Fatalf("a dismissed draft is not revived: %v", err)
	}
}
