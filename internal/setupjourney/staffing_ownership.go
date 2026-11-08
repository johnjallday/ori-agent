package setupjourney

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// PreparedStaffingToolGrants resolves read-only skill/profile metadata before
// the source fence. Its callback must neither look up profiles nor call their
// store; it writes only the already-resolved new profile's grant records.
type PreparedStaffingToolGrants interface {
	PrepareStaffingGrants(string, []string, bool) (func() error, error)
}

// ProfileOwnedStaffingToolGrants declares that grant records live inside the
// created profile's own directory. Deleting that exact owned definition also
// removes these records. Calling a name-based Revoke after deletion could
// instead alter a replacement profile, and calling it inside the source fence
// could re-enter profile lookup. Unknown/external grant owners are retained for
// reconciliation, never guessed at during destructive rollback.
type ProfileOwnedStaffingToolGrants interface {
	GrantsStoredWithProfile() bool
}

func (a *AssistantStaffingAdapter) createStaffingProfile(ctx context.Context, name string, config *store.CreateAgentConfig) (store.StaffingSelection, error) {
	if creator, ok := a.profiles.(store.StaffingProfileCreator); ok {
		return creator.CreateStaffingProfile(ctx, name, config)
	}
	// Legacy custom stores retain their existing create contract but cannot
	// gain destructive cleanup authority from a successful idempotent call.
	return store.StaffingSelection{}, a.profiles.CreateAgent(name, config)
}

func (a *AssistantStaffingAdapter) rollbackStaffingProfiles(ctx context.Context, created []store.StaffingSelection, granted map[string][]string) {
	guard, ok := a.profiles.(store.StaffingSourceGuard)
	if !ok {
		return
	}
	for index := len(created) - 1; index >= 0; index-- {
		selected := created[index]
		if selected.Witness == "" {
			continue
		}
		if len(granted[selected.Name]) > 0 {
			owned, ok := a.grants.(ProfileOwnedStaffingToolGrants)
			if !ok || !owned.GrantsStoredWithProfile() {
				continue
			}
		}
		_, _ = guard.DeleteStaffingProfile(ctx, selected, func(*agent.Agent) bool {
			used, err := workspace.HasWorkspaceAgentReference(ctx, a.workspaces, selected.Name, "")
			return err == nil && !used
		}, nil)
	}
}
