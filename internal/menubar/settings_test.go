package menubar

import (
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func TestPortConfiguration(t *testing.T) {
	// Create temp file for testing
	tmpFile := "/tmp/test_app_state_port.json"
	defer os.Remove(tmpFile)

	// Create managers
	mgr := onboarding.NewManager(tmpFile)
	settingsMgr := NewSettingsManager(mgr)

	// Test default port
	port := settingsMgr.GetPort()
	if port != 8765 {
		t.Errorf("Expected default port 8765, got %d", port)
	}

	// Test setting port
	newPort := 9000
	if err := settingsMgr.SetPort(newPort); err != nil {
		t.Fatalf("Failed to set port: %v", err)
	}

	// Test reading back
	port = settingsMgr.GetPort()
	if port != newPort {
		t.Errorf("Expected port %d, got %d", newPort, port)
	}

	// Create a new manager to test persistence
	mgr2 := onboarding.NewManager(tmpFile)
	settingsMgr2 := NewSettingsManager(mgr2)
	port = settingsMgr2.GetPort()
	if port != newPort {
		t.Errorf("Port not persisted correctly. Expected %d, got %d", newPort, port)
	}
}
