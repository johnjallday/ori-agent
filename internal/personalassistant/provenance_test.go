package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

func TestProfileProvenanceFromTags_ReadsOnlyNamespacedMarkers(t *testing.T) {
	provenance := ProfileProvenanceFromTags("Atlas", []string{
		"assistant", "personal", systemassistant.ProtectedMarker,
		ProfileAssistantMarker("assistant-a"), ProfileHireMarker("hire-req-1"),
	})
	if provenance.Name != "Atlas" || provenance.AssistantID != "assistant-a" || provenance.HireRequestID != "hire-req-1" {
		t.Fatalf("provenance = %#v", provenance)
	}
	if !provenance.OwnedBy("assistant-a") {
		t.Fatal("marked profile not recognized as owned")
	}
	for _, other := range []string{"assistant-b", "", "  "} {
		if provenance.OwnedBy(other) {
			t.Fatalf("OwnedBy(%q) accepted a foreign or empty identity", other)
		}
	}
}

func TestProfileProvenanceFromTags_UnmarkedProfileIsNeverOwned(t *testing.T) {
	// A user-created agent that happens to share the hired name carries no PAF
	// marker, so it can never be adopted by name.
	provenance := ProfileProvenanceFromTags("Atlas", []string{"personal", "assistant"})
	if provenance.AssistantID != "" || provenance.OwnedBy("assistant-a") {
		t.Fatalf("unmarked profile claimed ownership: %#v", provenance)
	}
}

func TestEnsureProfileMarkers(t *testing.T) {
	tags, err := EnsureProfileMarkers([]string{"personal"}, "assistant-a", "hire-req-1")
	if err != nil {
		t.Fatalf("EnsureProfileMarkers: %v", err)
	}
	provenance := ProfileProvenanceFromTags("Atlas", tags)
	if !provenance.OwnedBy("assistant-a") || provenance.HireRequestID != "hire-req-1" || tags[0] != "personal" {
		t.Fatalf("tags = %v", tags)
	}

	// Idempotent: a replay must not stack duplicate markers.
	again, err := EnsureProfileMarkers(tags, "assistant-a", "hire-req-1")
	if err != nil {
		t.Fatalf("EnsureProfileMarkers replay: %v", err)
	}
	if len(again) != len(tags) {
		t.Fatalf("replay stacked markers: %v", again)
	}

	// A profile already owned by another relationship is a conflict, never a
	// silent rebind.
	if _, err := EnsureProfileMarkers(tags, "assistant-b", "hire-req-2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign profile error = %v; want ErrConflict", err)
	}
	if _, err := EnsureProfileMarkers(nil, "  ", "hire-req-1"); err == nil {
		t.Fatal("blank assistant id accepted")
	}
}

func newPreHQService(t *testing.T, profiles ProfileReader) (*Service, *readTrackingStore) {
	t.Helper()
	store := &readTrackingStore{state: awaitingHQTestState("local", "assistant-a")}
	service := NewService(store, &fakeHQReader{}, &fakeBriefReader{},
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}}).
		WithProfileReader(profiles)
	return service, store
}

func TestServiceGet_PreHQValidatesOwnedProfileThroughProvenanceSeam(t *testing.T) {
	profiles := ownedProfileReader("Atlas", "assistant-a")
	service, _ := newPreHQService(t, profiles)

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateNeedsHQ || projection.GlobalAgentProfile != "Atlas" {
		t.Fatalf("projection = %#v", projection)
	}
	if profiles.reads != 1 {
		t.Fatalf("provenance seam consulted %d times; want exactly 1", profiles.reads)
	}
}

func TestServiceGet_PreHQFailsClosedWhenProfileCannotBeValidated(t *testing.T) {
	tests := []struct {
		name     string
		profiles ProfileReader
		status   AvailabilityStatus
		reason   string
	}{
		{
			name:     "profile vanished",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{}},
			status:   AvailabilityUnavailable, reason: "assistant_profile_missing",
		},
		{
			// The dangerous case: an unrelated agent now answers to the hired name.
			name: "same-named profile owned by nobody",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{
				"Atlas": {Name: "Atlas"},
			}},
			status: AvailabilityUnavailable, reason: "assistant_profile_conflict",
		},
		{
			name: "same-named profile owned by another relationship",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{
				"Atlas": {Name: "Atlas", AssistantID: "assistant-b"},
			}},
			status: AvailabilityUnavailable, reason: "assistant_profile_conflict",
		},
		{
			name:     "provenance seam unavailable",
			profiles: nil,
			status:   AvailabilityDependencyError, reason: "profile_reader_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, store := newPreHQService(t, test.profiles)
			projection, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if projection.State != APIStateRepairNeeded || projection.NextAction != NextActionRepair {
				t.Fatalf("state=%q next_action=%q", projection.State, projection.NextAction)
			}
			if projection.GlobalAgentProfile != "" || projection.Mandate != "" || projection.FocusAreas != nil {
				t.Fatalf("unvalidated identity leaked: %#v", projection)
			}
			source := projection.Availability.AgentInstance
			if source.Status != test.status || source.Reason != test.reason {
				t.Fatalf("agent_instance = %#v", source)
			}
			if store.mutationHit {
				t.Fatal("a failed read mutated durable state")
			}
		})
	}
}

func TestServiceGet_ActiveRelationshipIgnoresProfileSeam(t *testing.T) {
	// The active path already validates HQ -> entry instance, which is a stronger
	// check. Adding the pre-HQ seam must not change its behavior.
	service, _, _, _, _ := serviceMatrixFixture(StatusActive)
	profiles := &fakeProfileReader{profiles: map[string]ProfileProvenance{}}
	service.WithProfileReader(profiles)

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateActive {
		t.Fatalf("active projection changed: %#v", projection)
	}
	if profiles.reads != 0 {
		t.Fatalf("active read consulted the pre-HQ seam %d times", profiles.reads)
	}
}
