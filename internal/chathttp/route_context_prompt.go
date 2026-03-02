package chathttp

import (
	"fmt"
	"strings"
)

type chatRouteContext struct {
	Surface     string `json:"surface,omitempty"`
	PagePath    string `json:"page_path,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Origin      string `json:"origin,omitempty"`
}

type normalizedChatRouteContext struct {
	Surface     string
	PagePath    string
	WorkspaceID string
	Origin      string
}

const (
	maxRouteContextPagePathLen    = 256
	maxRouteContextWorkspaceIDLen = 128
	maxRouteContextOriginLen      = 96
	maxRouteContextSurfaceLen     = 32
)

var allowedRouteSurfaces = map[string]struct{}{
	"workspace_detail": {},
	"workspace_canvas": {},
	"workspace_chat":   {},
	"workspace_hub":    {},
	"dashboard":        {},
	"chat":             {},
}

func normalizeChatRouteContext(input *chatRouteContext) normalizedChatRouteContext {
	if input == nil {
		return normalizedChatRouteContext{}
	}

	pagePath := sanitizeRouteContextPath(input.PagePath)
	workspaceID := sanitizeRouteContextIdentifier(input.WorkspaceID, maxRouteContextWorkspaceIDLen)
	if workspaceID == "" {
		workspaceID = sanitizeRouteContextIdentifier(extractWorkspaceIDFromPagePath(pagePath), maxRouteContextWorkspaceIDLen)
	}
	if pagePath == "" && workspaceID != "" {
		pagePath = "/workspaces/" + workspaceID
	}
	if pagePath == "" {
		pagePath = "/"
	}

	surface := sanitizeRouteContextSurface(input.Surface)
	if surface == "" {
		surface = inferChatRouteSurface(pagePath, workspaceID)
	} else if _, ok := allowedRouteSurfaces[surface]; !ok {
		surface = inferChatRouteSurface(pagePath, workspaceID)
	}

	return normalizedChatRouteContext{
		Surface:     surface,
		PagePath:    pagePath,
		WorkspaceID: workspaceID,
		Origin:      sanitizeRouteContextOrigin(input.Origin),
	}
}

func inferChatRouteSurface(pathname, workspaceID string) string {
	path := strings.ToLower(strings.TrimSpace(pathname))
	if path == "" {
		if strings.TrimSpace(workspaceID) != "" {
			return "workspace_detail"
		}
		return "dashboard"
	}
	if strings.HasPrefix(path, "/workspaces/") || strings.HasPrefix(path, "/studios/") {
		if strings.Contains(path, "/canvas") {
			return "workspace_canvas"
		}
		return "workspace_detail"
	}
	if strings.HasPrefix(path, "/workspaces") || strings.HasPrefix(path, "/studios") {
		return "workspace_hub"
	}
	if strings.HasPrefix(path, "/chat") {
		if strings.TrimSpace(workspaceID) != "" {
			return "workspace_chat"
		}
		return "chat"
	}
	if path == "/" || strings.HasPrefix(path, "/dashboard") {
		return "dashboard"
	}
	if strings.TrimSpace(workspaceID) != "" {
		return "workspace_detail"
	}
	return "dashboard"
}

func extractWorkspaceIDFromPagePath(pathname string) string {
	path := strings.TrimSpace(pathname)
	if path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	prefixes := []string{"/workspaces/", "/studios/"}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := path[len(prefix):]
		if idx := strings.Index(rest, "/"); idx >= 0 {
			rest = rest[:idx]
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

func buildRouteContextSystemPrompt(ctx normalizedChatRouteContext) string {
	if strings.TrimSpace(ctx.Surface) == "" && strings.TrimSpace(ctx.WorkspaceID) == "" {
		return ""
	}

	lines := []string{
		"You are receiving this request from a specific app page. Treat this context as high-priority routing guidance.",
	}

	switch ctx.Surface {
	case "workspace_detail", "workspace_canvas", "workspace_chat":
		lines = append(lines,
			"Primary mode: workspace-focused assistance.",
			"Prioritize actions for this workspace: tasks, notes, files, directories, and workspace sessions.",
			"Only switch to broad/general chat when explicitly requested.",
		)
	case "workspace_hub":
		lines = append(lines,
			"Primary mode: workspace hub assistance.",
			"Prefer cross-workspace operations such as choosing, creating, or organizing workspaces and their dashboard entities.",
		)
	case "dashboard":
		lines = append(lines,
			"Primary mode: global dashboard assistance.",
			"Handle general requests, and suggest workspace-scoped actions only when relevant.",
		)
	case "chat":
		lines = append(lines,
			"Primary mode: general chat assistance.",
		)
	default:
		if strings.TrimSpace(ctx.WorkspaceID) != "" {
			lines = append(lines,
				"Primary mode: workspace-focused assistance.",
				"Prioritize actions for this workspace before unrelated general discussion.",
			)
		}
	}

	if strings.TrimSpace(ctx.WorkspaceID) != "" {
		lines = append(lines, fmt.Sprintf("Active workspace_id: %q", ctx.WorkspaceID))
	}
	if strings.TrimSpace(ctx.PagePath) != "" {
		lines = append(lines, fmt.Sprintf("Page path: %q", ctx.PagePath))
	}
	if strings.TrimSpace(ctx.Origin) != "" {
		lines = append(lines, fmt.Sprintf("Request origin: %q", ctx.Origin))
	}

	return strings.Join(lines, "\n")
}

func sanitizeRouteContextSurface(value string) string {
	surface := strings.ToLower(sanitizeRouteContextText(value, maxRouteContextSurfaceLen))
	return strings.ReplaceAll(surface, " ", "_")
}

func sanitizeRouteContextOrigin(value string) string {
	return sanitizeRouteContextIdentifier(strings.ToLower(value), maxRouteContextOriginLen)
}

func sanitizeRouteContextPath(value string) string {
	path := sanitizeRouteContextText(value, maxRouteContextPagePathLen)
	if path == "" {
		return ""
	}
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = strings.TrimSpace(path[:idx])
	}
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func sanitizeRouteContextIdentifier(value string, maxLen int) string {
	raw := sanitizeRouteContextText(value, maxLen)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == ':' {
			b.WriteRune(r)
		}
	}
	safe := b.String()
	if len(safe) > maxLen {
		safe = safe[:maxLen]
	}
	return safe
}

func sanitizeRouteContextText(value string, maxLen int) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if maxLen > 0 && len(text) > maxLen {
		text = text[:maxLen]
	}
	return strings.TrimSpace(text)
}
