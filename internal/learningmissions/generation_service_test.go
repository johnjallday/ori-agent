package learningmissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type designStub struct {
	config   ModelConfiguration
	calls    atomic.Int32
	generate func(context.Context, ContextSnapshot) (GenerationReply, error)
}

func (d *designStub) Configuration(context.Context) (ModelConfiguration, error) { return d.config, nil }
func (d *designStub) Generate(ctx context.Context, _ ModelConfiguration, snapshot ContextSnapshot) (GenerationReply, error) {
	d.calls.Add(1)
	if d.generate != nil {
		return d.generate(ctx, snapshot)
	}
	return designReply(snapshot), nil
}
func designReply(snapshot ContextSnapshot) GenerationReply {
	c := snapshot.Data.Candidates[0]
	data, _ := json.Marshal(GeneratedProposal{CandidateRef: c.Ref, Structure: c.Structure, Version: c.Version, Steps: c.Steps,
		Goal: "Draft a workshop welcome", Scope: "Only supplied text", Why: "Your explicit workspace choice", Hints: []string{}})
	tokens := 15
	return GenerationReply{Text: string(data), ActualTokens: &tokens}
}
func installDesigner(s *Service) (*designStub, ProposalRequest) {
	d := &designStub{config: ModelConfiguration{Provider: "controlled", Model: "fixture", Fingerprint: digest([]byte("model"))}}
	s.SetDesigner(d)
	return d, ProposalRequest{MutationRequest: MutationRequest{OperationID: "generate"}, WorkspaceID: "workspace",
		Personalize: true, ModelFingerprint: d.config.Fingerprint, ConsentVersion: DisclosureVersion}
}

func TestGenerationPersistsReservationBeforeOneCallAndRecoversTheSameReply(t *testing.T) {
	s, db := testService(t)
	d, req := installDesigner(s)
	status, err := s.Status(t.Context(), "local")
	if err != nil || status.Owner.Revision != 0 || status.Model == nil || d.calls.Load() != 0 {
		t.Fatalf("read enrolled/generated: %+v %v", status, err)
	}
	d.generate = func(ctx context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		op, err := s.store.Operation(ctx, "local", req.OperationID)
		if err != nil || op.Status != "pending" || op.ReservedTokens != GenerationReservedTokens || op.ModelFingerprint != d.config.Fingerprint {
			t.Fatalf("call without durable reservation: %+v %v", op, err)
		}
		return designReply(snapshot), nil
	}
	out, err := s.Propose(t.Context(), "local", req)
	if err != nil || out.Instance == nil || !out.Instance.Definition.Personalized || out.Operation.Status != "succeeded" || out.Operation.ActualTokens == nil {
		t.Fatalf("published proposal: %+v %v", out, err)
	}
	restarted := NewService(s.store, &resetstate.WorkGate{}, sourceStub{missing: true})
	again, err := restarted.Propose(t.Context(), "local", req)
	if err != nil || again.Instance == nil || again.Instance.ID != out.Instance.ID || d.calls.Load() != 1 {
		t.Fatalf("replayed model: %+v %v", again, err)
	}
	var record string
	if err := db.QueryRowContext(t.Context(), `SELECT record FROM learning_operations WHERE id='generate'`).Scan(&record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record, "workshop") || strings.Contains(record, "explicit_focus") || strings.Contains(record, "result_text") {
		t.Fatal("raw prompt/reply copied into accounting")
	}
}

func TestGenerationFailedCallsConsumePersistentCadenceAndDailyBudget(t *testing.T) {
	s, _ := testService(t)
	d, req := installDesigner(s)
	now := s.now()
	s.now = func() time.Time { return now }
	d.generate = func(context.Context, ContextSnapshot) (GenerationReply, error) {
		return GenerationReply{Text: `{"action":"forged"}`}, nil
	}
	for n := range DailyCalls {
		owner, _ := s.store.Owner(t.Context(), "local")
		req.OperationID, req.ExpectedRevision = fmt.Sprintf("generation-%d", n), owner.Revision
		out, err := s.Propose(t.Context(), "local", req)
		if err != nil || out.Instance != nil || out.Operation.Status != "failed" || out.Operation.Failure != "invalid_proposal" || out.Operation.ActualTokens != nil {
			t.Fatalf("failed call accounting: %+v %v", out, err)
		}
		again, err := s.Propose(t.Context(), "local", req)
		if err != nil || again.Operation.ID != out.Operation.ID || d.calls.Load() != int32(n+1) {
			t.Fatal("failed call was repaired/retried")
		}
		owner, _ = s.store.Owner(t.Context(), "local")
		fast := req
		fast.OperationID, fast.ExpectedRevision = fmt.Sprintf("too-fast-%d", n), owner.Revision
		if _, err := s.Propose(t.Context(), "local", fast); !errors.Is(err, ErrLimit) {
			t.Fatalf("spacing: %v", err)
		}
		now = now.Add(ExplicitSpacing + time.Second)
	}
	restarted := NewService(s.store, s.gate, s.source)
	restarted.now = s.now
	fresh, req := installDesigner(restarted)
	owner, _ := s.store.Owner(t.Context(), "local")
	req.OperationID, req.ExpectedRevision = "after-restart", owner.Revision
	if _, err := restarted.Propose(t.Context(), "local", req); !errors.Is(err, ErrLimit) || fresh.calls.Load() != 0 {
		t.Fatalf("restart erased quota: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if out, err := restarted.Propose(t.Context(), "local", req); err != nil || out.Instance == nil || fresh.calls.Load() != 1 {
		t.Fatalf("rolling window: %+v %v", out, err)
	}
}

func TestGenerationClearFencesHeldReplyAndDoesNotEraseCallBudget(t *testing.T) {
	s, _ := testService(t)
	d, req := installDesigner(s)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	d.generate = func(_ context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		close(entered)
		<-release
		return designReply(snapshot), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	result := make(chan error, 1)
	go func() { defer wg.Done(); _, err := s.Propose(context.Background(), "local", req); result <- err }()
	t.Cleanup(func() { unblock(); wg.Wait() })
	<-entered
	// A duplicate recovers the pending receipt even while the global slot is held.
	pending, err := s.Propose(t.Context(), "local", req)
	if err != nil || pending.Operation.Status != "pending" || d.calls.Load() != 1 {
		t.Fatalf("pending replay: %+v %v", pending, err)
	}
	other, _ := testService(t)
	otherDesigner, otherRequest := installDesigner(other)
	if _, err := other.Propose(t.Context(), "local", otherRequest); !errors.Is(err, ErrConflict) || otherDesigner.calls.Load() != 0 {
		t.Fatalf("process concurrency: %v", err)
	}
	status, _ := s.Status(t.Context(), "local")
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: status.Owner.Revision}); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-result; !errors.Is(err, ErrConflict) {
		t.Fatalf("late clear callback: %v", err)
	}
	status, _ = s.Status(t.Context(), "local")
	if len(status.Instances) != 0 {
		t.Fatal("late reply resurrected history")
	}
	old, err := s.store.Operation(t.Context(), "local", req.OperationID)
	if err != nil || old.Status != "invalidated" || old.ReservedTokens != GenerationReservedTokens {
		t.Fatalf("clearing erased budget: %+v %v", old, err)
	}
	req.OperationID, req.ExpectedRevision = "after-clear", status.Owner.Revision
	if _, err := s.Propose(t.Context(), "local", req); !errors.Is(err, ErrLimit) {
		t.Fatalf("clear bypassed spacing: %v", err)
	}
}

func TestGenerationRevalidatesModelSourceAndOwnerBeforePublication(t *testing.T) {
	for _, change := range []string{"model", "source", "revision"} {
		t.Run(change, func(t *testing.T) {
			s, db := testService(t)
			d, req := installDesigner(s)
			d.generate = func(ctx context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
				switch change {
				case "model":
					d.config.Fingerprint = digest([]byte("changed"))
				case "source":
					if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id='workspace'`); err != nil {
						t.Fatal(err)
					}
				case "revision":
					owner, _ := s.store.Owner(ctx, "local")
					if _, err := s.store.Change(ctx, "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error { m.owner.Preferences.Mode = "off"; return nil }); err != nil {
						t.Fatal(err)
					}
				}
				return designReply(snapshot), nil
			}
			out, err := s.Propose(t.Context(), "local", req)
			if err != nil || out.Instance != nil || out.Operation.Status != "invalidated" || d.calls.Load() != 1 {
				t.Fatalf("stale publication: %+v %v", out, err)
			}
		})
	}
}

func TestGenerationUnknownPublicationRecoversWithoutModelReplay(t *testing.T) {
	s, db := testService(t)
	d, req := installDesigner(s)
	d.generate = func(ctx context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_learning_publish BEFORE INSERT ON learning_instances BEGIN SELECT RAISE(FAIL,'injected publication interruption'); END`); err != nil {
			t.Fatal(err)
		}
		return designReply(snapshot), nil
	}
	if _, err := s.Propose(t.Context(), "local", req); err == nil {
		t.Fatal("injected failure did not fail")
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_learning_publish`); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(s.store, s.gate, s.source)
	after := s.now().Add(GenerationTimeout + time.Second)
	restarted.now = func() time.Time { return after }
	fresh, next := installDesigner(restarted)
	if err := restarted.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	old, err := restarted.Propose(t.Context(), "local", req)
	if err != nil || old.Operation.Status != "unknown" || old.Instance != nil || fresh.calls.Load() != 0 {
		t.Fatalf("restart replayed unknown call: %+v %v", old, err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	next.OperationID, next.ExpectedRevision = "new-explicit-request", owner.Revision
	if out, err := restarted.Propose(t.Context(), "local", next); err != nil || out.Instance == nil || fresh.calls.Load() != 1 {
		t.Fatalf("fresh explicit admission: %+v %v", out, err)
	}
}

func TestGenerationCancellationRecordsUnknownUsageWithoutRetry(t *testing.T) {
	s, _ := testService(t)
	d, req := installDesigner(s)
	d.generate = func(ctx context.Context, _ ContextSnapshot) (GenerationReply, error) {
		<-ctx.Done()
		return GenerationReply{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	out, err := s.Propose(ctx, "local", req)
	if err != nil || out.Operation.Failure != "timeout" || out.Operation.Status != "failed" || out.Operation.ActualTokens != nil || d.calls.Load() != 1 {
		t.Fatalf("timeout accounting: %+v %v", out, err)
	}
}

func TestGenerationOffAndExistingWorkMakeNoCall(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		s, _ := testService(t)
		d, req := installDesigner(s)
		if occupied {
			propose(t, s)
		} else {
			if _, err := s.store.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error {
				m.owner.Preferences.Mode = "off"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		owner, _ := s.store.Owner(t.Context(), "local")
		req.ExpectedRevision = owner.Revision
		if _, err := s.Propose(t.Context(), "local", req); err == nil || d.calls.Load() != 0 {
			t.Fatalf("off/occupied admission reached model: %v", err)
		}
	}
}

func TestGenerationOfferRequiresCurrentModelAtAcceptance(t *testing.T) {
	s, _ := testService(t)
	d, req := installDesigner(s)
	out, err := s.Propose(t.Context(), "local", req)
	if err != nil {
		t.Fatal(err)
	}
	d.config.Fingerprint = digest([]byte("another configured model"))
	status, statusErr := s.Status(t.Context(), "local")
	if statusErr != nil || len(status.Instances) != 1 || status.Instances[0].Progress.Blocker != "model_changed" || d.calls.Load() != 1 {
		t.Fatalf("stale offer was not disclosed read-only: %+v %v", status, statusErr)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	_, err = s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{
		MutationRequest: MutationRequest{OperationID: "accept", ExpectedRevision: owner.Revision}, InstanceRevision: 1, Decision: "accept"})
	if !errors.Is(err, ErrConflict) || d.calls.Load() != 1 {
		t.Fatalf("stale generation consent accepted or regenerated: %v", err)
	}
}
