package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// workspaceMarkerFile marks a folder as a workspace (workspace.FolderMetadataFile;
// this package cannot import internal/workspace).
const workspaceMarkerFile = "workspace.json"

// Root availability reasons reported by AgentRootStatus.
const (
	RootReasonMissing             = "missing"
	RootReasonOccupiedByWorkspace = "occupied_by_workspace"
)

// memberStore is what the composite needs from each of its stores.
type memberStore interface {
	Store
	AgentRenamer
}

// CompositeStore presents the system store and the workspace root's agents
// folder as one Store.
//
//   - The system store lives in the data dir and holds the built-in assistant.
//     Until the one-time migration has moved them, it also holds the user's
//     older agents, which therefore stay visible and editable where they are.
//   - The root store is <workspace root>/Agents: one folder per agent, no
//     index file, runtime state kept in the data dir.
//
// Each call is routed by agent name, so the 55 GetAgent call sites and every
// writer keep working through the ordinary Store interface.
type CompositeStore struct {
	mu        sync.RWMutex
	system    memberStore
	root      *fileStore // nil while the root folder is unavailable
	rootPath  string     // the workspace root, not its Agents folder
	stateRoot string     // <data dir>/agent_state
	defaults  types.Settings

	// legacyWrites sends new agents to the system store for this session.
	// Set when the migration could not move agents into the root (a workspace
	// occupies <root>/Agents, or the root is not writable): the user's agents
	// then keep living where they already are.
	legacyWrites bool
	migration    *RootMigrationReport

	// workspaces is the read-only view of trusted workspaces' own agent
	// copies. They are read where they are; nothing is mirrored into a store.
	workspaces workspaceAgentsCache
}

// NewCompositeStore routes between system, the data-dir store, and the agents
// folder of root. A missing root is not an error: the store starts with the
// system store alone and reports the root as unavailable.
func NewCompositeStore(system Store, root, stateRoot string, defaults types.Settings) (*CompositeStore, error) {
	member, ok := system.(memberStore)
	if !ok {
		return nil, fmt.Errorf("system agent store %T cannot rename agents", system)
	}
	c := &CompositeStore{
		system:    member,
		rootPath:  root,
		stateRoot: stateRoot,
		defaults:  defaults,
	}
	c.openRootLocked()
	return c, nil
}

// SetRoot points the store at another Workspace Directory. The roster switches
// to that root's agents at once, with no restart; nothing is moved or copied
// between roots, and the built-in assistant stays. A startup migration's
// outcome described the old root, so it no longer applies.
func (c *CompositeStore) SetRoot(root string) {
	c.mu.Lock()
	c.rootPath = root
	c.root = nil
	c.legacyWrites = false
	c.migration = nil
	c.openRootLocked()
	c.mu.Unlock()
	c.InvalidateWorkspaceAgents()
}

// ReloadRoot re-reads the root's agents from disk, so an edit made outside Ori
// (a text editor, a sync tool) shows without a restart. "Rescan from disk"
// calls it; there is no file watcher.
func (c *CompositeStore) ReloadRoot() {
	c.mu.Lock()
	c.root = nil
	c.openRootLocked()
	c.mu.Unlock()
	c.InvalidateWorkspaceAgents()
}

// SetMigrationReport records the outcome of the startup migration. A migration
// that could not move the agents keeps new agents in the legacy location for
// this session.
func (c *CompositeStore) SetMigrationReport(report *RootMigrationReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.migration = report
	c.legacyWrites = report != nil && report.KeepsLegacyLocation()
}

// AgentRootStatus describes the workspace root's agents folder for the Agents
// page: where it is, whether Ori can use it, and how the migration went.
type AgentRootStatus struct {
	Path      string               `json:"path"`
	Available bool                 `json:"available"`
	Reason    string               `json:"reason,omitempty"`
	Migration *RootMigrationStatus `json:"migration,omitempty"`
}

// RootMigrationStatus is the part of a migration report the Agents page shows.
type RootMigrationStatus struct {
	Status  string   `json:"status"`
	Skipped []string `json:"skipped,omitempty"`
}

// RootStatus reports the agents folder as it is right now.
func (c *CompositeStore) RootStatus() AgentRootStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := AgentRootStatus{Path: config.RootAgentsDir(c.rootPath)}
	status.Reason = inspectAgentRoot(c.rootPath)
	status.Available = status.Reason == ""
	if c.migration != nil && c.migration.Status != MigrationNotNeeded {
		status.Migration = &RootMigrationStatus{Status: c.migration.Status, Skipped: c.migration.Skipped}
	}
	return status
}

// inspectAgentRoot reports why root's agents folder cannot be used, or "".
func inspectAgentRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return RootReasonMissing
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return RootReasonMissing
	}
	if _, err := os.Stat(filepath.Join(config.RootAgentsDir(root), workspaceMarkerFile)); err == nil {
		return RootReasonOccupiedByWorkspace
	}
	return ""
}

// openRootLocked opens the root store when the root is usable and not yet
// open. It never creates the root: <root>/Agents is created by the first
// write, and only inside a root that exists. Assumes c.mu is held for writing.
func (c *CompositeStore) openRootLocked() {
	if c.root != nil || inspectAgentRoot(c.rootPath) != "" {
		return
	}
	state := NewRuntimeStateStore(c.stateRoot, c.rootPath)
	c.root = newDirStore(config.RootAgentsDir(c.rootPath), state, c.defaults)
}

// rootForWrite returns the root store for a write, opening it if the root has
// appeared since startup. It fails when the root is gone.
func (c *CompositeStore) rootForWrite() (*fileStore, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reason := inspectAgentRoot(c.rootPath); reason != "" {
		return nil, ErrAgentRootUnavailable
	}
	c.openRootLocked()
	return c.root, nil
}

// owner returns the store holding name, or nil. The built-in assistant is
// looked for in the system store first; any other name in the root first, so
// an older copy left behind in the data dir never shadows the user's agent.
func (c *CompositeStore) owner(name string) memberStore {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	if systemassistant.IsKnownName(name) {
		if _, ok := c.system.GetAgent(name); ok {
			return c.system
		}
	}
	if root != nil {
		if _, ok := root.GetAgent(name); ok {
			return root
		}
	}
	if _, ok := c.system.GetAgent(name); ok {
		return c.system
	}
	return nil
}

// targetForNew picks the store a new agent is created in.
func (c *CompositeStore) targetForNew(name string) (memberStore, error) {
	c.mu.RLock()
	legacy := c.legacyWrites
	c.mu.RUnlock()
	if systemassistant.IsCanonicalName(name) || legacy {
		return c.system, nil
	}
	root, err := c.rootForWrite()
	if err != nil {
		return nil, err
	}
	return root, nil
}

// writeTarget is the store an existing or new agent is written to.
func (c *CompositeStore) writeTarget(name string) (memberStore, error) {
	if owner := c.owner(name); owner != nil {
		if owner != c.system {
			// The root can disappear mid-session (an unmounted drive).
			if _, err := c.rootForWrite(); err != nil {
				return nil, err
			}
		}
		return owner, nil
	}
	return c.targetForNew(name)
}

// AgentFolder locates an agent's own folder, where its skill state and
// per-agent skills live: the data dir for the built-in assistant (and agents
// not yet migrated), <root>/Agents/<Name> for the user's agents. An agent only
// a workspace holds has no folder of its own: ("", true). A name the store does
// not hold at all reports handled=false.
func (c *CompositeStore) AgentFolder(name string) (dir string, handled bool) {
	if owner := c.owner(name); owner != nil {
		if fs, ok := owner.(*fileStore); ok {
			return filepath.Join(fs.agentsDir(), name), true
		}
		return "", false
	}
	if _, ok := c.workspaceOnly(name); ok {
		return "", true
	}
	return "", false
}

// RootAgentFolder returns <root>/Agents/<Name> for one of the user's agents in
// the Workspace Directory, and false for any other name (the built-in
// assistant, an agent still in the data dir, a workspace's own agent).
func (c *CompositeStore) RootAgentFolder(name string) (string, bool) {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	if root == nil || c.owner(name) != memberStore(root) {
		return "", false
	}
	return filepath.Join(root.agentsDir(), name), true
}

// UnreadableAgents lists agents in the data dir or the root whose definition
// file could not be read. They are not in ListAgents: the Agents page shows
// them as "could not be read", naming the file.
func (c *CompositeStore) UnreadableAgents() []UnreadableAgent {
	type unreadableLister interface{ UnreadableAgents() []UnreadableAgent }
	var out []UnreadableAgent
	if lister, ok := c.system.(unreadableLister); ok {
		out = append(out, lister.UnreadableAgents()...)
	}
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	if root != nil {
		out = append(out, root.UnreadableAgents()...)
	}
	return out
}

// unreadableError refuses a write naming an unreadable agent, or nil.
func (c *CompositeStore) unreadableError(name string) error {
	for _, entry := range c.UnreadableAgents() {
		if entry.Name == name {
			return &UnreadableAgentError{UnreadableAgent: entry}
		}
	}
	return nil
}

// ListAgents is the union of the system agents, the root agents, and the
// agents only a trusted workspace holds, each name once. A workspace copy of a
// system or root agent is that agent's customisation, not a second entry.
func (c *CompositeStore) ListAgents() []string {
	names := c.memberNames()
	for _, entries := range c.workspaceAgentView().byName {
		names = append(names, entries[0].AgentName)
	}
	return uniqueNamesFold(names)
}

// uniqueNamesFold keeps the first spelling of each name, compared
// case-insensitively because macOS file systems are, and sorts the result.
func uniqueNamesFold(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// GetAgent resolves the system agent, then the root agent, then an agent only
// a trusted workspace holds (from the workspace with the lowest ID).
func (c *CompositeStore) GetAgent(name string) (*agent.Agent, bool) {
	if owner := c.owner(name); owner != nil {
		return owner.GetAgent(name)
	}
	if entry, ok := c.workspaceOnly(name); ok {
		return entry.Agent, true
	}
	return nil, false
}

// CreateAgent creates one of the user's agents. A name only a workspace holds
// is free to use: the new agent then owns it, and the workspace's copy becomes
// that agent's customisation.
func (c *CompositeStore) CreateAgent(name string, cfg *CreateAgentConfig) error {
	target, err := c.writeTarget(name)
	if err != nil {
		return err
	}
	if err := target.CreateAgent(name, cfg); err != nil {
		return err
	}
	c.InvalidateWorkspaceAgents()
	return nil
}

func (c *CompositeStore) SetAgent(name string, ag *agent.Agent) error {
	if c.owner(name) == nil {
		if err := c.unreadableError(name); err != nil {
			return err
		}
		if err := c.workspaceOwnedError(name); err != nil {
			return err
		}
	}
	target, err := c.writeTarget(name)
	if err != nil {
		return err
	}
	return target.SetAgent(name, ag)
}

func (c *CompositeStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	owner := c.owner(name)
	if owner == nil {
		if err := c.unreadableError(name); err != nil {
			return err
		}
		if err := c.workspaceOwnedError(name); err != nil {
			return err
		}
		// The agent may have been added on disk since the store loaded; the
		// root store reloads a definition it has not seen before.
		c.mu.RLock()
		root := c.root
		c.mu.RUnlock()
		if root == nil {
			return fmt.Errorf("agent %q not found", name)
		}
		owner = root
	}
	if owner != c.system {
		if _, err := c.rootForWrite(); err != nil {
			return err
		}
	}
	return owner.UpdateAgent(name, updateFn)
}

func (c *CompositeStore) DeleteAgent(name string) error {
	owner := c.owner(name)
	if owner == nil {
		if err := c.unreadableError(name); err != nil {
			return err
		}
		return c.workspaceOwnedError(name)
	}
	if owner != c.system {
		if _, err := c.rootForWrite(); err != nil {
			return err
		}
	}
	return owner.DeleteAgent(name)
}

// RenameAgent moves an agent within the store that holds it.
func (c *CompositeStore) RenameAgent(oldName, newName string) error {
	owner := c.owner(oldName)
	if owner == nil {
		if err := c.workspaceOwnedError(oldName); err != nil {
			return err
		}
		return fmt.Errorf("agent %q not found", oldName)
	}
	if other := c.owner(newName); other != nil && other != owner {
		return fmt.Errorf("agent %q already exists", newName)
	}
	if owner != c.system {
		if _, err := c.rootForWrite(); err != nil {
			return err
		}
	}
	return owner.RenameAgent(oldName, newName)
}

func (c *CompositeStore) ClearAgents() error {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	err := c.system.ClearAgents()
	if root != nil {
		err = errors.Join(err, root.ClearAgents())
	}
	return err
}

func (c *CompositeStore) Save() error {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	err := c.system.Save()
	if root != nil && inspectAgentRoot(c.rootPath) == "" {
		err = errors.Join(err, root.Save())
	}
	return err
}

// ResetLocations reports everything an agents reset removes: the system
// store's own files, the runtime state folder, and the current root's Agents
// folder, which reset reviews separately because it is visible to the user.
func (c *CompositeStore) ResetLocations() ResetLocations {
	locations := ResetLocations{State: c.stateRoot}
	locations.Index, locations.Profiles, locations.Projection = c.PersistencePaths()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if strings.TrimSpace(c.rootPath) != "" {
		locations.RootAgents = config.RootAgentsDir(c.rootPath)
	}
	return locations
}

// PersistencePaths reports the system store's paths, which reset still
// treats as the agent store.
func (c *CompositeStore) PersistencePaths() (index, profiles, projection string) {
	if paths, ok := c.system.(interface {
		PersistencePaths() (string, string, string)
	}); ok {
		return paths.PersistencePaths()
	}
	return "", "", ""
}
