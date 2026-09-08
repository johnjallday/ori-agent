package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestResetPreviewValidatesOptionsBeforeReadingRuntime(t *testing.T) {
	calls := 0
	h := NewResetHandler(nil, nil, "")
	h.SetPreviewPlanner(settingsreset.NewPlanner(func() settingsreset.Owners { calls++; return settingsreset.Owners{} }))
	for _, query := range []string{
		"", "category=agents", "intent=selected_data", "settings=false", "settings=1",
		"intent=start_fresh&category=agents", "intent=selected_data&category=plugins",
		"intent=selected_data&category=agents&category=agents", "agents=true&agents=false",
		"intent=selected_data&intent=start_fresh&category=agents", "settings=true&category=agents",
		"settings=true&target=/outside", "settings=true&data_dir=/outside", "settings=true&%invalid",
		"settings=true&unused=" + strings.Repeat("x", maxResetRequestSize),
	} {
		t.Run(query[:min(65, len(query))], func(t *testing.T) {
			w := httptest.NewRecorder()
			h.GetResetPreview(w, httptest.NewRequest(http.MethodGet, "/api/reset/preview?"+query, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("preview error was cacheable")
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid input reached runtime inspection")
	}
	w := httptest.NewRecorder()
	h.GetResetPreview(w, httptest.NewRequest(http.MethodPost, "/api/reset/preview?settings=true", nil))
	if w.Code != http.StatusMethodNotAllowed || calls != 0 {
		t.Fatal("preview accepted a mutating verb")
	}
}

func TestResetPreviewParsesStartFreshWithoutClientCategories(t *testing.T) {
	intent, selected, err := resetPreviewSelection("intent=start_fresh")
	if err != nil || intent != settingsreset.IntentStartFresh || len(selected) != 0 {
		t.Fatalf("Start Fresh client selection = %q %v, %v", intent, selected, err)
	}
	h := NewResetHandler(nil, nil, "")
	h.SetPreviewPlanner(settingsreset.NewPlanner(func() settingsreset.Owners { return settingsreset.Owners{} }))
	w := httptest.NewRecorder()
	h.GetResetPreview(w, httptest.NewRequest(http.MethodGet, "/api/reset/preview?intent=start_fresh", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Start Fresh HTTP preview status = %d: %s", w.Code, w.Body.String())
	}
}

func TestResetPreviewRealHTTPReportsCountsAndBlockersWithoutMutation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	agents := &fakeAgentStore{}
	h := NewResetHandler(nil, agents, p.DataDir)
	h.SetPreviewPlanner(settingsreset.NewPlanner(func() settingsreset.Owners {
		// Deliberately missing lifecycle/other owners: show counts but block
		// confirmation, rather than inventing a working runtime or empty store.
		return settingsreset.Owners{DataDir: p.DataDir, Database: db, Agents: agents}
	}))
	before, err := os.ReadFile(filepath.Join(p.DataDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := f.StartHTTP(t, http.HandlerFunc(h.GetResetPreview))
	response, err := server.Do(t.Context(), http.MethodGet, "/api/reset/preview?intent=selected_data&category=app_records", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var preview settingsreset.Preview
	if err := json.NewDecoder(response.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || len(preview.Categories) != 1 {
		t.Fatal("missing read-only preview contract")
	}
	if !slices.ContainsFunc(preview.Blockers, func(item settingsreset.Blocker) bool { return item.Code == "lifecycle_unavailable" }) {
		t.Fatal("unsupported host was presented as safe to reset")
	}
	factIndex := slices.IndexFunc(preview.Categories[0].Facts, func(fact settingsreset.CountFact) bool { return fact.Name == "sessions" })
	if factIndex < 0 || preview.Categories[0].Facts[factIndex].Count == nil || *preview.Categories[0].Facts[factIndex].Count != 1 {
		t.Fatal("real session count was not returned")
	}
	after, err := os.ReadFile(filepath.Join(p.DataDir, "settings.json"))
	if err != nil || !bytes.Equal(before, after) || agents.clearCalls != 0 {
		t.Fatal("preview changed settings or agents:", err)
	}
	f.AssertPreserved(t)
}

func TestResetPreviewLegacyAllOptionsDoNotBecomeStartFresh(t *testing.T) {
	intent, categories, err := resetPreviewSelection("settings=true&agents=true&sessions=true&onboarding=true")
	if err != nil {
		t.Fatal(err)
	}
	if intent != settingsreset.IntentSelectedData || len(categories) != 4 || !slices.Contains(categories, settingsreset.CategoryAppRecords) || !slices.Contains(categories, settingsreset.CategorySetupSteps) {
		t.Fatal("legacy options were broadened or renamed incorrectly")
	}
}
