package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ErrAgentNotFound reports a definition mutation naming an agent the store
// does not hold.
var ErrAgentNotFound = errors.New("agent not found")

// DefinitionMutator is an optional capability: a store that can change an
// agent's editable definition as one protected operation.
//
// The ordinary GetAgent/SetAgent pair hands every caller the store's live
// record, so an editor that assigns a field and then fails a later check leaves
// that field changed in memory, and two editors that each checked a version
// before writing can both write. MutateAgentDefinition closes both gaps: the
// callback edits a private copy, under the store's lock, after the store has
// confirmed the file on disk is the one it last read or wrote. Nothing the
// callback does is visible to anyone until the definition file has been
// replaced; a callback error or a failed write changes nothing.
type DefinitionMutator interface {
	// MutateAgentDefinition runs mutate on a private copy of name's current
	// definition and, when it returns nil, durably writes and adopts the result.
	//
	// It returns a copy of the definition as committed. Errors:
	//   - whatever mutate returned (nothing changed);
	//   - ErrAgentChangedOnDisk when the file was edited outside Ori (the store
	//     reloads it first, so a retry starts from what is really there);
	//   - ErrAgentNotFound, an *UnreadableAgentError, ErrWorkspaceOwnedAgent or
	//     ErrAgentRootUnavailable for an agent this store cannot edit;
	//   - a write error, in which case the definition file and the live record
	//     both still hold the previous definition.
	MutateAgentDefinition(name string, mutate func(draft *agent.Agent) error) (*agent.Agent, error)
}

// MutateDefinition changes name's definition through st's DefinitionMutator
// when it has one. A store without the capability (an in-memory test double)
// gets the same contract from a process-wide lock and a private copy: mutate
// never sees the live record, and SetAgent receives a fresh one.
func MutateDefinition(st Store, name string, mutate func(draft *agent.Agent) error) (*agent.Agent, error) {
	if mutator, ok := st.(DefinitionMutator); ok {
		return mutator.MutateAgentDefinition(name, mutate)
	}
	fallbackMutationMu.Lock()
	defer fallbackMutationMu.Unlock()
	live, ok := st.GetAgent(name)
	if !ok || live == nil {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	draft := cloneAgentDefinition(live)
	if err := mutate(draft); err != nil {
		return nil, err
	}
	next := cloneAgentDefinition(live)
	adoptAgentDefinition(next, draft)
	if err := st.SetAgent(name, next); err != nil {
		return nil, err
	}
	return cloneAgentDefinition(next), nil
}

var fallbackMutationMu sync.Mutex

// MutateAgentDefinition implements DefinitionMutator for the file store.
func (s *fileStore) MutateAgentDefinition(name string, mutate func(draft *agent.Agent) error) (*agent.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unreadableErrorUnlocked(name); err != nil {
		return nil, err
	}
	// The draft must start from what is on disk. A file changed outside Ori is
	// reloaded and the request refused: the caller's expectations were formed
	// against the old content, and accepting would discard the outside edit.
	changed, err := s.definitionChangedOnDiskUnlocked(name)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := s.reloadAgentUnlocked(name); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrAgentChangedOnDisk, name)
	}
	live, ok := s.agents[name]
	if !ok || live == nil {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	if live.WorkspaceLocalConfigID != "" {
		return nil, ErrWorkspaceOwnedAgent
	}

	draft := cloneAgentDefinition(live)
	if err := mutate(draft); err != nil {
		return nil, err
	}
	data, err := encodeDefinition(draft)
	if err != nil {
		return nil, err
	}

	if known, ok := s.knownDefinitions[name]; !ok || string(known) != string(data) {
		path := s.definitionPath(name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
		// The atomic replace is the commit point: before it, nothing changed;
		// after it, the new definition is what every reader gets.
		if err := writeFileIfChanged(path, data); err != nil {
			return nil, err
		}
		s.rememberDefinitionUnlocked(name, data)
		s.rememberKnownDefinitionUnlocked(name, data)
		adoptAgentDefinition(live, draft)
		// "Last updated" is runtime state. It follows a real definition change,
		// and failing to record it does not undo the change that just landed.
		if live.Statistics != nil {
			live.Statistics.UpdatedAt = time.Now()
		}
		if err := s.persistStateUnlocked(name); err != nil {
			logger.Warn("Agent definition saved; its runtime state could not be updated", logger.Fields{
				"agent": name, "error": err.Error(),
			})
		}
		if err := s.writeIndexUnlocked(); err != nil {
			logger.Warn("Agent definition saved; the compatibility index could not be updated", logger.Fields{
				"agent": name, "error": err.Error(),
			})
		}
	} else {
		adoptAgentDefinition(live, draft)
	}
	return cloneAgentDefinition(live), nil
}

// MutateAgentDefinition routes the mutation to the store that holds name. An
// agent only a workspace holds is that workspace's copy and is never edited
// here.
func (c *CompositeStore) MutateAgentDefinition(name string, mutate func(draft *agent.Agent) error) (*agent.Agent, error) {
	owner := c.owner(name)
	if owner == nil {
		if err := c.unreadableError(name); err != nil {
			return nil, err
		}
		if err := c.workspaceOwnedError(name); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	if owner != c.system {
		if _, err := c.rootForWrite(); err != nil {
			return nil, err
		}
	}
	return MutateDefinition(owner, name, mutate)
}

// cloneAgentDefinition copies everything an editor may change, so writing to
// the copy can never reach the original. Runtime state (status, statistics,
// evolution) and in-memory conversation fields are shared, not copied: a
// definition edit must not touch them.
func cloneAgentDefinition(src *agent.Agent) *agent.Agent {
	if src == nil {
		return nil
	}
	c := *src
	if src.Capabilities != nil {
		c.Capabilities = append([]string{}, src.Capabilities...)
	}
	c.Settings = cloneSettings(src.Settings)
	c.Metadata = cloneMetadata(src.Metadata)
	c.Appearance = src.Appearance.Clone()
	if src.AssistantSetup != nil {
		setup := *src.AssistantSetup
		c.AssistantSetup = &setup
	}
	return &c
}

// adoptAgentDefinition moves an edited definition onto the live record in
// place. The record is updated rather than replaced because other code holds
// the same pointer (a chat turn counting statistics, say) and writes it back;
// a replaced record would let that write restore the old definition.
func adoptAgentDefinition(live, draft *agent.Agent) {
	live.Role = draft.Role
	live.Capabilities = draft.Capabilities
	live.Settings = draft.Settings
	live.Metadata = draft.Metadata
	live.Appearance = draft.Appearance
	live.AssistantSetup = draft.AssistantSetup
}

func cloneBool(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneSettings(s types.Settings) types.Settings {
	s.AllowWebSearch = cloneBool(s.AllowWebSearch)
	s.AllowNativeMCPTools = cloneBool(s.AllowNativeMCPTools)
	s.FallbackAllowCloud = cloneBool(s.FallbackAllowCloud)
	return s
}

func cloneMetadata(m *types.AgentMetadata) *types.AgentMetadata {
	if m == nil {
		return nil
	}
	c := *m
	if m.Tags != nil {
		c.Tags = append([]string{}, m.Tags...)
	}
	c.ReviewEnabled = cloneBool(m.ReviewEnabled)
	c.ExpertMode = cloneBool(m.ExpertMode)
	if m.RoutingProfile != nil {
		profile := *m.RoutingProfile
		profile.MatchPhrases = cloneStrings(m.RoutingProfile.MatchPhrases)
		profile.ExampleRequests = cloneStrings(m.RoutingProfile.ExampleRequests)
		profile.Domains = cloneStrings(m.RoutingProfile.Domains)
		profile.ExternalSystems = cloneStrings(m.RoutingProfile.ExternalSystems)
		c.RoutingProfile = &profile
	}
	return &c
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}
