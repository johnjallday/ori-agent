package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestAssistantLibraryHTTP_ArrangementHistoryDoesNotInitializeOrImpersonateManager(t *testing.T) {
	handler, store, home, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	recordID := "unknown-record"
	call := func(detail bool, query string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(http.MethodGet, "/library/arrangements"+query, home.ID, "")
		r.SetPathValue("arrangementID", recordID)
		w := httptest.NewRecorder()
		if detail {
			handler.GetAssistantLibraryArrangement(w, r)
		} else {
			handler.GetAssistantLibraryArrangements(w, r)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("owner history allowed caching")
		}
		return w
	}
	if response := call(false, ""); response.Code != http.StatusConflict {
		t.Fatalf("absent library: %d %s", response.Code, response.Body.String())
	}
	state := home.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "arrangement-history-init"); err != nil {
		t.Fatal(err)
	}
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if response := call(false, ""); response.Code != http.StatusOK {
		t.Fatalf("empty history: %d %s", response.Code, response.Body.String())
	}
	if response := call(true, ""); response.Code != http.StatusNotFound {
		t.Fatalf("invented record: %d %s", response.Code, response.Body.String())
	}
	for _, detail := range []bool{false, true} {
		if response := call(detail, "?manager_name=forged"); response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected arguments: %d", response.Code)
		}
	}
	afterV1, err := library.Read(scope)
	if err != nil || !reflect.DeepEqual(before, afterV1) {
		t.Fatal("history changed v1", err)
	}
	// Seed a canonical historical v2 fixture, not a model or a Manager run.
	// Its old Manager need not be installed/bound to read inert owner history.
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(context.Context) (workspacearrangement.Snapshot, error) {
		return workspacearrangement.Snapshot{Kind: workspacearrangement.HomeLibrary, Revision: "fixture", Authority: "fixture-owner", Coverage: workspacearrangement.Coverage{Origin: "saved_home_catalog"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := evidence.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record, err := evidence.NewRecord(t.Context(), workspacearrangement.Draft{Version: 1, SourceRevision: overview.Revision, Goal: "Discuss saved intent"}, workspacearrangement.Provenance{Origin: "manual", SourceID: home.ID})
	if err != nil {
		t.Fatal(err)
	}
	before.SchemaVersion = projectlibrary.ArrangementSchemaVersion
	before.Revision++
	before.Arrangements = []projectlibrary.Arrangement{{Record: record, CatalogRevision: before.Revision, ManagerInstanceID: "historical-instance", ManagerName: "Historical Manager", BindingDigest: strings.Repeat("a", 64)}}
	discussion, err := evidence.NewClarificationRecord(t.Context(), "Organize by client", workspacearrangement.Provenance{Origin: "model", SourceID: home.ID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	before.Arrangements = append(before.Arrangements, projectlibrary.Arrangement{Record: discussion, CatalogRevision: before.Revision, ManagerInstanceID: "historical-instance", ManagerName: "Historical Manager", BindingDigest: strings.Repeat("a", 64), Kind: "clarification", Clarification: "Which projects share a client?"})
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(home.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.ProjectLibrary = raw
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recordID = record.ID
	response := call(true, "")
	var history projectlibrary.ArrangementHistory
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Arrangement.ID != record.ID || history.Kind != "proposal" || history.Clarification != "" || history.Arrangement.ReviewAvailable || !history.CatalogCurrent {
		t.Fatalf("canonical history: %d %s", response.Code, response.Body.String())
	}
	if len(response.Body.Bytes()) > 4096 || strings.Contains(response.Body.String(), "binding_digest") || strings.Contains(response.Body.String(), "Historical Manager") {
		t.Fatal("history leaked authority or was unbounded")
	}
	recordID = discussion.ID
	response = call(true, "")
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Kind != "clarification" || history.Clarification != "Which projects share a client?" || history.Arrangement.State != "needs_more_evidence" || history.Arrangement.ReviewAvailable {
		t.Fatalf("clarification history: %d %s", response.Code, response.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return "foreign-owner", nil }
	for _, detail := range []bool{false, true} {
		if response := call(detail, ""); response.Code != http.StatusNotFound {
			t.Fatalf("foreign owner: %d", response.Code)
		}
	}
	after, err := library.Read(scope)
	if err != nil || !reflect.DeepEqual(before, after) || after.SchemaVersion != projectlibrary.ArrangementSchemaVersion {
		t.Fatal("history mutated/upgraded the library", err)
	}
}
