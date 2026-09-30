package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// A reviewed Home package upgrade (docs/architecture/independent-program-homes.md
// §6.1) moves a Home and its linked projects from exactly one installed
// package evidence to exactly one newer one. These primitives are pure record
// transformations: they read nothing and write nothing themselves, and a rerun
// after a crash finds the record already moved and changes nothing.

var (
	// ErrHomeProviderPinUnexpected means a Home or linked project is pinned to
	// neither the old nor the new package evidence (or only partly moved). The
	// upgrade must stop for an owner-reviewed reconciliation; nothing guesses.
	ErrHomeProviderPinUnexpected = errors.New("pinned to neither the reviewed old nor the new package evidence")
	// ErrInvalidHomeProviderUpgrade is a malformed upgrade description.
	ErrInvalidHomeProviderUpgrade = errors.New("invalid Home package upgrade")
)

const maxHomeProviderUpgradeReceipts = 16

// AssistantHomeProviderUpgradeReceipt records one reviewed package upgrade on
// the Home it moved.
type AssistantHomeProviderUpgradeReceipt struct {
	OperationID     string    `json:"operation_id"`
	FromVersion     string    `json:"from_version"`
	ToVersion       string    `json:"to_version"`
	FromFingerprint string    `json:"from_fingerprint"`
	ToFingerprint   string    `json:"to_fingerprint"`
	UpgradedAt      time.Time `json:"upgraded_at"`
}

// HomeRolePromptChange is one Home role's system prompt before and after.
type HomeRolePromptChange struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// HomeProviderUpgrade describes one package upgrade: the exact evidence a
// Home is moved from and to, and the role prompts that change with it. Roles
// not named in RolePrompts keep their prompt.
type HomeProviderUpgrade struct {
	OperationID string
	From        AssistantProgramHomeOwner
	To          AssistantProgramHomeOwner
	RolePrompts map[string]HomeRolePromptChange
	UpgradedAt  time.Time
}

func (u HomeProviderUpgrade) valid() bool {
	if strings.TrimSpace(u.OperationID) == "" || !u.From.Valid() || !u.To.Valid() || u.From == u.To ||
		!strings.EqualFold(u.From.PluginID, u.To.PluginID) || u.From.ProgramID != u.To.ProgramID ||
		u.From.HomeSchemaVersion != u.To.HomeSchemaVersion || u.From.HomeVersion != u.To.HomeVersion ||
		u.UpgradedAt.IsZero() {
		return false
	}
	for roleID, change := range u.RolePrompts {
		if strings.TrimSpace(roleID) == "" || change.Old == change.New {
			return false
		}
	}
	return true
}

// Declarations derives both sides of a Home declaration from whichever side
// `current` is on. ok is false when current matches neither (for example one
// changed role still has its old prompt and another already has its new one).
func (u HomeProviderUpgrade) Declarations(current *AssistantProgramDeclaration) (before, after *AssistantProgramDeclaration, ok bool) {
	if current == nil {
		return nil, nil, false
	}
	before, after = CloneAssistantProgramDeclaration(current), CloneAssistantProgramDeclaration(current)
	onOld, onNew := true, true
	seen := 0
	for i := range current.Roles {
		change, changed := u.RolePrompts[current.Roles[i].ID]
		if !changed {
			continue
		}
		seen++
		onOld = onOld && current.Roles[i].SystemPrompt == change.Old
		onNew = onNew && current.Roles[i].SystemPrompt == change.New
		before.Roles[i].SystemPrompt, after.Roles[i].SystemPrompt = change.Old, change.New
	}
	if seen != len(u.RolePrompts) || (!onOld && !onNew) {
		return nil, nil, false
	}
	return before, after, true
}

// RebindHome moves one Home. It reports whether the record changed; false
// with no error means the Home was already moved (a rerun after a crash).
func (u HomeProviderUpgrade) RebindHome(home *Workspace) (bool, error) {
	if !u.valid() || home == nil {
		return false, ErrInvalidHomeProviderUpgrade
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil {
		return false, ErrHomeProviderPinUnexpected
	}
	before, after, ok := u.Declarations(state.Declaration)
	if !ok {
		return false, ErrHomeProviderPinUnexpected
	}
	switch *state.HomeProvider {
	case u.To:
		if !sameDeclaration(state.Declaration, after) {
			return false, ErrHomeProviderPinUnexpected
		}
		return false, nil
	case u.From:
		if !sameDeclaration(state.Declaration, before) {
			return false, ErrHomeProviderPinUnexpected
		}
	default:
		return false, ErrHomeProviderPinUnexpected
	}
	to := u.To.Clone()
	state.HomeProvider = &to
	state.Declaration = after
	state.StateRevision++
	state.ProviderUpgrades = append(state.ProviderUpgrades, AssistantHomeProviderUpgradeReceipt{
		OperationID: u.OperationID, FromVersion: u.From.PluginVersion, ToVersion: u.To.PluginVersion,
		FromFingerprint: u.From.ComponentFingerprint, ToFingerprint: u.To.ComponentFingerprint,
		UpgradedAt: u.UpgradedAt.UTC(),
	})
	if extra := len(state.ProviderUpgrades) - maxHomeProviderUpgradeReceipts; extra > 0 {
		state.ProviderUpgrades = append([]AssistantHomeProviderUpgradeReceipt(nil), state.ProviderUpgrades[extra:]...)
	}
	home.SetAssistantProgramState(state)
	return true, nil
}

// RebindProject moves one project linked to homeID: the Home pin on its link
// and on its Group Requirement snapshot, and its copy of the Home declaration.
// homeDeclaration is the Home's declaration on either side of the upgrade.
// Creation-time digests in the snapshot are left as recorded. The link's
// StateRevision is deliberately not advanced: approved library folders bind
// the exact link revision, and the owner decided they stay approved. The
// Home's revision does advance (RebindHome), which cancels pending library
// reviews and scans.
func (u HomeProviderUpgrade) RebindProject(child *Workspace, homeID string, homeDeclaration *AssistantProgramDeclaration) (bool, error) {
	if !u.valid() || child == nil || strings.TrimSpace(homeID) == "" {
		return false, ErrInvalidHomeProviderUpgrade
	}
	link := child.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID != homeID {
		return false, ErrHomeProviderPinUnexpected
	}
	before, after, ok := u.Declarations(homeDeclaration)
	if !ok {
		return false, ErrHomeProviderPinUnexpected
	}
	changed := false
	move := func(pin **AssistantProgramHomeOwner) error {
		if *pin == nil {
			return nil
		}
		switch **pin {
		case u.To:
			return nil
		case u.From:
			to := u.To.Clone()
			*pin = &to
			changed = true
			return nil
		default:
			return ErrHomeProviderPinUnexpected
		}
	}
	if err := move(&link.HomeProvider); err != nil {
		return false, err
	}
	provenance := child.GetTemplateProvenance()
	if provenance != nil {
		if provenance.GroupRequirement != nil {
			if home := strings.TrimSpace(provenance.GroupRequirement.HomeWorkspaceID); home != "" && home != homeID {
				return false, ErrHomeProviderPinUnexpected
			}
			if err := move(&provenance.GroupRequirement.HomeProvider); err != nil {
				return false, err
			}
		}
		if provenance.AssistantProgram != nil {
			switch {
			case sameDeclaration(provenance.AssistantProgram, after):
			case sameDeclaration(provenance.AssistantProgram, before):
				provenance.AssistantProgram = CloneAssistantProgramDeclaration(after)
				changed = true
			default:
				return false, ErrHomeProviderPinUnexpected
			}
		}
	}
	if !changed {
		return false, nil
	}
	child.SetAssistantProjectLink(link)
	if provenance != nil {
		child.SetTemplateProvenance(provenance)
	}
	return true, nil
}

func sameDeclaration(a, b *AssistantProgramDeclaration) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}
