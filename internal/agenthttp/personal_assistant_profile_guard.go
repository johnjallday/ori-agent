package agenthttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// hiredProfileGuardCode is the bounded response code for an ordinary rename or
// delete aimed at a hired assistant that has not built Personal HQ yet.
const hiredProfileGuardCode = "personal_assistant_profile_protected"

// hiredProfileGuardMessage explains the block and names the action that clears
// it. Building HQ attaches the profile to a workspace, after which the ordinary
// attachment guards take over and this one stops applying.
const hiredProfileGuardMessage = "This is your personal assistant's profile, and Personal HQ has not been built yet. " +
	"Build Personal HQ first, or manage the assistant from the relationship itself."

// protectsHiredProfile reports whether name is the current personal-assistant
// relationship's global profile during the window where it exists but is not
// yet attached to any workspace.
//
// The window is exactly needs_hq and provisioning_hq. Before that there is no
// profile; after HQ is built the profile is attached to the HQ workspace and
// the existing attached-agent guards already block rename and delete. An
// unrelated agent — including one that merely shares the name — is never
// matched, because the projection reports the relationship's own profile name
// and is itself validated against durable provenance.
func (c personalAssistantSupportClassifier) protectsHiredProfile(ctx context.Context, name string) bool {
	name = strings.TrimSpace(name)
	if c.reader == nil || name == "" {
		return false
	}
	userID := userprofile.LocalUserID
	if c.provider != nil {
		resolved, err := c.provider.CurrentUserID(ctx)
		if err != nil {
			// Fail closed on an unresolvable user: refusing a destructive action is
			// recoverable; deleting the hired assistant's only profile is not.
			return true
		}
		if strings.TrimSpace(resolved) != "" {
			userID = resolved
		}
	}
	projection, err := c.reader.Get(ctx, userID)
	if err != nil {
		return true
	}
	if projection == nil {
		return false
	}
	switch projection.State {
	case personalassistant.APIStateNeedsHQ, personalassistant.APIStateProvisioningHQ:
	default:
		return false
	}
	return strings.EqualFold(strings.TrimSpace(projection.GlobalAgentProfile), name)
}
