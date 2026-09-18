package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ErrWorkspaceOwnedAgent refuses a write, through the global store, to an
// agent that exists only as a workspace's own copy. That copy is edited
// through the workspace, or added to the user's agents first.
var ErrWorkspaceOwnedAgent = errors.New("agent belongs to a workspace")

// ErrAgentAlreadyExists refuses to add a workspace's agent under a name the
// user's agents already use.
var ErrAgentAlreadyExists = errors.New("an agent with that name already exists")

// ErrWorkspaceAgentNotFound reports that no trusted workspace holds the agent.
var ErrWorkspaceAgentNotFound = errors.New("workspace agent not found")

// WorkspaceOwnedAgentError is ErrWorkspaceOwnedAgent with the owning workspace.
type WorkspaceOwnedAgentError struct {
	Agent         string
	WorkspaceID   string
	WorkspaceName string
}

func (e *WorkspaceOwnedAgentError) Error() string {
	return fmt.Sprintf("agent %q belongs to the workspace %q", e.Agent, e.WorkspaceName)
}

func (e *WorkspaceOwnedAgentError) Unwrap() error { return ErrWorkspaceOwnedAgent }

// WorkspaceAgentEntry is one agent as a workspace holds it: the workspace's own
// copy, read where it is.
type WorkspaceAgentEntry struct {
	WorkspaceID   string
	WorkspaceName string
	AgentName     string
	Agent         *agent.Agent
}

// WorkspaceAgentSource lists the agent copies held by the workspaces this
// machine trusts (on the allowlist, not trashed). internal/workspace
// implements it; this package cannot import that one.
type WorkspaceAgentSource interface {
	WorkspaceAgents() []WorkspaceAgentEntry
}

// WorkspaceRef names a workspace for the roster.
type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Roster sources reported by AgentOrigin.
const (
	SourceSystem    = "system"
	SourceRoster    = "roster"
	SourceWorkspace = "workspace"
)

// AgentOrigin says where a roster entry comes from.
type AgentOrigin struct {
	Source string `json:"source"`
	// WorkspaceID and WorkspaceName name the owning workspace of a
	// workspace entry.
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	// CustomisedIn lists the workspaces holding their own copy of a roster
	// or system agent.
	CustomisedIn []WorkspaceRef `json:"customised_in,omitempty"`
}

// workspaceViewTTL bounds how stale the cached workspace view can get between
// explicit invalidations. Import, rescan, a root switch, and "Add to my
// agents" invalidate at once; anything else is picked up within this window.
const workspaceViewTTL = 3 * time.Second

// workspaceView is the roster's read-only view of trusted workspaces' copies.
type workspaceView struct {
	builtAt time.Time
	// entries by lower-cased agent name; the first is the chosen one (lowest
	// workspace ID), the rest are the same name in other workspaces.
	byName map[string][]WorkspaceAgentEntry
}

type workspaceAgentsCache struct {
	mu        sync.Mutex
	source    WorkspaceAgentSource
	view      *workspaceView
	conflicts string // last logged duplicate names, so a warning is not repeated
}

// SetWorkspaceAgentSource connects the trusted workspaces' agent copies. It is
// injected after the workspace store exists; nil means none.
func (c *CompositeStore) SetWorkspaceAgentSource(source WorkspaceAgentSource) {
	c.workspaces.mu.Lock()
	c.workspaces.source = source
	c.workspaces.view = nil
	c.workspaces.mu.Unlock()
}

// InvalidateWorkspaceAgents drops the cached view, so the next read sees the
// workspaces as they are now.
func (c *CompositeStore) InvalidateWorkspaceAgents() {
	c.workspaces.mu.Lock()
	c.workspaces.view = nil
	c.workspaces.mu.Unlock()
}

// workspaceAgentView returns the cached view, rebuilding it when stale.
func (c *CompositeStore) workspaceAgentView() *workspaceView {
	c.workspaces.mu.Lock()
	defer c.workspaces.mu.Unlock()
	if c.workspaces.view != nil && time.Since(c.workspaces.view.builtAt) < workspaceViewTTL {
		return c.workspaces.view
	}
	view := &workspaceView{builtAt: time.Now(), byName: map[string][]WorkspaceAgentEntry{}}
	if c.workspaces.source != nil {
		entries := c.workspaces.source.WorkspaceAgents()
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].WorkspaceID != entries[j].WorkspaceID {
				return entries[i].WorkspaceID < entries[j].WorkspaceID
			}
			return entries[i].AgentName < entries[j].AgentName
		})
		for _, entry := range entries {
			if entry.Agent == nil || strings.TrimSpace(entry.AgentName) == "" {
				continue
			}
			key := strings.ToLower(entry.AgentName)
			view.byName[key] = append(view.byName[key], entry)
		}
	}
	c.workspaces.view = view
	c.logWorkspaceConflictsLocked(view)
	return view
}

// logWorkspaceConflictsLocked warns once about a workspace-only name held by
// two trusted workspaces: the lowest workspace ID wins so the result is stable.
func (c *CompositeStore) logWorkspaceConflictsLocked(view *workspaceView) {
	var conflicts []string
	for key, entries := range view.byName {
		if c.memberHasFold(key) {
			continue
		}
		owners := map[string]bool{}
		for _, entry := range entries {
			owners[entry.WorkspaceID] = true
		}
		if len(owners) > 1 {
			conflicts = append(conflicts, key)
		}
	}
	sort.Strings(conflicts)
	signature := strings.Join(conflicts, "\x00")
	if signature == c.workspaces.conflicts {
		return
	}
	c.workspaces.conflicts = signature
	for _, key := range conflicts {
		entries := view.byName[key]
		logger.Warn("Two trusted workspaces hold an agent with the same name; the roster uses the first", logger.Fields{
			"agent":  entries[0].AgentName,
			"used":   entries[0].WorkspaceName + " (" + entries[0].WorkspaceID + ")",
			"hidden": entries[1].WorkspaceName + " (" + entries[1].WorkspaceID + ")",
		})
	}
}

// memberHasFold reports whether the system or root store holds name, compared
// case-insensitively.
func (c *CompositeStore) memberHasFold(name string) bool {
	for _, existing := range c.memberNames() {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}

func (c *CompositeStore) memberNames() []string {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	names := c.system.ListAgents()
	if root != nil {
		names = append(names, root.ListAgents()...)
	}
	return names
}

// workspaceOnly returns the workspace copy that stands in the roster for name:
// one no system or root agent shares, case-insensitively.
func (c *CompositeStore) workspaceOnly(name string) (WorkspaceAgentEntry, bool) {
	entries := c.workspaceAgentView().byName[strings.ToLower(strings.TrimSpace(name))]
	if len(entries) == 0 || c.memberHasFold(name) {
		return WorkspaceAgentEntry{}, false
	}
	return entries[0], true
}

// workspaceOwnedError is the refusal for a write to a workspace-only agent.
func (c *CompositeStore) workspaceOwnedError(name string) error {
	if entry, ok := c.workspaceOnly(name); ok {
		return &WorkspaceOwnedAgentError{Agent: entry.AgentName, WorkspaceID: entry.WorkspaceID, WorkspaceName: entry.WorkspaceName}
	}
	return nil
}

// AgentOrigin reports where a roster entry comes from, and false for a name
// that is not in the roster.
func (c *CompositeStore) AgentOrigin(name string) (AgentOrigin, bool) {
	if owner := c.owner(name); owner != nil {
		origin := AgentOrigin{Source: SourceRoster}
		if owner == c.system && isSystemRecord(owner, name) {
			origin.Source = SourceSystem
		}
		seen := map[string]bool{}
		for _, entry := range c.workspaceAgentView().byName[strings.ToLower(name)] {
			if seen[entry.WorkspaceID] {
				continue
			}
			seen[entry.WorkspaceID] = true
			origin.CustomisedIn = append(origin.CustomisedIn, WorkspaceRef{ID: entry.WorkspaceID, Name: entry.WorkspaceName})
		}
		return origin, true
	}
	if entry, ok := c.workspaceOnly(name); ok {
		return AgentOrigin{Source: SourceWorkspace, WorkspaceID: entry.WorkspaceID, WorkspaceName: entry.WorkspaceName}, true
	}
	return AgentOrigin{}, false
}

// isSystemRecord reports whether the record under name is the built-in
// assistant rather than a user agent still in the data dir.
func isSystemRecord(st Store, name string) bool {
	if systemassistant.IsCanonicalName(name) {
		return true
	}
	ag, ok := st.GetAgent(name)
	return ok && isMarkedSystemAgent(ag)
}

// AddWorkspaceAgent copies a trusted workspace's agent into the user's agents
// ("Add to my agents"). It is the only way a workspace copy becomes one of the
// user's agents; nothing does it automatically. It returns the agent's name.
func (c *CompositeStore) AddWorkspaceAgent(workspaceID, name string) (string, error) {
	c.InvalidateWorkspaceAgents()
	var entry *WorkspaceAgentEntry
	for _, candidate := range c.workspaceAgentView().byName[strings.ToLower(strings.TrimSpace(name))] {
		if candidate.WorkspaceID == workspaceID {
			found := candidate
			entry = &found
			break
		}
	}
	if entry == nil {
		return "", ErrWorkspaceAgentNotFound
	}
	if c.memberHasFold(entry.AgentName) {
		return "", ErrAgentAlreadyExists
	}
	target, err := c.targetForNew(entry.AgentName)
	if err != nil {
		return "", err
	}
	if err := target.SetAgent(entry.AgentName, copyDefinition(entry.Agent)); err != nil {
		return "", err
	}
	c.InvalidateWorkspaceAgents()
	return entry.AgentName, nil
}

// copyDefinition returns a new agent with src's definition and fresh runtime
// state: statistics belong to the agent that earned them, not to its copy.
func copyDefinition(src *agent.Agent) *agent.Agent {
	dst := &agent.Agent{
		Role:         src.Role,
		Capabilities: append([]string{}, src.Capabilities...),
		Settings:     src.Settings,
		Status:       types.AgentStatusIdle,
	}
	if src.Status == types.AgentStatusDisabled {
		dst.Status = types.AgentStatusDisabled
	}
	if src.Metadata != nil {
		metadata := *src.Metadata
		metadata.Tags = append([]string{}, src.Metadata.Tags...)
		dst.Metadata = &metadata
	}
	if src.Appearance != nil {
		dst.Appearance = src.Appearance.Clone()
	}
	dst.InitializeStatistics()
	dst.InitializeEvolution()
	return dst
}
