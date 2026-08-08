package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/githubhttp"
)

// githubProposer adapts the agent-facing github_propose_change tool to the
// confirm-gated broker.
//
// It lives in server wiring rather than in chathttp so the chat package does
// not depend on the GitHub connection surface for one call, and so the
// translation from "what a model typed" to "what the broker will accept"
// happens in exactly one place.
type githubProposer struct {
	broker *githubhttp.Broker
	repos  githubhttp.RepoResolver
}

func newGitHubProposer(broker *githubhttp.Broker, repos githubhttp.RepoResolver) *githubProposer {
	return &githubProposer{broker: broker, repos: repos}
}

// githubWorkspaceStore resolves the workspace store for GitHub repo bindings,
// or nil when it does not exist yet or cannot serve folder reads.
//
// A method rather than a captured value because handlers are wired in phase 17
// and the workspace store is assigned in phase 18: anything that reads
// b.workspaceStore at wiring time gets nil.
func (b *ServerBuilder) githubWorkspaceStore() githubhttp.WorkspaceStore {
	if b == nil || b.workspaceStore == nil {
		return nil
	}
	folders, ok := b.workspaceStore.(githubhttp.WorkspaceStore)
	if !ok {
		return nil
	}
	return folders
}

// toolArgs is the shape the model supplies. The repository is deliberately not
// among the fields: it comes from the workspace's binding, so a model cannot
// name one, mistype one, or be talked into one by issue text it read.
type toolArgs struct {
	Kind         string   `json:"kind"`
	Issue        int      `json:"issue"`
	Body         string   `json:"body"`
	AddLabels    []string `json:"add_labels"`
	RemoveLabels []string `json:"remove_labels"`
	State        string   `json:"state"`
	StateReason  string   `json:"state_reason"`
	Rationale    string   `json:"rationale"`
}

// ProposeChange records an inert proposal and returns what the agent should
// tell the user.
func (p *githubProposer) ProposeChange(workspaceID string, raw json.RawMessage) (string, error) {
	if p == nil || p.broker == nil || p.repos == nil {
		return "", fmt.Errorf("this workspace cannot propose GitHub changes")
	}

	var args toolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("those arguments could not be read as a proposed change")
	}
	if strings.TrimSpace(args.Rationale) == "" {
		return "", fmt.Errorf("say why you are proposing this change; the user sees the reason alongside it")
	}

	repo, ok := p.repos.BoundRepo(workspaceID)
	if !ok {
		return "", fmt.Errorf("this workspace has no GitHub repository bound yet")
	}

	change := githubhttp.Change{
		Kind:         githubhttp.ProposalKind(strings.TrimSpace(args.Kind)),
		Repo:         repo,
		Issue:        args.Issue,
		Body:         args.Body,
		AddLabels:    trimAll(args.AddLabels),
		RemoveLabels: trimAll(args.RemoveLabels),
		State:        strings.TrimSpace(args.State),
		StateReason:  strings.TrimSpace(args.StateReason),
		Rationale:    strings.TrimSpace(args.Rationale),
	}

	proposal, err := p.broker.Propose(workspaceID, change)
	if err != nil {
		return "", err
	}

	// The return value is what the model relays, so it states the pending
	// status explicitly. A model told only "proposal created" tends to report
	// the change as done.
	return fmt.Sprintf(
		"Proposed (NOT applied): %s on %s. It is waiting for the user's approval and will not reach GitHub until they approve it. Tell them what you proposed and that it needs their approval.",
		proposal.Summary, repo), nil
}

func trimAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
