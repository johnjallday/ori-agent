package setupwizard

import (
	"context"
	"slices"
	"testing"
)

// aliasedAdapter is a minimal adapter that declares extra registry keys.
type aliasedAdapter struct {
	id      string
	aliases []string
}

func (a aliasedAdapter) ID() string { return a.id }

func (a aliasedAdapter) Aliases() []string { return a.aliases }

func (a aliasedAdapter) Evaluate(context.Context, StepRequest) (StepReadiness, error) {
	return StepReadiness{Ready: true, Summary: "ok"}, nil
}

func (a aliasedAdapter) Confirm(context.Context, StepRequest, StepAction) (StepReadiness, error) {
	return StepReadiness{Ready: true, Summary: "ok"}, nil
}

// plainAdapter declares no aliases, proving the optional interface stays
// optional.
type plainAdapter struct{ id string }

func (a plainAdapter) ID() string { return a.id }

func (a plainAdapter) Evaluate(context.Context, StepRequest) (StepReadiness, error) {
	return StepReadiness{}, nil
}

func (a plainAdapter) Confirm(context.Context, StepRequest, StepAction) (StepReadiness, error) {
	return StepReadiness{}, nil
}

// TestRegistry_ResolvesAdapterUnderItsAliases is the FR-134 guarantee at the
// registry: a workspace whose persisted wizard snapshot names the legacy
// adapter, and a blueprint naming the canonical one, must reach the same
// compiled adapter. Otherwise a rename strands every workspace mid-setup with a
// step no adapter serves.
func TestRegistry_ResolvesAdapterUnderItsAliases(t *testing.T) {
	registry := NewRegistry()
	adapter := aliasedAdapter{id: "downloads_janitor", aliases: []string{"file_janitor"}}
	if err := registry.Register(adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, key := range []string{"downloads_janitor", "file_janitor", "  FILE_JANITOR  "} {
		resolved, ok := registry.Lookup(key)
		if !ok {
			t.Fatalf("key %q did not resolve", key)
		}
		if resolved.ID() != "downloads_janitor" {
			t.Fatalf("key %q resolved to %q", key, resolved.ID())
		}
	}

	if _, ok := registry.Lookup("not_registered"); ok {
		t.Fatal("an unregistered key resolved; the registry must stay fail-closed")
	}
	if _, ok := registry.Lookup(""); ok {
		t.Fatal("a blank key resolved")
	}
}

// TestRegistry_IDsListsCanonicalKeysOnly keeps the manifest-parity contract
// meaningful: aliases are compatibility keys, not separate adapters, and must
// not appear as if a manifest were expected to declare them.
func TestRegistry_IDsListsCanonicalKeysOnly(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(aliasedAdapter{id: "downloads_janitor", aliases: []string{"file_janitor"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(plainAdapter{id: "reaper_song"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ids := registry.IDs()
	if !slices.Equal(ids, []string{"downloads_janitor", "reaper_song"}) {
		t.Fatalf("IDs() = %v, want the canonical ids only", ids)
	}

	keys := registry.Keys()
	if !slices.Equal(keys, []string{"downloads_janitor", "file_janitor", "reaper_song"}) {
		t.Fatalf("Keys() = %v, want every resolvable key", keys)
	}
}

// TestRegistry_ConflictingAliasLeavesRegistryUnchanged proves a failed
// registration is atomic. A half-registered adapter — resolvable under some
// names but not others — would be far harder to diagnose than an absent one.
func TestRegistry_ConflictingAliasLeavesRegistryUnchanged(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(plainAdapter{id: "file_janitor"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// This adapter's alias collides with the already-registered adapter.
	err := registry.Register(aliasedAdapter{id: "downloads_janitor", aliases: []string{"file_janitor"}})
	if err == nil {
		t.Fatal("expected a conflicting alias to fail registration")
	}

	if _, ok := registry.Lookup("downloads_janitor"); ok {
		t.Fatal("the rejected adapter was registered under its primary id anyway")
	}
	resolved, ok := registry.Lookup("file_janitor")
	if !ok || resolved.ID() != "file_janitor" {
		t.Fatal("the pre-existing adapter was disturbed by a rejected registration")
	}
	if ids := registry.IDs(); !slices.Equal(ids, []string{"file_janitor"}) {
		t.Fatalf("IDs() = %v, want only the surviving adapter", ids)
	}
}

func TestRegistry_DuplicatePrimaryIDStillRejected(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(plainAdapter{id: "calendar_ops"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(plainAdapter{id: "calendar_ops"}); err == nil {
		t.Fatal("expected a duplicate primary id to be rejected")
	}
}

// TestRegistry_SelfAliasIsIgnored guards a plausible authoring slip: an adapter
// listing its own ID among its aliases must not collide with itself.
func TestRegistry_SelfAliasIsIgnored(t *testing.T) {
	registry := NewRegistry()
	adapter := aliasedAdapter{id: "downloads_janitor", aliases: []string{"downloads_janitor", "", "   "}}
	if err := registry.Register(adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if keys := registry.Keys(); !slices.Equal(keys, []string{"downloads_janitor"}) {
		t.Fatalf("Keys() = %v, want just the primary id", keys)
	}
}
