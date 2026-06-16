package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryProvenance attributes a memory mutation to its execution context:
// task-scoped executions record the task ID, everything else the agent name.
func (p *WorkspaceToolProvider) memoryProvenance() string {
	agentName := strings.TrimSpace(p.executingAgent)
	if taskID := strings.TrimSpace(p.taskID); taskID != "" {
		if agentName != "" {
			return fmt.Sprintf("task:%s (%s)", taskID, agentName)
		}
		return "task:" + taskID
	}
	if agentName != "" {
		return "agent:" + agentName
	}
	return "agent:unknown"
}

func (p *WorkspaceToolProvider) workspaceMemoryStore() (*workspace.MemoryStore, error) {
	if p.fileStore == nil {
		return nil, fmt.Errorf("workspace folder storage is unavailable, so workspace memory cannot be accessed")
	}
	return workspace.NewMemoryStore(p.fileStore), nil
}

// --- memory_write (workspace-internal write; allowed under every autonomy policy) ---

func (p *WorkspaceToolProvider) memoryWriteTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        workspace.MemoryWriteToolName,
			Description: "Save one short operational fact to this workspace's persistent memory (MEMORY.md), so every future run and chat in this workspace starts knowing it. Use it for stable facts, learned constraints, decisions with rationale, dead ends (failed approaches), watch-state/baselines, and open threads. Do NOT use it for deliverables (write a note), work items (create a task), anything cheaply re-derivable from workspace files, or secrets (Vault only — secret-looking text is refused). One line, max 500 characters.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The fact to remember, as one self-contained line.",
					},
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"fact", "feedback", "decision", "dead-end", "watch", "thread"},
						"description": "Kind of knowledge: fact (stable workspace fact), feedback (user guidance), decision (choice + rationale), dead-end (failed approach), watch (cursor/baseline state), thread (open investigation). Defaults to fact.",
					},
				},
				"required": []string{"text"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Text string `json:"text"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			text, err := workspace.ValidateMemoryText(req.Text)
			if err != nil {
				return "", err
			}
			store, err := p.workspaceMemoryStore()
			if err != nil {
				return "", err
			}
			entry := workspace.MemoryEntry{
				Type:       workspace.NormalizeMemoryEntryType(req.Type),
				Date:       time.Now().Format("2006-01-02"),
				Provenance: p.memoryProvenance(),
				Text:       text,
			}
			if err := store.Append(p.workspaceID, entry); err != nil {
				return "", fmt.Errorf("failed to save memory entry: %w", err)
			}
			return marshalToolResponse(map[string]any{
				"entry":   entry,
				"message": "Saved to workspace memory. Future runs and chats in this workspace will start with this in context.",
			})
		},
	}
}

// --- memory_forget (workspace-internal write; allowed under every autonomy policy) ---

func (p *WorkspaceToolProvider) memoryForgetTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        workspace.MemoryForgetToolName,
			Description: "Remove exactly one entry from this workspace's persistent memory (MEMORY.md), identified by its exact text or a unique substring. Use it when a remembered fact is wrong or obsolete; to revise an entry, forget it and then memory_write the corrected version. If several entries match you'll get the candidate list back — retry with a more specific match.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"match": map[string]any{
						"type":        "string",
						"description": "Exact entry text, or a substring that matches exactly one entry.",
					},
				},
				"required": []string{"match"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Match string `json:"match"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			store, err := p.workspaceMemoryStore()
			if err != nil {
				return "", err
			}
			removed, err := store.Forget(p.workspaceID, req.Match)
			if err != nil {
				return "", err
			}
			return marshalToolResponse(map[string]any{
				"removed": removed,
				"message": fmt.Sprintf("Removed from workspace memory: %q", removed.Text),
			})
		},
	}
}

// --- profile_set (user-scoped behavioral profile write) ---

func (p *WorkspaceToolProvider) profileSetTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "profile_set",
			Description: "Update durable About You profile fields for the current user. Use only for explicitly stated, stable assistant-behavior facts. Allowed fields are about and preferences.response_style, preferences.units, preferences.language. Do not store secrets; credentials belong in the Vault. Identity fields such as display_name, email, timezone, and locale are user-set and this tool will refuse them.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{
						"type":        "string",
						"description": "Single field to update, such as about or preferences.response_style.",
					},
					"value": map[string]any{
						"type":        "string",
						"description": "Value for field.",
					},
					"fields": map[string]any{
						"type":        "object",
						"description": "Optional map of field names to values for updating multiple fields at once.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if p.userStore == nil {
				return "", fmt.Errorf("user profile storage is unavailable")
			}
			var req struct {
				Field  string         `json:"field"`
				Value  any            `json:"value"`
				Fields map[string]any `json:"fields"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			fields := map[string]any{}
			for key, value := range req.Fields {
				fields[key] = value
			}
			if strings.TrimSpace(req.Field) != "" {
				fields[strings.TrimSpace(req.Field)] = req.Value
			}
			if len(fields) == 0 {
				return "", fmt.Errorf("field or fields is required")
			}
			userID := userprofile.LocalUserID
			if p.userProvider != nil {
				resolved, err := p.userProvider.CurrentUserID(ctx)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(resolved) != "" {
					userID = strings.TrimSpace(resolved)
				}
			}
			profile, err := p.userStore.SetFields(ctx, userID, fields)
			if err != nil {
				return "", err
			}
			return marshalToolResponse(map[string]any{
				"profile": profile,
				"message": "Updated About You. Future workspace chats and task runs will include this profile context.",
			})
		},
	}
}
