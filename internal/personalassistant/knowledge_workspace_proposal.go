package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceAgentSourceKind is the reviewed-knowledge source for a fact about
// the user that an agent in any workspace proposed into global memory with
// the memory_propose_global tool.
const WorkspaceAgentSourceKind = "workspace_agent"

const (
	workspaceProposalPendingLimit = 10
	workspaceProposalDailyLimit   = 10
)

// GlobalMemoryUnavailableMessage is the one explanation every global-memory
// control shows when there is no hired assistant with a Personal HQ.
const GlobalMemoryUnavailableMessage = "Global memory needs a personal assistant with a Personal HQ. Set one up from Home."

// ErrGlobalMemoryUnavailable means the user has no hired assistant with a
// usable Personal HQ, so there is no global memory to propose into.
var ErrGlobalMemoryUnavailable = errors.New("personal assistant: global memory is unavailable")

// WorkspaceFactRequest is one memory_propose_global call. Only Category and
// Text come from the model. The host fills the workspace, agent and task from
// the tool provider's verified execution context; a model never names them.
type WorkspaceFactRequest struct {
	WorkspaceID   string
	WorkspaceName string
	Agent         string
	TaskID        string // empty for a chat turn
	Category      string
	Text          string
}

// WorkspaceFactResult says what a proposal did. Refusals are errors:
// ErrKnowledgeSuppressed, ErrKnowledgeQuota, ErrKnowledgePaused,
// ErrGlobalMemoryUnavailable and ErrValidation.
type WorkspaceFactResult string

const (
	WorkspaceFactProposed       WorkspaceFactResult = "proposed"
	WorkspaceFactAlreadyPending WorkspaceFactResult = "already_pending"
	WorkspaceFactAlreadyKnown   WorkspaceFactResult = "already_known"
)

// ProposeWorkspaceFact is the only constructor of a workspace_agent proposal.
// It admits an inert review candidate into the Personal HQ sidecar and never
// writes MEMORY.md; the user approves it on the Profile page.
func (s *KnowledgeLearningService) ProposeWorkspaceFact(ctx context.Context, userID string, req WorkspaceFactRequest) (WorkspaceFactResult, KnowledgeItem, error) {
	if s == nil || s.store == nil || s.now == nil {
		return "", KnowledgeItem{}, ErrGlobalMemoryUnavailable
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return "", KnowledgeItem{}, fmt.Errorf("%w: workspace is required", ErrValidation)
	}
	text, err := workspace.ValidateMemoryText(req.Text)
	if err != nil {
		return "", KnowledgeItem{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if !validWorkspaceFactCategory(req.Category) {
		return "", KnowledgeItem{}, fmt.Errorf("%w: unsupported category", ErrValidation)
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return "", KnowledgeItem{}, globalMemoryResolveError(err)
	}
	if binding.Paused {
		return "", KnowledgeItem{}, ErrKnowledgePaused
	}
	// A line already in the HQ file is global memory, whether or not it has a
	// reviewed record (the assistant may have written it inside HQ).
	if s.memory != nil {
		snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
		if err != nil {
			return "", KnowledgeItem{}, err
		}
		key := knowledgeTextKey(text)
		for _, entry := range snapshot.Entries {
			if knowledgeTextKey(entry.Entry.Text) == key {
				return WorkspaceFactAlreadyKnown, KnowledgeItem{}, nil
			}
		}
	}
	item, created, err := s.Propose(ctx, userID, KnowledgeProposal{
		SourceKind: WorkspaceAgentSourceKind,
		ScopeID:    workspaceID,
		SubjectID:  "user",
		Predicate:  "states",
		Value:      knowledgeTextKey(text),
		Category:   req.Category,
		Text:       text,
		Evidence: []KnowledgeEvidence{{
			SourceKind: WorkspaceAgentSourceKind,
			SourceID:   workspaceID,
			ObservedAt: s.now().UTC(),
			Summary:    workspaceOriginSummary(req.WorkspaceName, req.Agent, req.TaskID),
		}},
	})
	if err != nil {
		return "", KnowledgeItem{}, err
	}
	switch {
	case created:
		return WorkspaceFactProposed, item, nil
	case item.State == KnowledgeCandidate:
		return WorkspaceFactAlreadyPending, item, nil
	default:
		return WorkspaceFactAlreadyKnown, item, nil
	}
}

func validWorkspaceFactCategory(category string) bool {
	switch category {
	case "how_you_work", "people", "projects", "routines", "sources":
		return true
	}
	return false
}

// WorkspaceProposalOrigin attributes a workspace_agent proposal. The sidecar
// keeps it in the evidence (workspace ID plus summary) rather than in a new
// field, because the sidecar is decoded strictly and an older Ori must still
// read it.
type WorkspaceProposalOrigin struct {
	WorkspaceID   string
	WorkspaceName string // as named when the agent proposed; may be empty
	Agent         string
	TaskID        string // empty for a chat turn
	ObservedAt    time.Time
}

const (
	workspaceOriginSeparator = " · "
	workspaceOriginMaxLabel  = 120
	workspaceOriginNoAgent   = "an agent"
)

// WorkspaceProposalOriginOf reads a workspace_agent item's attribution back
// from its evidence; ok is false for any other source.
func WorkspaceProposalOriginOf(evidence []KnowledgeEvidence) (WorkspaceProposalOrigin, bool) {
	for _, entry := range evidence {
		if entry.SourceKind != WorkspaceAgentSourceKind || entry.SourceID == "" {
			continue
		}
		origin := WorkspaceProposalOrigin{WorkspaceID: entry.SourceID, ObservedAt: entry.ObservedAt}
		// Split from the right: the run and agent labels never contain the
		// separator, so a workspace name that does stays whole.
		parts := strings.Split(entry.Summary, workspaceOriginSeparator)
		if len(parts) < 2 {
			return origin, true
		}
		run := parts[len(parts)-1]
		switch {
		case run == "chat":
		case strings.HasPrefix(run, "task "):
			origin.TaskID = strings.TrimPrefix(run, "task ")
		default:
			return origin, true
		}
		origin.Agent = parts[len(parts)-2]
		origin.WorkspaceName = strings.Join(parts[:len(parts)-2], workspaceOriginSeparator)
		return origin, true
	}
	return WorkspaceProposalOrigin{}, false
}

// workspaceOriginSummary is "<workspace> · <agent> · task <id>" for a task run
// and "<workspace> · <agent> · chat" for a chat turn (FR 7). A label that is
// not a clean short line is left out rather than stored.
func workspaceOriginSummary(workspaceName, agent, taskID string) string {
	run := "chat"
	if label := workspaceOriginLabel(taskID); label != "" {
		run = "task " + label
	}
	agent = workspaceOriginLabel(agent)
	if agent == "" {
		agent = workspaceOriginNoAgent // keeps the right-split parse unambiguous
	}
	parts := []string{agent, run}
	if name := workspaceOriginLabel(workspaceName); name != "" {
		parts = append([]string{name}, parts...)
	}
	summary, err := workspace.ValidateMemoryText(strings.Join(parts, workspaceOriginSeparator))
	if err != nil {
		return ""
	}
	return summary
}

func workspaceOriginLabel(raw string) string {
	label, err := workspace.ValidateMemoryText(strings.ReplaceAll(raw, "·", "-"))
	if err != nil || len(label) > workspaceOriginMaxLabel {
		return ""
	}
	return label
}
