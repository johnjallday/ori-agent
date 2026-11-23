package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceSourceFoldersHTTP_OnlyCurrentReciprocalMembersOfRequestedGroup(t *testing.T) {
	h, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	store := workspace.NewInMemoryStore()
	h.SetWorkspaceTaskStore(store)
	owner := "local"
	h.currentUserID = func(context.Context) (string, error) { return owner, nil }
	key := workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	save := func(ws *workspace.Workspace) {
		t.Helper()
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	group := func(id, parent, groupOwner string) *workspace.Workspace {
		return &workspace.Workspace{ID: id, FolderSlug: id + "-slug", Name: "Same group", Kind: "group", ParentID: parent, OwnerUserID: groupOwner, Status: workspace.StatusActive}
	}
	home := group("home", "", owner)
	for _, g := range []*workspace.Workspace{group("demo", home.ID, owner), group("deeper", "demo", owner),
		group("sibling", home.ID, owner), group("outside", "", owner), group("foreign", "demo", "other"), group("cycle", "cycle", owner), group("other-home", "demo", owner)} {
		if g.ID == "other-home" {
			g.SetAssistantProgramState(&workspace.AssistantProgramState{Key: workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: "other", ProgramID: "other"}})
		}
		save(g)
	}
	save(group("below-foreign", "foreign", owner))
	save(group("below-other-home", "other-home", owner))
	trashed := group("trashed", home.ID, owner)
	trashed.Status = workspace.StatusTrashed
	save(trashed)
	now := time.Now().UTC()
	rootPath := filepath.Join(t.TempDir(), "Approved source")
	doc := projectlibrary.Document{SchemaVersion: 1, Revision: 1, OwnerUserID: owner, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	state := &workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key}
	for _, rootID := range []string{"primary", "overlap", "revoked", "inactive"} {
		root := projectlibrary.Root{ID: rootID, Path: rootPath, FileIdentity: rootID, Revision: 1, ApprovedAt: now}
		if rootID == "revoked" {
			root.RevokedAt = &now
		}
		doc.Roots = append(doc.Roots, root)
		doc.Scans = append(doc.Scans, projectlibrary.Scan{ID: rootID, RootID: rootID, RootRevision: 1,
			Status: "complete", RootDigest: strings.Repeat("a", 64), ResultDigest: strings.Repeat("b", 64), StartedAt: now, FinishedAt: &now})
	}
	state.ProjectLibraryInactiveRoots = []string{"inactive"}
	for _, tc := range []struct{ id, parent, relative, root string }{
		{"track", "demo", "Picker Songs/Artist/Track", "primary"}, {"album", "demo", "Albums/Album", "primary"},
		{"deeper-song", "deeper", "Albums/Nested", "primary"}, {"sibling-song", "sibling", "Sibling Secret/Other", "primary"},
		{"stray", "outside", "Outside Secret/Track", "primary"}, {"foreign-chain", "foreign", "Foreign Chain/Track", "primary"},
		{"foreign-song", "demo", "Foreign Secret/Track", "primary"}, {"stale", "demo", "Stale Secret/Track", "primary"},
		{"broken", "demo", "Broken Secret/Track", "primary"}, {"unlisted", "demo", "Unlisted Secret/Track", "primary"},
		{"unavailable", "demo", "Missing Source/Track", "primary"}, {"overlap", "demo", "Overlap/Track", "primary"},
		{"revoked", "demo", "Revoked Secret/Track", "revoked"}, {"inactive", "demo", "Inactive Secret/Track", "inactive"},
		{"direct-root", "demo", "Direct", "primary"}, {"link-only", "demo", "", ""}, {"home-song", "home", "Home Only/Track", "primary"},
	} {
		child := &workspace.Workspace{ID: tc.id, FolderSlug: tc.id + "-slug", Name: "Same title", OwnerUserID: owner, ParentID: tc.parent, Status: workspace.StatusActive}
		link := workspace.AssistantProjectLink{SchemaVersion: 1, ID: workspace.AssistantProjectLinkID(home.ID, tc.id),
			StationWorkspaceID: home.ID, Key: key, StateRevision: 1}
		entry := projectlibrary.Entry{ID: "catalog-" + tc.id, Revision: 1, Link: &projectlibrary.ExactLink{WorkspaceID: tc.id, LinkID: link.ID, Revision: link.StateRevision}}
		if tc.root != "" {
			obs := projectlibrary.Observation{RootID: tc.root, RelativeFolder: tc.relative, FileIdentity: tc.id,
				Format: "reaper", Availability: "available", ScanID: tc.root, ScannedAt: now}
			if tc.id == "unavailable" {
				obs.Availability = "unavailable"
			}
			entry.Observations = append(entry.Observations, obs)
			if tc.id == "overlap" {
				obs.RootID, obs.ScanID = "overlap", "overlap"
				entry.Observations = append(entry.Observations, obs)
			}
		}
		doc.Entries = append(doc.Entries, entry)
		if tc.id == "foreign-song" {
			child.OwnerUserID = "other"
		}
		if tc.id == "stale" {
			link.StateRevision++
		}
		if tc.id != "broken" {
			child.SetAssistantProjectLink(&link)
		}
		if tc.id != "unlisted" {
			state.LinkedProjectIDs = append(state.LinkedProjectIDs, tc.id)
		}
		save(child)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	state.ProjectLibrary = encoded
	home.SetAssistantProgramState(state)
	save(home)

	read := func(id string, wantCode int) projectlibrary.WorkspaceFolders {
		t.Helper()
		response := httptest.NewRecorder()
		// An untrusted Home / member hint must not affect the scope.
		h.GetWorkspaceSourceFolders(response, assistantProgramRequest(http.MethodGet, "/source-folders?home_id=home&workspace_id=sibling-song", id, ""))
		if response.Code != wantCode || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", id, response.Code, response.Body.String())
		}
		if wantCode != http.StatusOK {
			return projectlibrary.WorkspaceFolders{}
		}
		wire := response.Body.String()
		for _, private := range []string{rootPath, "catalog-", "relative_folder", "next_cursor", "station_workspace_id", "Same title"} {
			if strings.Contains(wire, private) {
				t.Fatalf("scope response leaked %q: %s", private, wire)
			}
		}
		var result projectlibrary.WorkspaceFolders
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Revision != 1 || result.Rows == nil {
			t.Fatalf("%s: invalid snapshot: %+v %v", id, result, err)
		}
		return result
	}
	assertRows := func(id string, want map[string]string) projectlibrary.WorkspaceFolders {
		t.Helper()
		result := read(id, http.StatusOK)
		got := map[string]string{}
		for _, row := range result.Rows {
			got[row.WorkspaceID] = row.FolderSection.Label
		}
		if len(result.Rows) != len(want) || !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: expected labels %v, got %v", id, want, got)
		}
		return result
	}
	demo := assertRows("demo", map[string]string{"track": "Picker Songs", "album": "Albums", "deeper-song": "Albums"})
	if demo.Rows[0].FolderSection.Breadcrumb != "Artist" || demo.Rows[0].FolderSection.ID == "" {
		t.Fatal(demo.Rows[0])
	}
	assertRows("sibling", map[string]string{"sibling-song": "Sibling Secret"})
	assertRows("home", map[string]string{"track": "Picker Songs", "album": "Albums", "deeper-song": "Albums", "sibling-song": "Sibling Secret", "home-song": "Home Only"})
	for _, id := range []string{"outside", "track", "demo-slug", "foreign", "cycle", "below-foreign", "below-other-home", "trashed"} {
		read(id, http.StatusNotFound)
	}
	owner = "other"
	read("demo", http.StatusNotFound)
	owner = "local"
	h.SetWorkspaceTaskStore(sourceFoldersMovedFixture{store})
	assertRows("demo", map[string]string{"album": "Albums", "deeper-song": "Albums"})
}

// A topology changed since the catalog snapshot (normal updates are guarded).
type sourceFoldersMovedFixture struct{ workspace.Store }

func (s sourceFoldersMovedFixture) Get(id string) (*workspace.Workspace, error) {
	ws, err := s.Store.Get(id)
	if id == "track" && ws != nil {
		ws.ParentID = "sibling"
	}
	return ws, err
}
