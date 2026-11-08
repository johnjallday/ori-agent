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

// HomeUpgradeChange is what an accepted newer release of one Home declaration
// changes for a Home that already exists.
type HomeUpgradeChange struct {
	// RolePrompts are the roles whose system prompt changed, in declaration order.
	RolePrompts []HomeRolePromptChange
	// AddsHomeProfile is true when the newer release adds a profile card the
	// installed one does not have; HomeProfileTitle is that card's title.
	AddsHomeProfile  bool
	HomeProfileTitle string
	// AddsProjectCard is true when the newer release adds a `project_card`
	// words section the installed one does not have (PRD #792 FR10);
	// ProjectCardNoun and ProjectCardNounPlural are its declared words.
	AddsProjectCard       bool
	ProjectCardNoun       string
	ProjectCardNounPlural string
	// AddedResourceAttachments are resource attachments the newer release
	// appends after every existing attachment, in declaration order.
	AddedResourceAttachments []AssistantProgramAllowedProjectAttachment
}

// AcceptedHomeChange compares two releases of one Home declaration and accepts
// exactly four classes of difference, alone or together:
//
//   - guidance only: role prompts changed (GuidanceOnlyHomeChange);
//   - additive profile: the newer release adds a `home_profile` section and
//     the installed one has none;
//   - additive project-card words: the newer release adds a `project_card`
//     section and the installed one has none (PRD #792 FR10);
//   - additive resource attachment: the newer release appends resource
//     attachments after the installed ones, which stay byte-identical and in
//     the same order.
//
// Anything else is ErrHomeUpgradeNotGuidanceOnly: a profile or project-card
// section that was changed or removed, an existing attachment that was
// widened, reordered or removed, an appended project attachment, or any other
// difference. Every addition is inert for the Homes it reaches: a card that
// reads nothing until it is opened, words that only rename what is shown, and
// an allowance that creates nothing until the owner sets the resource
// workspace up.
func AcceptedHomeChange(current, next AssistantProgramHome) (HomeUpgradeChange, error) {
	change := HomeUpgradeChange{}
	if current.HomeProfile == nil && next.HomeProfile != nil {
		change.AddsHomeProfile, change.HomeProfileTitle = true, next.HomeProfile.Title
		// Set the added section aside; everything else must still be
		// guidance-only.
		next.HomeProfile = nil
	}
	if current.ProjectCard == nil && next.ProjectCard != nil {
		change.AddsProjectCard = true
		change.ProjectCardNoun, change.ProjectCardNounPlural = next.ProjectCard.Noun, next.ProjectCard.NounPlural
		next.ProjectCard = nil
	}
	if added := len(next.AllowedProjectAttachments) - len(current.AllowedProjectAttachments); added > 0 {
		appended := next.AllowedProjectAttachments[len(current.AllowedProjectAttachments):]
		for _, attachment := range appended {
			if !attachment.IsResource() {
				return HomeUpgradeChange{}, ErrHomeUpgradeNotGuidanceOnly
			}
		}
		change.AddedResourceAttachments = append([]AssistantProgramAllowedProjectAttachment(nil), appended...)
		// The existing entries are compared byte for byte below with the
		// rest of the declaration, so only an append is set aside.
		next.AllowedProjectAttachments = next.AllowedProjectAttachments[:len(current.AllowedProjectAttachments):len(current.AllowedProjectAttachments)]
	}
	prompts, err := GuidanceOnlyHomeChange(current, next)
	if err != nil {
		return HomeUpgradeChange{}, err
	}
	change.RolePrompts = prompts
	return change, nil
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
