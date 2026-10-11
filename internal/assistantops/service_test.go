package assistantops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// fakeGuard is the host as a test sees it. Each field can be changed between
// calls, which is how a test loses a conversation, a workspace or a relationship.
type fakeGuard struct {
	mu           sync.Mutex
	user         string
	owner        Owner
	conversation error
	workspaces   map[string]Context
	relationship error
	userErr      error
}

func (g *fakeGuard) CurrentUser(context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.user, g.userErr
}

func (g *fakeGuard) Relationship(context.Context, string) (Owner, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.owner, g.relationship
}

func (g *fakeGuard) ConversationOwned(context.Context, Owner) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conversation
}

func (g *fakeGuard) Workspace(_ context.Context, _ string, id string) (Context, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ws, ok := g.workspaces[id]
	if !ok {
		return Context{}, fmt.Errorf("%w: workspace gone", ErrInvalidated)
	}
	return ws, nil
}

func (g *fakeGuard) set(change func(*fakeGuard)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	change(g)
}

type fixture struct {
	t       *testing.T
	db      *database.DB
	store   *SQLiteStore
	guard   *fakeGuard
	service *Service
	clock   time.Time
	owner   Owner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "ops.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{t: t, db: db, store: NewSQLiteStore(db), clock: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}
	f.owner = Owner{UserID: "local", AssistantID: "asst-1", StateVersion: 4, HQWorkspaceID: "hq", ProfileName: "Atlas", ConversationID: "conv-1", TurnMessageID: "msg-1"}
	f.conversation("conv-1")
	relationship := f.owner
	relationship.ConversationID, relationship.TurnMessageID = "", ""
	f.guard = &fakeGuard{user: "local", owner: relationship, workspaces: map[string]Context{
		"ws-album": {WorkspaceID: "ws-album", WorkspaceSlug: "album-1", WorkspaceName: "Album 1"},
	}}
	f.service = NewService(f.store, f.guard)
	f.service.now = func() time.Time { return f.clock }
	return f
}

func (f *fixture) conversation(id string) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, "Chat", "Atlas"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) create(tab string, watch bool) *Snapshot {
	f.t.Helper()
	snapshot, err := f.service.Create(f.t.Context(), CreateRequest{
		Owner: f.owner, Kind: KindNavigation, TabID: tab, Watch: watch,
		Context: Context{WorkspaceID: "ws-album", AgentName: "Mixer", AgentNodeID: "Mixer-node-1"},
		Actions: []Action{ActionOpenWorkspace, ActionSelectAgent},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func (f *fixture) rows() int {
	f.t.Helper()
	var count int
	if err := f.db.QueryRowContext(f.t.Context(), `SELECT COUNT(*) FROM assistant_operations`).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

func TestCreate_BindsHostResolvedContextAndDeliversOnlyToTheWatchingTab(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	if snapshot.Status != StatusRequested || !snapshot.Watching || snapshot.Pending != 2 || len(snapshot.Commands) != 2 {
		t.Fatalf("originating tab: %+v", snapshot)
	}
	open, selectAgent := snapshot.Commands[0], snapshot.Commands[1]
	// The address comes from the host's read of the workspace, not the request.
	if open.Action != ActionOpenWorkspace || open.WorkspaceSlug != "album-1" || snapshot.Context.WorkspaceName != "Album 1" {
		t.Fatalf("open command: %+v", open)
	}
	if selectAgent.Action != ActionSelectAgent || selectAgent.AgentName != "Mixer" || selectAgent.AgentNodeID != "Mixer-node-1" {
		t.Fatalf("select command: %+v", selectAgent)
	}
	other, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-b")
	if err != nil || other.Watching || other.Pending != 2 || len(other.Commands) != 0 {
		t.Fatalf("second tab must see no commands: %+v %v", other, err)
	}
	noTab, err := f.service.Snapshot(t.Context(), snapshot.ID, "")
	if err != nil || len(noTab.Commands) != 0 {
		t.Fatalf("a tabless read sees no commands: %+v %v", noTab, err)
	}
}

func TestCreate_RefusesUnresolvedOrForeignRequests(t *testing.T) {
	f := newFixture(t)
	base := CreateRequest{Owner: f.owner, Kind: KindNavigation, TabID: "tab-a", Watch: true,
		Context: Context{WorkspaceID: "ws-album"}, Actions: []Action{ActionOpenWorkspace}}
	cases := map[string]struct {
		change func(*CreateRequest)
		want   error
	}{
		"unknown workspace":       {func(r *CreateRequest) { r.Context.WorkspaceID = "ws-else" }, ErrInvalidated},
		"agent missing":           {func(r *CreateRequest) { r.Actions = []Action{ActionSelectAgent} }, ErrInvalidCommand},
		"draft cannot start one":  {func(r *CreateRequest) { r.Actions = []Action{ActionOpenTaskDraft} }, ErrInvalidCommand},
		"unknown action":          {func(r *CreateRequest) { r.Actions = []Action{"click_selector"} }, ErrInvalidCommand},
		"no actions":              {func(r *CreateRequest) { r.Actions = nil }, ErrBounds},
		"watch without tab":       {func(r *CreateRequest) { r.TabID = "" }, ErrInvalidCommand},
		"malformed tab":           {func(r *CreateRequest) { r.TabID = "tab a<script>" }, ErrInvalidCommand},
		"unsaved conversation":    {func(r *CreateRequest) { r.Owner.ConversationID = "" }, ErrInvalidCommand},
		"another user":            {func(r *CreateRequest) { r.Owner.UserID = "someone" }, ErrForbidden},
		"older relationship":      {func(r *CreateRequest) { r.Owner.StateVersion = 3 }, ErrInvalidated},
		"another assistant":       {func(r *CreateRequest) { r.Owner.AssistantID = "asst-0" }, ErrInvalidated},
		"conversation not stored": {func(r *CreateRequest) { r.Owner.ConversationID = "conv-missing" }, ErrConflict},
	}
	for name, tc := range cases {
		req := base
		tc.change(&req)
		if _, err := f.service.Create(t.Context(), req); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if f.rows() != 0 {
		t.Fatalf("a refused request stores nothing, found %d rows", f.rows())
	}
}

func TestAcknowledge_RecordsViewFactsInOrderAndRepeatsAreHarmless(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	open, selectAgent := snapshot.Commands[0], snapshot.Commands[1]
	ack := Acknowledgement{OperationID: snapshot.ID, CommandID: open.ID, Revision: snapshot.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}

	// The later step cannot be reported before the earlier one.
	early := Acknowledgement{OperationID: snapshot.ID, CommandID: selectAgent.ID, Revision: snapshot.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{AgentKey: "mixer"}}
	if _, err := f.service.Acknowledge(t.Context(), early); !errors.Is(err, ErrConflict) {
		t.Fatalf("out of order: %v", err)
	}
	// Arrival somewhere else is not arrival here.
	wrong := ack
	wrong.Observed = &Observation{Path: "/workspaces/other"}
	if _, err := f.service.Acknowledge(t.Context(), wrong); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("wrong page: %v", err)
	}

	after, err := f.service.Acknowledge(t.Context(), ack)
	if err != nil || after.Revision != snapshot.Revision+1 || after.Pending != 1 || after.Commands[0].Status != CommandAcknowledged {
		t.Fatalf("first ack: %+v %v", after, err)
	}
	if len(after.Receipts) != 1 || after.Receipts[0].Kind != ReceiptViewOpened {
		t.Fatalf("receipt: %+v", after.Receipts)
	}
	// A retry after a lost response carries the old revision and changes nothing.
	again, err := f.service.Acknowledge(t.Context(), ack)
	if err != nil || again.Revision != after.Revision || len(again.Receipts) != 1 {
		t.Fatalf("repeat ack must be harmless: %+v %v", again, err)
	}
	// A different outcome for a settled command is a conflict, not an overwrite.
	flip := ack
	flip.Outcome, flip.Revision = CommandUnavailable, after.Revision
	if _, err := f.service.Acknowledge(t.Context(), flip); !errors.Is(err, ErrConflict) {
		t.Fatalf("flip: %v", err)
	}

	stale := early // still carries the first revision
	if _, err := f.service.Acknowledge(t.Context(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	early.Revision = after.Revision
	done, err := f.service.Acknowledge(t.Context(), early)
	if err != nil || done.Pending != 0 || done.Receipts[1].Kind != ReceiptAgentSelected || done.Status != StatusRequested {
		t.Fatalf("second ack: %+v %v", done, err)
	}
}

func TestAcknowledge_RefusesForgedStaleAndForeignCallers(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	good := Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID, Revision: snapshot.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1/"}}

	forgedCommand := good
	forgedCommand.CommandID = "not-a-command"
	if _, err := f.service.Acknowledge(t.Context(), forgedCommand); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("forged command: %v", err)
	}
	forgedOperation := good
	forgedOperation.OperationID = "not-an-operation"
	if _, err := f.service.Acknowledge(t.Context(), forgedOperation); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forged operation: %v", err)
	}
	otherTab := good
	otherTab.TabID = "tab-b"
	if _, err := f.service.Acknowledge(t.Context(), otherTab); !errors.Is(err, ErrTabMismatch) {
		t.Fatalf("other tab: %v", err)
	}
	noEvidence := good
	noEvidence.Observed = nil
	if _, err := f.service.Acknowledge(t.Context(), noEvidence); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("no evidence: %v", err)
	}
	// Another user's operation is reported exactly like a missing one.
	f.guard.set(func(g *fakeGuard) { g.user = "someone" })
	if _, err := f.service.Acknowledge(t.Context(), good); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign user: %v", err)
	}
	if _, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	f.guard.set(func(g *fakeGuard) { g.user = "local" })
	stored, err := f.store.Get(t.Context(), snapshot.ID)
	if err != nil || stored.Revision != snapshot.Revision || len(stored.Receipts) != 0 {
		t.Fatalf("refused calls must not write: %+v %v", stored, err)
	}
	if _, err := f.service.Acknowledge(t.Context(), good); err != nil {
		t.Fatalf("the trailing slash is still that page: %v", err)
	}
}

func TestAcknowledge_UnavailableTargetKeepsTheManualRoute(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	after, err := f.service.Acknowledge(t.Context(), Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID,
		Revision: snapshot.Revision, TabID: "tab-a", Outcome: CommandUnavailable, Reason: "an editor has unsaved\nchanges"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Pending != 0 || after.Commands[0].Status != CommandUnavailable || after.Commands[1].Status != CommandSuperseded {
		t.Fatalf("later steps stop: %+v", after.Commands)
	}
	if after.Commands[0].Reason != "an editor has unsaved changes" || after.Receipts[0].Kind != ReceiptTargetMissing {
		t.Fatalf("reason is bounded and recorded: %+v %+v", after.Commands[0], after.Receipts)
	}
	// The operation itself is still open: nothing was cancelled or invalidated.
	if after.Status != StatusRequested || after.Context.WorkspaceSlug != "album-1" {
		t.Fatalf("operation stays usable for the manual route: %+v", after)
	}
}

func TestRevalidation_EndsContinuationWhenTheHostSaysSo(t *testing.T) {
	cases := map[string]struct {
		change func(*fixture)
		status Status
		want   error
	}{
		"conversation deleted or replaced": {func(f *fixture) {
			f.guard.set(func(g *fakeGuard) { g.conversation = fmt.Errorf("%w: gone", ErrInvalidated) })
		}, StatusInvalidated, ErrInvalidated},
		"assistant reset": {func(f *fixture) {
			f.guard.set(func(g *fakeGuard) { g.owner.AssistantID, g.owner.StateVersion = "asst-2", 1 })
		}, StatusInvalidated, ErrInvalidated},
		"relationship edited": {func(f *fixture) {
			f.guard.set(func(g *fakeGuard) { g.owner.StateVersion++ })
		}, StatusInvalidated, ErrInvalidated},
		"relationship no longer ready": {func(f *fixture) {
			f.guard.set(func(g *fakeGuard) { g.relationship = fmt.Errorf("%w: not ready", ErrInvalidated) })
		}, StatusInvalidated, ErrInvalidated},
		"workspace access lost": {func(f *fixture) {
			f.guard.set(func(g *fakeGuard) { delete(g.workspaces, "ws-album") })
		}, StatusInvalidated, ErrInvalidated},
		"expired": {func(f *fixture) { f.clock = f.clock.Add(DefaultTTL) }, StatusExpired, ErrExpired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			snapshot := f.create("tab-a", true)
			tc.change(f)
			ack := Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID, Revision: snapshot.Revision,
				TabID: "tab-a", Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}
			if _, err := f.service.Acknowledge(t.Context(), ack); !errors.Is(err, tc.want) {
				t.Fatalf("ack: got %v, want %v", err, tc.want)
			}
			// The ended operation is readable, explains itself and hands out nothing.
			read, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a")
			if err != nil || read.Status != tc.status || read.Pending != 0 {
				t.Fatalf("ended snapshot: %+v %v", read, err)
			}
			last := read.Receipts[len(read.Receipts)-1]
			if last.Kind != ReceiptContinuationEnd || last.Ref != string(tc.status) {
				t.Fatalf("ending is recorded: %+v", read.Receipts)
			}
			for _, command := range read.Commands {
				if command.Status != CommandSuperseded {
					t.Fatalf("no command survives the end: %+v", command)
				}
			}
			// It cannot be revived by undoing the change or by a later call.
			f.guard.set(func(g *fakeGuard) { g.conversation, g.relationship = nil, nil })
			if _, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-a", read.Revision, true, true); !errors.Is(err, tc.want) {
				t.Fatalf("revival: %v", err)
			}
		})
	}
}

func TestRevalidation_CouldNotCheckChangesNothing(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	for name, breakIt := range map[string]func(*fakeGuard){
		"user lookup":         func(g *fakeGuard) { g.userErr = errors.New("db locked") },
		"relationship lookup": func(g *fakeGuard) { g.relationship = errors.New("db locked") },
		"conversation lookup": func(g *fakeGuard) { g.conversation = errors.New("db locked") },
	} {
		f.guard.set(breakIt)
		if _, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s: %v", name, err)
		}
		f.guard.set(func(g *fakeGuard) { g.userErr, g.relationship, g.conversation = nil, nil, nil })
	}
	read, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a")
	if err != nil || read.Status != StatusRequested || read.Revision != snapshot.Revision || read.Pending != 2 {
		t.Fatalf("a failed check must not end or change the operation: %+v %v", read, err)
	}
}

func TestRevalidation_FollowsARenamedWorkspace(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	f.guard.set(func(g *fakeGuard) {
		g.workspaces["ws-album"] = Context{WorkspaceID: "ws-album", WorkspaceSlug: "album-one", WorkspaceName: "Album One"}
	})
	read, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a")
	if err != nil || read.Context.WorkspaceSlug != "album-one" || read.Commands[0].WorkspaceSlug != "album-one" {
		t.Fatalf("pending commands follow the current address: %+v %v", read, err)
	}
	old := Acknowledgement{OperationID: snapshot.ID, CommandID: read.Commands[0].ID, Revision: read.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}
	if _, err := f.service.Acknowledge(t.Context(), old); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("the old address is no longer arrival: %v", err)
	}
}

func TestWatch_IsAnExplicitPerTabLease(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", false)
	if snapshot.Watching || len(snapshot.Commands) != 0 || snapshot.Pending != 2 {
		t.Fatalf("watch off delivers nothing: %+v", snapshot)
	}
	// Reporting a command without watching is refused.
	stored, _ := f.store.Get(t.Context(), snapshot.ID)
	if _, err := f.service.Acknowledge(t.Context(), Acknowledgement{OperationID: snapshot.ID, CommandID: stored.Commands[0].ID,
		Revision: snapshot.Revision, TabID: "tab-a", Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}); !errors.Is(err, ErrTabMismatch) {
		t.Fatalf("ack without watch: %v", err)
	}
	// Another tab cannot take the view without an explicit adoption.
	if _, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-b", snapshot.Revision, true, false); !errors.Is(err, ErrTabMismatch) {
		t.Fatalf("silent takeover: %v", err)
	}
	if _, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-b", snapshot.Revision, false, true); !errors.Is(err, ErrTabMismatch) {
		t.Fatalf("a foreign tab cannot switch watching off: %v", err)
	}
	on, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-a", snapshot.Revision, true, false)
	if err != nil || !on.Watching || len(on.Commands) != 2 {
		t.Fatalf("the originating tab opts in: %+v %v", on, err)
	}
	// Repeating the same choice writes nothing, whatever revision it carries.
	same, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-a", 1, true, false)
	if err != nil || same.Revision != on.Revision {
		t.Fatalf("repeat: %+v %v", same, err)
	}
	if _, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-b", snapshot.Revision, true, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("adoption needs the current revision: %v", err)
	}
	adopted, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-b", on.Revision, true, true)
	if err != nil || !adopted.Watching {
		t.Fatalf("explicit adoption: %+v %v", adopted, err)
	}
	former, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a")
	if err != nil || former.Watching || len(former.Commands) != 0 {
		t.Fatalf("the former holder stops receiving commands: %+v %v", former, err)
	}
	off, err := f.service.SetWatch(t.Context(), snapshot.ID, "tab-b", adopted.Revision, false, false)
	if err != nil || off.Watching || off.Status != StatusRequested || off.Pending != 2 {
		t.Fatalf("switching watch off cancels nothing: %+v %v", off, err)
	}
}

func TestCancel_EndsContinuationOnceAndIsRepeatable(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	if _, err := f.service.Cancel(t.Context(), snapshot.ID, "tab-a", snapshot.Revision+5); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cancel: %v", err)
	}
	cancelled, err := f.service.Cancel(t.Context(), snapshot.ID, "tab-a", snapshot.Revision)
	if err != nil || cancelled.Status != StatusCancelled || cancelled.Pending != 0 {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	again, err := f.service.Cancel(t.Context(), snapshot.ID, "tab-a", 1)
	if err != nil || again.Revision != cancelled.Revision {
		t.Fatalf("repeat cancel is harmless: %+v %v", again, err)
	}
	if _, err := f.service.Acknowledge(t.Context(), Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID,
		Revision: cancelled.Revision, TabID: "tab-a", Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}); !errors.Is(err, ErrTransition) {
		t.Fatalf("a cancelled operation accepts no ack: %v", err)
	}
}

func TestCreate_NewerRequestSupersedesTheSameTabsPendingCommands(t *testing.T) {
	f := newFixture(t)
	first := f.create("tab-a", true)
	elsewhere := f.create("tab-b", true)
	second := f.create("tab-a", true)

	old, err := f.service.Snapshot(t.Context(), first.ID, "tab-a")
	if err != nil || old.Pending != 0 || old.Status != StatusRequested {
		t.Fatalf("the earlier request stops driving this tab: %+v %v", old, err)
	}
	if _, err := f.service.Acknowledge(t.Context(), Acknowledgement{OperationID: first.ID, CommandID: first.Commands[0].ID,
		Revision: old.Revision, TabID: "tab-a", Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a superseded command cannot be acknowledged: %v", err)
	}
	other, err := f.service.Snapshot(t.Context(), elsewhere.ID, "tab-b")
	if err != nil || other.Pending != 2 {
		t.Fatalf("another tab's operation is untouched: %+v %v", other, err)
	}
	if current, err := f.service.Snapshot(t.Context(), second.ID, "tab-a"); err != nil || current.Pending != 2 {
		t.Fatalf("the newer request is the live one: %+v %v", current, err)
	}
}

func TestRetention_IsBoundedPerUserAndSweptInBatches(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for i := range MaxActivePerUser + 3 {
		f.clock = f.clock.Add(time.Second)
		ids = append(ids, f.create(fmt.Sprintf("tab-%d", i), true).ID)
	}
	open, err := f.store.OpenForUser(t.Context(), "local")
	if err != nil || len(open) != MaxActivePerUser {
		t.Fatalf("open operations are capped at %d, got %d (%v)", MaxActivePerUser, len(open), err)
	}
	oldest, err := f.store.Get(t.Context(), ids[0])
	if err != nil || oldest.Status != StatusExpired {
		t.Fatalf("the oldest is retired first: %+v %v", oldest, err)
	}
	newest, err := f.store.Get(t.Context(), ids[len(ids)-1])
	if err != nil || newest.Status != StatusRequested {
		t.Fatalf("the newest is kept: %+v %v", newest, err)
	}

	// Everything still open times out. The three retired long ago are removed;
	// the ones that only just ended stay readable for a while.
	f.clock = f.clock.Add(DefaultTTL + time.Hour)
	result, err := f.store.Sweep(t.Context(), f.clock, CleanupBatch)
	if err != nil || result.Expired != MaxActivePerUser || result.Purged != 3 {
		t.Fatalf("sweep expires what timed out: %+v %v", result, err)
	}
	if f.rows() != MaxActivePerUser {
		t.Fatalf("just-ended operations stay readable: %d rows", f.rows())
	}
	f.clock = f.clock.Add(PurgeAfter + time.Hour)
	small, err := f.store.Sweep(t.Context(), f.clock, 5)
	if err != nil || small.Purged != 5 {
		t.Fatalf("purge honours its batch: %+v %v", small, err)
	}
	rest, err := f.store.Sweep(t.Context(), f.clock, 1000)
	if err != nil || rest.Purged != MaxActivePerUser-5 || f.rows() != 0 {
		t.Fatalf("an oversized limit is clamped to one batch and finishes: %+v rows=%d %v", rest, f.rows(), err)
	}
}

func TestConversationDeletion_RemovesItsOperations(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='conv-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Snapshot(t.Context(), snapshot.ID, "tab-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an operation does not outlive its conversation: %v", err)
	}
}

func TestConcurrentAcknowledgements_RecordOneOutcome(t *testing.T) {
	f := newFixture(t)
	snapshot := f.create("tab-a", true)
	ack := Acknowledgement{OperationID: snapshot.ID, CommandID: snapshot.Commands[0].ID, Revision: snapshot.Revision, TabID: "tab-a",
		Outcome: CommandAcknowledged, Observed: &Observation{Path: "/workspaces/album-1"}}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.Acknowledge(context.Background(), ack)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// A racing duplicate either sees the settled command or loses the swap.
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	stored, err := f.store.Get(t.Context(), snapshot.ID)
	if err != nil || stored.Revision != snapshot.Revision+1 || len(stored.Receipts) != 1 {
		t.Fatalf("exactly one acknowledgement is recorded: %+v %v", stored, err)
	}
}

func TestStore_RoundTripsEveryFieldAndRefusesStaleWrites(t *testing.T) {
	f := newFixture(t)
	now := f.clock
	claimed := now.Add(time.Minute)
	op := &Operation{
		Version: ContractVersion, ID: "op-full", Kind: KindTask, Status: StatusReviewed, Revision: 1, Owner: f.owner,
		Context: Context{WorkspaceID: "ws-album", WorkspaceSlug: "album-1", WorkspaceName: "Album 1", AgentName: "Mixer", AgentNodeID: "Mixer-node-1"},
		TabID:   "tab-a", Watch: true,
		Commands: []Command{{ID: "c1", Sequence: 1, Action: ActionOpenTaskDraft, Status: CommandAcknowledged, DraftRevision: 2,
			Observed: &Observation{DraftRevision: 2}, IssuedAt: now, SettledAt: &now}},
		Draft:     &Draft{Revision: 2, Title: "Mix the chorus", Brief: "Two passes — “wide”, then narrow.", AgentName: "Mixer", AgentNodeID: "Mixer-node-1"},
		Task:      &TaskBinding{TaskID: "task-9", RunID: "run-3", ClaimedAt: &claimed},
		Receipts:  []Receipt{{Kind: ReceiptTaskCreated, At: now, Ref: "task-9"}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(DefaultTTL),
	}
	if err := f.store.Insert(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	read, err := f.store.Get(t.Context(), "op-full")
	if err != nil {
		t.Fatal(err)
	}
	if read.Draft.Brief != op.Draft.Brief || read.Task.RunID != "run-3" || !read.Task.ClaimedAt.Equal(claimed) ||
		read.Commands[0].Observed.DraftRevision != 2 || read.Receipts[0].Ref != "task-9" || !read.ExpiresAt.Equal(op.ExpiresAt) ||
		read.Owner != f.owner || read.Context != op.Context || !read.Watch {
		t.Fatalf("round trip lost a field: %+v", read)
	}

	read.Status, read.UpdatedAt = StatusStartRequested, now.Add(time.Second)
	stored, err := f.store.Update(t.Context(), read, 1)
	if err != nil || stored.Revision != 2 || stored.Status != StatusStartRequested {
		t.Fatalf("update: %+v %v", stored, err)
	}
	// Owner columns are never rewritten, whatever the caller passes.
	stored.Owner.UserID, stored.Owner.ConversationID = "someone", "conv-x"
	if kept, err := f.store.Update(t.Context(), stored, 2); err != nil || kept.Owner != f.owner {
		t.Fatalf("owner must be immutable: %+v %v", kept, err)
	}
	if _, err := f.store.Update(t.Context(), read, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	missing := *read
	missing.ID = "op-none"
	if _, err := f.store.Update(t.Context(), &missing, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// One task belongs to one operation.
	dup := *op
	dup.ID, dup.Commands = "op-dup", nil
	if err := f.store.Insert(t.Context(), &dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("a task cannot be bound twice: %v", err)
	}
	// An oversized column is refused before the schema has to.
	big := *op
	big.ID, big.Task = "op-big", nil
	big.Draft = &Draft{Revision: 1, Title: "t", Brief: string(make([]rune, MaxDraftBriefRunes))}
	if err := f.store.Insert(t.Context(), &big); !errors.Is(err, ErrBounds) {
		t.Fatalf("draft bytes are bounded: %v", err)
	}
}
