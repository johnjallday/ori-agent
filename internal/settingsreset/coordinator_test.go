package settingsreset

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

type fixtureLifecycle struct {
	mu             sync.Mutex
	active, fenced bool
	fences, drains int
	afterFence     func()
	drain          func(context.Context) error
}

func (l *fixtureLifecycle) TryFence(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fences++
	if l.active || l.fenced {
		return ErrActiveWork
	}
	l.fenced = true
	if l.afterFence != nil {
		l.afterFence()
	}
	return nil
}
func (l *fixtureLifecycle) Drain(ctx context.Context) error {
	l.mu.Lock()
	l.drains++
	l.mu.Unlock()
	if l.drain != nil {
		return l.drain(ctx)
	}
	return nil
}

func coordinatorFixture(t *testing.T) (*resetfixture.Fixture, *Owners, *Coordinator, *fixtureLifecycle) {
	t.Helper()
	if !resetstate.Supported() {
		t.Skip("native reset admission lease unavailable")
	}
	f, owners, planner := previewFixture(t)
	lease, err := resetstate.Acquire(f.Paths().DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, lease.Close()) })
	life := &fixtureLifecycle{}
	return f, owners, NewCoordinator(lease, planner, life), life
}

func coordinatorRequest(t *testing.T, c *Coordinator, categories ...CategoryID) (Preview, ExecuteRequest) {
	t.Helper()
	v, err := c.planner.Create(t.Context(), IntentSelectedData, categories)
	mustPreview(t, err)
	if len(v.Blockers) != 0 {
		t.Fatalf("unexpected fixture blockers: %+v", v.Blockers)
	}
	return v, ExecuteRequest{PreviewID: v.ID, RequestID: "owned-request", Confirmation: "RESET"}
}

func TestCoordinatorStagesOnceWithoutDeletingSelectedData(t *testing.T) {
	f, owners, c, life := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategoryAgents, CategorySetupSteps)
	before := treeBytes(t, f.Paths().Root)
	writer := resetfixture.NewWriter(t, owners.Config.Save)
	paused, err := writer.Begin(t.Context())
	mustPreview(t, err)
	life.drain = func(context.Context) error { writer.Stop(); return nil }
	op, err := c.Stage(t.Context(), req)
	mustPreview(t, err)
	if op.ID != v.OperationID || op.State != StateAwaitingRestart || op.Revision != 2 || life.drains != 1 {
		t.Fatal("not durably staged once:", op)
	}
	if err := paused.Wait(t.Context()); !errors.Is(err, resetfixture.ErrWriterStopped) {
		t.Fatal("writer did not join:", err)
	}
	for _, result := range op.Results {
		if result.Outcome != OutcomePending {
			t.Fatal("admission pretended to apply:", result)
		}
	}
	after := treeBytes(t, f.Paths().Root)
	for path := range after {
		if strings.Contains(path, string(filepath.Separator)+resetstate.Directory+string(filepath.Separator)) {
			delete(after, path)
			delete(before, path)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("staging deleted or rewrote selected application data")
	}
	f.AssertPreserved(t)
	// Expired preview/reconstructed coordinator: replay still recovers the same
	// durable operation and never calls a lifecycle or rewrites its revision.
	c.planner.now = func() time.Time { return time.Now().Add(time.Hour) }
	receipt, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	other := NewCoordinator(c.lease, nil, nil)
	again, err := other.Stage(t.Context(), req)
	mustPreview(t, err)
	stored, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	if !reflect.DeepEqual(op, again) || !bytes.Equal(receipt, stored) || life.fences != 1 || life.drains != 1 {
		t.Fatal("replay admitted new work")
	}
	for _, changed := range []ExecuteRequest{{v.ID, "other-request", "RESET"}, {"other-preview", req.RequestID, "RESET"}} {
		if _, err := c.Stage(t.Context(), changed); !errors.Is(err, ErrOperationConflict) {
			t.Fatal("reused an admitted identity:", err)
		}
	}
}

func TestCoordinatorConcurrentInstancesShareInstallationAdmission(t *testing.T) {
	_, _, c, life := coordinatorFixture(t)
	_, req := coordinatorRequest(t, c, CategorySetupSteps)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	ids := make(chan string, 10)
	for range 10 {
		wg.Go(func() {
			op, err := NewCoordinator(c.lease, c.planner, life).Stage(t.Context(), req)
			errs <- err
			ids <- op.ID
		})
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		mustPreview(t, err)
	}
	id := ""
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("multiple operations admitted")
		}
		id = next
	}
	if life.fences != 1 || life.drains != 1 {
		t.Fatal("multiple lifecycles admitted")
	}
}

func TestCoordinatorBusyOrChangedScopeNeverStartsDrain(t *testing.T) {
	_, owners, c, life := coordinatorFixture(t)
	_, req := coordinatorRequest(t, c, CategorySetupSteps)
	life.active = true // Work arrived after preview.
	if _, err := c.Stage(t.Context(), req); !errors.Is(err, ErrActiveWork) {
		t.Fatal(err)
	}
	if life.fenced || life.drains != 0 {
		t.Fatal("busy rejection cancelled or drained work")
	}
	data, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	if data != nil {
		t.Fatal("busy rejection consumed confirmation")
	}
	life.active = false
	life.afterFence = func() { owners.Setup = onboarding.NewManager(filepath.Join(owners.DataDir, "changed.json")) }
	op, err := c.Stage(t.Context(), req)
	mustPreview(t, err)
	if op.State != StateBlocked || life.drains != 0 || !life.fenced {
		t.Fatal("scope changed during admission but draining proceeded")
	}
}

func TestCoordinatorStatusDuringDrainAndFailureDoesNotRetry(t *testing.T) {
	_, _, c, life := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategorySetupSteps)
	entered, release := make(chan struct{}), make(chan struct{})
	life.drain = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return errors.New("synthetic PRIVATE provider detail")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan Operation, 1)
	errCh := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() { cancel(); <-finished })
	go func() { defer close(finished); op, err := c.Stage(ctx, req); done <- op; errCh <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("drain never started")
	}
	op, err := c.Status(t.Context(), v.OperationID)
	mustPreview(t, err)
	if op.State != StatePreparing {
		t.Fatal("status did not expose durable preparing boundary")
	}
	close(release)
	op = <-done
	mustPreview(t, <-errCh)
	if op.State != StateBlocked || life.drains != 1 {
		t.Fatal("failed drain looked successful")
	}
	data, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	if bytes.Contains(data, []byte("PRIVATE")) {
		t.Fatal("raw provider error persisted")
	}
	_, err = c.Stage(t.Context(), req)
	mustPreview(t, err)
	_, err = c.Status(t.Context(), v.OperationID)
	mustPreview(t, err)
	if life.drains != 1 {
		t.Fatal("read or replay retried a failed drain")
	}
}

func TestCoordinatorWriteFailureFencesEvenAnotherInstance(t *testing.T) {
	f, _, c, life := coordinatorFixture(t)
	_, req := coordinatorRequest(t, c, CategorySetupSteps)
	life.afterFence = func() {
		// Deterministic non-regular target; no reliance on chmod/root privileges.
		mustPreview(t, os.Mkdir(filepath.Join(f.Paths().DataDir, resetstate.Directory, "operation.json"), 0o700))
	}
	op, err := c.Stage(t.Context(), req)
	if !errors.Is(err, ErrAdmissionUncertain) || op.State != StateInterrupted || !life.fenced || life.drains != 0 {
		t.Fatal("write failure falsely staged/drained:", err, op)
	}
	mustPreview(t, os.Remove(filepath.Join(f.Paths().DataDir, resetstate.Directory, "operation.json")))
	_, err = NewCoordinator(c.lease, c.planner, &fixtureLifecycle{}).Stage(t.Context(), req)
	if !errors.Is(err, ErrAdmissionUncertain) {
		t.Fatal("new coordinator bypassed uncertain admission:", err)
	}
}

func TestCoordinatorPanicDuringDrainBecomesInterruptedWithoutRetry(t *testing.T) {
	_, _, c, life := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategorySetupSteps)
	life.drain = func(context.Context) error { panic("fixture panic") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("fixture did not panic")
			}
		}()
		_, _ = c.Stage(t.Context(), req)
	}()
	op, err := c.Status(t.Context(), v.OperationID)
	mustPreview(t, err)
	if op.State != StateInterrupted {
		t.Fatal("panicked worker still looked active")
	}
	_, err = c.Stage(t.Context(), req)
	if !errors.Is(err, ErrAdmissionUncertain) || life.drains != 1 {
		t.Fatal("replay retried after panic:", err)
	}
}
