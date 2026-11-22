package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func readFilingExpansion(t *testing.T, h http.Handler) (economy.ExpansionQuote, progression.Status) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/progression", nil))
	var result struct {
		progression.Status
		Quote economy.ExpansionQuote `json:"filing_expansion"`
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("progression status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Quote, result.Status
}

func TestFilingExpansionQuoteFromRealHireAndHQIsReadOnly(t *testing.T) {
	t.Setenv("ORI_ECONOMY_ENABLED", "true")
	b, handler := newDailyBriefTestServer(t)
	if q, _ := readFilingExpansion(t, handler); q.State != economy.ExpansionNeedsHQ || q.EffectiveCost != nil {
		t.Fatalf("before HQ: %+v", q)
	}
	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", rec.Code, rec.Body.String())
	}
	if q, _ := readFilingExpansion(t, handler); q.State != economy.ExpansionNeedsHQ {
		t.Fatalf("hire is not a built HQ: %+v", q)
	}
	setup := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", strings.NewReader(`{"name":"Command Post"}`))
	setup.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, setup)
	if rec.Code >= 300 {
		t.Fatalf("build HQ: %d %s", rec.Code, rec.Body.String())
	}
	for range 2 {
		q, status := readFilingExpansion(t, handler)
		if q.State != economy.ExpansionAffordable || q.BaseCost != 7 || q.EffectiveCost == nil || *q.EffectiveCost != 7 || q.Balance == nil || *q.Balance != 7 {
			t.Fatalf("after building HQ: %+v", q)
		}
		assertFreeFolderMission(t, status)
	}
	if got := craftBalance(t, b); got != 7 {
		t.Fatalf("quote changed balance: %d", got)
	}
	rows, err := b.sessionStore.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("quote created a workspace: %+v %v", rows, err)
	}
	filingID, err := b.sessionHandler.CreateFromTemplate(t.Context(), "My inbox Filing", fileJanitorBlueprintID)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := readFilingExpansion(t, handler)
	row, err := b.sessionStore.GetWorkspace(t.Context(), filingID)
	if err != nil || q.State != economy.ExpansionBuilt || q.EffectiveCost != nil || len(q.Destinations) != 1 ||
		q.Destinations[0].Route != "/workspaces/"+row.FolderSlug+"?panel=file-janitor" {
		t.Fatalf("quote did not find existing Filing: %+v, %v", q, err)
	}
	b.economyService = nil
	if q, _ := readFilingExpansion(t, handler); q.State != economy.ExpansionBuilt || q.Balance != nil {
		t.Fatalf("unknown balance suppressed an existing workspace: %+v", q)
	}
	t.Setenv("ORI_ECONOMY_ENABLED", "false")
	q, status := readFilingExpansion(t, handler)
	if q.State != economy.ExpansionDisabled || q.Balance != nil {
		t.Fatalf("disabled economy: %+v", q)
	}
	assertFreeFolderMission(t, status)
}

func assertFreeFolderMission(t *testing.T, status progression.Status) {
	t.Helper()
	for _, q := range status.Missions {
		if q.ID == progression.ShowFolderQuestID && !q.Locked && q.Status != "completed" && q.ActionURL == progression.ShowFolderActionURL {
			return
		}
	}
	t.Fatalf("expected the unmodified free folder mission, got %+v", status.Missions)
}

func TestExistingFilingDestinationsUsesEvidenceAndLeavesSeveralChoices(t *testing.T) {
	const owner = userprofile.LocalUserID
	source := fakeStarterSource{}
	var rows []session.Workspace
	add := func(id, user string, status session.WorkspaceStatus) *workspace.Workspace {
		rows = append(rows, session.Workspace{ID: id, Name: "Downloads", FolderSlug: id, OwnerUserID: user, Status: status})
		ws := &workspace.Workspace{ID: id, Name: "Downloads", FolderSlug: id, OwnerUserID: user, Status: workspace.WorkspaceStatus(status)}
		source[id] = ws
		return ws
	}
	active := session.WorkspaceStatusActive
	legacy := &workspace.TemplateProvenance{TemplateID: "downloads-janitor", Builtin: true}
	add("legacy-blueprint", owner, active).SetTemplateProvenance(legacy)
	add("configured", owner, active)
	pending := add("legacy-pending", owner, active)
	pending.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "old", DirectoryRequirements: []workspace.DirectoryRequirement{{Key: "downloads-root"}}})
	plain := add("just-a-name", owner, active)
	plain.DirectoryReferences = []workspace.DirectoryReference{{Name: "Downloads", Path: "/tmp/Downloads"}}
	add("other-owner", "someone-else", active).SetTemplateProvenance(legacy)
	add("trashed", owner, session.WorkspaceStatusTrashed).SetTemplateProvenance(legacy)
	configured := func(id string) (bool, error) { return id == "configured", nil }
	matches, err := existingFilingDestinations(rows, source, owner, configured)
	if err != nil || len(matches) != 3 {
		t.Fatalf("matches = %+v, %v", matches, err)
	}
	for i, id := range []string{"legacy-blueprint", "configured", "legacy-pending"} {
		if matches[i].Route != "/workspaces/"+id+"?panel=file-janitor" {
			t.Fatalf("match %d = %+v", i, matches[i])
		}
	}
}
