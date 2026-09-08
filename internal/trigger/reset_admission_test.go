package trigger

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionTriggerWebhookRetainsDebounceAfterAcceptance(t *testing.T) {
	_, source := newTestStore(t, "ws1")
	service, err := NewService(ServiceConfig{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeService := func() { closeOnce.Do(service.Close) }
	t.Cleanup(closeService)
	created, err := service.Create(webhookTrigger())
	if err != nil {
		t.Fatal(err)
	}
	fireID, result := service.IngestWebhook(created.Webhook.Token, "", Event{Kind: "webhook", Timestamp: time.Now()})
	if result != IngestAccepted || fireID == "" {
		t.Fatalf("ingest = %q, %v", fireID, result)
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("accepted debounce not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced later work: %v", err)
	}
	release()
	closeService() // Closing an open window discards it and releases its permit.
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionTriggerOwnershipSpansPendingAndFinalPersistence(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	created, err := store.Create(webhookTrigger())
	if err != nil {
		t.Fatal(err)
	}
	gate := &resetstate.WorkGate{}
	store.SetAdmissionGate(gate)
	started, finish := make(chan struct{}), make(chan struct{})
	var first sync.Once
	dispatch := func(trigger Trigger, fire PendingFire) {
		first.Do(func() { close(started); <-finish })
		if err := store.RecordFire(trigger.WorkspaceID, trigger.ID, FireRecord{FireID: fire.FireID, FiredAt: time.Now()}); err != nil {
			testingTError(t, err)
		}
	}
	coalescer := NewCoalescer(store, dispatch)
	coalescer.SetAdmissionGate(gate)
	coalescer.debounceFor = func(Trigger) time.Duration { return 5 * time.Millisecond }
	finishOnce := sync.OnceFunc(func() { close(finish) })
	var closeOnce sync.Once
	closeCoalescer := func() { closeOnce.Do(func() { coalescer.Close(); coalescer.WaitIdle() }) }
	t.Cleanup(func() { finishOnce(); closeCoalescer() })
	firstFire := coalescer.Observe(created, Event{Kind: "webhook", Timestamp: time.Now()})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("trigger dispatch did not start")
	}
	secondFire := coalescer.Observe(created, Event{Kind: "webhook", Timestamp: time.Now()})
	if firstFire == "" || secondFire == "" || firstFire == secondFire {
		t.Fatalf("fire IDs = %q, %q", firstFire, secondFire)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		persisted, getErr := store.Get("ws1", created.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if persisted.PendingFire != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending fire was not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("pending/dispatch not tracked: %v", err)
	}
	finishOnce()
	closeCoalescer()
	persisted, err := store.Get("ws1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PendingFire != nil {
		t.Fatal("completed pending fire was not cleared")
	}
	if len(persisted.FireHistory) != 2 {
		t.Fatalf("final fire history = %+v", persisted.FireHistory)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// testingTError keeps the injected DispatchFunc signature while making an
// unexpected fixture persistence failure visible without calling Fatal in a worker.
func testingTError(t *testing.T, err error) {
	t.Helper()
	t.Errorf("trigger fixture persistence: %v", err)
}

func TestResetAdmissionTriggerRefusesBeforeStoreAndStart(t *testing.T) {
	store, source := newTestStore(t, "ws1")
	created, err := store.Create(webhookTrigger())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceConfig{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced Start = %v", err)
	}
	if _, err := service.Create(webhookTrigger()); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced Create = %v", err)
	}
	// The independently seeded store proves webhook admission maps the fence
	// explicitly instead of acknowledging a fire that can never be owned.
	service.store = store
	store.SetAdmissionGate(gate)
	if _, result := service.IngestWebhook(created.Webhook.Token, "", Event{}); result != IngestUnavailable {
		t.Fatalf("fenced ingest = %v", result)
	}
	if got := StatusForIngest(IngestUnavailable); got != 503 {
		t.Fatalf("unavailable status = %d", got)
	}
}
