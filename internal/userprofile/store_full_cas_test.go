package userprofile

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestFullProfileCASCompetingEditorsKeepOnlyOneCanonicalVersion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{ID: "local", DisplayName: "Jules", Preferences: map[string]string{"response_style": "brief"}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"First", "Second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			copy := *before
			copy.DisplayName = name
			results <- store.UpsertIfVersion(ctx, &copy, before.UpdatedAt)
		}(name)
	}
	wg.Wait()
	close(results)
	var saved, conflicts int
	for err := range results {
		switch {
		case err == nil:
			saved++
		case errors.Is(err, ErrProfileConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing editor error: %v", err)
		}
	}
	current, err := store.Get(ctx, "local")
	if err != nil || saved != 1 || conflicts != 1 || (current.DisplayName != "First" && current.DisplayName != "Second") || current.Preferences["response_style"] != "brief" || !current.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("full-form CAS lost winner or overwrote newer work: %+v successes=%d conflicts=%d err=%v", current, saved, conflicts, err)
	}
}
