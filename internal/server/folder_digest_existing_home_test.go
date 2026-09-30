package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Only the first guard is pinned here: with no builder, no stores, or no owner
// the read refuses before consulting anything. The provider-key match, the
// owner-scoped station lookup, and the group-row/route checks need a real Home
// station and are covered by the browser acceptance (existing-Home collection
// intake), not by this unit test.
func TestReviewedExistingHomeFailsClosedWithoutTrustedInputs(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		builder  *ServerBuilder
		user     string
		provider string
	}{
		"no builder":             {nil, "local", "music_project_management"},
		"builder without stores": {&ServerBuilder{}, "local", "music_project_management"},
		"blank owner":            {&ServerBuilder{}, "", "music_project_management"},
	} {
		t.Run(name, func(t *testing.T) {
			home, err := reviewedExistingHome(ctx, tc.builder, tc.user, tc.provider)
			if !errors.Is(err, personalassistant.ErrFolderWorkspaceRefused) {
				t.Fatalf("err = %v, want the workspace refusal", err)
			}
			if home.WorkspaceID != "" || home.Route != "" {
				t.Fatalf("a refused read still returned a Home: %+v", home)
			}
		})
	}
}
