package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
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
			handled := false
			if write := p.hqVisibility.MemoryWrite; write != nil {
				handled, err = write(ctx, p.workspaceID, entry)
				if err != nil {
					return "", err
				}
			}
			if !handled {
				if p.hqVisibility.MemoryWriteGuard != nil {
					if err := p.hqVisibility.MemoryWriteGuard(ctx, p.workspaceID, text); err != nil {
						return "", err
					}
				}
				if err := store.Append(p.workspaceID, entry); err != nil {
					return "", fmt.Errorf("failed to save memory entry: %w", err)
				}
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

// --- memory_recall (personal assistant only; read-only) ---

// SetExternalMemoryRecall enables memory_recall. Only the chat handler calls
// it, for the personal assistant's own turns.
func (p *WorkspaceToolProvider) SetExternalMemoryRecall(reader ExternalMemoryReader) {
	if p != nil {
		p.externalRecall = reader
	}
}

func (p *WorkspaceToolProvider) memoryRecallTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        workspace.MemoryRecallToolName,
			Description: externalmemory.RecallToolDescription,
			Parameters:  externalmemory.RecallToolParameters(),
		},
		call: func(_ context.Context, args string) (string, error) {
			q, err := externalmemory.ParseRecallArgs(args)
			if err != nil {
				return "", err
			}
			return p.externalRecall.Recall(q), nil
		},
	}
}

// --- memory_propose_global (review-only; allowed under every autonomy policy) ---

func (p *WorkspaceToolProvider) memoryProposeGlobalTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        workspace.MemoryProposeGlobalToolName,
			Description: "Propose one durable fact about the USER (not about this workspace) for their global memory, which their personal assistant reads on every page. Nothing is saved until the user approves it on their Profile page. Use it for stable facts about the person: how they like to work, people they work with, their projects across workspaces, routines, sources they rely on. For facts specific to this workspace, use memory_write instead. Never propose guesses or secrets. One line, max 500 characters.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The fact about the user, as one self-contained line.",
					},
					"category": map[string]any{
						"type":        "string",
						"enum":        globalFactCategories,
						"description": "how_you_work (working style, preferences, tools), people (who they work with), projects (their ongoing work), routines (schedules, habits), sources (references and services they rely on).",
					},
				},
				"required": []string{"text", "category"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			// Only text and category come from the model. The workspace, agent
			// and task are this provider's verified execution context.
			var req struct {
				Text     string `json:"text"`
				Category string `json:"category"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			text, err := workspace.ValidateMemoryText(req.Text)
			if err != nil {
				return "", err
			}
			category := strings.TrimSpace(req.Category)
			if !isGlobalFactCategory(category) {
				return "", fmt.Errorf("category must be one of %s", strings.Join(globalFactCategories, ", "))
			}
			propose := p.hqVisibility.ProposeGlobal
			if propose == nil {
				return globalFactReply("unavailable", personalassistant.GlobalMemoryUnavailableMessage)
			}
			taskID := p.taskID
			if taskID == "" {
				taskID = p.runTaskID
			}
			result, err := propose(ctx, personalassistant.WorkspaceFactRequest{
				WorkspaceID: p.workspaceID,
				Agent:       p.executingAgent,
				TaskID:      taskID,
				Category:    category,
				Text:        text,
			})
			switch {
			case errors.Is(err, personalassistant.ErrGlobalMemoryUnavailable):
				return globalFactReply("unavailable", personalassistant.GlobalMemoryUnavailableMessage+" Do not retry; use memory_write if the fact matters to this workspace.")
			case errors.Is(err, personalassistant.ErrKnowledgeSuppressed):
				return globalFactReply("refused", "The user rejected or forgot this fact. Do not propose it again.")
			case errors.Is(err, personalassistant.ErrKnowledgeQuota):
				return globalFactReply("refused", "The user's global-memory review queue is full for now. Do not retry; if the fact matters to this workspace, save it with memory_write instead.")
			case errors.Is(err, personalassistant.ErrKnowledgePaused):
				return globalFactReply("refused", "The user paused their assistant's suggestions, so global memory is not taking proposals. Do not retry; use memory_write if the fact matters to this workspace.")
			case err != nil:
				return "", err
			}
			switch result {
			case personalassistant.WorkspaceFactAlreadyKnown:
				return globalFactReply("already_known", "Already known globally: the user's global memory holds this fact. Do not propose it again.")
			case personalassistant.WorkspaceFactAlreadyPending:
				return globalFactReply("already_pending", "This fact is already waiting for the user's review. Do not propose it again.")
			}
			return globalFactReply("proposed", "Sent for the user's review on their Profile page. It is not active until they approve it. Keep using memory_write for facts specific to this workspace.")
		},
	}
}

var globalFactCategories = []string{"how_you_work", "people", "projects", "routines", "sources"}

func isGlobalFactCategory(category string) bool {
	for _, allowed := range globalFactCategories {
		if category == allowed {
			return true
		}
	}
	return false
}

func globalFactReply(status, message string) (string, error) {
	return marshalToolResponse(map[string]any{"status": status, "message": message})
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
			var profile *userprofile.UserProfile
			var err error
			if guard := p.hqVisibility.ProfileWriteGuard; guard != nil {
				var version time.Time
				version, err = guard(ctx, userID, fields)
				if err != nil {
					return "", err
				}
				if !version.IsZero() {
					store, ok := p.userStore.(interface {
						SetFieldsIfVersion(context.Context, string, map[string]any, time.Time) (*userprofile.UserProfile, error)
					})
					if !ok {
						return "", fmt.Errorf("versioned profile storage is unavailable")
					}
					profile, err = store.SetFieldsIfVersion(ctx, userID, fields, version)
				} else {
					profile, err = p.userStore.SetFields(ctx, userID, fields)
				}
			} else {
				profile, err = p.userStore.SetFields(ctx, userID, fields)
			}
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
