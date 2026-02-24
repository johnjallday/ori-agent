package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
)

func TestResolvePlaywrightEnv_AutoPreservesExistingOverrides(t *testing.T) {
	existing := map[string]string{
		"PLAYWRIGHT_MCP_BROWSER":         "chrome",
		"PLAYWRIGHT_MCP_EXECUTABLE_PATH": "/tmp/brave",
		"OTHER_ENV":                      "keep",
	}

	next := resolvePlaywrightEnv(existing, config.UtilitySettings{
		PlaywrightBrowser:    "auto",
		PlaywrightExecutable: "",
	})

	if next["PLAYWRIGHT_MCP_BROWSER"] != "chrome" {
		t.Fatalf("expected PLAYWRIGHT_MCP_BROWSER to remain unchanged in auto mode, got %q", next["PLAYWRIGHT_MCP_BROWSER"])
	}
	if next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"] != "/tmp/brave" {
		t.Fatalf("expected PLAYWRIGHT_MCP_EXECUTABLE_PATH to remain unchanged in auto mode, got %q", next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"])
	}
	if next["OTHER_ENV"] != "keep" {
		t.Fatalf("expected unrelated env var to remain, got %q", next["OTHER_ENV"])
	}
}

func TestResolvePlaywrightEnv_WebkitWithoutExecutable(t *testing.T) {
	next := resolvePlaywrightEnv(nil, config.UtilitySettings{
		PlaywrightBrowser: "webkit",
	})

	if next["PLAYWRIGHT_MCP_BROWSER"] != "webkit" {
		t.Fatalf("expected PLAYWRIGHT_MCP_BROWSER=webkit, got %q", next["PLAYWRIGHT_MCP_BROWSER"])
	}
	if _, ok := next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"]; ok {
		t.Fatalf("did not expect executable path for webkit, got %q", next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"])
	}
}

func TestResolvePlaywrightEnv_BraveUsesChromeAndExecutable(t *testing.T) {
	next := resolvePlaywrightEnv(nil, config.UtilitySettings{
		PlaywrightBrowser:    "brave",
		PlaywrightExecutable: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	})

	if next["PLAYWRIGHT_MCP_BROWSER"] != "chrome" {
		t.Fatalf("expected PLAYWRIGHT_MCP_BROWSER=chrome for brave mode, got %q", next["PLAYWRIGHT_MCP_BROWSER"])
	}
	if next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"] != "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" {
		t.Fatalf("expected executable path to be preserved, got %q", next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"])
	}
}
