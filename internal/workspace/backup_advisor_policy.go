package workspace

import (
	"errors"
	"strings"

	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

// BackupAdvisorRoleID is a responsibility identity, never a display-name or
// prompt match. It NARROWS execution; it cannot create backup-domain authority.
const BackupAdvisorRoleID = "backup-advisor"

var ErrBackupAdvisorRuntime = errors.New("Backup Advisor is available only through its isolated stored-facts conversation")

// BackupAdvisorRuntimeRestricted rejects general execution of the role, plus
// global/unassigned fallback outside its workspace. A shared profile genuinely
// attached in another role keeps that workspace's independent policy.
func BackupAdvisorRuntimeRestricted(workspaces Store, workspaceID, agentName string, agents agentstore.AgentGetter) bool {
	if workspaces == nil {
		return false
	}
	if strings.TrimSpace(workspaceID) != "" {
		view, err := workspaces.Get(workspaceID)
		if err != nil || view == nil || IsBackupAdvisorExecution(view, agentName, agents) {
			return true
		}
		canonical := agentName
		if agents != nil {
			if _, resolved, ok := agentstore.ResolveAgent(agents, agentName); ok {
				canonical = resolved
			}
		}
		for _, instance := range view.GetAgentInstances() {
			bound := instance.Name
			if agents != nil {
				if _, resolved, ok := agentstore.ResolveAgent(agents, bound); ok {
					bound = resolved
				}
			}
			if strings.EqualFold(strings.TrimSpace(bound), strings.TrimSpace(canonical)) {
				return false
			}
		}
	}
	ids, err := workspaces.List()
	if err != nil {
		return true
	}
	for _, id := range ids {
		view, err := workspaces.Get(id)
		if err != nil || view == nil || IsBackupAdvisorExecution(view, agentName, agents) {
			return true
		}
	}
	return false
}

// IsBackupAdvisorAttachment checks every matching attachment, not a caller-
// selected node. Capability removal cannot widen the surviving role. No shared
// agent, filesystem grant or other capability's policy is changed here.
func IsBackupAdvisorAttachment(ws *Workspace, agentName string) bool {
	if ws == nil || strings.TrimSpace(agentName) == "" {
		return false
	}
	for _, instance := range ws.GetAgentInstances() {
		if instance.RoleID == BackupAdvisorRoleID && strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(agentName)) {
			return true
		}
	}
	return false
}

// Resolve only existing agent identity aliases; a retired system-agent name
// must not bypass the role guard when a shared definition fills this slot.
func IsBackupAdvisorExecution(ws *Workspace, name string, agents agentstore.AgentGetter) bool {
	if IsBackupAdvisorAttachment(ws, name) {
		return true
	}
	if ws == nil || agents == nil {
		return false
	}
	_, canonical, ok := agentstore.ResolveAgent(agents, name)
	if !ok {
		return false
	}
	for _, instance := range ws.GetAgentInstances() {
		if instance.RoleID != BackupAdvisorRoleID {
			continue
		}
		_, bound, found := agentstore.ResolveAgent(agents, instance.Name)
		if found && strings.EqualFold(canonical, bound) {
			return true
		}
	}
	return false
}
