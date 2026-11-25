package server

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// detachRetiredAssistantFromRosters takes Ori's retired protected assistant off
// every workspace roster that still names it.
//
// An existing profile can have workspaces whose lead was that assistant. The
// record itself is now hidden, so such a workspace would keep showing a lead
// that can never answer — and its own snapshot copy of the assistant could
// still answer as the retired persona. Detaching the dangling roster entry
// leaves the workspace honestly unstaffed, so its normal "add a lead" recovery
// applies (a Blank workspace's Manager slot reads vacant and fillable).
//
// What it never touches: the retired record (kept on disk as it was),
// sessions and message history, any other roster entry, and a person's own
// agent that happens to share the name — a name held by a visible agent is
// not reported as retired at all. It is idempotent: a workspace with nothing
// to detach is not written.
func detachRetiredAssistantFromRosters(agents store.Store, workspaces workspace.Store) int {
	if agents == nil || workspaces == nil {
		return 0
	}
	retired := store.RetiredAssistantRecordNames(agents)
	if len(retired) == 0 {
		return 0
	}
	names := make(map[string]struct{}, len(retired))
	for _, name := range retired {
		names[strings.TrimSpace(name)] = struct{}{}
	}
	isRetired := func(name string) bool {
		_, found := names[strings.TrimSpace(name)]
		return found
	}

	ids, err := workspaces.List()
	if err != nil {
		logger.Warn("Could not list workspaces to detach the retired assistant", logger.Fields{"error": err})
		return 0
	}
	detached := 0
	for _, id := range ids {
		current, err := workspaces.Get(id)
		if err != nil || current == nil || !rosterNamesRetired(current, isRetired) {
			continue
		}
		err = workspaces.Update(id, func(ws *workspace.Workspace) error {
			kept := make([]workspace.AgentInstance, 0, len(ws.AgentInstances))
			for _, instance := range ws.AgentInstances {
				if !isRetired(instance.Name) {
					kept = append(kept, instance)
				}
			}
			ws.AgentInstances = kept
			if isRetired(ws.EntryAgentName()) {
				next := ""
				if len(kept) > 0 {
					next = kept[0].Name
				}
				return ws.SetEntryAgentName(next)
			}
			return nil
		})
		if err != nil {
			logger.Warn("Could not detach the retired assistant from a workspace", logger.Fields{"workspace_id": id, "error": err})
			continue
		}
		detached++
	}
	if detached > 0 {
		logger.Info("Detached Ori's retired assistant from workspace rosters", logger.Fields{"workspaces": detached})
	}
	return detached
}

func rosterNamesRetired(ws *workspace.Workspace, isRetired func(string) bool) bool {
	if isRetired(ws.EntryAgentName()) {
		return true
	}
	for _, instance := range ws.AgentInstances {
		if isRetired(instance.Name) {
			return true
		}
	}
	return false
}
