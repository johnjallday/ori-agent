package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// GitHubProposer records a proposed change to a GitHub issue. It never writes
// to GitHub: a proposal is inert until the user confirms it in the UI.
//
// The interface lives here rather than importing internal/githubhttp, which
// would pull the whole connection surface into the chat package for one call.
type GitHubProposer interface {
	ProposeChange(workspaceID string, raw json.RawMessage) (summary string, err error)
}

// SetGitHubProposer enables the github_propose_change tool.
func (p *WorkspaceToolProvider) SetGitHubProposer(proposer GitHubProposer) {
	if p != nil {
		p.githubProposer = proposer
	}
}

// githubProposeChangeTool is how an agent asks for a change to a GitHub issue.
//
// It is the ONLY way an agent can express intent to write to GitHub. The
// mutating GitHub MCP tools are classified `external`, which every autonomy
// policy denies, so an agent cannot call them; this tool records a proposal
// the user then reviews and approves. The description says so plainly, because
// a model that understands the flow proposes better changes than one that
// keeps trying to write directly and being refused.
func (p *WorkspaceToolProvider) githubProposeChangeTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name: "github_propose_change",
			Description: "Propose a change to an issue in this workspace's GitHub repository: " +
				"a comment, a label change, or closing/reopening it. This does NOT change anything on " +
				"GitHub. It records a proposal that the user sees in full and must approve before it is " +
				"applied. Use it whenever you would otherwise want to write to GitHub. Always say what " +
				"you proposed and that it is waiting for their approval.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{
						"type":        "string",
						"enum":        []string{"comment", "labels", "state"},
						"description": "comment: post a comment. labels: add/remove labels. state: close or reopen.",
					},
					"issue": map[string]any{
						"type":        "integer",
						"description": "The issue number this change applies to.",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "For kind=comment: the exact comment text. The user sees this verbatim.",
					},
					"add_labels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "For kind=labels: label names to add. They must already exist on the repository.",
					},
					"remove_labels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "For kind=labels: label names to remove.",
					},
					"state": map[string]any{
						"type":        "string",
						"enum":        []string{"closed", "open"},
						"description": "For kind=state: the state to move the issue to.",
					},
					"state_reason": map[string]any{
						"type":        "string",
						"enum":        []string{"completed", "not_planned", "duplicate"},
						"description": "For kind=state when closing: why it is being closed.",
					},
					"rationale": map[string]any{
						"type": "string",
						"description": "One line on why you are proposing this. Shown to the user " +
							"alongside the change; never posted to GitHub.",
					},
				},
				"required": []string{"kind", "issue", "rationale"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if p.githubProposer == nil {
				return "", fmt.Errorf("this workspace cannot propose GitHub changes")
			}
			raw := json.RawMessage(strings.TrimSpace(args))
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			summary, err := p.githubProposer.ProposeChange(p.workspaceID, raw)
			if err != nil {
				return "", err
			}
			return summary, nil
		},
	}
}
