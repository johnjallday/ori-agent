package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func TestAssistantLibraryHomes_ListsOnlyTheOwnersInitializedLibrariesByName(t *testing.T) {
	h, store, home, _ := assistantPortfolioHTTPFixture(t)
	owner := home.OwnerUserID
	h.currentUserID = func(context.Context) (string, error) { return owner, nil }
	list := func(path string) (int, string, []AssistantLibraryHome) {
		w := httptest.NewRecorder()
		h.ListAssistantLibraryHomes(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable owner Home list")
		}
		var body struct {
			Homes []AssistantLibraryHome `json:"homes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, w.Body.String(), body.Homes
	}
	// A Home whose library was never initialized is not offered.
	if code, raw, homes := list("/api/assistant-program/library-homes"); code != http.StatusOK || len(homes) != 0 {
		t.Fatalf("uninitialized library listed: %d %s", code, raw)
	}
	state := home.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: owner, HomeID: home.ID, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "homes-init"); err != nil {
		t.Fatal(err)
	}
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	code, raw, homes := list("/api/assistant-program/library-homes")
	if code != http.StatusOK || len(homes) != 1 || homes[0].ID != home.ID || homes[0].Name != home.Name || !strings.HasPrefix(homes[0].Route, "/workspaces/") {
		t.Fatalf("initialized Home: %d %s", code, raw)
	}
	// Names and routes only: no catalog, roots, Manager or provider details.
	for _, forbidden := range []string{"entries", "roots", "manager", "provider", "revision", "catalog"} {
		if strings.Contains(strings.ToLower(raw), `"`+forbidden) {
			t.Fatalf("Home list exposed %q: %s", forbidden, raw)
		}
	}
	if after, err := library.Read(scope); err != nil || after.Revision != before.Revision {
		t.Fatal("listing Homes wrote the library", err)
	}
	if code, _, _ := list("/api/assistant-program/library-homes?all=1"); code != http.StatusBadRequest {
		t.Fatalf("arguments accepted: %d", code)
	}
	owner = "someone-else"
	if code, raw, homes := list("/api/assistant-program/library-homes"); code != http.StatusOK || len(homes) != 0 {
		t.Fatalf("another owner saw this Home: %d %s", code, raw)
	}
}
