package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestStoreRoundTripsAtomicallyWithPrivatePermissions(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	want := model.NewBridgeState()
	want.Features["repo:feature"] = model.FeatureState{Feature: model.Feature{Name: "feature", Path: "/tmp/feature"}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("state mode = %o, want 0600", got)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Features["repo:feature"].Feature.Name != "feature" {
		t.Fatalf("Load() = %#v", got)
	}
}

func TestStoreRejectsCorruptOrFutureState(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	if err := os.MkdirAll(store.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted corrupt JSON")
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":99,"features":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted a future state version")
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":0,"features":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() silently accepted an unsupported legacy state version")
	}
}

func TestStoreLockSerializesBridgeOperations(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	unlock, err := store.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_, err = store.Lock(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Lock() error = %v, want deadline while first lock is held", err)
	}
	unlock()

	unlockAgain, err := store.Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock() after release: %v", err)
	}
	unlockAgain()
	info, err := os.Stat(filepath.Join(store.dir, lockFileName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("lock file = %#v, %v", info, err)
	}
}
