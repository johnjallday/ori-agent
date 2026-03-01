package chathttp

import (
	"strings"
	"testing"
)

func TestNormalizeChatRouteContext_InfersWorkspaceFromPath(t *testing.T) {
	ctx := normalizeChatRouteContext(&chatRouteContext{
		PagePath: "/workspaces/abc-123",
	})

	if ctx.WorkspaceID != "abc-123" {
		t.Fatalf("expected workspace id abc-123, got %q", ctx.WorkspaceID)
	}
	if ctx.Surface != "workspace_detail" {
		t.Fatalf("expected workspace_detail surface, got %q", ctx.Surface)
	}
}

func TestNormalizeChatRouteContext_InfersCanvasSurface(t *testing.T) {
	ctx := normalizeChatRouteContext(&chatRouteContext{
		PagePath: "/workspaces/abc-123/canvas",
	})

	if ctx.Surface != "workspace_canvas" {
		t.Fatalf("expected workspace_canvas surface, got %q", ctx.Surface)
	}
}

func TestBuildRouteContextSystemPrompt_WorkspaceDetail(t *testing.T) {
	prompt := buildRouteContextSystemPrompt(normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/ws-1",
		WorkspaceID: "ws-1",
		Origin:      "ask_ori",
	})

	if !strings.Contains(prompt, "Primary mode: workspace-focused assistance.") {
		t.Fatalf("expected workspace-focused instructions, got %q", prompt)
	}
	if !strings.Contains(prompt, "Active workspace_id: ws-1") {
		t.Fatalf("expected workspace id in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Page path: /workspaces/ws-1") {
		t.Fatalf("expected page path in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Request origin: ask_ori") {
		t.Fatalf("expected origin in prompt, got %q", prompt)
	}
}

func TestBuildRouteContextSystemPrompt_EmptyContext(t *testing.T) {
	prompt := buildRouteContextSystemPrompt(normalizedChatRouteContext{})
	if prompt != "" {
		t.Fatalf("expected empty prompt, got %q", prompt)
	}
}
