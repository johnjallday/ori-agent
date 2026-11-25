// Package workspacemanager holds the one definition of a workspace's own
// manager: the agent a new workspace starts with so it is never a dead end
// before anyone has said what it is for.
//
// A manager is an ordinary agent profile bound to one workspace by creation
// provenance (agent.WorkspaceManagerProvenance), never by its display name. It
// inherits nothing from Ori's retired protected assistant: no protection, no
// chat carve-out, and no role outside its workspace.
//
// This package only names and describes a manager. Creating one, recording its
// provenance and attaching it is the caller's job.
package workspacemanager

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// RoleID is the stable, host-owned role a manager fills. It does not
	// follow the agent's or the workspace's name, so renaming either never
	// strands the slot.
	RoleID = "manager"
	// RoleLabel names that role wherever a roster shows it.
	RoleLabel = "Manager"

	// MaxNameLength matches the reviewed-override name limit, so a proposed
	// name is always one the review form accepts back unchanged.
	MaxNameLength = 100

	nameSuffix   = " Manager"
	fallbackName = "Manager"
	maxTries     = 50
)

// Prompt is a manager's starting instructions. It names no workspace: the
// runtime already supplies the current workspace's context, and a baked-in
// name would go stale the first time the workspace is renamed.
const Prompt = "You are this workspace's manager. " +
	"Someone may arrive before they know what the workspace is for: when they ask, help them work out its purpose " +
	"and a sensible first step. When they ask for work, start on it. Work only with this workspace's files, notes " +
	"and tickets; other workspaces are separate and you cannot act in them. Do not require an interview, scan or " +
	"index folders, write files, or hire agents unless the person asks for that."

// BaseName derives "<Workspace> Manager" from a workspace name, keeping only
// the characters a reviewed agent name may contain.
func BaseName(workspaceName string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.TrimSpace(workspaceName) {
		allowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !allowed {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	base := b.String()
	if base == "" {
		return fallbackName
	}
	if strings.EqualFold(base, fallbackName) || strings.HasSuffix(strings.ToLower(base), strings.ToLower(nameSuffix)) {
		return truncate(base, MaxNameLength)
	}
	return truncate(base, MaxNameLength-len(nameSuffix)) + nameSuffix
}

// Propose returns the first "<Workspace> Manager" name taken does not report,
// suffixing " 2", " 3", … as needed. An existing agent is never reused by name.
// An empty result means no free name was found and no manager may be made.
func Propose(workspaceName string, taken func(string) bool) string {
	base := BaseName(workspaceName)
	for i := 1; i <= maxTries; i++ {
		candidate := base
		if i > 1 {
			suffix := fmt.Sprintf(" %d", i)
			candidate = truncate(base, MaxNameLength-len(suffix)) + suffix
		}
		if taken == nil || !taken(candidate) {
			return candidate
		}
	}
	return ""
}

// TakenIn returns a case-insensitive "is this name taken" check over a
// snapshot of existing agent names — the same comparison the strict create
// that will make the manager applies.
func TakenIn(existing []string) func(string) bool {
	names := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		names[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return func(candidate string) bool {
		_, found := names[strings.ToLower(strings.TrimSpace(candidate))]
		return found
	}
}

func truncate(name string, limit int) string {
	if utf8.RuneCountInString(name) <= limit {
		return name
	}
	return strings.TrimSpace(string([]rune(name)[:limit]))
}
