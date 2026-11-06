package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestAssistantLibraryHTTP_CollectionQueriesRemainOwnerScopedAndRevisionPinned(t *testing.T) {
	handler, store, home, _ := assistantPortfolioHTTPFixture(t)
	state := home.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	handler.currentUserID = func(context.Context) (string, error) { return scope.OwnerUserID, nil }
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "browser-query-http"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	doc.SchemaVersion = projectlibrary.ArrangementSchemaVersion
	now := time.Now().UTC()
	doc.Collections = []projectlibrary.LogicalCollection{
		{ID: "one", Name: "Exact members", Revision: 1, EntryIDs: []string{doc.Entries[0].ID}, Origin: "manual", Author: scope.OwnerUserID, CreatedAt: now, UpdatedAt: now},
		{ID: "empty", Name: "Empty", Revision: 1, Origin: "manual", Author: scope.OwnerUserID, CreatedAt: now, UpdatedAt: now},
	}
	save := func() {
		t.Helper()
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
			state := home.GetAssistantProgramState()
			state.ProjectLibrary = encoded
			home.SetAssistantProgramState(state)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	search := func(query string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(http.MethodGet, "/library/projects?"+query, scope.HomeID, "")
		w := httptest.NewRecorder()
		handler.SearchAssistantLibrary(w, r)
		return w
	}
	for _, query := range []string{"collection_id=one", "collection_id=one&collection_revision=1&view=recently_saved"} {
		w := search(query)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":1`) || !strings.Contains(w.Body.String(), `"collection":{"id":"one"`) {
			t.Fatalf("canonical collection: %d %s", w.Code, w.Body.String())
		}
	}
	if w := search("collection_id=empty"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("empty scope broadened: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"collection_id=one&collection_id=empty", "collection_revision=0", "collection_revision=-1", "collection_revision=99999999999999999999", "collection_revision=", "view=all&view=archived", "membership=one"} {
		if w := search(query); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %s: %d", query, w.Code)
		}
	}
	if w := search("collection_id=one&collection_revision=2"); w.Code != http.StatusConflict {
		t.Fatalf("stale membership: %d", w.Code)
	}
	if w := search("collection_id=foreign"); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), doc.Entries[0].ID) {
		t.Fatalf("foreign collection: %d %s", w.Code, w.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other", nil }
	if w := search("collection_id=one"); w.Code != http.StatusNotFound {
		t.Fatalf("foreign owner: %d", w.Code)
	}
	handler.currentUserID = func(context.Context) (string, error) { return scope.OwnerUserID, nil }
	doc.Collections = nil
	doc.Revision++
	save()
	if w := search("collection_id=one"); w.Code != http.StatusNotFound {
		t.Fatalf("deleted collection silently broadened: %d", w.Code)
	}
}
