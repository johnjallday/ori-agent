package workspace

import (
	"testing"
	"time"
)

func newTicketTestService(t *testing.T) (*TicketService, Store) {
	t.Helper()
	// FileStore, not InMemoryStore: FileStore.Get deserializes a fresh copy on
	// every read, which is what makes store.Update genuinely atomic. The
	// atomicity assertions below depend on that.
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewTicketService(store), store
}

func newTicketTestWorkspace(t *testing.T, store Store, name string) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace %q: %v", name, err)
	}
	return ws
}

func mustCreateTicket(t *testing.T, svc *TicketService, input TicketCreateInput) *Ticket {
	t.Helper()
	ticket, err := svc.Create(input)
	if err != nil {
		t.Fatalf("Create(%q): %v", input.Title, err)
	}
	return ticket
}

// FR-19: the Backlog/Ready choice is explicit. There is no silent default,
// and creation can never be used as a lifecycle shortcut.
func TestTicketService_Create_RequiresExplicitCaptureState(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	if _, err := svc.Create(TicketCreateInput{WorkspaceID: ws.ID, Title: "no state"}); err == nil {
		t.Fatalf("expected creation without an explicit state to fail")
	}
	for _, state := range []TicketState{TicketStateInProgress, TicketStateReview, TicketStateDone, TicketStateCancelled} {
		_, err := svc.Create(TicketCreateInput{WorkspaceID: ws.ID, State: state, Title: "shortcut"})
		if err == nil {
			t.Fatalf("creating directly in %s should be refused", state)
		}
	}
	if _, err := svc.Create(TicketCreateInput{State: TicketStateBacklog, Title: "no workspace"}); err == nil {
		t.Fatalf("expected creation without studio_id to fail")
	}
}

func TestTicketService_Create_BacklogAndReady(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	backlog := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "  capture me  ",
		Description: "body", Tags: []string{"Infra", "infra"}, Priority: 2,
	})
	if backlog.State != TicketStateBacklog {
		t.Fatalf("State = %q, want backlog", backlog.State)
	}
	if backlog.Title != "capture me" {
		t.Fatalf("Title = %q, want trimmed", backlog.Title)
	}
	if len(backlog.Tags) != 1 {
		t.Fatalf("Tags = %v, want de-duplicated", backlog.Tags)
	}
	if backlog.Version != 1 {
		t.Fatalf("Version = %d, want 1 on creation", backlog.Version)
	}
	if backlog.Assignee != "" || backlog.ScheduleEnabled {
		t.Fatalf("backlog ticket must not carry commitment fields: %+v", backlog)
	}
	if len(backlog.StateHistory) != 1 || backlog.StateHistory[0].To != TicketStateBacklog {
		t.Fatalf("creation should record one history entry, got %+v", backlog.StateHistory)
	}

	// FR-24/FR-25: newly Ready work is quiescent.
	ready := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "committed work",
	})
	if !ready.AwaitingExecutionIntent {
		t.Fatalf("newly Ready ticket must stay quiescent until explicit intent")
	}
	if ready.Assignee != "" {
		t.Fatalf("creating Ready must not assign an agent, got %q", ready.Assignee)
	}
}

// FR-140: numbers are unique, immutable, and never reused — not even after
// the Ticket that held one is deleted.
func TestTicketService_Create_AllocatesImmutableNeverReusedNumbers(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	first := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "one"})
	second := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "two"})
	if first.Number != 1 || second.Number != 2 {
		t.Fatalf("numbers = %d, %d; want 1, 2", first.Number, second.Number)
	}
	if first.DisplayNumber != "#1" {
		t.Fatalf("DisplayNumber = %q", first.DisplayNumber)
	}

	if err := svc.Delete(ws.ID, second.ID, 0); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	third := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "three"})
	if third.Number != 3 {
		t.Fatalf("number after deletion = %d, want 3 — numbers must never be reused", third.Number)
	}

	// The number survives state changes.
	promoted, err := svc.Transition(ws.ID, first.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if promoted.Number != first.Number || promoted.ID != first.ID {
		t.Fatalf("identity changed across transition: %d/%s → %d/%s",
			first.Number, first.ID, promoted.Number, promoted.ID)
	}
}

// Numbers are workspace-local: two workspaces both start at #1 (FR-140).
func TestTicketService_Create_NumbersAreWorkspaceLocal(t *testing.T) {
	svc, store := newTicketTestService(t)
	alpha := newTicketTestWorkspace(t, store, "Alpha")
	beta := newTicketTestWorkspace(t, store, "Beta")

	a := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: alpha.ID, State: TicketStateBacklog, Title: "a"})
	b := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: beta.ID, State: TicketStateBacklog, Title: "b"})
	if a.Number != 1 || b.Number != 1 {
		t.Fatalf("workspace-local numbering broken: %d, %d", a.Number, b.Number)
	}
	if a.QualifiedNumber != "Alpha #1" || b.QualifiedNumber != "Beta #1" {
		t.Fatalf("qualified numbers must disambiguate: %q, %q", a.QualifiedNumber, b.QualifiedNumber)
	}
}

// FR-15: rank spaces are per state, and a new Ticket lands last in its state.
func TestTicketService_Create_AssignsPerStateRank(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	b1 := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "b1"})
	b2 := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "b2"})
	r1 := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "r1"})

	if b1.StateRank != 1 || b2.StateRank != 2 {
		t.Fatalf("backlog ranks = %d, %d; want 1, 2", b1.StateRank, b2.StateRank)
	}
	if r1.StateRank != 1 {
		t.Fatalf("ready rank = %d, want 1 — rank spaces are per state", r1.StateRank)
	}
}

// FR-20: Backlog safety invariants hold on every capture path, and an invalid
// request leaves nothing behind.
func TestTicketService_Create_ValidationIsAtomic(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	for _, tc := range []struct {
		name  string
		input TicketCreateInput
	}{
		{"empty title", TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "  "}},
		{"multiline title", TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "a\nb"}},
		{"bad priority", TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "x", Priority: 9}},
		{"bad source", TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "x", Source: "telepathy"}},
		{"bad reference url", TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "x", ReferenceURL: "javascript:alert(1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(tc.input); err == nil {
				t.Fatalf("expected validation failure")
			}
		})
	}

	tickets, err := svc.List(TicketQuery{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("failed creations persisted %d tickets, want 0", len(tickets))
	}
}

// FR-9: an ID belonging to another workspace is reported exactly like an
// unknown one, so this route cannot be used to probe for foreign records.
func TestTicketService_Get_ForeignIDIsNotFound(t *testing.T) {
	svc, store := newTicketTestService(t)
	alpha := newTicketTestWorkspace(t, store, "Alpha")
	beta := newTicketTestWorkspace(t, store, "Beta")

	owned := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: beta.ID, State: TicketStateBacklog, Title: "beta work"})

	_, err := svc.Get(alpha.ID, owned.ID)
	if err == nil {
		t.Fatalf("expected foreign ticket lookup to fail")
	}
	if !IsTicketNotFound(err) {
		t.Fatalf("error = %v, want ErrTicketNotFound", err)
	}
	if _, err := svc.Get(alpha.ID, "does-not-exist"); !IsTicketNotFound(err) {
		t.Fatalf("unknown ID error = %v, want ErrTicketNotFound", err)
	}
}

// FR-10/FR-11/FR-12: roll-up is read-only, never copies, and always carries
// the real owner so mutations can be routed to it.
func TestTicketService_List_DescendantRollUpPreservesOwnership(t *testing.T) {
	svc, store := newTicketTestService(t)
	parent := newTicketTestWorkspace(t, store, "Parent")

	child := NewWorkspace(CreateWorkspaceParams{Name: "Child"})
	child.ParentID = parent.ID
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}

	mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: parent.ID, State: TicketStateBacklog, Title: "parent work"})
	childTicket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: child.ID, State: TicketStateBacklog, Title: "child work"})

	local, err := svc.List(TicketQuery{WorkspaceID: parent.ID})
	if err != nil {
		t.Fatalf("List local: %v", err)
	}
	if len(local) != 1 {
		t.Fatalf("local list returned %d tickets, want 1 — roll-up must be opt-in", len(local))
	}

	rolled, err := svc.List(TicketQuery{WorkspaceID: parent.ID, IncludeDescendants: true})
	if err != nil {
		t.Fatalf("List roll-up: %v", err)
	}
	if len(rolled) != 2 {
		t.Fatalf("roll-up returned %d tickets, want 2", len(rolled))
	}

	var found bool
	for _, ticket := range rolled {
		if ticket.ID != childTicket.ID {
			continue
		}
		found = true
		if ticket.OwningWorkspaceID != child.ID || ticket.OwningWorkspaceName != "Child" {
			t.Fatalf("rolled-up ticket lost owner identity: %+v", ticket)
		}
		if ticket.QualifiedNumber != "Child #1" {
			t.Fatalf("QualifiedNumber = %q, want disambiguating owner context", ticket.QualifiedNumber)
		}
	}
	if !found {
		t.Fatalf("child ticket missing from roll-up")
	}

	// FR-12: the parent cannot mutate a descendant Ticket under its own ID.
	if _, err := svc.Update(parent.ID, childTicket.ID, TicketUpdateInput{}); !IsTicketNotFound(err) {
		t.Fatalf("parent-scoped mutation of a child ticket = %v, want ErrTicketNotFound", err)
	}
}

// FR-93: a stale version token is refused rather than silently overwriting.
func TestTicketService_Update_OptimisticConcurrency(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "original"})

	newTitle := "first editor wins"
	updated, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{Title: &newTitle, IfVersion: ticket.Version})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != ticket.Version+1 {
		t.Fatalf("Version = %d, want %d", updated.Version, ticket.Version+1)
	}
	if updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("UpdatedAt should advance on mutation")
	}

	// The second editor still holds the original version.
	losingTitle := "second editor loses"
	_, err = svc.Update(ws.ID, ticket.ID, TicketUpdateInput{Title: &losingTitle, IfVersion: ticket.Version})
	if !IsTicketVersionConflict(err) {
		t.Fatalf("stale update error = %v, want ErrTicketVersionConflict", err)
	}

	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Title != "first editor wins" {
		t.Fatalf("Title = %q — the losing editor overwrote the winner", current.Title)
	}
}

// FR-94: a validation failure leaves the Ticket completely unchanged, even
// when earlier fields in the same request were valid.
func TestTicketService_Update_InvalidFieldLeavesTicketUnchanged(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "original", Priority: 2,
	})

	goodTitle := "accepted title"
	badPriority := 99
	_, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{Title: &goodTitle, Priority: &badPriority})
	if err == nil {
		t.Fatalf("expected priority validation failure")
	}

	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Title != "original" || current.Priority != 2 || current.Version != ticket.Version {
		t.Fatalf("partial write leaked through: title=%q priority=%d version=%d",
			current.Title, current.Priority, current.Version)
	}
}

func TestTicketService_Transition(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	t.Run("rejects an illegal destination without changing the ticket", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "skip the queue"})
		_, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateDone, Actor: TicketActorUser})
		if _, ok := IsIllegalTicketTransition(err); !ok {
			t.Fatalf("error = %v, want IllegalTicketTransitionError", err)
		}
		current, _ := svc.Get(ws.ID, ticket.ID)
		if current.State != TicketStateBacklog || current.Version != ticket.Version {
			t.Fatalf("refused transition mutated the ticket: %+v", current)
		}
	})

	t.Run("is idempotent for the current state", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "promote twice"})
		first, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("first promote: %v", err)
		}
		second, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("repeat promote should be idempotent, got %v", err)
		}
		if second.Version != first.Version {
			t.Fatalf("idempotent promote bumped version %d → %d", first.Version, second.Version)
		}
		if len(second.StateHistory) != len(first.StateHistory) {
			t.Fatalf("idempotent promote appended history")
		}
	})

	t.Run("assigns a fresh rank in the destination state", func(t *testing.T) {
		mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "existing ready"})
		ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "moving"})

		moved, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("Transition: %v", err)
		}
		if moved.StateRank <= 1 {
			t.Fatalf("StateRank = %d, want a rank past the existing Ready items", moved.StateRank)
		}
		if !moved.AwaitingExecutionIntent {
			t.Fatalf("promoted ticket must remain quiescent (FR-24)")
		}
	})

	t.Run("enforces the version token", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "versioned"})
		_, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
			To: TicketStateReady, Actor: TicketActorUser, IfVersion: ticket.Version + 5,
		})
		if !IsTicketVersionConflict(err) {
			t.Fatalf("error = %v, want ErrTicketVersionConflict", err)
		}
	})

	t.Run("records the full lifecycle in history", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "full lifecycle"})
		steps := []TicketState{TicketStateReady, TicketStateInProgress, TicketStateReview, TicketStateDone, TicketStateReady}
		var last *Ticket
		for _, next := range steps {
			var err error
			last, err = svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: next, Actor: TicketActorUser})
			if err != nil {
				t.Fatalf("transition to %s: %v", next, err)
			}
		}
		// One creation entry plus one per transition.
		if len(last.StateHistory) != len(steps)+1 {
			t.Fatalf("StateHistory has %d entries, want %d", len(last.StateHistory), len(steps)+1)
		}
		if last.State != TicketStateReady {
			t.Fatalf("final state = %q, want ready after reopen", last.State)
		}
		if last.ID != ticket.ID || last.Number != ticket.Number {
			t.Fatalf("identity drifted across the lifecycle")
		}
	})
}

// FR-91: reordering is atomic within one owner and one state.
func TestTicketService_Reorder(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	other := newTicketTestWorkspace(t, store, "Other")

	a := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "a"})
	b := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "b"})
	c := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "c"})
	ready := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "ready"})
	foreign := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: other.ID, State: TicketStateBacklog, Title: "foreign"})

	ordered, err := svc.Reorder(ws.ID, TicketStateBacklog, []string{c.ID, a.ID, b.ID})
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if len(ordered) != 3 {
		t.Fatalf("Reorder returned %d tickets, want 3", len(ordered))
	}
	if ordered[0].ID != c.ID || ordered[1].ID != a.ID || ordered[2].ID != b.ID {
		t.Fatalf("order = %q, %q, %q; want c, a, b", ordered[0].Title, ordered[1].Title, ordered[2].Title)
	}

	// Every rejection case must leave the persisted order untouched.
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"duplicate id", []string{c.ID, c.ID, a.ID}},
		{"unknown id", []string{c.ID, "nope"}},
		{"foreign owner", []string{c.ID, foreign.ID}},
		{"wrong state", []string{c.ID, ready.ID}},
		{"empty request", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Reorder(ws.ID, TicketStateBacklog, tc.ids); err == nil {
				t.Fatalf("expected reorder rejection")
			}
			current, err := svc.List(TicketQuery{WorkspaceID: ws.ID, States: []TicketState{TicketStateBacklog}})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(current) != 3 || current[0].ID != c.ID || current[1].ID != a.ID || current[2].ID != b.ID {
				t.Fatalf("rejected reorder partially applied: %+v", current)
			}
		})
	}
}

func TestTicketService_Delete(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "doomed"})

	if err := svc.Delete(ws.ID, ticket.ID, ticket.Version+3); !IsTicketVersionConflict(err) {
		t.Fatalf("stale delete error = %v, want ErrTicketVersionConflict", err)
	}
	if _, err := svc.Get(ws.ID, ticket.ID); err != nil {
		t.Fatalf("refused delete removed the ticket: %v", err)
	}

	if err := svc.Delete(ws.ID, ticket.ID, ticket.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ws.ID, ticket.ID); !IsTicketNotFound(err) {
		t.Fatalf("ticket survived deletion: %v", err)
	}
	if err := svc.Delete(ws.ID, "unknown", 0); !IsTicketNotFound(err) {
		t.Fatalf("deleting an unknown ticket = %v, want ErrTicketNotFound", err)
	}
}

// FR-2/FR-5: the Ticket IS the existing record. Advanced execution
// configuration set by other subsystems must survive canonical mutations.
func TestTicketService_PreservesAdvancedTaskFields(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "advanced"})

	// Simulate the execution engine stamping its own fields on the record.
	err := store.Update(ws.ID, func(w *Workspace) error {
		return w.MutateTask(ticket.ID, func(task *Task) error {
			task.RequiredCapabilities = []string{"email"}
			task.OutputSpec = &TaskOutputSpec{}
			task.ExecutionHistory = []TaskExecution{{}}
			task.Timeout = 30 * time.Second
			return nil
		})
	})
	if err != nil {
		t.Fatalf("seed advanced fields: %v", err)
	}

	newTitle := "renamed"
	if _, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{Title: &newTitle}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateInProgress, Actor: TicketActorUser}); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	task, err := stored.GetTask(ticket.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(task.RequiredCapabilities) != 1 || task.OutputSpec == nil ||
		len(task.ExecutionHistory) != 1 || task.Timeout != 30*time.Second {
		t.Fatalf("canonical mutations dropped advanced execution fields: %+v", task)
	}
	// And there is exactly one record — no parallel Ticket row (FR-2).
	if len(stored.Tasks) != 1 {
		t.Fatalf("workspace holds %d records, want 1 — no duplicate ticket entity", len(stored.Tasks))
	}
}

// FR-98/FR-99: canonical events carry the identity consumers need.
func TestTicketService_PublishesCanonicalEvents(t *testing.T) {
	svc, store := newTicketTestService(t)
	bus := NewEventBus(32, 32)
	svc.SetEventBus(bus)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	received := make(chan Event, 16)
	bus.SubscribeToEventType(EventTicketCreated, func(e Event) { received <- e })
	bus.SubscribeToEventType(EventTicketStateChanged, func(e Event) { received <- e })

	ticket := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "eventful"})
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser}); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	seen := map[EventType]Event{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case event := <-received:
			seen[event.Type] = event
		case <-deadline:
			t.Fatalf("timed out waiting for canonical events, saw %v", seen)
		}
	}

	created := seen[EventTicketCreated]
	if created.Data["ticket_id"] != ticket.ID || created.Data["studio_id"] != ws.ID {
		t.Fatalf("ticket.created payload missing identity: %+v", created.Data)
	}
	changed := seen[EventTicketStateChanged]
	if changed.Data["previous_state"] != string(TicketStateBacklog) ||
		changed.Data["next_state"] != string(TicketStateReady) ||
		changed.Data["actor"] != TicketActorUser {
		t.Fatalf("ticket.state_changed payload incomplete: %+v", changed.Data)
	}
}
