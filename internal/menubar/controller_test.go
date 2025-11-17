package menubar

import (
	"testing"
)

func TestControllerPortConfiguration(t *testing.T) {
	// Create controller with default port
	controller := NewController(8765)

	// Test getting initial port
	if port := controller.GetPort(); port != 8765 {
		t.Errorf("Expected initial port 8765, got %d", port)
	}

	// Test setting port when server is stopped
	newPort := 9000
	if err := controller.SetPort(newPort); err != nil {
		t.Errorf("Failed to set port when server is stopped: %v", err)
	}

	// Verify port was updated
	if port := controller.GetPort(); port != newPort {
		t.Errorf("Expected port %d after setting, got %d", newPort, port)
	}
}

func TestControllerSetPortWhileRunning(t *testing.T) {
	controller := NewController(8765)

	// Simulate server running state
	controller.statusMu.Lock()
	controller.status = StatusRunning
	controller.statusMu.Unlock()

	// Try to set port while running (should fail)
	if err := controller.SetPort(9000); err == nil {
		t.Error("Expected error when setting port while server is running, got nil")
	}

	// Reset to stopped
	controller.statusMu.Lock()
	controller.status = StatusStopped
	controller.statusMu.Unlock()

	// Now it should work
	if err := controller.SetPort(9000); err != nil {
		t.Errorf("Failed to set port when server is stopped: %v", err)
	}
}
