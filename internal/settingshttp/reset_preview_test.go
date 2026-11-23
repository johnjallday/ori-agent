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
		"sessions=true&archive_personal_hq=true", "intent=replay_setup&archive_personal_hq=true",
		"intent=selected_data&category=agents&archive_personal_hq=false",
		"intent=start_fresh&archive_personal_hq=true&archive_personal_hq=true",
		"intent=start_fresh&archive_personal_hq=1", "intent=start_fresh&archive_source=/outside",
		"intent=start_fresh&archive_destination=/outside",
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
	intent, selected, _, err := resetPreviewSelection("intent=start_fresh")
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

func TestResetPreviewArchiveIntentIsExplicitAndScoped(t *testing.T) {
	h := NewResetHandler(nil, nil, "")
	h.SetPreviewPlanner(settingsreset.NewPlanner(func() settingsreset.Owners { return settingsreset.Owners{} }))
	for _, intent := range []string{"start_fresh", "selected_data&category=app_records"} {
		for _, option := range []string{"", "&archive_personal_hq=false", "&archive_personal_hq=true"} {
			w := httptest.NewRecorder()
			h.GetResetPreview(w, httptest.NewRequest(http.MethodGet, "/api/reset/preview?intent="+intent+option, nil))
			var preview settingsreset.Preview
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.HQArchive == nil ||
				preview.HQArchive.Requested != (option == "&archive_personal_hq=true") || preview.HQArchive.Source != "" || preview.HQArchive.UnavailableReason == "" {
				t.Fatalf("preview = %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestArchiveExecuteAcceptsOnlyTheScopeWitness(t *testing.T) {
	base := `{"preview_id":"p","request_id":"r","confirmation":"RESET"`
	witness := strings.Repeat("a", 64)
	req, legacy, err := decodeResetRequest(strings.NewReader(base + `,"confirm_hq_archive":"` + witness + `"}`))
	if err != nil || legacy || req.ConfirmHQArchive != witness {
		t.Fatalf("reviewed confirmation = %+v, legacy %t, err %v", req, legacy, err)
	}
	for _, extra := range []string{`"archive_personal_hq":true`, `"source":"/a"`, `"destination":"/b"`,
		`"confirm_hq_archive":true`, `"confirm_hq_archive":"/path"`, `"confirm_hq_archive":null`,
		`"confirm_hq_archive":"` + witness + `","confirm_hq_archive":"` + witness + `"`} {
		if _, _, err := decodeResetRequest(strings.NewReader(base + "," + extra + "}")); err == nil {
			t.Fatalf("confirmed request accepted extraneous or invalid field %s", extra)
		}
	}
}

func TestResetPreviewLegacyAllOptionsDoNotBecomeStartFresh(t *testing.T) {
	intent, categories, _, err := resetPreviewSelection("settings=true&agents=true&sessions=true&onboarding=true")
	if err != nil {
		t.Fatal(err)
	}
	if intent != settingsreset.IntentSelectedData || len(categories) != 4 || !slices.Contains(categories, settingsreset.CategoryAppRecords) || !slices.Contains(categories, settingsreset.CategorySetupSteps) {
		t.Fatal("legacy options were broadened or renamed incorrectly")
	}
	// The legacy boolean form has no plugin member and must not acquire one: it
	// normalizes only to the four families it always meant.
	if slices.Contains(categories, settingsreset.CategoryInstalledPlugins) {
		t.Fatal("legacy options silently gained the installed plugins category")
	}
}

func TestResetPreviewAcceptsInstalledPluginsThroughTheHeldPreviewAPI(t *testing.T) {
	intent, categories, _, err := resetPreviewSelection("intent=selected_data&category=installed_plugins")
	if err != nil || intent != settingsreset.IntentSelectedData ||
		!slices.Equal(categories, []settingsreset.CategoryID{settingsreset.CategoryInstalledPlugins}) {
		t.Fatalf("plugin-only selection = %q %v, %v", intent, categories, err)
	}
	_, five, _, err := resetPreviewSelection(
		"intent=selected_data&category=settings&category=agents&category=app_records&category=setup_steps&category=installed_plugins")
	if err != nil || len(five) != 5 {
		t.Fatalf("five-category selection = %v, %v", five, err)
	}
	// POST authority is not broadened: categories still travel only on the
	// preview query, never in the confirmation body.
	for _, body := range []string{
		`{"preview_id":"p","request_id":"r","confirmation":"RESET","installed_plugins":true}`,
		`{"preview_id":"p","request_id":"r","confirmation":"RESET","category":"installed_plugins"}`,
	} {
		if _, _, err := decodeResetRequest(strings.NewReader(body)); err == nil {
			t.Fatalf("execution accepted a client-supplied category: %s", body)
		}
	}
	h := NewResetHandler(nil, nil, "")
	h.SetPreviewPlanner(settingsreset.NewPlanner(func() settingsreset.Owners { return settingsreset.Owners{} }))
	w := httptest.NewRecorder()
	h.GetResetPreview(w, httptest.NewRequest(http.MethodGet, "/api/reset/preview?intent=selected_data&category=installed_plugins", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("plugin preview status = %d: %s", w.Code, w.Body.String())
	}
	var preview settingsreset.Preview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Categories) != 1 || preview.Categories[0].ID != settingsreset.CategoryInstalledPlugins {
		t.Fatalf("plugin preview did not return the category: %+v", preview.Categories)
	}
	// Without an attached plugin owner the preview blocks rather than claiming a
	// safe empty inventory.
	if !slices.ContainsFunc(preview.Blockers, func(item settingsreset.Blocker) bool { return item.Code == "plugin_owner_unavailable" }) {
		t.Fatalf("a missing plugin owner was presented as safe: %+v", preview.Blockers)
	}
}
