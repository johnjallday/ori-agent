package mcp

import (
	"strings"
	"testing"
)

func TestToolAllowed_NoPolicyPermitsEverything(t *testing.T) {
	SetToolExposureHook(nil)
	if !toolAllowed("https://anything.example/mcp", "any_tool") {
		t.Error("with no policy installed, every tool must be permitted")
	}
}

func TestToolAllowed_FailClosedPolicy(t *testing.T) {
	// Emulate the host's Google Drive policy: cap drivemcp to a read-only
	// allowlist, pass every other server through untouched.
	allow := map[string]bool{"read_file_content": true, "search_files": true}
	SetToolExposureHook(func(serverURL, toolName string) bool {
		if strings.Contains(serverURL, "drivemcp.googleapis.com") {
			return allow[toolName]
		}
		return true
	})
	defer SetToolExposureHook(nil)

	const driveURL = "https://drivemcp.googleapis.com/mcp/v1"
	if !toolAllowed(driveURL, "read_file_content") {
		t.Error("allowlisted Drive tool must be permitted")
	}
	if toolAllowed(driveURL, "create_file") {
		t.Error("mutating Drive tool must be denied (fail-closed)")
	}
	if toolAllowed(driveURL, "some_future_tool") {
		t.Error("unknown/future Drive tool must be denied (fail-closed)")
	}
	// A non-Drive server is unaffected by the Drive policy.
	if !toolAllowed("https://example.com/mcp", "create_file") {
		t.Error("non-Drive server must not be restricted by the Drive policy")
	}
}
