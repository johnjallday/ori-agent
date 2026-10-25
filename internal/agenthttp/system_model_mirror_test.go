package agenthttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// mirrorSystemModelWriter records what an agent edit writes to Settings →
// System Model.
type mirrorSystemModelWriter struct {
	provider, model, effort      string
	saves                        int
	modelErr, effortErr, saveErr error
}

func (w *mirrorSystemModelWriter) GetSystemModel() (string, string) { return w.provider, w.model }

func (w *mirrorSystemModelWriter) SetSystemModel(provider, model string) error {
	if w.modelErr != nil {
		return w.modelErr
	}
	w.provider, w.model = provider, model
	return nil
}

func (w *mirrorSystemModelWriter) SetSystemReasoningEffort(effort string) error {
	if w.effortErr != nil {
		return w.effortErr
	}
	w.effort = effort
	return nil
}

func (w *mirrorSystemModelWriter) Save() error {
	w.saves++
	return w.saveErr
}

type mirrorProviders map[string]bool

func (p mirrorProviders) GetProvider(name string) (llm.Provider, error) {
	if p[name] {
		return nil, nil
	}
	return nil, fmt.Errorf("provider %q is not registered", name)
}

// mirrorHandler builds a handler with the hired assistant "Atlas" (HQ built),
// the protected system assistant on the current System Model, an unrelated
// agent, and a recording System Model writer that knows codex and claude.
func mirrorHandler(t *testing.T) (*Handler, *mirrorSystemModelWriter) {
	t.Helper()
	h := guardTestHandler(t, []string{"Atlas", "Unrelated"}, nil)
	if err := ensureSystemAssistantAgentWithSystemModel(h.State, "codex", "gpt-6-astra"); err != nil {
		t.Fatalf("ensure system assistant: %v", err)
	}
	h.SetPersonalAssistantSupport(guardStateReader{projection: &personalassistant.Projection{
		State: personalassistant.APIStateActive, AssistantID: "assistant-1", DisplayName: "Atlas",
		GlobalAgentProfile: "Atlas", HQWorkspaceID: "hq-1", StateVersion: 3,
	}}, guardUserProvider{userID: userprofile.LocalUserID})
	writer := &mirrorSystemModelWriter{provider: "codex", model: "gpt-6-astra", effort: "medium"}
	h.SetSystemModelWriter(writer, mirrorProviders{"codex": true, "claude": true})
	return h, writer
}

func patchAgentJSON(t *testing.T, h *Handler, name, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPatch, "/api/agents/"+url.PathEscape(name), strings.NewReader(body)))
	decoded := map[string]any{}
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s: %v", rr.Body.String(), err)
		}
	}
	return rr, decoded
}

func TestSystemModelMirror_HiredAssistantEditSetsSystemModel(t *testing.T) {
	h, writer := mirrorHandler(t)

	rr, body := patchAgentJSON(t, h, "Atlas", `{"model":"claude-sonnet-5-5","llm_provider":"claude"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if writer.provider != "claude" || writer.model != "claude-sonnet-5-5" || writer.effort != "" || writer.saves != 1 {
		t.Fatalf("system model = %s/%s effort=%q saves=%d, want claude/claude-sonnet-5-5 with no effort, saved once",
			writer.provider, writer.model, writer.effort, writer.saves)
	}
	mirror, _ := body["system_model"].(map[string]any)
	if mirror["mirrored"] != true || mirror["provider"] != "claude" || mirror["model"] != "claude-sonnet-5-5" {
		t.Fatalf("system_model in response = %v", body["system_model"])
	}
	// The protected system assistant follows, as it does on a Settings save.
	ori, ok := h.State.GetAgent(systemAssistantAgentName)
	if !ok || ori.Settings.Provider != "claude" || ori.Settings.Model != "claude-sonnet-5-5" {
		t.Fatalf("system assistant = %+v, want claude/claude-sonnet-5-5", ori.Settings)
	}
}

func TestSystemModelMirror_SystemAssistantEditCarriesCodexEffort(t *testing.T) {
	h, writer := mirrorHandler(t)

	rr, _ := patchAgentJSON(t, h, systemAssistantAgentName, `{"model":"gpt-6-astra","llm_provider":"codex","reasoning_effort":"high"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if writer.provider != "codex" || writer.model != "gpt-6-astra" || writer.effort != "high" || writer.saves != 1 {
		t.Fatalf("system model = %s/%s effort=%q saves=%d, want codex/gpt-6-astra at high, saved once",
			writer.provider, writer.model, writer.effort, writer.saves)
	}
}

func TestSystemModelMirror_UnrelatedAgentLeavesSystemModelAlone(t *testing.T) {
	h, writer := mirrorHandler(t)

	rr, body := patchAgentJSON(t, h, "Unrelated", `{"model":"claude-sonnet-5-5","llm_provider":"claude"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if writer.provider != "codex" || writer.model != "gpt-6-astra" || writer.saves != 0 {
		t.Fatalf("system model changed to %s/%s (saves=%d) by an unrelated agent", writer.provider, writer.model, writer.saves)
	}
	if _, reported := body["system_model"]; reported {
		t.Fatalf("unrelated edit reported a system model: %v", body["system_model"])
	}
}

func TestSystemModelMirror_NonModelEditDoesNotMirror(t *testing.T) {
	h, writer := mirrorHandler(t)

	rr, body := patchAgentJSON(t, h, "Atlas", `{"description":"keeps my week in order"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if writer.saves != 0 {
		t.Fatalf("a description edit saved the system model %d time(s)", writer.saves)
	}
	if _, reported := body["system_model"]; reported {
		t.Fatalf("description edit reported a system model: %v", body["system_model"])
	}
}

func TestSystemModelMirror_UnavailableProviderIsReportedNotWritten(t *testing.T) {
	h, writer := mirrorHandler(t)

	rr, body := patchAgentJSON(t, h, "Atlas", `{"model":"gemma4:26b","llm_provider":"ollama"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	// The agent edit itself lands; only the mirror is declined.
	if ag, ok := h.State.GetAgent("Atlas"); !ok || ag.Settings.Model != "gemma4:26b" {
		t.Fatalf("agent edit did not save: ok=%v settings=%+v", ok, ag)
	}
	if writer.provider != "codex" || writer.saves != 0 {
		t.Fatalf("system model written for an unavailable provider: %s/%s saves=%d", writer.provider, writer.model, writer.saves)
	}
	mirror, _ := body["system_model"].(map[string]any)
	reason, _ := mirror["reason"].(string)
	if mirror["mirrored"] != false || !strings.Contains(reason, `"ollama"`) {
		t.Fatalf("system_model in response = %v", body["system_model"])
	}
}

func TestSystemModelMirror_PartialFailuresKeepProfileSaveDistinct(t *testing.T) {
	for _, stage := range []string{"model", "reasoning", "save"} {
		t.Run(stage, func(t *testing.T) {
			h, writer := mirrorHandler(t)
			failure := errors.New("controlled " + stage + " failure")
			switch stage {
			case "model":
				writer.modelErr = failure
			case "reasoning":
				writer.effortErr = failure
			case "save":
				writer.saveErr = failure
			}
			rr, body := patchAgentJSON(t, h, "Atlas", `{"model":"profile-alias","llm_provider":"claude"}`)
			if rr.Code != http.StatusOK {
				t.Fatal(rr.Body.String())
			}
			profile, _ := h.State.GetAgent("Atlas")
			if profile.Settings.Model != "profile-alias" {
				t.Fatal("profile save was lost")
			}
			mirror := body["system_model"].(map[string]any)
			if mirror["mirrored"] != false || mirror["reason"] != failure.Error() {
				t.Fatalf("partial result: %v", mirror)
			}
			// No rollback is claimed: the existing writer can have changed memory
			// before persistence failed. The UI must reread, not say 'unchanged'.
			want := "profile-alias"
			if stage == "model" {
				want = "gpt-6-astra"
			}
			if writer.model != want {
				t.Fatalf("effective readback = %q, want %q", writer.model, want)
			}
		})
	}
}

type rejectedProfileStore struct{ store.Store }

func (rejectedProfileStore) SetAgent(string, *agent.Agent) error {
	return errors.New("controlled profile write failure")
}

func TestSystemModelMirror_ProfileSaveFailureNeverMirrors(t *testing.T) {
	h, writer := mirrorHandler(t)
	h.State = rejectedProfileStore{h.State}
	rr, body := patchAgentJSON(t, h, "Atlas", `{"model":"profile-alias","llm_provider":"claude"}`)
	if rr.Code == http.StatusOK || writer.saves != 0 || writer.model != "gpt-6-astra" || body["system_model"] != nil {
		t.Fatalf("failed profile save touched default: status=%d body=%v writer=%+v", rr.Code, body, writer)
	}
}

func TestSystemModelMirror_DetailReportsLinkedAgents(t *testing.T) {
	h, _ := mirrorHandler(t)

	for name, want := range map[string]bool{"Atlas": true, systemAssistantAgentName: true, "Unrelated": false} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents/"+url.PathEscape(name), nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body=%s", name, rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got, _ := body["system_model_linked"].(bool); got != want {
			t.Errorf("%s system_model_linked = %v, want %v", name, got, want)
		}
	}

	// The Agents page modal reads the dashboard detail; it carries the same flag.
	dash := NewDashboardHandler(h.State)
	dash.SetPersonalAssistantSupport(h.support.reader, h.support.provider)
	rr := httptest.NewRecorder()
	dash.GetAgentDetail(rr, httptest.NewRequest(http.MethodGet, "/api/agents/Atlas/detail", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail["system_model_linked"] != true {
		t.Fatalf("dashboard detail system_model_linked = %v, want true", detail["system_model_linked"])
	}
}
