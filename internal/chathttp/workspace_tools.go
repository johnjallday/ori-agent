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
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/oriagent/ori-pluginapi"
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

	// Optional dependencies for management tools (Phase 2)
	agentStore    store.Store
	mcpRegistry   mcpServerLister
	skillsManager skillLister
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

// SetManagementDeps sets optional dependencies needed for management tools.
func (p *WorkspaceToolProvider) SetManagementDeps(agentStore store.Store, mcpReg mcpServerLister, skillsMgr skillLister) {
	p.agentStore = agentStore
	p.mcpRegistry = mcpReg
	p.skillsManager = skillsMgr
}

// Tools returns all workspace tools as pluginapi-compatible tools.
func (p *WorkspaceToolProvider) Tools() []pluginapi.PluginTool {
	tools := []pluginapi.PluginTool{
		// Phase 1: Context tools
		p.readNotesTool(),
		p.saveNoteTool(),
		p.readTasksTool(),
		p.readSessionsTool(),
		p.readSessionDetailTool(),
		p.readFilesTool(),
		p.readDirectoriesTool(),
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

	return tools
}

// --- workspace_notes (read) ---

func (p *WorkspaceToolProvider) readNotesTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_notes",
			Description: "List and read notes in the current workspace. Use without arguments to list all notes. Provide a note_id to read the full content of a specific note.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"note_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional. The ID of a specific note to read in full. Omit to list all notes.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				NoteID string `json:"note_id"`
			}
			if strings.TrimSpace(args) != "" {
				_ = json.Unmarshal([]byte(args), &req)
			}

			// Read a specific note
			if req.NoteID != "" {
				note, err := p.sessionStore.GetNote(ctx, req.NoteID)
				if err != nil {
					return "", fmt.Errorf("note not found: %w", err)
				}
				result := map[string]interface{}{
					"id":         note.ID,
					"name":       note.Name,
					"content":    note.Content,
					"created_at": note.CreatedAt.Format(time.RFC3339),
					"updated_at": note.UpdatedAt.Format(time.RFC3339),
				}
				raw, _ := json.Marshal(result)
				return string(raw), nil
			}

			// List all notes
			notes, err := p.sessionStore.ListNotesByWorkspace(ctx, p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("failed to list notes: %w", err)
			}
			if len(notes) == 0 {
				return `{"notes":[],"message":"No notes in this workspace."}`, nil
			}

			items := make([]map[string]interface{}, 0, len(notes))
			for _, n := range notes {
				items = append(items, map[string]interface{}{
					"id":         n.ID,
					"name":       n.Name,
					"preview":    n.Preview,
					"updated_at": n.UpdatedAt.Format(time.RFC3339),
				})
			}
			raw, _ := json.Marshal(map[string]interface{}{"notes": items})
			return string(raw), nil
		},
	}
}

// --- workspace_save_note (write) ---

func (p *WorkspaceToolProvider) saveNoteTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_save_note",
			Description: "Create or update a note in the current workspace. Provide name and content to create a new note, or note_id with content to update an existing one.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"note_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional. ID of an existing note to update. Omit to create a new note.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name/title of the note. Required when creating a new note.",
					},
					"content": map[string]interface{}{
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
					CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt,
				})
				raw, _ := json.Marshal(map[string]interface{}{
					"id":      existing.ID,
					"name":    existing.Name,
					"action":  "updated",
					"message": fmt.Sprintf("Note '%s' updated successfully.", existing.Name),
				})
				return string(raw), nil
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
				CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt,
			})
			logger.Info("Workspace tool created note", logger.Fields{
				"workspace_id": p.workspaceID,
				"note_id":      note.ID,
				"name":         note.Name,
			})
			raw, _ := json.Marshal(map[string]interface{}{
				"id":      note.ID,
				"name":    note.Name,
				"action":  "created",
				"message": fmt.Sprintf("Note '%s' created successfully.", note.Name),
			})
			return string(raw), nil
		},
	}
}

// --- workspace_tasks (read) ---

func (p *WorkspaceToolProvider) readTasksTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_tasks",
			Description: "List tasks in the current workspace. Returns task descriptions, statuses, assignees, and priorities.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{
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
				_ = json.Unmarshal([]byte(args), &req)
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

			items := make([]map[string]interface{}, 0, len(tasks))
			for _, t := range tasks {
				item := map[string]interface{}{
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
			raw, _ := json.Marshal(map[string]interface{}{"tasks": items, "total": len(items)})
			return string(raw), nil
		},
	}
}

// --- workspace_sessions (read list) ---

func (p *WorkspaceToolProvider) readSessionsTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_sessions",
			Description: "List chat sessions in the current workspace. Returns session titles, agents, and timestamps. Use workspace_session_detail to read a specific session's messages.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
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

			items := make([]map[string]interface{}, 0, len(result.Sessions))
			for _, s := range result.Sessions {
				items = append(items, map[string]interface{}{
					"id":            s.ID,
					"title":         s.Title,
					"agent_name":    s.AgentName,
					"message_count": s.MessageCount,
					"updated_at":    s.UpdatedAt.Format(time.RFC3339),
				})
			}
			raw, _ := json.Marshal(map[string]interface{}{"sessions": items, "total": result.Total})
			return string(raw), nil
		},
	}
}

// --- workspace_session_detail (read messages) ---

func (p *WorkspaceToolProvider) readSessionDetailTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_session_detail",
			Description: "Read the messages from a specific session in the workspace. Use workspace_sessions first to find the session ID.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{
						"type":        "string",
						"description": "The ID of the session to read.",
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

			sess, err := p.sessionStore.GetSession(ctx, req.SessionID)
			if err != nil {
				return "", fmt.Errorf("session not found: %w", err)
			}

			// Verify session belongs to this workspace
			if sess.FolderID != p.workspaceID {
				return "", fmt.Errorf("session does not belong to this workspace")
			}

			messages, err := p.sessionStore.GetMessages(ctx, req.SessionID)
			if err != nil {
				return "", fmt.Errorf("failed to load messages: %w", err)
			}

			msgItems := make([]map[string]interface{}, 0, len(messages))
			for _, m := range messages {
				content := m.Content
				if len(content) > 2000 {
					content = content[:2000] + "... (truncated)"
				}
				msgItems = append(msgItems, map[string]interface{}{
					"role":    m.Role,
					"content": content,
				})
			}

			raw, _ := json.Marshal(map[string]interface{}{
				"session_id": sess.ID,
				"title":      sess.Title,
				"agent_name": sess.AgentName,
				"messages":   msgItems,
			})
			return string(raw), nil
		},
	}
}

// --- workspace_files (read) ---

func (p *WorkspaceToolProvider) readFilesTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_files",
			Description: "List files attached to the current workspace. Returns file names, types, and metadata.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}

			files := make([]map[string]interface{}, 0)
			for _, a := range ws.Attachments {
				if a.DeletedAt != nil {
					continue
				}
				item := map[string]interface{}{
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

			raw, _ := json.Marshal(map[string]interface{}{"files": files, "total": len(files)})
			return string(raw), nil
		},
	}
}

// --- workspace_directories (read) ---

func (p *WorkspaceToolProvider) readDirectoriesTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_directories",
			Description: "List directories referenced by the current workspace. Returns directory names and filesystem paths.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
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

			items := make([]map[string]interface{}, 0, len(ws.DirectoryReferences))
			for _, d := range ws.DirectoryReferences {
				items = append(items, map[string]interface{}{
					"id":   d.ID,
					"name": d.Name,
					"path": d.Path,
				})
			}
			raw, _ := json.Marshal(map[string]interface{}{"directories": items, "total": len(items)})
			return string(raw), nil
		},
	}
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================
// Phase 2: Management Tools
// ============================================================

// --- workspace_manage_agents ---

func (p *WorkspaceToolProvider) manageAgentsTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_manage_agents",
			Description: "Manage agents in the current workspace. Actions: 'list' shows workspace agents, 'available' shows all agents that can be added, 'add' adds an agent, 'remove' removes an agent.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "add", "remove"},
					},
					"agent_name": map[string]interface{}{
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
				items := make([]map[string]interface{}, 0, len(instances))
				for _, inst := range instances {
					items = append(items, map[string]interface{}{
						"name":    inst.Name,
						"node_id": inst.NodeID,
					})
				}
				raw, _ := json.Marshal(map[string]interface{}{"agents": items})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"available_agents": available,
					"workspace_agents": len(ws.AgentInstances),
					"total_agents":     len(allNames),
				})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":  "added",
					"agent":   req.AgentName,
					"message": fmt.Sprintf("Agent '%s' added to workspace.", req.AgentName),
				})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":  "removed",
					"agent":   req.AgentName,
					"message": fmt.Sprintf("Agent '%s' removed from workspace.", req.AgentName),
				})
				return string(raw), nil

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, add, or remove", req.Action)
			}
		},
	}
}

// --- workspace_manage_mcp ---

func (p *WorkspaceToolProvider) manageMCPTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_manage_mcp",
			Description: "Manage MCP server bindings in the current workspace. Actions: 'list' shows workspace MCP bindings, 'available' shows all MCP servers that can be attached, 'attach' adds a binding, 'detach' removes a binding.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "attach", "detach"},
					},
					"server_name": map[string]interface{}{
						"type":        "string",
						"description": "MCP server name. Required for 'attach' and 'detach' actions.",
					},
					"binding_id": map[string]interface{}{
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
				items := make([]map[string]interface{}, 0, len(bindings))
				for _, b := range bindings {
					items = append(items, map[string]interface{}{
						"id":          b.ID,
						"server_name": b.ServerName,
						"alias":       b.Alias,
						"enabled":     b.Enabled,
					})
				}
				raw, _ := json.Marshal(map[string]interface{}{"mcp_bindings": items})
				return string(raw), nil

			case "available":
				servers := p.mcpRegistry.ListServers()
				boundMap := make(map[string]bool)
				for _, b := range ws.GetMCPBindings() {
					boundMap[strings.ToLower(b.ServerName)] = true
				}
				available := make([]map[string]interface{}, 0)
				for _, s := range servers {
					available = append(available, map[string]interface{}{
						"name":             s.Name,
						"enabled":          s.Enabled,
						"already_attached": boundMap[strings.ToLower(s.Name)],
					})
				}
				raw, _ := json.Marshal(map[string]interface{}{"available_servers": available})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":     "attached",
					"binding_id": binding.ID,
					"server":     req.ServerName,
					"message":    fmt.Sprintf("MCP server '%s' attached to workspace.", req.ServerName),
				})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":  "detached",
					"message": "MCP server detached from workspace.",
				})
				return string(raw), nil

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, attach, or detach", req.Action)
			}
		},
	}
}

// --- workspace_manage_skills ---

func (p *WorkspaceToolProvider) manageSkillsTool() pluginapi.PluginTool {
	return &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "workspace_manage_skills",
			Description: "Manage skill bindings in the current workspace. Actions: 'list' shows workspace skills, 'available' shows all skills that can be attached, 'attach' adds a skill, 'detach' removes a skill.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "The action to perform.",
						"enum":        []string{"list", "available", "attach", "detach"},
					},
					"skill_name": map[string]interface{}{
						"type":        "string",
						"description": "Skill name. Required for 'attach' and 'detach' actions.",
					},
					"binding_id": map[string]interface{}{
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
				items := make([]map[string]interface{}, 0, len(bindings))
				for _, b := range bindings {
					items = append(items, map[string]interface{}{
						"id":         b.ID,
						"skill_name": b.SkillName,
						"enabled":    b.Enabled,
						"trusted":    b.Trusted,
					})
				}
				raw, _ := json.Marshal(map[string]interface{}{"skill_bindings": items})
				return string(raw), nil

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
				available := make([]map[string]interface{}, 0)
				for _, s := range allSkills {
					available = append(available, map[string]interface{}{
						"name":             s.Name,
						"description":      truncate(s.Description, 200),
						"already_attached": boundMap[strings.ToLower(s.Name)],
					})
				}
				raw, _ := json.Marshal(map[string]interface{}{"available_skills": available, "total": len(available)})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":     "attached",
					"binding_id": binding.ID,
					"skill":      req.SkillName,
					"message":    fmt.Sprintf("Skill '%s' attached to workspace.", req.SkillName),
				})
				return string(raw), nil

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
				raw, _ := json.Marshal(map[string]interface{}{
					"action":  "detached",
					"message": "Skill detached from workspace.",
				})
				return string(raw), nil

			default:
				return "", fmt.Errorf("unknown action '%s'; use list, available, attach, or detach", req.Action)
			}
		},
	}
}
