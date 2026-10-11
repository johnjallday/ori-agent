package assistantops

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validOperation(now time.Time) *Operation {
	return &Operation{
		Version: ContractVersion, ID: "op-1", Kind: KindNavigation, Status: StatusRequested, Revision: 1,
		Owner:     Owner{UserID: "local", AssistantID: "asst", StateVersion: 3, HQWorkspaceID: "hq", ProfileName: "Atlas", ConversationID: "conv"},
		Context:   Context{WorkspaceID: "ws", WorkspaceSlug: "album", WorkspaceName: "Album"},
		Commands:  []Command{{ID: "c1", Sequence: 1, Action: ActionOpenWorkspace, Status: CommandPending, WorkspaceID: "ws", WorkspaceSlug: "album", IssuedAt: now}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(DefaultTTL),
	}
}

func TestOperationValidate_RefusesUnknownActionsAndBounds(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	cases := map[string]func(op *Operation){
		"unknown action": func(op *Operation) { op.Commands[0].Action = "run_javascript" },
		"duplicate ids":  func(op *Operation) { op.Commands = append(op.Commands, op.Commands[0]) },
		"too many": func(op *Operation) {
			for i := 0; i <= MaxCommands; i++ {
				c := op.Commands[0]
				c.ID = strings.Repeat("x", i+2)
				op.Commands = append(op.Commands, c)
			}
		},
		"no workspace":     func(op *Operation) { op.Context.WorkspaceSlug = "" },
		"wrong version":    func(op *Operation) { op.Version = 2 },
		"owner incomplete": func(op *Operation) { op.Owner.StateVersion = 0 },
		"bad expiry":       func(op *Operation) { op.ExpiresAt = op.CreatedAt },
		"long reason":      func(op *Operation) { op.Commands[0].Reason = strings.Repeat("r", MaxReasonRunes+1) },
		"bad status":       func(op *Operation) { op.Status = "running" },
		"long draft": func(op *Operation) {
			op.Draft = &Draft{Revision: 1, Title: "t", Brief: strings.Repeat("b", MaxDraftBriefRunes+1)}
		},
	}
	if err := validOperation(now).Validate(); err != nil {
		t.Fatalf("baseline must validate: %v", err)
	}
	for name, mutate := range cases {
		op := validOperation(now)
		mutate(op)
		if err := op.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestLifecycle_IsClosedAndStartIsNeverReclaimedFromStarted(t *testing.T) {
	allowed := [][2]Status{
		{StatusRequested, StatusPrepared}, {StatusPrepared, StatusPrepared}, {StatusPrepared, StatusReviewed},
		{StatusReviewed, StatusStartRequested}, {StatusStartRequested, StatusStarted},
		{StatusStartRequested, StatusUnresolved}, {StatusUnresolved, StatusStarted}, {StatusStarted, StatusCancelled},
	}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("%s -> %s must be allowed", pair[0], pair[1])
		}
	}
	refused := [][2]Status{
		{StatusStarted, StatusStartRequested}, {StatusStarted, StatusReviewed}, {StatusRequested, StatusStartRequested},
		{StatusRequested, StatusStarted}, {StatusCancelled, StatusRequested}, {StatusExpired, StatusStarted},
		{StatusInvalidated, StatusPrepared}, {StatusReviewed, StatusStarted},
	}
	for _, pair := range refused {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("%s -> %s must be refused", pair[0], pair[1])
		}
	}
	for _, status := range []Status{StatusCancelled, StatusExpired, StatusInvalidated} {
		if !status.Terminal() || len(transitions[status]) != 0 {
			t.Errorf("%s must be terminal", status)
		}
	}
}

func TestAcknowledgementValidate(t *testing.T) {
	good := Acknowledgement{OperationID: "op", CommandID: "c1", Revision: 1, TabID: "tab", Outcome: CommandAcknowledged}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Acknowledgement{
		{CommandID: "c1", Revision: 1, TabID: "tab", Outcome: CommandAcknowledged},
		{OperationID: "op", CommandID: "c1", Revision: 0, TabID: "tab", Outcome: CommandAcknowledged},
		{OperationID: "op", CommandID: "c1", Revision: 1, TabID: "", Outcome: CommandAcknowledged},
		{OperationID: "op", CommandID: "c1", Revision: 1, TabID: "tab", Outcome: CommandPending},
		{OperationID: "op", CommandID: "c1", Revision: 1, TabID: "tab", Outcome: CommandSuperseded},
		{OperationID: "op", CommandID: "c1", Revision: 1, TabID: "tab", Outcome: CommandUnavailable, Reason: strings.Repeat("r", MaxReasonRunes+1)},
	}
	for i, ack := range bad {
		if err := ack.Validate(); err == nil {
			t.Errorf("case %d: expected refusal", i)
		} else if !errors.Is(err, ErrInvalidCommand) && !errors.Is(err, ErrBounds) {
			t.Errorf("case %d: unexpected error kind %v", i, err)
		}
	}
}

func TestSnapshotFor_ShowsPendingCommandsOnlyToTheLeaseHolder(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	op := validOperation(now)
	op.TabID, op.Watch = "tab-a", true
	op.Commands = append(op.Commands, Command{ID: "c2", Sequence: 2, Action: ActionSelectAgent, Status: CommandAcknowledged, IssuedAt: now})

	holder := SnapshotFor(op, "tab-a")
	if !holder.Watching || len(holder.Commands) != 2 {
		t.Fatalf("lease holder sees both commands: %+v", holder)
	}
	other := SnapshotFor(op, "tab-b")
	if other.Watching || len(other.Commands) != 1 || other.Commands[0].ID != "c2" {
		t.Fatalf("foreign tab sees settled facts only: %+v", other)
	}
	op.Watch = false
	if SnapshotFor(op, "tab-a").Watching {
		t.Fatal("watch off hides pending commands from every tab")
	}
	if SnapshotFor(nil, "tab-a") != nil {
		t.Fatal("nil operation has no snapshot")
	}
}

func TestDraftValidate(t *testing.T) {
	if err := (Draft{Revision: 1, Title: "Mix the chorus", Brief: "two passes"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, draft := range []Draft{
		{Revision: 0, Title: "t"},
		{Revision: 1, Title: "   "},
		{Revision: 1, Title: strings.Repeat("t", MaxDraftTitleRunes+1)},
	} {
		if draft.Validate() == nil {
			t.Errorf("expected refusal for %+v", draft)
		}
	}
}
