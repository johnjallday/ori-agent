package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postJSONTest posts a JSON body against the real handler and decodes the
// response into a generic map, failing the test on transport/decode errors
// only — callers assert on the status code and body themselves.
func postJSONTest(t *testing.T, handler http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s response: %v body=%s", path, err, rec.Body.String())
		}
	}
	return rec.Code, decoded
}

func getJSONTest(t *testing.T, handler http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s response: %v body=%s", path, err, rec.Body.String())
		}
	}
	return rec.Code, decoded
}

// TestOnboardingReset_PreservesNeedsHQRelationshipAndResumesTheQuest covers
// task 5.3: an onboarding reset must restart the onboarding WIZARD only. The
// durable relationship — and the fact that it still has no Personal HQ — must
// survive untouched, so a returning user resumes the guided quest rather than
// getting hired a second time.
func TestOnboardingReset_PreservesNeedsHQRelationshipAndResumesTheQuest(t *testing.T) {
	_, handler := newDailyBriefTestServer(t)

	// Hire, landing in needs_hq.
	status, hireBody := postJSONTest(t, handler, "/api/personal-assistant/hire",
		`{"request_id":"compat-hire-1","if_version":0,"display_name":"Atlas","mandate":"Keep this week honest.","focus_areas":["plan_my_day"]}`)
	if status != http.StatusCreated {
		t.Fatalf("hire status = %d body=%v", status, hireBody)
	}
	assistant, ok := hireBody["personal_assistant"].(map[string]any)
	if !ok || assistant["state"] != "needs_hq" {
		t.Fatalf("hire result = %v", hireBody)
	}
	assistantID := assistant["assistant_id"]

	// Reset onboarding — this must touch only the wizard, never the relationship.
	if status, _ := postJSONTest(t, handler, "/api/onboarding/reset", ""); status != http.StatusOK {
		t.Fatalf("onboarding reset status = %d", status)
	}

	// Onboarding must genuinely be reset.
	if status, onboarding := getJSONTest(t, handler, "/api/onboarding/status"); status != http.StatusOK || onboarding["needs_onboarding"] != true {
		t.Fatalf("onboarding status after reset = %d %v", status, onboarding)
	}

	// The relationship must be exactly as it was: same assistant, still needs_hq.
	status, afterReset := getJSONTest(t, handler, "/api/personal-assistant")
	if status != http.StatusOK {
		t.Fatalf("personal-assistant status = %d", status)
	}
	afterAssistant, ok := afterReset["personal_assistant"].(map[string]any)
	if !ok {
		t.Fatalf("personal-assistant response = %v", afterReset)
	}
	if afterAssistant["state"] != "needs_hq" || afterAssistant["next_action"] != "build_hq" {
		t.Fatalf("relationship after reset = %v", afterAssistant)
	}
	if afterAssistant["assistant_id"] != assistantID {
		t.Fatalf("reset changed the stable assistant id: before=%v after=%v", assistantID, afterAssistant["assistant_id"])
	}

	// A same-request-ID hire retry after the reset must resume — not fork a
	// second profile or relationship.
	status, replay := postJSONTest(t, handler, "/api/personal-assistant/hire",
		`{"request_id":"compat-hire-1","if_version":0,"display_name":"Atlas","mandate":"Keep this week honest.","focus_areas":["plan_my_day"]}`)
	if status != http.StatusOK {
		t.Fatalf("hire replay after reset status = %d body=%v", status, replay)
	}
	replayAssistant, _ := replay["personal_assistant"].(map[string]any)
	if replayAssistant["assistant_id"] != assistantID || replayAssistant["resumed"] != true {
		t.Fatalf("hire replay after reset forked identity: %v", replayAssistant)
	}
}

// TestAbandonedWalkthrough_LeavesResumableStateNotAssumedCompletion covers
// task 5.3: visiting the guided route, or abandoning it entirely (equivalent
// to disabling JavaScript, since the walkthrough is presentation-only), must
// never itself complete or skip the quest. Only a real designation does that.
func TestAbandonedWalkthrough_LeavesResumableStateNotAssumedCompletion(t *testing.T) {
	_, handler := newDailyBriefTestServer(t)

	if status, _ := postJSONTest(t, handler, "/api/personal-assistant/hire",
		`{"request_id":"compat-hire-2","if_version":0,"display_name":"Atlas","mandate":"Keep this week honest.","focus_areas":["plan_my_day"]}`); status != http.StatusCreated {
		t.Fatalf("hire status = %d", status)
	}

	// A plain page load of the guided route (no JS execution at all, since this
	// is a server-side GET) must be side-effect-free.
	req := httptest.NewRequest(http.MethodGet, "/?quest=build-hq", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("guided route status = %d", rec.Code)
	}

	// Repeated reads (simulating a user reloading, or a JS-disabled browser that
	// never ran the walkthrough at all) must never change the relationship.
	for i := range 3 {
		status, body := getJSONTest(t, handler, "/api/personal-assistant")
		if status != http.StatusOK {
			t.Fatalf("read %d status = %d", i, status)
		}
		assistant, _ := body["personal_assistant"].(map[string]any)
		if assistant["state"] != "needs_hq" || assistant["next_action"] != "build_hq" {
			t.Fatalf("read %d unexpectedly changed relationship state: %v", i, assistant)
		}
	}

	status, progression := getJSONTest(t, handler, "/api/progression")
	if status != http.StatusOK {
		t.Fatalf("progression status = %d", status)
	}
	found := false
	for _, tier := range asSlice(progression["tiers"]) {
		for _, quest := range asSlice(asMap(tier)["quests"]) {
			q := asMap(quest)
			if q["id"] == "t2-build-hq" {
				found = true
				if q["status"] == "completed" {
					t.Fatal("merely visiting the guided route completed Build My HQ")
				}
			}
		}
	}
	if !found {
		t.Fatal("t2-build-hq missing from progression")
	}
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
