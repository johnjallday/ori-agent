package sessionhttp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

// A Blank workspace's manager is an ordinary agent profile that the workspace's
// own create operation makes for it. Three things keep it bound to that one
// workspace rather than to a display name:
//
//   - its proposed name is free when proposed, so Blank never reuses an
//     existing agent by name match;
//   - creation stamps agent.WorkspaceManagerProvenance with the workspace ID,
//     which survives renaming either the agent or the workspace;
//   - its role has a stable host-owned id ("manager"), so the roster slot does
//     not move when the workspace is renamed.
//
// It inherits nothing from Ori's former protected assistant: no protection, no
// chat-preflight carve-out, no fallback role outside its workspace.
const (
	blankManagerRoleID    = "manager"
	blankManagerRoleLabel = "Manager"

	// maxManagerNameLength matches the reviewed-override name limit, so a
	// proposed name is always one the review form accepts back unchanged.
	maxManagerNameLength = 100
	managerNameSuffix    = " Manager"
	fallbackManagerName  = "Manager"
	maxManagerNameTries  = 50
)

// blankWorkspaceManagerPrompt is the manager's starting instructions. It names
// no workspace: the runtime already supplies the current workspace's context,
// and a baked-in name would go stale the first time the workspace is renamed.
const blankWorkspaceManagerPrompt = "You are this workspace's manager. " +
	"Someone may arrive before they know what the workspace is for: when they ask, help them work out its purpose " +
	"and a sensible first step. When they ask for work, start on it. Work only with this workspace's files, notes " +
	"and tickets; other workspaces are separate and you cannot act in them. Do not require an interview, scan or " +
	"index folders, write files, or hire agents unless the person asks for that."

// legacyBlankRoleIDs are role ids an earlier Blank roster derived from the
// names of Ori's former protected assistant. Reading an existing Blank
// workspace's roster maps them onto the manager slot so a holder recorded under
// one is not listed as unassigned. This is a read-time roster mapping only: no
// record is rewritten and no agent name is routed anywhere.
var legacyBlankRoleIDs = map[string]struct{}{
	"ask-ori":           {},
	"workspace-manager": {},
}

func isLegacyBlankRoleID(roleID string) bool {
	_, legacy := legacyBlankRoleIDs[strings.ToLower(strings.TrimSpace(roleID))]
	return legacy
}

// workspaceManagerBaseName derives "<Workspace> Manager" from a workspace name,
// keeping only the characters a reviewed agent name may contain.
func workspaceManagerBaseName(workspaceName string) string {
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
		return fallbackManagerName
	}
	if strings.EqualFold(base, fallbackManagerName) || strings.HasSuffix(strings.ToLower(base), strings.ToLower(managerNameSuffix)) {
		return truncateManagerName(base, maxManagerNameLength)
	}
	return truncateManagerName(base, maxManagerNameLength-len(managerNameSuffix)) + managerNameSuffix
}

func truncateManagerName(name string, limit int) string {
	if utf8.RuneCountInString(name) <= limit {
		return name
	}
	return strings.TrimSpace(string([]rune(name)[:limit]))
}

// proposeWorkspaceManagerName returns the first free "<Workspace> Manager"
// name, suffixing " 2", " 3", … when an agent already holds it. "Free" is
// case-insensitive, like the strict staffing create that will make it. An empty
// result means no free name was found and the caller must not seed a manager.
func (h *Handler) proposeWorkspaceManagerName(workspaceName string) string {
	base := workspaceManagerBaseName(workspaceName)
	taken := h.agentNameTaken()
	for i := 1; i <= maxManagerNameTries; i++ {
		candidate := base
		if i > 1 {
			suffix := fmt.Sprintf(" %d", i)
			candidate = truncateManagerName(base, maxManagerNameLength-len(suffix)) + suffix
		}
		if !taken(candidate) {
			return candidate
		}
	}
	return ""
}

// agentNameTaken snapshots the saved agent names once and reports whether a
// candidate collides with one, case-insensitively, or with a name the strict
// staffing create refuses outright.
func (h *Handler) agentNameTaken() func(string) bool {
	existing := map[string]struct{}{}
	if h != nil && h.agentStore != nil {
		for _, name := range h.agentStore.ListAgents() {
			existing[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
		}
	}
	return func(candidate string) bool {
		if systemassistant.IsKnownName(candidate) {
			return true
		}
		_, found := existing[strings.ToLower(strings.TrimSpace(candidate))]
		return found
	}
}
