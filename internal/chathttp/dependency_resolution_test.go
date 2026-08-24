package chathttp

import "testing"

func TestInferDependencyResolutionFromText_MCPPermissionDenied(t *testing.T) {
	responseText := "The calendar MCP tool isn't enabled in the current permission mode. Please enable the `mcp__calendar` tool in your permission settings and try again."

	resolution := inferDependencyResolutionFromText(responseText, normalizedChatRouteContext{WorkspaceID: "workspace-1"}, "claude_code")
	if resolution == nil {
		t.Fatal("expected dependency resolution")
	}
	if resolution.ReasonCode != "provider_permission_denied" {
		t.Fatalf("expected provider_permission_denied, got %q", resolution.ReasonCode)
	}
	if resolution.Title != "Calendar MCP tool permission required" {
		t.Fatalf("unexpected title %q", resolution.Title)
	}
	if len(resolution.Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(resolution.Steps))
	}
	if resolution.Steps[0].Type != dependencyTypeToolPermission {
		t.Fatalf("expected tool permission step, got %q", resolution.Steps[0].Type)
	}
	if len(resolution.Steps[0].Actions) < 2 {
		t.Fatalf("expected setup actions, got %#v", resolution.Steps[0].Actions)
	}
}

func TestInferDependencyResolutionFromText_IgnoresRegularResponses(t *testing.T) {
	resolution := inferDependencyResolutionFromText("Media project created successfully.", normalizedChatRouteContext{}, "claude_code")
	if resolution != nil {
		t.Fatalf("expected nil resolution, got %#v", resolution)
	}
}
