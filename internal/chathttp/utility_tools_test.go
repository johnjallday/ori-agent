package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNewUtilityToolRegistry_RegistersExpectedTools(t *testing.T) {
	registry := NewUtilityToolRegistry(
		UtilityAdapters{Time: SystemTimeAdapter{}},
		DefaultUtilityCallPolicy(),
	)

	defs := registry.ListToolDefinitions()
	if len(defs) != 5 {
		t.Fatalf("expected 5 utility tools, got %d", len(defs))
	}

	expected := map[string]bool{
		"time":       true,
		"weather":    true,
		"web_search": true,
		"web_fetch":  true,
		"browser":    true,
	}
	for _, def := range defs {
		delete(expected, def.Name)
	}
	if len(expected) > 0 {
		t.Fatalf("missing expected utility tools: %v", expected)
	}
}

func TestUtilityToolRegistry_GetTool(t *testing.T) {
	registry := NewDefaultUtilityToolRegistry()

	if _, ok := registry.GetTool("time"); !ok {
		t.Fatalf("expected time tool to exist")
	}
	if _, ok := registry.GetTool("does_not_exist"); ok {
		t.Fatalf("did not expect unknown tool to exist")
	}
}

func TestUtilityTimeTool_CallSuccess(t *testing.T) {
	registry := NewDefaultUtilityToolRegistry()
	tool, ok := registry.GetTool("time")
	if !ok {
		t.Fatalf("expected time tool to exist")
	}

	result, err := tool.Call(context.Background(), `{"timezone":"Asia/Tokyo"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed TimeResponse
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse time tool response: %v", err)
	}

	if parsed.Timezone != "Asia/Tokyo" {
		t.Fatalf("expected timezone Asia/Tokyo, got %q", parsed.Timezone)
	}
	if parsed.LocalTime == "" || parsed.ISOTime == "" || parsed.Unix == 0 {
		t.Fatalf("expected populated time fields, got %+v", parsed)
	}
}

func TestUtilityTimeTool_InvalidTimezone(t *testing.T) {
	registry := NewDefaultUtilityToolRegistry()
	tool, ok := registry.GetTool("time")
	if !ok {
		t.Fatalf("expected time tool to exist")
	}

	_, err := tool.Call(context.Background(), `{"timezone":"Mars/Base"}`)
	if err == nil {
		t.Fatalf("expected error for invalid timezone")
	}
	if !errors.Is(err, ErrUtilityInvalidInput) {
		t.Fatalf("expected ErrUtilityInvalidInput, got %v", err)
	}
}

func TestUtilityWeatherTool_ProviderUnavailable(t *testing.T) {
	registry := NewUtilityToolRegistry(UtilityAdapters{}, DefaultUtilityCallPolicy())
	tool, ok := registry.GetTool("weather")
	if !ok {
		t.Fatalf("expected weather tool to exist")
	}

	_, err := tool.Call(context.Background(), `{"location":"San Francisco, CA"}`)
	if err == nil {
		t.Fatalf("expected provider unavailable error")
	}
	if !errors.Is(err, ErrUtilityProviderUnavailable) {
		t.Fatalf("expected ErrUtilityProviderUnavailable, got %v", err)
	}
}

func TestExecuteUtilityCall_RetryOnTransientError(t *testing.T) {
	policy := UtilityCallPolicy{
		Timeout:       2 * time.Second,
		RetryAttempts: 1,
		RetryDelay:    1 * time.Millisecond,
	}

	calls := 0
	got, err := executeUtilityCall(context.Background(), policy, func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("transient failure")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected result ok, got %q", got)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestExecuteUtilityCall_NoRetryOnWrappedInvalidInput(t *testing.T) {
	policy := UtilityCallPolicy{
		Timeout:       2 * time.Second,
		RetryAttempts: 3,
		RetryDelay:    1 * time.Millisecond,
	}

	calls := 0
	_, err := executeUtilityCall(context.Background(), policy, func(context.Context) (string, error) {
		calls++
		return "", fmt.Errorf("%w: bad args", ErrUtilityInvalidInput)
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrUtilityInvalidInput) {
		t.Fatalf("expected wrapped ErrUtilityInvalidInput, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call for invalid input, got %d", calls)
	}
}

func TestNormalizeUtilityError_Deadline(t *testing.T) {
	err := normalizeUtilityError(context.DeadlineExceeded)
	if err == nil || err.Error() != "utility request timed out" {
		t.Fatalf("expected timeout normalization, got %v", err)
	}
}
