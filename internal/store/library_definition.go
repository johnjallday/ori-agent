package store

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Library record states reported by LibraryDefinition.
const (
	LibraryPresent    = "present"
	LibraryAbsent     = "absent"
	LibraryUnreadable = "unreadable"
)

// Library record owners reported by LibraryDefinition.
const (
	LibraryOwnerLibrary = "library"
	LibraryOwnerSystem  = "system"
)

// LibraryRecord is an inspection read of the user's own (library) definition
// of an agent: the system store's built-in assistant or one of the user's
// agents. Definition is a detached copy, so inspecting it can never race with
// or reach the live record an editor adopts its changes into.
type LibraryRecord struct {
	Definition *agent.Agent
	State      string
	Owner      string
}

// LibraryDefinition reads name's library definition for inspection only.
//
// Unlike GetAgent on a CompositeStore, it never answers with a workspace's own
// copy: an agent only a workspace holds has no library definition, and showing
// the lowest-ID workspace's copy in its place would present one workspace's
// settings as the agent's profile. The name is matched exactly.
//
// It writes nothing, starts nothing and never reloads a definition.
func LibraryDefinition(st Store, name string) LibraryRecord {
	if st == nil || name == "" {
		return LibraryRecord{State: LibraryAbsent}
	}
	switch s := st.(type) {
	case *CompositeStore:
		return s.libraryDefinition(name)
	case *fileStore:
		// AGENT_STORE_PATH keeps every agent here: a user agent may carry an
		// assistant-like name, so ask the record, not the name.
		owner := LibraryOwnerLibrary
		if isSystemRecord(s, name) {
			owner = LibraryOwnerSystem
		}
		return s.libraryRecord(name, owner)
	}
	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		return LibraryRecord{State: LibraryAbsent}
	}
	return LibraryRecord{Definition: inspectionClone(ag), State: LibraryPresent, Owner: ownerFor(name)}
}

func ownerFor(name string) string {
	if systemassistant.IsCanonicalName(name) {
		return LibraryOwnerSystem
	}
	return LibraryOwnerLibrary
}

func (c *CompositeStore) libraryDefinition(name string) LibraryRecord {
	owner := c.owner(name)
	if owner == nil {
		if c.unreadableError(name) != nil {
			return LibraryRecord{State: LibraryUnreadable, Owner: LibraryOwnerLibrary}
		}
		return LibraryRecord{State: LibraryAbsent}
	}
	label := LibraryOwnerLibrary
	if owner == c.system && isSystemRecord(owner, name) {
		label = LibraryOwnerSystem
	}
	if fs, ok := owner.(*fileStore); ok {
		return fs.libraryRecord(name, label)
	}
	ag, ok := owner.GetAgent(name)
	if !ok || ag == nil {
		return LibraryRecord{State: LibraryAbsent}
	}
	return LibraryRecord{Definition: inspectionClone(ag), State: LibraryPresent, Owner: label}
}

// inspectionClone is cloneAgentDefinition plus a private Default Toolbox:
// that record is updated in place by its editor, so a reader must not share
// the pointer with the live agent.
func inspectionClone(src *agent.Agent) *agent.Agent {
	out := cloneAgentDefinition(src)
	if out != nil && src.DefaultToolbox != nil {
		toolbox := *src.DefaultToolbox
		toolbox.Skills = append([]types.DefaultToolboxSkillRef(nil), src.DefaultToolbox.Skills...)
		out.DefaultToolbox = &toolbox
	}
	return out
}

// libraryRecord clones name's definition under the store lock, the same lock
// MutateAgentDefinition holds while it adopts an edit into the live record.
func (s *fileStore) libraryRecord(name, owner string) LibraryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreadableErrorUnlocked(name) != nil {
		return LibraryRecord{State: LibraryUnreadable, Owner: owner}
	}
	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return LibraryRecord{State: LibraryAbsent}
	}
	return LibraryRecord{Definition: inspectionClone(ag), State: LibraryPresent, Owner: owner}
}
