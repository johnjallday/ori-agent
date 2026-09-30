package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ErrHomeUpgradeNotGuidanceOnly refuses a newer Home declaration that changes
// anything but role prompts. Such a release is a different Home, not an
// upgrade an existing Home can be moved onto.
var ErrHomeUpgradeNotGuidanceOnly = errors.New("the newer Home declaration changes more than role prompts")

// HomeRolePromptChange is one Home role whose system prompt differs between
// the installed and the newer release.
type HomeRolePromptChange struct {
	RoleID string `json:"role_id"`
	Label  string `json:"label"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// GuidanceOnlyHomeChange compares two releases of one Home declaration. They
// must be byte-identical once every role's system prompt is set aside; it
// returns the roles whose prompt changed, in declaration order.
func GuidanceOnlyHomeChange(current, next AssistantProgramHome) ([]HomeRolePromptChange, error) {
	if len(current.Roles) != len(next.Roles) {
		return nil, ErrHomeUpgradeNotGuidanceOnly
	}
	withoutPrompts := func(home AssistantProgramHome) ([]byte, error) {
		home.Roles = append([]AssistantProgramHomeRole(nil), home.Roles...)
		for i := range home.Roles {
			home.Roles[i].SystemPrompt = ""
		}
		return json.Marshal(home)
	}
	before, err := withoutPrompts(current)
	if err != nil {
		return nil, err
	}
	after, err := withoutPrompts(next)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before, after) {
		return nil, ErrHomeUpgradeNotGuidanceOnly
	}
	var changes []HomeRolePromptChange
	for i := range current.Roles {
		if current.Roles[i].SystemPrompt != next.Roles[i].SystemPrompt {
			changes = append(changes, HomeRolePromptChange{RoleID: current.Roles[i].ID,
				Label: current.Roles[i].Label, Old: current.Roles[i].SystemPrompt, New: next.Roles[i].SystemPrompt})
		}
	}
	return changes, nil
}
