package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceToolProvider provides workspace-scoped tools that give the assistant
// read/write access to workspace data (notes, tasks, sessions, files, directories)
// and management operations (agents, MCPs, skills).
// These tools are only registered when the chat is on a workspace surface.
type WorkspaceToolProvider struct {
	sessionStore   session.HybridStore
	workspaceStore workspace.Store
	fileStore      *workspace.FileStore // Optional: for syncing notes to disk
	workspaceID    string
	userStore      userprofile.UserStore
	userProvider   userprofile.UserProvider

	// taskID, when set, marks this chat as scoped to a single task and
	// enables the current_task / task_runs tools.
	taskID string

	// executingAgent is the name of the agent driving this provider. It gates
	// coordinator-only tools (delegate_task) to the workspace coordinator.
	executingAgent string

	// Optional dependencies for management tools (Phase 2)
	agentStore    store.Store
	mcpRegistry   mcpServerLister
	skillsManager skillLister

	// Optional dependencies for project-template tools
	templatesRootResolver func() string
	projectEventBus       *workspace.EventBus
}

// mcpServerLister allows listing available MCP servers.
type mcpServerLister interface {
	ListServers() []mcp.ServerConfig
}

// skillLister allows listing available skills.
type skillLister interface {
	ListSkills(agentName string) ([]skills.Skill, error)
}

// NewWorkspaceToolProvider creates a provider scoped to a specific workspace.
func NewWorkspaceToolProvider(sessionStore session.HybridStore, workspaceStore workspace.Store, workspaceID string) *WorkspaceToolProvider {
	return &WorkspaceToolProvider{
		sessionStore:   sessionStore,
		workspaceStore: workspaceStore,
		workspaceID:    workspaceID,
	}
}

// SetFileStore sets the folder-based workspace store for syncing notes to disk.
func (p *WorkspaceToolProvider) SetFileStore(fs *workspace.FileStore) {
	p.fileStore = fs
}

func (p *WorkspaceToolProvider) SetUserProfileDeps(store userprofile.UserStore, provider userprofile.UserProvider) {
	p.userStore = store
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	p.userProvider = provider
}

// SetTaskID scopes the provider to a single task and enables the
// current_task / task_runs tools.
func (p *WorkspaceToolProvider) SetTaskID(taskID string) {
	p.taskID = strings.TrimSpace(taskID)
}

// SetExecutingAgent records which agent is driving this provider, used to gate
// the coordinator-only delegate_task tool.
func (p *WorkspaceToolProvider) SetExecutingAgent(name string) {
	p.executingAgent = strings.TrimSpace(name)
}

// SetManagementDeps sets optional dependencies needed for management tools.
func (p *WorkspaceToolProvider) SetManagementDeps(agentStore store.Store, mcpReg mcpServerLister, skillsMgr skillLister) {
	p.agentStore = agentStore
	p.mcpRegistry = mcpReg
	p.skillsManager = skillsMgr
}

// Tools returns all workspace tools as toolapi-compatible tools.
func (p *WorkspaceToolProvider) Tools() []toolapi.Tool {
	tools := []toolapi.Tool{
		// Phase 1: Context tools
		p.readNotesTool(),
		p.saveNoteTool(),
		p.readTasksTool(),
		p.readSessionsTool(),
		p.readSessionDetailTool(),
		p.readFilesTool(),
		p.readDirectoriesTool(),
	}

	// Workspace memory tools (need folder storage for MEMORY.md)
	if p.fileStore != nil {
		tools = append(tools, p.memoryWriteTool(), p.memoryForgetTool())
	}
	if p.userStore != nil {
		tools = append(tools, p.profileSetTool())
	}

	// Task-scoped tools (only when the chat is bound to a specific task)
	if p.taskID != "" {
		tools = append(tools, p.currentTaskTool(), p.taskRunsTool())
	}

	// Project-template tools (only when the library resolver is wired)
	if p.templatesRootResolver != nil {
		tools = append(tools, p.projectTemplatesTool(), p.createProjectTool())
	}

	// Phase 2: Management tools (only when dependencies are available)
	if p.agentStore != nil {
		tools = append(tools, p.manageAgentsTool())
	}
	if p.mcpRegistry != nil {
		tools = append(tools, p.manageMCPTool())
	}
	if p.skillsManager != nil {
		tools = append(tools, p.manageSkillsTool())
	}

	// Coordinator-only: the entry agent can delegate work to specialists.
	// Exposing this solely to the coordinator structurally enforces single-level
	// delegation (specialists never receive the tool, so they cannot re-delegate).
	if p.delegationEnabled() {
		tools = append(tools, p.delegateTaskTool())
	}

	return tools
}

// --- current_task (read) ---

func (p *WorkspaceToolProvider) currentTaskTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "current_task",
			Description: "Read the full record of the task this chat is scoped to. Returns description, status, assignee, latest result, error, schedule, and identifiers.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}
			for _, t := range ws.Tasks {
				if t.ID != p.taskID {
					continue
				}
				out := map[string]any{
					"id":             t.ID,
					"workspace_id":   t.WorkspaceID,
					"description":    t.Description,
					"status":         string(t.Status),
					"assigned_to":    t.To,
					"priority":       t.Priority,
					"created_at":     t.CreatedAt.Format(time.RFC3339),
					"execution_mode": string(t.ExecutionMode),
				}
				if t.Details != "" {
					out["details"] = t.Details
				}
				if t.Result != "" {
					out["result"] = t.Result
				}
				if t.Error != "" {
					out["error"] = t.Error
				}
				if t.StartedAt != nil {
					out["started_at"] = t.StartedAt.Format(time.RFC3339)
				}
				if t.CompletedAt != nil {
					out["completed_at"] = t.CompletedAt.Format(time.RFC3339)
				}
				if t.CurrentRunID != "" {
					out["current_run_id"] = t.CurrentRunID
				}
				if t.ParentTaskID != "" {
					out["parent_task_id"] = t.ParentTaskID
				}
				if t.AssignedNodeID != "" {
					out["assigned_node_id"] = t.AssignedNodeID
				}
				if t.ScheduleEnabled {
					out["schedule_enabled"] = true
					out["execution_count"] = t.ExecutionCount
					out["failure_count"] = t.FailureCount
					if t.NextRun != nil {
						out["next_run"] = t.NextRun.Format(time.RFC3339)
					}
					if t.LastRun != nil {
						out["last_run"] = t.LastRun.Format(time.RFC3339)
					}
				}
				if t.ResultStorage != nil {
					out["result_storage"] = t.ResultStorage
				}
				return marshalToolResponse(out)
			}
			return marshalToolResponse(map[string]any{
				"task_found": false,
				"task_id":    p.taskID,
				"message":    "Task not found in this workspace.",
			})
		},
	}
}

// --- task_runs (read) ---

func (p *WorkspaceToolProvider) taskRunsTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "task_runs",
			Description: "List recorded runs (execution history) for the active task. Returns each run's status, summary, full result/error, duration, and timestamp.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Optional. Cap the number of most-recent runs returned. Defaults to 20.",
					},
					"status": map[string]any{
						"type":        "string",
						"description": "Optional. Filter by run status: success, failed, blocked.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Limit  int    `json:"limit"`
				Status string `json:"status"`
			}
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &req); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			if req.Limit <= 0 {
				req.Limit = 20
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}
			for _, t := range ws.Tasks {
				if t.ID != p.taskID {
					continue
				}
				runs := t.ExecutionHistory
				if req.Status != "" {
					filtered := make([]workspace.TaskExecution, 0, len(runs))
					for _, run := range runs {
						if strings.EqualFold(run.Status, req.Status) {
							filtered = append(filtered, run)
						}
					}
					runs = filtered
				}
				if len(runs) > req.Limit {
					runs = runs[len(runs)-req.Limit:]
				}
				items := make([]map[string]any, 0, len(runs))
				for _, run := range runs {
					item := map[string]any{
						"executed_at": run.ExecutedAt.Format(time.RFC3339),
						"status":      run.Status,
						"duration_ms": run.Duration,
					}
					if run.RunID != "" {
						item["run_id"] = run.RunID
					}
					if run.Summary != "" {
						item["summary"] = run.Summary
					}
					if run.Result != "" {
						item["result"] = run.Result
					}
					if run.Error != "" {
						item["error"] = run.Error
					}
					items = append(items, item)
				}
				return marshalToolResponse(map[string]any{
					"task_id":    p.taskID,
					"total_runs": len(t.ExecutionHistory),
					"returned":   len(items),
					"runs":       items,
				})
			}
			return marshalToolResponse(map[string]any{
				"task_found": false,
				"task_id":    p.taskID,
				"runs":       []any{},
			})
		},
	}
}

// --- workspace_notes (read) ---

func (p *WorkspaceToolProvider) readNotesTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_notes",
			Description: "List and read notes in the current workspace. Use without arguments to list all notes. Provide the exact id field from the list output to read a specific note.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"note_id": map[string]any{
						"type":        "string",
						"description": "Optional. The exact note ID from the list output. If a note name is passed, the tool will only accept it when it uniquely matches one note in this workspace.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				NoteID string `json:"note_id"`
			}
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &req); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}

			// Read a specific note
			if req.NoteID != "" {
				note, guidance, err := p.resolveWorkspaceNoteReference(ctx, req.NoteID)
				if err != nil {
					return "", err
				}
				if guidance != nil {
					return marshalToolResponse(map[string]any{
						"note_found":      false,
						"requested_note":  req.NoteID,
						"message":         guidance.Message,
						"available_notes": guidance.Notes,
					})
				}
				result := map[string]any{
					"id":         note.ID,
					"name":       note.Name,
					"content":    note.Content,
					"created_at": note.CreatedAt.Format(time.RFC3339),
					"updated_at": note.UpdatedAt.Format(time.RFC3339),
				}
				return marshalToolResponse(result)
			}

			// List all notes
			notes, err := p.sessionStore.ListNotesByWorkspace(ctx, p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("failed to list notes: %w", err)
			}
			if len(notes) == 0 {
				return `{"notes":[],"message":"No notes in this workspace."}`, nil
			}

			items := make([]map[string]any, 0, len(notes))
			for _, n := range notes {
				items = append(items, map[string]any{
					"id":         n.ID,
					"name":       n.Name,
					"preview":    n.Preview,
					"updated_at": n.UpdatedAt.Format(time.RFC3339),
				})
			}
			return marshalToolResponse(map[string]any{"notes": items})
		},
	}
}

type workspaceNoteReferenceGuidance struct {
	Message string
	Notes   []map[string]any
}

func (p *WorkspaceToolProvider) resolveWorkspaceNoteReference(ctx context.Context, noteRef string) (*session.WorkspaceNote, *workspaceNoteReferenceGuidance, error) {
	noteRef = strings.TrimSpace(noteRef)
	if noteRef == "" {
		return nil, nil, fmt.Errorf("note_id is required")
	}

	if note, err := p.sessionStore.GetNote(ctx, noteRef); err == nil {
		if note.WorkspaceID != p.workspaceID {
			guidance, guidanceErr := p.buildWorkspaceNoteReferenceGuidance(ctx, fmt.Sprintf("Note %q does not belong to this workspace. Use one of the available workspace note ids instead.", noteRef))
			if guidanceErr != nil {
				return nil, nil, guidanceErr
			}
			return nil, guidance, nil
		}
		return note, nil, nil
	} else if err != session.ErrNoteNotFound {
		return nil, nil, fmt.Errorf("failed to load note: %w", err)
	}

	notes, err := p.sessionStore.ListNotesByWorkspace(ctx, p.workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list notes: %w", err)
	}

	var exactMatches []session.WorkspaceNoteListItem
	for _, item := range notes {
		if strings.EqualFold(strings.TrimSpace(item.Name), noteRef) {
			exactMatches = append(exactMatches, item)
		}
	}

	if len(exactMatches) == 1 {
		note, err := p.sessionStore.GetNote(ctx, exactMatches[0].ID)
		if err != nil {
			return nil, nil, fmt.Errorf("note matched by name but could not be loaded: %w", err)
		}
		return note, nil, nil
	}
	if len(exactMatches) > 1 {
		guidance, guidanceErr := p.buildWorkspaceNoteReferenceGuidance(ctx, fmt.Sprintf("Multiple workspace notes share the name %q. Use one of the exact ids below with workspace_notes.", noteRef))
		if guidanceErr != nil {
			return nil, nil, guidanceErr
		}
		return nil, guidance, nil
	}

	guidance, guidanceErr := p.buildWorkspaceNoteReferenceGuidance(ctx, fmt.Sprintf("Note %q was not found in this workspace. Use one of the exact ids below from workspace_notes instead of guessing.", noteRef))
	if guidanceErr != nil {
		return nil, nil, guidanceErr
	}
	return nil, guidance, nil
}

func (p *WorkspaceToolProvider) buildWorkspaceNoteReferenceGuidance(ctx context.Context, message string) (*workspaceNoteReferenceGuidance, error) {
	notes, err := p.sessionStore.ListNotesByWorkspace(ctx, p.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list notes: %w", err)
	}

	items := make([]map[string]any, 0, len(notes))
	for _, item := range notes {
		items = append(items, map[string]any{
			"id":         item.ID,
			"name":       item.Name,
			"preview":    item.Preview,
			"updated_at": item.UpdatedAt.Format(time.RFC3339),
		})
	}

	if len(items) == 0 {
		message = strings.TrimSpace(message) + " There are no notes in this workspace yet."
	}

	return &workspaceNoteReferenceGuidance{
		Message: message,
		Notes:   items,
	}, nil
}

// --- workspace_save_note (write) ---

func (p *WorkspaceToolProvider) saveNoteTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_save_note",
			Description: "Create or update a note in the current workspace. Provide name and content to create a new note, or note_id with content to update an existing one.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"note_id": map[string]any{
						"type":        "string",
						"description": "Optional. ID of an existing note to update. Omit to create a new note.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Name/title of the note. Required when creating a new note.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The markdown content of the note.",
					},
				},
				"required": []string{"content"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				NoteID  string `json:"note_id"`
				Name    string `json:"name"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(req.Content) == "" {
				return "", fmt.Errorf("content is required")
			}

			// Update existing note
			if req.NoteID != "" {
				existing, err := p.sessionStore.GetNote(ctx, req.NoteID)
				if err != nil {
					return "", fmt.Errorf("note not found: %w", err)
				}
				if existing.WorkspaceID != p.workspaceID {
					return "", fmt.Errorf("note does not belong to this workspace")
				}
				existing.Content = req.Content
				if req.Name != "" {
					existing.Name = req.Name
				}
				existing.UpdatedAt = time.Now()
				if err := p.sessionStore.UpdateNote(ctx, existing); err != nil {
					return "", fmt.Errorf("failed to update note: %w", err)
				}
				workspace.SyncNoteFile(p.fileStore, workspace.NoteFileParams{
					ID: existing.ID, WorkspaceID: existing.WorkspaceID,
					Name: existing.Name, Content: existing.Content,
					Tags:      existing.Tags,
					CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt,
				})
				return marshalToolResponse(map[string]any{
					"id":      existing.ID,
					"name":    existing.Name,
					"action":  "updated",
					"message": fmt.Sprintf("Note '%s' updated successfully.", existing.Name),
				})
			}

			// Create new note
			name := strings.TrimSpace(req.Name)
			if name == "" {
				name = "Untitled Note"
			}
			note := &session.WorkspaceNote{
				ID:          uuid.New().String(),
				WorkspaceID: p.workspaceID,
				Name:        name,
				Content:     req.Content,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := p.sessionStore.CreateNote(ctx, note); err != nil {
				return "", fmt.Errorf("failed to create note: %w", err)
			}
			workspace.SyncNoteFile(p.fileStore, workspace.NoteFileParams{
				ID: note.ID, WorkspaceID: note.WorkspaceID,
				Name: note.Name, Content: note.Content,
				Tags:      note.Tags,
				CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt,
			})
			logger.Info("Workspace tool created note", logger.Fields{
				"workspace_id": p.workspaceID,
				"note_id":      note.ID,
				"name":         note.Name,
			})
			return marshalToolResponse(map[string]any{
				"id":      note.ID,
				"name":    note.Name,
				"action":  "created",
				"message": fmt.Sprintf("Note '%s' created successfully.", note.Name),
			})
		},
	}
}

// --- workspace_tasks (read) ---

func (p *WorkspaceToolProvider) readTasksTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_tasks",
			Description: "List tasks in the current workspace. Returns task descriptions, statuses, assignees, and priorities.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Optional. Filter by status: pending, in_progress, completed, failed, blocked, cancelled.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Status string `json:"status"`
			}
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &req); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			tasks := ws.Tasks
			if req.Status != "" {
				filtered := make([]workspace.Task, 0)
				for _, t := range tasks {
					if string(t.Status) == req.Status {
						filtered = append(filtered, t)
					}
				}
				tasks = filtered
			}

			if len(tasks) == 0 {
				return `{"tasks":[],"message":"No tasks match the criteria."}`, nil
			}

			items := make([]map[string]any, 0, len(tasks))
			for _, t := range tasks {
				item := map[string]any{
					"id":          t.ID,
					"description": t.Description,
					"status":      string(t.Status),
					"assigned_to": t.To,
					"priority":    t.Priority,
					"created_at":  t.CreatedAt.Format(time.RFC3339),
				}
				if t.Result != "" {
					item["result_preview"] = truncate(t.Result, 300)
				}
				if t.ParentTaskID != "" {
					item["parent_task_id"] = t.ParentTaskID
				}
				items = append(items, item)
			}
			return marshalToolResponse(map[string]any{"tasks": items, "total": len(items)})
		},
	}
}

// --- workspace_sessions (read list) ---

func (p *WorkspaceToolProvider) readSessionsTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_sessions",
			Description: "List chat sessions in the current workspace. Returns each session's id, title, agent, and timestamps. Use the id field with workspace_session_detail.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			folderID := p.workspaceID
			filter := &session.SessionFilter{FolderID: &folderID}
			opts := &session.ListOptions{Limit: 20, Sort: session.SortByUpdatedDesc}

			result, err := p.sessionStore.ListSessions(ctx, filter, opts)
			if err != nil {
				return "", fmt.Errorf("failed to list sessions: %w", err)
			}

			if len(result.Sessions) == 0 {
				return `{"sessions":[],"message":"No sessions in this workspace."}`, nil
			}

			items := make([]map[string]any, 0, len(result.Sessions))
			for _, s := range result.Sessions {
				items = append(items, map[string]any{
					"id":            s.ID,
					"title":         s.Title,
					"agent_name":    s.AgentName,
					"message_count": s.MessageCount,
					"updated_at":    s.UpdatedAt.Format(time.RFC3339),
				})
			}
			return marshalToolResponse(map[string]any{"sessions": items, "total": result.Total})
		},
	}
}

// --- workspace_session_detail (read messages) ---

func (p *WorkspaceToolProvider) readSessionDetailTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_session_detail",
			Description: "Read the messages from a specific session in the workspace. Use workspace_sessions first and pass the exact id field. Do not guess or invent session IDs.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "The exact session ID from workspace_sessions. If a title is passed, the tool will only accept it when it uniquely matches one session in this workspace.",
					},
				},
				"required": []string{"session_id"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if req.SessionID == "" {
				return "", fmt.Errorf("session_id is required")
			}

			sess, guidance, err := p.resolveWorkspaceSessionReference(ctx, req.SessionID)
			if err != nil {
				return "", err
			}
			if guidance != nil {
				return marshalToolResponse(map[string]any{
					"session_found":      false,
					"requested_session":  req.SessionID,
					"message":            guidance.Message,
					"available_sessions": guidance.Sessions,
				})
			}

			messages, err := p.sessionStore.GetMessages(ctx, sess.ID)
			if err != nil {
				return "", fmt.Errorf("failed to load messages: %w", err)
			}

			msgItems := make([]map[string]any, 0, len(messages))
			for _, m := range messages {
				content := m.Content
				if len(content) > 2000 {
					content = content[:2000] + "... (truncated)"
				}
				msgItems = append(msgItems, map[string]any{
					"role":    m.Role,
					"content": content,
				})
			}

			return marshalToolResponse(map[string]any{
				"session_id": sess.ID,
				"title":      sess.Title,
				"agent_name": sess.AgentName,
				"messages":   msgItems,
			})
		},
	}
}

type workspaceSessionReferenceGuidance struct {
	Message  string
	Sessions []map[string]any
}

func (p *WorkspaceToolProvider) resolveWorkspaceSessionReference(ctx context.Context, sessionRef string) (*session.Session, *workspaceSessionReferenceGuidance, error) {
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	if sess, err := p.sessionStore.GetSession(ctx, sessionRef); err == nil {
		if sess.FolderID != p.workspaceID {
			guidance, guidanceErr := p.buildWorkspaceSessionReferenceGuidance(ctx, fmt.Sprintf("Session %q does not belong to this workspace. Use one of the available workspace session ids instead.", sessionRef))
			if guidanceErr != nil {
				return nil, nil, guidanceErr
			}
			return nil, guidance, nil
		}
		return sess, nil, nil
	} else if err != session.ErrSessionNotFound {
		return nil, nil, fmt.Errorf("failed to load session: %w", err)
	}

	filter := &session.SessionFilter{FolderID: &p.workspaceID}
	opts := &session.ListOptions{Limit: 100, Sort: session.SortByUpdatedDesc}
	result, err := p.sessionStore.ListSessions(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	var exactMatches []session.SessionListItem
	refLower := strings.ToLower(sessionRef)
	for _, item := range result.Sessions {
		if strings.EqualFold(strings.TrimSpace(item.Title), sessionRef) || strings.ToLower(strings.TrimSpace(item.Title)) == refLower {
			exactMatches = append(exactMatches, item)
		}
	}

	if len(exactMatches) == 1 {
		sess, err := p.sessionStore.GetSession(ctx, exactMatches[0].ID)
		if err != nil {
			return nil, nil, fmt.Errorf("session matched by title but could not be loaded: %w", err)
		}
		return sess, nil, nil
	}
	if len(exactMatches) > 1 {
		guidance, guidanceErr := p.buildWorkspaceSessionReferenceGuidance(ctx, fmt.Sprintf("Multiple workspace sessions share the title %q. Use one of the exact ids below with workspace_session_detail.", sessionRef))
		if guidanceErr != nil {
			return nil, nil, guidanceErr
		}
		return nil, guidance, nil
	}

	guidance, guidanceErr := p.buildWorkspaceSessionReferenceGuidance(ctx, fmt.Sprintf("Session %q was not found in this workspace. Use one of the exact ids below from workspace_sessions instead of guessing.", sessionRef))
	if guidanceErr != nil {
		return nil, nil, guidanceErr
	}
	return nil, guidance, nil
}

func (p *WorkspaceToolProvider) buildWorkspaceSessionReferenceGuidance(ctx context.Context, message string) (*workspaceSessionReferenceGuidance, error) {
	filter := &session.SessionFilter{FolderID: &p.workspaceID}
	opts := &session.ListOptions{Limit: 10, Sort: session.SortByUpdatedDesc}
	result, err := p.sessionStore.ListSessions(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	items := make([]map[string]any, 0, len(result.Sessions))
	for _, item := range result.Sessions {
		items = append(items, map[string]any{
			"id":            item.ID,
			"title":         item.Title,
			"agent_name":    item.AgentName,
			"message_count": item.MessageCount,
			"updated_at":    item.UpdatedAt.Format(time.RFC3339),
		})
	}

	if len(items) == 0 {
		message = strings.TrimSpace(message) + " There are no sessions in this workspace yet."
	}

	return &workspaceSessionReferenceGuidance{
		Message:  message,
		Sessions: items,
	}, nil
}

// --- workspace_files (read) ---

func (p *WorkspaceToolProvider) readFilesTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_files",
			Description: "List files attached to the current workspace. Returns file names, types, and metadata.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			files := make([]map[string]any, 0)
			for _, a := range ws.Attachments {
				if a.DeletedAt != nil {
					continue
				}
				item := map[string]any{
					"id":         a.ID,
					"title":      a.Title,
					"type":       string(a.Type),
					"created_at": a.CreatedAt.Format(time.RFC3339),
				}
				if a.File != nil {
					item["filename"] = a.File.Name
					item["mime_type"] = a.File.Mime
					item["size_bytes"] = a.File.Size
				}
				files = append(files, item)
			}

			if len(files) == 0 {
				return `{"files":[],"message":"No files attached to this workspace."}`, nil
			}

			return marshalToolResponse(map[string]any{"files": files, "total": len(files)})
		},
	}
}

// --- workspace_directories (read) ---

func (p *WorkspaceToolProvider) readDirectoriesTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_directories",
			Description: "List directories referenced by the current workspace. Returns directory names and filesystem paths.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			if len(ws.DirectoryReferences) == 0 {
				return `{"directories":[],"message":"No directories in this workspace."}`, nil
			}

			items := make([]map[string]any, 0, len(ws.DirectoryReferences))
			for _, d := range ws.DirectoryReferences {
				items = append(items, map[string]any{
					"id":   d.ID,
					"name": d.Name,
					"path": d.Path,
				})
			}
			return marshalToolResponse(map[string]any{"directories": items, "total": len(items)})
		},
	}
}

// marshalToolResponse marshals a tool response to JSON, returning an error if marshaling fails.
func marshalToolResponse(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool response: %w", err)
	}
	return string(raw), nil
}

// truncate shortens a string to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// ============================================================
// Phase 2: Management Tools
// ============================================================

// --- workspace_manage_agents ---

func (p *WorkspaceToolProvider) manageAgentsTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_manage_agents",
			Description: "Manage agents in the current workspace. Actions: 'list' shows workspace agents, 'available' shows all agents that can be added, 'add' adds an agent, 'remove' removes an agent.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "add", "remove"},
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Agent name. Required for 'add' and 'remove' actions.",
					},
				},
				"required": []string{"action"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Action    string `json:"action"`
				AgentName string `json:"agent_name"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			switch req.Action {
			case "list":
				instances := ws.AgentInstances
				if len(instances) == 0 {
					return `{"agents":[],"message":"No agents in this workspace."}`, nil
				}
				items := make([]map[string]any, 0, len(instances))
				for _, inst := range instances {
					items = append(items, map[string]any{
						"name":    inst.Name,
						"node_id": inst.NodeID,
					})
				}
				return marshalToolResponse(map[string]any{"agents": items})

			case "available":
				allNames := p.agentStore.ListAgents()
				wsAgentMap := make(map[string]bool)
				for _, inst := range ws.AgentInstances {
					wsAgentMap[strings.ToLower(inst.Name)] = true
				}
				available := make([]string, 0)
				for _, name := range allNames {
					if !wsAgentMap[strings.ToLower(name)] {
						available = append(available, name)
					}
				}
				return marshalToolResponse(map[string]any{
					"available_agents": available,
					"workspace_agents": len(ws.AgentInstances),
					"total_agents":     len(allNames),
				})

			case "add":
				if strings.TrimSpace(req.AgentName) == "" {
					return "", fmt.Errorf("agent_name is required for 'add' action")
				}
				if err := ws.AddAgent(req.AgentName); err != nil {
					return "", fmt.Errorf("failed to add agent: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool added agent", logger.Fields{
					"workspace_id": p.workspaceID,
					"agent_name":   req.AgentName,
				})
				return marshalToolResponse(map[string]any{
					"action":  "added",
					"agent":   req.AgentName,
					"message": fmt.Sprintf("Agent '%s' added to workspace.", req.AgentName),
				})

			case "remove":
				if strings.TrimSpace(req.AgentName) == "" {
					return "", fmt.Errorf("agent_name is required for 'remove' action")
				}
				if err := ws.RemoveAgent(req.AgentName); err != nil {
					return "", fmt.Errorf("failed to remove agent: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool removed agent", logger.Fields{
					"workspace_id": p.workspaceID,
					"agent_name":   req.AgentName,
				})
				return marshalToolResponse(map[string]any{
					"action":  "removed",
					"agent":   req.AgentName,
					"message": fmt.Sprintf("Agent '%s' removed from workspace.", req.AgentName),
				})

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, add, or remove", req.Action)
			}
		},
	}
}

// --- workspace_manage_mcp ---

func (p *WorkspaceToolProvider) manageMCPTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_manage_mcp",
			Description: "Manage MCP server bindings in the current workspace. Actions: 'list' shows workspace MCP bindings, 'available' shows all MCP servers that can be attached, 'attach' adds a binding, 'detach' removes a binding.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "attach", "detach"},
					},
					"server_name": map[string]any{
						"type":        "string",
						"description": "MCP server name. Required for 'attach' and 'detach' actions.",
					},
					"binding_id": map[string]any{
						"type":        "string",
						"description": "Binding ID. Required for 'detach'. Returned by 'list'.",
					},
				},
				"required": []string{"action"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Action     string `json:"action"`
				ServerName string `json:"server_name"`
				BindingID  string `json:"binding_id"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			switch req.Action {
			case "list":
				bindings := ws.GetMCPBindings()
				if len(bindings) == 0 {
					return `{"mcp_bindings":[],"message":"No MCP servers attached to this workspace."}`, nil
				}
				items := make([]map[string]any, 0, len(bindings))
				for _, b := range bindings {
					items = append(items, map[string]any{
						"id":          b.ID,
						"server_name": b.ServerName,
						"alias":       b.Alias,
						"enabled":     b.Enabled,
					})
				}
				return marshalToolResponse(map[string]any{"mcp_bindings": items})

			case "available":
				servers := p.mcpRegistry.ListServers()
				boundMap := make(map[string]bool)
				for _, b := range ws.GetMCPBindings() {
					boundMap[strings.ToLower(b.ServerName)] = true
				}
				available := make([]map[string]any, 0)
				for _, s := range servers {
					available = append(available, map[string]any{
						"name":             s.Name,
						"enabled":          s.Enabled,
						"already_attached": boundMap[strings.ToLower(s.Name)],
					})
				}
				return marshalToolResponse(map[string]any{"available_servers": available})

			case "attach":
				if strings.TrimSpace(req.ServerName) == "" {
					return "", fmt.Errorf("server_name is required for 'attach' action")
				}
				binding := workspace.WorkspaceMCPBinding{
					ID:         uuid.New().String(),
					ServerName: req.ServerName,
					Enabled:    true,
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				}
				if err := ws.UpsertMCPBinding(binding); err != nil {
					return "", fmt.Errorf("failed to attach MCP server: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool attached MCP server", logger.Fields{
					"workspace_id": p.workspaceID,
					"server_name":  req.ServerName,
					"binding_id":   binding.ID,
				})
				return marshalToolResponse(map[string]any{
					"action":     "attached",
					"binding_id": binding.ID,
					"server":     req.ServerName,
					"message":    fmt.Sprintf("MCP server '%s' attached to workspace.", req.ServerName),
				})

			case "detach":
				bindingID := strings.TrimSpace(req.BindingID)
				if bindingID == "" {
					// Try to find binding by server name
					if strings.TrimSpace(req.ServerName) == "" {
						return "", fmt.Errorf("binding_id or server_name is required for 'detach' action")
					}
					for _, b := range ws.GetMCPBindings() {
						if strings.EqualFold(b.ServerName, req.ServerName) {
							bindingID = b.ID
							break
						}
					}
					if bindingID == "" {
						return "", fmt.Errorf("no MCP binding found for server '%s'", req.ServerName)
					}
				}
				if err := ws.DeleteMCPBinding(bindingID); err != nil {
					return "", fmt.Errorf("failed to detach MCP server: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool detached MCP server", logger.Fields{
					"workspace_id": p.workspaceID,
					"binding_id":   bindingID,
				})
				return marshalToolResponse(map[string]any{
					"action":  "detached",
					"message": "MCP server detached from workspace.",
				})

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, attach, or detach", req.Action)
			}
		},
	}
}

// --- workspace_manage_skills ---

func (p *WorkspaceToolProvider) manageSkillsTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_manage_skills",
			Description: "Manage skill bindings in the current workspace. Actions: 'list' shows workspace skills, 'available' shows all skills that can be attached, 'attach' adds a skill, 'detach' removes a skill.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "attach", "detach"},
					},
					"skill_name": map[string]any{
						"type":        "string",
						"description": "Skill name. Required for 'attach' and 'detach' actions.",
					},
					"binding_id": map[string]any{
						"type":        "string",
						"description": "Binding ID. Required for 'detach'. Returned by 'list'.",
					},
				},
				"required": []string{"action"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				Action    string `json:"action"`
				SkillName string `json:"skill_name"`
				BindingID string `json:"binding_id"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			switch req.Action {
			case "list":
				bindings := ws.GetSkillBindings()
				if len(bindings) == 0 {
					return `{"skill_bindings":[],"message":"No skills attached to this workspace."}`, nil
				}
				items := make([]map[string]any, 0, len(bindings))
				for _, b := range bindings {
					items = append(items, map[string]any{
						"id":         b.ID,
						"skill_name": b.SkillName,
						"enabled":    b.Enabled,
						"trusted":    b.Trusted,
					})
				}
				return marshalToolResponse(map[string]any{"skill_bindings": items})

			case "available":
				// List skills for a generic agent name (empty uses defaults)
				allSkills, err := p.skillsManager.ListSkills("")
				if err != nil {
					return "", fmt.Errorf("failed to list available skills: %w", err)
				}
				boundMap := make(map[string]bool)
				for _, b := range ws.GetSkillBindings() {
					boundMap[strings.ToLower(b.SkillName)] = true
				}
				available := make([]map[string]any, 0)
				for _, s := range allSkills {
					available = append(available, map[string]any{
						"name":             s.Name,
						"description":      truncate(s.Description, 200),
						"already_attached": boundMap[strings.ToLower(s.Name)],
					})
				}
				return marshalToolResponse(map[string]any{"available_skills": available, "total": len(available)})

			case "attach":
				if strings.TrimSpace(req.SkillName) == "" {
					return "", fmt.Errorf("skill_name is required for 'attach' action")
				}
				binding := workspace.WorkspaceSkillBinding{
					ID:        uuid.New().String(),
					SkillName: req.SkillName,
					Enabled:   true,
					Trusted:   false,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := ws.UpsertSkillBinding(binding); err != nil {
					return "", fmt.Errorf("failed to attach skill: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool attached skill", logger.Fields{
					"workspace_id": p.workspaceID,
					"skill_name":   req.SkillName,
					"binding_id":   binding.ID,
				})
				return marshalToolResponse(map[string]any{
					"action":     "attached",
					"binding_id": binding.ID,
					"skill":      req.SkillName,
					"message":    fmt.Sprintf("Skill '%s' attached to workspace.", req.SkillName),
				})

			case "detach":
				bindingID := strings.TrimSpace(req.BindingID)
				if bindingID == "" {
					// Try to find binding by skill name
					if strings.TrimSpace(req.SkillName) == "" {
						return "", fmt.Errorf("binding_id or skill_name is required for 'detach' action")
					}
					for _, b := range ws.GetSkillBindings() {
						if strings.EqualFold(b.SkillName, req.SkillName) {
							bindingID = b.ID
							break
						}
					}
					if bindingID == "" {
						return "", fmt.Errorf("no skill binding found for '%s'", req.SkillName)
					}
				}
				if err := ws.DeleteSkillBinding(bindingID); err != nil {
					return "", fmt.Errorf("failed to detach skill: %w", err)
				}
				if err := p.workspaceStore.Save(ws); err != nil {
					return "", fmt.Errorf("failed to save workspace: %w", err)
				}
				logger.Info("Workspace tool detached skill", logger.Fields{
					"workspace_id": p.workspaceID,
					"binding_id":   bindingID,
				})
				return marshalToolResponse(map[string]any{
					"action":  "detached",
					"message": "Skill detached from workspace.",
				})

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, attach, or detach", req.Action)
			}
		},
	}
}
