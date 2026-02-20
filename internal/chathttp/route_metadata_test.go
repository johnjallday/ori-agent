package chathttp

import "testing"

func TestAttachRouteMetadata(t *testing.T) {
	payload := attachRouteMetadata(map[string]any{
		"response": "ok",
	}, chatRouteMetadata{
		Mode:      "utility_direct",
		ToolName:  "time",
		Provider:  "system-clock",
		Reason:    "matched time utility intent",
		ToolCount: 1,
	})

	if mode, _ := payload["route_mode"].(string); mode != "utility_direct" {
		t.Fatalf("expected route_mode utility_direct, got %v", payload["route_mode"])
	}

	route, ok := payload["route"].(map[string]any)
	if !ok {
		t.Fatalf("expected route object, got %T", payload["route"])
	}
	if route["mode"] != "utility_direct" {
		t.Fatalf("expected route.mode utility_direct, got %v", route["mode"])
	}
	if route["tool_name"] != "time" {
		t.Fatalf("expected route.tool_name time, got %v", route["tool_name"])
	}
	if route["provider"] != "system-clock" {
		t.Fatalf("expected route.provider system-clock, got %v", route["provider"])
	}
}

func TestInferUtilityProvider(t *testing.T) {
	if got := inferUtilityProvider("time", `{"timezone":"Asia/Tokyo"}`); got != "system-clock" {
		t.Fatalf("expected system-clock for time tool, got %q", got)
	}
	if got := inferUtilityProvider("weather", `{"source":"open-meteo.com"}`); got != "open-meteo.com" {
		t.Fatalf("expected open-meteo.com from payload source, got %q", got)
	}
	if got := inferUtilityProvider("web_search", `{}`); got == "" {
		t.Fatalf("expected non-empty fallback provider for web_search")
	}
}

func TestIsNativeUtilityToolName(t *testing.T) {
	if !isNativeUtilityToolName("weather") {
		t.Fatalf("expected weather to be recognized as native utility tool")
	}
	if isNativeUtilityToolName("git") {
		t.Fatalf("expected git to not be recognized as native utility tool")
	}
}
