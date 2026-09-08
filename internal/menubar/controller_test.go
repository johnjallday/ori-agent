package menubar

import (
	"context"
	"strings"
	"testing"
)

func TestControllerOldStartWaiterCannotReportOrOverwriteNewRuntime(t *testing.T) {
	for _, status := range []ServerStatus{StatusStarting, StatusRunning, StatusStopping, StatusStopped} {
		t.Run(status.String(), func(t *testing.T) {
			controller := NewController(0)
			controller.generation = 2
			controller.status = status
			controller.errorMsg = "current runtime evidence"
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // Deterministically wake an older waiter without real timers or ports.
			if err := controller.waitForRunning(ctx, 1); err == nil || !strings.Contains(err.Error(), "superseded") {
				t.Fatal("old waiter accepted the wrong runtime:", err)
			}
			if controller.GetStatus() != status || controller.GetErrorMessage() != "current runtime evidence" {
				t.Fatal("old waiter overwrote a newer lifecycle")
			}
		})
	}
}

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
