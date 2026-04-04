package workspace

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

const (
	dependencyActionEnableWorkspaceMCP  = "workspace_enable_mcp_binding"
	dependencyActionSuppressPrompt      = "suppress_dependency_prompt"
	dependencyPreferenceValueSuppressed = "suppressed"
)

type resolveDependencyActionRequest struct {
	Type           string `json:"type"`
	ServerName     string `json:"server_name,omitempty"`
	SkillName      string `json:"skill_name,omitempty"`
	DependencyType string `json:"dependency_type,omitempty"`
	PreferenceKey  string `json:"preference_key,omitempty"`
}

func (h *HTTPHandler) ResolveDependencyAction(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req resolveDependencyActionRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	switch strings.TrimSpace(req.Type) {
	case dependencyActionEnableWorkspaceMCP:
		h.resolveEnableWorkspaceMCP(w, workspaceID, workspace, req)
		return
	case dependencyActionSuppressPrompt:
		h.resolveSuppressDependencyPrompt(w, workspaceID, workspace, req)
		return
	default:
		orihttp.BadRequest(w, "unsupported dependency action")
		return
	}
}

func (h *HTTPHandler) resolveEnableWorkspaceMCP(w http.ResponseWriter, workspaceID string, workspace *Workspace, req resolveDependencyActionRequest) {
	serverName := strings.TrimSpace(req.ServerName)
	if serverName == "" {
		orihttp.BadRequest(w, "server_name is required")
		return
	}

	now := time.Now()
	binding := findWorkspaceMCPBindingByServer(workspace.GetMCPBindings(), serverName)
	created := false
	if binding == nil {
		created = true
		binding = &WorkspaceMCPBinding{
			ID:         uuid.NewString(),
			ServerName: serverName,
			Alias:      serverName,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
	} else {
		binding.Enabled = true
		binding.UpdatedAt = now
	}

	if err := workspace.UpsertMCPBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if created {
		h.publishWorkspaceMCPEvent(workspaceID, "mcp_binding_created", map[string]interface{}{"binding": binding})
	} else {
		h.publishWorkspaceMCPEvent(workspaceID, "mcp_binding_updated", map[string]interface{}{"binding": binding})
	}

	orihttp.WriteJSON(w, map[string]any{
		"success":     true,
		"retry_ready": true,
		"binding":     binding,
		"workspace":   workspaceID,
	})
}

func (h *HTTPHandler) resolveSuppressDependencyPrompt(w http.ResponseWriter, workspaceID string, workspace *Workspace, req resolveDependencyActionRequest) {
	dependencyType := strings.TrimSpace(req.DependencyType)
	if !isWorkspaceSuppressibleDependencyType(dependencyType) {
		orihttp.BadRequest(w, "dependency type does not support prompt suppression")
		return
	}

	preferenceKey := strings.TrimSpace(req.PreferenceKey)
	if preferenceKey == "" {
		preferenceKey = DependencyPreferenceKey(dependencyType, req.ServerName)
	}
	if preferenceKey == "" {
		orihttp.BadRequest(w, "preference_key is required")
		return
	}

	target := strings.TrimSpace(req.ServerName)
	if target == "" {
		target = strings.TrimSpace(req.SkillName)
	}

	workspace.SetDependencyPreference(preferenceKey, DependencyPreference{
		Value:          dependencyPreferenceValueSuppressed,
		DependencyType: dependencyType,
		Target:         target,
	})
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	orihttp.WriteJSON(w, map[string]any{
		"success":        true,
		"retry_ready":    false,
		"preference_key": preferenceKey,
		"workspace":      workspaceID,
	})
}

func isWorkspaceSuppressibleDependencyType(dependencyType string) bool {
	switch strings.ToLower(strings.TrimSpace(dependencyType)) {
	case "mcp_missing", "workspace_mcp_binding", "skill_missing":
		return true
	default:
		return false
	}
}

func findWorkspaceMCPBindingByServer(bindings []WorkspaceMCPBinding, serverName string) *WorkspaceMCPBinding {
	normalizedServer := strings.ToLower(strings.TrimSpace(serverName))
	for i := range bindings {
		if strings.ToLower(strings.TrimSpace(bindings[i].ServerName)) == normalizedServer {
			cp := bindings[i]
			return &cp
		}
	}
	return nil
}
