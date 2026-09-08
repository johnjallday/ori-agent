package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func waitResetSession(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("session writer missed owned checkpoint")
	}
}

func TestResetAdmissionPeriodicSessionFlushOwnsFinalWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	store := NewHybridStoreWithDB(db, 4).(*hybridStore)
	gate := &resetstate.WorkGate{}
	SetAdmissionGate(store, gate)
	created := &Session{ID: "session", Title: "before", AgentName: "fixture"}
	if err := store.CreateSession(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	cached, err := store.GetSession(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	cached.Title = "periodic flush completed"
	started, persist := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.beforeFlush = func() { once.Do(func() { close(started); <-persist }) }
	finish := sync.OnceFunc(func() { close(persist) })
	t.Cleanup(func() { finish(); _ = store.Close() })
	store.startPeriodicFlush(time.Millisecond)
	waitResetSession(t, started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("periodic flush not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced ordinary work: %v", err)
	}
	release()
	finish()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}

	reopened, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	persisted, err := NewSQLiteStore(reopened).GetSession(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Title != "periodic flush completed" {
		t.Fatalf("lost periodic write: %+v", persisted)
	}
}

func TestResetAdmissionSessionBackgroundRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := &hybridStore{admissionGate: gate, config: &HybridStoreConfig{CleanupThresholdDays: 1}}
	if err := store.FlushToStorage(t.Context()); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, err := store.Cleanup(t.Context(), 1); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if err := store.enforceStorageLimits(t.Context()); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
}

func TestResetAdmissionSessionCloseJoinsOnce(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	store := NewHybridStoreWithDB(db, 2).(*hybridStore)
	store.startPeriodicFlush(time.Millisecond)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- store.Close() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
