package workspace

import (
	"path/filepath"
	"strings"
)

// Store nodes were canvas objects that said where a ticket's result file was
// written. The canvas is retired and every ticket target now lives on the ticket
// itself (its result_storage), so a workspace that still carries store nodes is
// rewritten, once, into plain file targets. MigrateLegacyStoreNodes is that
// rewrite. It is pure and idempotent: it stamps no time and no version, it never
// touches the disk, and running it on its own output changes nothing, which the
// continuity fence relies on (it compares canonical JSON).
//
// It runs wherever a workspace is decoded, before anything else normalizes it,
// so runtime code never sees a store node. The decisions it applies are the
// store-node migration contract of the canvas retirement.

// LegacyStoreChange is one place where the rewrite could not reproduce what the
// store node did, or where it changed what a ticket does. It is reported so the
// startup pass can record it; the rewrite itself still goes ahead.
type LegacyStoreChange struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
	Reason      string `json:"reason"`
}

// LegacyStoreMigration is what one rewrite did.
type LegacyStoreMigration struct {
	// Changed reports whether the workspace was modified at all.
	Changed bool
	// Lossy lists the tickets whose behaviour is not exactly what it was.
	Lossy []LegacyStoreChange
}

// HasLegacyStoreNodes reports whether ws still carries anything the rewrite
// would change: a store node, or a ticket that names one.
func HasLegacyStoreNodes(ws *Workspace) bool {
	if ws == nil {
		return false
	}
	if len(ws.LegacyStoreNodes) > 0 {
		return true
	}
	for i := range ws.Tasks {
		if storage := ws.Tasks[i].ResultStorage; storage != nil && storage.LegacyStoreNodeID != "" {
			return true
		}
	}
	return false
}

// MigrateLegacyStoreNodes rewrites ws's store nodes into plain ticket targets
// and reports whether it changed anything. See LegacyStoreMigration for the
// detail of what it did.
func MigrateLegacyStoreNodes(ws *Workspace) bool {
	return MigrateLegacyStoreNodesWithReport(ws).Changed
}

// MigrateLegacyStoreNodesWithReport is MigrateLegacyStoreNodes plus a report of
// the tickets it could not reproduce exactly.
//
// A ticket that names a store node (by its id or its canvas node id) gets that
// store's destination written into its own result_storage and loses the
// reference. A store node bound to an agent with auto-store on gives the same
// destination to every existing ticket assigned to that agent that has no
// enabled storage of its own; tickets created later do not inherit it. A ticket
// that names a store node which no longer exists keeps everything else and only
// loses the dangling reference. Afterwards the workspace holds no store nodes.
func MigrateLegacyStoreNodesWithReport(ws *Workspace) LegacyStoreMigration {
	var out LegacyStoreMigration
	if !HasLegacyStoreNodes(ws) {
		return out
	}
	nodes := ws.LegacyStoreNodes

	// The executor matched the first node whose id or canvas node id equalled the
	// reference, so a duplicate id never reached a later node.
	find := func(ref string) *LegacyStoreNode {
		for i := range nodes {
			if nodes[i].ID == ref || nodes[i].CanvasNodeID == ref {
				return &nodes[i]
			}
		}
		return nil
	}
	note := func(task *Task, reason string) {
		out.Lossy = append(out.Lossy, LegacyStoreChange{WorkspaceID: ws.ID, TaskID: task.ID, Reason: reason})
	}

	// A ticket that names a store.
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		storage := task.ResultStorage
		if storage == nil || storage.LegacyStoreNodeID == "" {
			continue
		}
		ref := storage.LegacyStoreNodeID
		storage.LegacyStoreNodeID = ""
		node := find(ref)
		if node == nil {
			// A dangling reference used to write nothing. The ticket now saves
			// wherever a ticket with no destination saves, which is its outputs
			// folder. Everything else on it is kept.
			if storage.Enabled {
				note(task, "its store node no longer existed, so it used to save nothing; it now saves to the workspace outputs folder")
			}
			continue
		}
		applyLegacyStoreTarget(storage, node, func(reason string) { note(task, reason) })
	}

	// A store bound to an agent, for the tickets that agent already has.
	seen := make(map[string]bool, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		agentNode := node.AgentNodeID
		if agentNode == "" || agentNode == "unassigned" || seen[agentNode] {
			continue
		}
		// Only the first store bound to an agent ever took its results.
		seen[agentNode] = true
		if !node.AutoStore {
			continue
		}
		for t := range ws.Tasks {
			task := &ws.Tasks[t]
			if task.AssignedNodeID != agentNode {
				continue
			}
			if task.ResultStorage != nil && task.ResultStorage.Enabled {
				continue
			}
			storage := &ResultStorageConfig{Enabled: true, Format: legacyAgentBoundFormat(node.Format)}
			if NormalizeStorageTarget(node.StorageTarget) != StorageTargetWorkspaceFolder && strings.TrimSpace(node.BaseDir) == "" {
				// The store had no directory, so it never wrote a file.
				note(task, "its agent's store had no base directory and never saved a file, so nothing was copied")
				continue
			}
			applyLegacyStoreTarget(storage, node, func(reason string) { note(task, reason) })
			if node.Format == "binary" {
				note(task, "its agent's store used the binary format, which a ticket cannot; results are saved as text")
			}
			task.ResultStorage = storage
		}
	}

	ws.LegacyStoreNodes = nil
	out.Changed = true
	return out
}

// legacyAgentBoundFormat is the format an agent-bound store wrote in, as a
// ticket format. A store's binary format has no ticket equivalent.
func legacyAgentBoundFormat(format string) string {
	switch format {
	case "json", "markdown", "text", "csv":
		return format
	default:
		return "text"
	}
}

// applyLegacyStoreTarget points storage at the destination node used to write
// to, keeping the ticket's own format, write mode and file name. The node's
// format, write mode and auto_create_dir are not carried: the ticket's format
// decides the bytes, and a ticket target creates its directory.
func applyLegacyStoreTarget(storage *ResultStorageConfig, node *LegacyStoreNode, lossy func(reason string)) {
	filePath := strings.TrimSpace(storage.FilePath)
	if legacyStoreRejectsFilePath(filePath) {
		// A store refused an absolute path or one that climbs out of its
		// directory, whatever kind of store it was, so the ticket never saved.
		// Keep the path as written and keep the ticket off.
		storage.Enabled = false
		lossy("its file path was one a store refused, so it never saved a file; the ticket stays off")
		return
	}

	if NormalizeStorageTarget(node.StorageTarget) == StorageTargetWorkspaceFolder {
		// A store on a workspace folder needs no path resolution: the ticket
		// target names the same folder.
		storage.StorageTarget = StorageTargetWorkspaceFolder
		storage.Folder = node.Folder
		return
	}

	base := strings.TrimSpace(node.BaseDir)
	if base == "" {
		// The store never wrote a file; leave the ticket off rather than let it
		// start saving somewhere it never did.
		storage.Enabled = false
		lossy("its store had no base directory and never saved a file; the ticket stays off")
		return
	}

	storage.StorageTarget = ""
	storage.Folder = ""
	switch {
	case filePath == "":
		// The default name is written into the store's directory.
		storage.FilePath = filepath.Clean(base) + string(filepath.Separator)
	case node.WriteMode == "append" && !strings.EqualFold(strings.TrimSpace(storage.WriteMode), "append"):
		// A store that appended to one fixed file for a ticket that was not in
		// append mode. A ticket target cannot append raw text, so each run writes
		// its own timestamped file beside the old log, which is never overwritten.
		storage.FilePath = filepath.Dir(filepath.Join(base, filePath)) + string(filepath.Separator)
		lossy("its store appended to one fixed file; each run now writes its own timestamped file in that folder")
	default:
		storage.FilePath = filepath.Join(base, filePath)
	}
}

// legacyStoreRejectsFilePath mirrors what a store refused as a ticket's file
// path: an absolute path, or one that climbs out of the store's directory.
func legacyStoreRejectsFilePath(filePath string) bool {
	if filePath == "" {
		return false
	}
	if filepath.IsAbs(filePath) {
		return true
	}
	clean := filepath.Clean(filePath)
	return strings.Contains(clean, "..") || strings.HasPrefix(clean, "/")
}
