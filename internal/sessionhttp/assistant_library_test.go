package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLibraryFolderOwners_ForwardsPortableProvenanceWithoutReplacingPrimary(t *testing.T) {
	primary := workspace.NewInMemoryStore()
	folder, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album-3"})
	project.OwnerUserID = "owner"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "reaper-song"})
	project.ProjectPath = "/sandbox/source/Album-3"
	if err := folder.Save(project); err != nil {
		t.Fatal(err)
	}
	primaryRecord := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album-3"})
	primaryRecord.ID = project.ID
	primaryRecord.FolderSlug = project.FolderSlug
	primaryRecord.OwnerUserID = project.OwnerUserID
	// SQLite primary does not carry the portable template provenance.
	if err := primary.Save(primaryRecord); err != nil {
		t.Fatal(err)
	}
	owners := libraryFolderOwners{Store: primary, folders: folder}
	current, err := owners.Get(project.ID)
	if err != nil || current.GetTemplateProvenance() != nil {
		t.Fatalf("primary read must stay authoritative: %v, %+v", err, current)
	}
	portable, err := owners.GetFolderWorkspace(project.ID)
	if err != nil || portable.GetTemplateProvenance() == nil || portable.ProjectPath != project.ProjectPath {
		t.Fatalf("creator needs canonical project provenance: %v, %+v", err, portable)
	}
}

func TestAssistantLibraryHTTP_ThousandSavedProjectsStayPageBounded(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "http-thousand-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootID, scanID := "fixture-root", "fixture-scan"
	fixtureRoot := t.TempDir()
	doc.Roots = append(doc.Roots, projectlibrary.Root{ID: rootID, Path: fixtureRoot,
		FileIdentity: "fixture", Revision: 1, ApprovedAt: now})
	for i := 0; i < 21; i++ {
		doc.Roots = append(doc.Roots, projectlibrary.Root{ID: fmt.Sprintf("other-root-%02d", i),
			Path:         filepath.Join(fixtureRoot, fmt.Sprintf("Approved-%02d", i)),
			FileIdentity: fmt.Sprintf("fixture:%d", i), Revision: 1, ApprovedAt: now})
	}
	doc.Scans = append(doc.Scans, projectlibrary.Scan{ID: scanID, RootID: rootID, RootRevision: 1,
		RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
		Status: "complete", StartedAt: now, FinishedAt: &now})
	for i := 0; i < 1000; i++ {
		fields := projectlibrary.Fields{DisplayName: fmt.Sprintf("Song %04d", i)}
		if i < 40 {
			fields.Status, fields.Revision, fields.Source, fields.UpdatedAt = "active", 1, "test_edit", now
		}
		doc.Entries = append(doc.Entries, projectlibrary.Entry{ID: fmt.Sprintf("entry-%04d", i), Revision: 1,
			Fields: fields, Observations: []projectlibrary.Observation{{RootID: rootID,
				RelativeFolder: fmt.Sprintf("Folder-%04d", i), FileIdentity: fmt.Sprintf("fixture:%d", i),
				Format: "reaper", Availability: "available", ScanID: scanID, ScannedAt: now}}})
	}
	doc.Revision++
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rootPage := func(offset int) map[string]any {
		request := assistantProgramRequest(http.MethodGet, fmt.Sprintf("/library/roots?offset=%d", offset), station.ID, "")
		response := httptest.NewRecorder()
		handler.ListAssistantLibraryRoots(response, request)
		if response.Code != http.StatusOK || response.Body.Len() > 128<<10 {
			t.Fatalf("oversized root status page: %d (%d bytes)", response.Code, response.Body.Len())
		}
		var page map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	firstRoots := rootPage(0)
	lastRoots := rootPage(20)
	if firstRoots["total_roots"] != float64(22) || len(firstRoots["roots"].([]any)) != 20 ||
		firstRoots["next_offset"] != float64(20) || len(lastRoots["roots"].([]any)) != 2 ||
		lastRoots["next_offset"] != float64(0) || lastRoots["revision"] != firstRoots["revision"] {
		t.Fatalf("root pages lost or duplicated grants: first=%+v last=%+v", firstRoots, lastRoots)
	}
	start := time.Now()
	cursor := ""
	count := 0
	for {
		url := "/library/projects?page_size=25&sort=name"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		request := assistantProgramRequest(http.MethodGet, url, station.ID, "")
		result := httptest.NewRecorder()
		handler.SearchAssistantLibrary(result, request)
		if result.Code != http.StatusOK || len(result.Body.Bytes()) > 128<<10 {
			t.Fatalf("oversized/stale HTTP page %d: %d (%d bytes): %s", count, result.Code, result.Body.Len(), result.Body.String())
		}
		var page projectlibrary.SearchPage
		if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil || len(page.Rows) > 25 || page.Total != 1001 {
			t.Fatalf("invalid HTTP page: %+v %v", page, err)
		}
		count += len(page.Rows)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if count > 1001 {
			t.Fatal("pagination repeated a project")
		}
	}
	if count != 1001 {
		t.Fatalf("pagination lost projects: %d", count)
	}
	t.Logf("cold-to-last HTTP pagination: %s for %d saved projects", time.Since(start), count)
}

func TestAssistantLibraryHTTP_DetailStaysUnderWireBudgetWithEscapedSourceEvidence(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "http-large-detail-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("linked catalog fixture: %+v %v", doc, err)
	}
	base := t.TempDir()
	now := time.Now().UTC()
	for i := 0; i < 12; i++ {
		rootID, scanID := fmt.Sprintf("detail-root-%d", i), fmt.Sprintf("detail-scan-%d", i)
		doc.Roots = append(doc.Roots, projectlibrary.Root{ID: rootID, Path: filepath.Join(base, rootID),
			FileIdentity: rootID, Revision: 1, ApprovedAt: now})
		doc.Scans = append(doc.Scans, projectlibrary.Scan{ID: scanID, RootID: rootID, RootRevision: 1,
			RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
			Status: "complete", StartedAt: now, FinishedAt: &now})
		alternates := make([]string, 64)
		for j := range alternates {
			alternates[j] = fmt.Sprintf("%02d-%s", j, strings.Repeat("<", 237))
		}
		doc.Entries[0].Observations = append(doc.Entries[0].Observations, projectlibrary.Observation{
			RootID: rootID, RelativeFolder: "Track-" + strings.Repeat("<", 990),
			FileIdentity: rootID, Format: "reaper", Availability: "available", ScanID: scanID,
			ScannedAt: now, Alternates: alternates})
	}
	doc.Revision++
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(home *workspace.Workspace) error {
		current := home.GetAssistantProgramState()
		current.ProjectLibrary = encoded
		home.SetAssistantProgramState(current)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := assistantProgramRequest(http.MethodGet, "/library/projects/"+doc.Entries[0].ID, station.ID, "")
	request.SetPathValue("entryID", doc.Entries[0].ID)
	response := httptest.NewRecorder()
	handler.GetAssistantLibraryProject(response, request)
	if response.Code != http.StatusOK || response.Body.Len() >= 128<<10 ||
		!strings.Contains(response.Body.String(), `"total_sources":12`) ||
		strings.Contains(response.Body.String(), base) {
		t.Fatalf("unsafe or oversized HTTP detail: %d (%d bytes)", response.Code, response.Body.Len())
	}
}

func TestAssistantLibraryHTTP_ExactOwnerHomeBoundedQueries(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "http-library-owner-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("expected linked project: %+v %v", doc, err)
	}
	entryID := doc.Entries[0].ID
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	search := func(homeID, query string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.SearchAssistantLibrary(response, assistantProgramRequest(http.MethodGet,
			"/library/projects"+query, homeID, ""))
		return response
	}
	page := search(station.ID, "?page_size=1")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"total":1`) ||
		!strings.Contains(page.Body.String(), entryID) || strings.Contains(page.Body.String(), "project_library") {
		t.Fatalf("bounded owner search: %d %s", page.Code, page.Body.String())
	}
	request := assistantProgramRequest(http.MethodGet, "/library/projects/"+entryID, station.ID, "")
	request.SetPathValue("entryID", entryID)
	detail := httptest.NewRecorder()
	handler.GetAssistantLibraryProject(detail, request)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), entryID) ||
		strings.Contains(detail.Body.String(), "project_library") {
		t.Fatalf("bounded owner detail: %d %s", detail.Code, detail.Body.String())
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{})
	if readOnly := search(station.ID, ""); readOnly.Code != http.StatusOK ||
		!strings.Contains(readOnly.Body.String(), `"provider_read_only":true`) {
		t.Fatalf("removed provider did not make library read-only: %d %s", readOnly.Code, readOnly.Body.String())
	}
	missing := assistantProgramRequest(http.MethodGet, "/library/projects/foreign-entry", station.ID, "")
	missing.SetPathValue("entryID", "foreign-entry")
	missingResponse := httptest.NewRecorder()
	handler.GetAssistantLibraryProject(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign entry ID was not hidden: %d %s", missingResponse.Code, missingResponse.Body.String())
	}
	if result := search(project.ID, ""); result.Code != http.StatusNotFound {
		t.Fatalf("linked project route leaked Home catalog: %d %s", result.Code, result.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other-owner", nil }
	if result := search(station.ID, ""); result.Code != http.StatusNotFound {
		t.Fatalf("other owner saw Home catalog: %d %s", result.Code, result.Body.String())
	}
	handler.currentUserID = nil
	if result := search(station.ID, ""); result.Code != http.StatusNotFound {
		t.Fatalf("missing auth provider saw Home catalog: %d %s", result.Code, result.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	for _, query := range []string{"?page_size=51", "?page_size=-1", "?priority=secret", "?sort=../../secret",
		"?path=/tmp/private", "?status=active&status=unknown", "?text=%broken"} {
		result := search(station.ID, query)
		if result.Code == http.StatusOK {
			t.Fatalf("malformed/unsafe query %q accepted: %s", query, result.Body.String())
		}
	}
}
