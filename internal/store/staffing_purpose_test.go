package store

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestCreateStaffingProfilePublicPurposeIsAtomicAndNeverAnExistingEdit(t *testing.T) {
	index := filepath.Join(t.TempDir(), "agents.json")
	profiles, err := NewFileStore(index, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := profiles.(StaffingProfileCreator).CreateStaffingProfile(t.Context(), "Aster", &CreateAgentConfig{
		Description: "Coordinates a music project collection.", SystemPrompt: "PRIVATE_PROMPT_CANARY",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := ReadStaffingSnapshot(t.Context(), profiles)
	if len(before.Profiles) != 1 || before.Profiles[0].Witness != created.Witness || before.Profiles[0].Purpose != "Coordinates a music project collection." {
		t.Fatal("initial witness predates public purpose")
	}
	if err := profiles.CreateAgent("Aster", &CreateAgentConfig{Description: "Must not replace authored purpose"}); err != nil {
		t.Fatal(err)
	}
	after := ReadStaffingSnapshot(t.Context(), profiles)
	if len(after.Profiles) != 1 || after.Profiles[0].Witness != created.Witness || after.Profiles[0].Purpose != before.Profiles[0].Purpose {
		t.Fatal("legacy idempotent creation rewrote an existing profile")
	}
	reloaded, err := NewFileStore(index, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	retained := ReadStaffingSnapshot(t.Context(), reloaded)
	if len(retained.Profiles) != 1 || retained.Profiles[0].Purpose != before.Profiles[0].Purpose {
		t.Fatal("retained definition lost public purpose")
	}
}
