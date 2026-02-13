package mcp

import (
	"testing"
	"time"
)

func TestResolveMCPHealthCheckInterval_Default(t *testing.T) {
	t.Setenv(mcpHealthCheckIntervalEnvVar, "")

	got := resolveMCPHealthCheckInterval()
	if got != defaultMCPHealthCheckInterval {
		t.Fatalf("expected default interval %v, got %v", defaultMCPHealthCheckInterval, got)
	}
}

func TestResolveMCPHealthCheckInterval_DurationSyntax(t *testing.T) {
	t.Setenv(mcpHealthCheckIntervalEnvVar, "5m")

	got := resolveMCPHealthCheckInterval()
	if got != 5*time.Minute {
		t.Fatalf("expected 5m interval, got %v", got)
	}
}

func TestResolveMCPHealthCheckInterval_SecondsSyntax(t *testing.T) {
	t.Setenv(mcpHealthCheckIntervalEnvVar, "120")

	got := resolveMCPHealthCheckInterval()
	if got != 120*time.Second {
		t.Fatalf("expected 120s interval, got %v", got)
	}
}

func TestResolveMCPHealthCheckInterval_InvalidFallsBack(t *testing.T) {
	t.Setenv(mcpHealthCheckIntervalEnvVar, "not-a-duration")

	got := resolveMCPHealthCheckInterval()
	if got != defaultMCPHealthCheckInterval {
		t.Fatalf("expected default interval for invalid value, got %v", got)
	}
}

func TestResolveMCPHealthCheckInterval_NonPositiveFallsBack(t *testing.T) {
	t.Setenv(mcpHealthCheckIntervalEnvVar, "0")

	got := resolveMCPHealthCheckInterval()
	if got != defaultMCPHealthCheckInterval {
		t.Fatalf("expected default interval for non-positive value, got %v", got)
	}
}
