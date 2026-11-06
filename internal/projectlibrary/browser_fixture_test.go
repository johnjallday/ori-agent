package projectlibrary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// This exporter is test-only. It creates portable saved metadata for the real
// host's ordinary import route, never a product backdoor or a fake HTTP API.
// No agents, paid providers, installed packages or user folders are required.
type browserFixture struct {
	ProviderSource string   `json:"provider_source,omitempty"`
	HomeID         string   `json:"home_id"`
	HomeFolder     string   `json:"home_folder"`
	ChildFolders   []string `json:"child_folders"`
	SourceRoot     string   `json:"source_root"`
	EntryCount     int      `json:"entry_count"`
}

func browserFixtureSandbox(raw, owner string) (string, error) {
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("an absolute disposable sandbox is required")
	}
	root, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", err
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil || filepath.Dir(root) != tmp ||
		(!strings.HasPrefix(filepath.Base(root), "ori-e2e.") && !strings.HasPrefix(filepath.Base(root), "ori-demo.")) {
		return "", fmt.Errorf("fixture must be directly inside an Ori temporary sandbox")
	}
	marker := filepath.Join(root, ".ori-demo-owner")
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("sandbox ownership marker is missing or unsafe")
	}
	data, err := os.ReadFile(marker) // #nosec G304 -- fixed marker under the verified disposable sandbox
	if err != nil || strings.TrimSpace(string(data)) != "worktree="+owner {
		return "", fmt.Errorf("sandbox belongs to another worktree")
	}
	return root, nil
}

func createBrowserFixture(t *testing.T, root string) (*Store, Scope, browserFixture) {
	t.Helper()
	// root is a new t.TempDir or a newly created directory in the checked sandbox.
	exports := filepath.Join(root, "exports")
	folders, err := workspace.NewFileStore(exports)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	scope := Scope{OwnerUserID: "local", HomeID: "browser-home", ProviderID: "browser-fixture", ProgramID: "browser-guide"}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Project Browser Fixture"})
	home.ID, home.OwnerUserID = scope.HomeID, scope.OwnerUserID
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key, StateRevision: 1,
		PluginAvailable: true, PrimaryName: "Guide", StageID: "helper", Level: 1,
		Declaration: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: scope.ProgramID,
			StationName: home.Name, DefaultPrimaryName: "Guide", HireTitle: "Set up guide",
			Roles: []workspace.AssistantProgramRoleSpec{{ID: "guide", Label: "Guide", Scope: workspace.AssistantRoleScopeHome,
				Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Fixture only; never execute."}},
			Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		},
	})
	if err := folders.Save(home); err != nil {
		t.Fatal(err)
	}
	library := NewStore(folders)
	initializeLibrary(t, library, scope)
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	doc.SchemaVersion = ArrangementSchemaVersion
	fixture := browserFixture{HomeID: home.ID, SourceRoot: filepath.Join(root, "sources"), EntryCount: 105}
	mkdir := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	identity := func(path string) string {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := directoryIdentity(info)
		if err != nil {
			t.Skip("fixture requires supported persistent directory identity")
		}
		return id
	}
	for _, rootID := range []string{"active", "revoked"} {
		path := filepath.Join(fixture.SourceRoot, rootID)
		mkdir(path)
		r := Root{ID: rootID, Path: path, FileIdentity: identity(path), Revision: 1, ApprovedAt: now}
		if rootID == "revoked" {
			r.RevokedAt = &now
		}
		doc.Roots = append(doc.Roots, r)
		doc.Scans = append(doc.Scans, Scan{ID: "scan-" + rootID, RootID: rootID, RootRevision: 1,
			RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
			Status: "complete", StartedAt: now, FinishedAt: &now})
	}
	collection := LogicalCollection{ID: "collection-wide", Name: "Across the catalog", Revision: 1,
		Author: "local", Origin: "manual", CreatedAt: now, UpdatedAt: now}
	for i := range fixture.EntryCount {
		id := fmt.Sprintf("browser-%03d", i)
		rootID := "active"
		if i == 1 || i == 2 {
			rootID = "revoked"
		}
		folder := filepath.Join(fixture.SourceRoot, rootID, id)
		mkdir(folder)
		write(filepath.Join(folder, "Project.rpp"), "<REAPER_PROJECT 0.1 \"fixture only\"\n>\n")
		at := now.Add(-time.Duration(i) * time.Hour)
		entry := Entry{ID: id, Revision: 1,
			Fields: Fields{DisplayName: fmt.Sprintf("Project %03d", i)},
			Observations: []Observation{{RootID: rootID, RelativeFolder: id, FileIdentity: identity(folder),
				Format: "reaper", Alternates: []string{"Project.rpp"}, ScanID: "scan-" + rootID,
				ScannedAt: now, FileModifiedAt: at, Availability: "available"}}}
		switch i {
		case 6, 7:
			entry.Fields.DisplayName = "Same display name"
		case 8:
			entry.Fields.DisplayName = "<img src=x onerror=alert(1)> & project"
		case 9:
			entry.Fields.DisplayName = strings.Repeat("Long project name ", 8)
		case 10:
			entry.Fields = Fields{}
			entry.Observations[0].FileModifiedAt = time.Time{}
			entry.Observations[0].Availability = "not_scanned"
		case 104:
			entry.Fields.DisplayName = "Zebra beyond page four"
		}
		if i%10 == 0 && i != 10 {
			entry.Fields.Status = "archived"
		}
		if i%3 == 0 {
			entry.Fields.Stage = "writing"
		}
		if i == 5 {
			entry.Observations[0].Availability = "ambiguous"
			entry.Observations[0].Alternates = append(entry.Observations[0].Alternates, "Alternate.rpp")
			write(filepath.Join(folder, "Alternate.rpp"), "another fixture")
		}
		if i == 4 {
			entry.Observations[0].Availability = "unavailable"
			if err := os.Rename(folder, folder+"-offline"); err != nil {
				t.Fatal(err)
			}
		}
		if i < 2 || i == 3 {
			child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: fmt.Sprintf("Connected fixture %d", i)})
			child.ID, child.OwnerUserID = fmt.Sprintf("browser-child-%d", i), scope.OwnerUserID
			linkID := workspace.AssistantProjectLinkID(scope.HomeID, child.ID)
			child.SetAssistantProjectLink(&workspace.AssistantProjectLink{SchemaVersion: workspace.AssistantProjectLinkSchemaVersion,
				ID: linkID, StationWorkspaceID: scope.HomeID, Key: key, StateRevision: 1})
			if err := projectconnection.RecordAttachedProject(child, "Original project folder", folder, "Project.rpp", "project-source"); err != nil {
				t.Fatal(err)
			}
			if err := folders.Save(child); err != nil {
				t.Fatal(err)
			}
			childFolder, err := folders.GetFolderPath(child.ID)
			if err != nil {
				t.Fatal(err)
			}
			fixture.ChildFolders = append(fixture.ChildFolders, childFolder)
			mkdir(filepath.Join(childFolder, "files"))
			write(filepath.Join(childFolder, "files", "managed-only.txt"), "not the original project folder")
			entry.Link = &ExactLink{WorkspaceID: child.ID, LinkID: linkID, Revision: 1}
			if i == 3 {
				entry.Link.Revision = 2 // Deliberately broken reciprocal link, not a foreign file grant.
			}
			if err := folders.Update(home.ID, func(ws *workspace.Workspace) error {
				state := ws.GetAssistantProgramState()
				if i != 3 {
					state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
				}
				ws.SetAssistantProgramState(state)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		doc.Entries = append(doc.Entries, entry)
		if i%2 == 0 {
			collection.EntryIDs = append(collection.EntryIDs, id)
		}
	}
	doc.Collections = []LogicalCollection{collection, {ID: "collection-empty", Name: "Empty collection", Revision: 1,
		Author: "local", Origin: "manual", CreatedAt: now, UpdatedAt: now}}
	doc.Revision++
	if !doc.valid(scope) {
		t.Fatal("browser fixture is not valid canonical library metadata")
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := folders.Update(home.ID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fixture.HomeFolder, err = folders.GetFolderPath(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	connected := filepath.Join(fixture.SourceRoot, "active", "browser-000")
	for _, dir := range []string{"Empty", "Nested space 日本語", "Large"} {
		mkdir(filepath.Join(connected, dir))
	}
	write(filepath.Join(connected, "Nested space 日本語", "read me.txt"), "Only the original source. <script>alert(1)</script>\n")
	write(filepath.Join(connected, "notes.md"), "# Project notes\n<script>alert(1)</script>\n[unsafe](javascript:alert(1))\n")
	write(filepath.Join(connected, "page.html"), "<script>window.previewExecuted=true</script>")
	write(filepath.Join(connected, "vector.svg"), "<svg xmlns=\"http://www.w3.org/2000/svg\" onload=\"alert(1)\"/>")
	write(filepath.Join(connected, "oversized.txt"), strings.Repeat("x", (512<<10)+1))
	for i := range 501 {
		write(filepath.Join(connected, "Large", fmt.Sprintf("file-%03d.txt", i)), "bounded fixture")
	}
	return library, scope, fixture
}

func TestBrowserFixture_PersistsScaledCatalogAndIndependentReferences(t *testing.T) {
	library, scope, fixture := createBrowserFixture(t, t.TempDir())
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	page, err := library.Query(scope, Search{})
	if err != nil || len(page.Rows) != 25 || page.Total != 105 || page.NextCursor == "" {
		t.Fatalf("default bounded page: %+v %v", page, err)
	}
	late, err := library.Query(scope, Search{Text: "Zebra"})
	if err != nil || len(late.Rows) != 1 || late.Rows[0].ID != "browser-104" {
		t.Fatalf("late-page query: %+v %v", late, err)
	}
	for id, connection := range map[string]string{"browser-000": "connected", "browser-001": "connected", "browser-002": "catalog_only", "browser-003": "needs_review"} {
		detail, err := library.Detail(scope, id)
		if err != nil || detail.Row.Connection != connection {
			t.Fatalf("%s: %+v %v", id, detail.Row, err)
		}
	}
	cards, err := library.CollectionCatalog(t.Context(), scope, "", 0)
	if err != nil || cards.Total != 2 {
		t.Fatalf("collection cards: %+v %v", cards, err)
	}
	members, err := library.CollectionMembers(t.Context(), scope, "collection-wide", cards.ContextPin, 0)
	if err != nil || members.Total != 53 || len(members.Members) != 50 || members.NextOffset == nil {
		t.Fatalf("multi-page canonical members: %+v %v", members, err)
	}
	ids, err := library.workspaces.List()
	if err != nil || len(ids) != 4 || len(fixture.ChildFolders) != 3 {
		t.Fatalf("fixture activated too many projects: %v %v", ids, err)
	}
	after, err := library.Read(scope)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("metadata inspection mutated the saved library")
	}
	for _, id := range ids {
		ws, err := library.workspaces.Get(id)
		if err != nil || len(ws.AgentInstances) != 0 {
			t.Fatalf("fixture staffed an agent: %s %v", id, err)
		}
	}
}

func TestBrowserFixture_SandboxGuard(t *testing.T) {
	for _, path := range []string{"", ".", os.Getenv("HOME"), t.TempDir()} {
		if _, err := browserFixtureSandbox(path, "owner"); err == nil {
			t.Fatalf("accepted unsafe sandbox %q", path)
		}
	}
	root, err := os.MkdirTemp("", "ori-e2e.browser-guard-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	marker := filepath.Join(root, ".ori-demo-owner")
	if err := os.WriteFile(marker, []byte("worktree=owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := browserFixtureSandbox(root, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := browserFixtureSandbox(root, "other"); err == nil {
		t.Fatal("accepted foreign worktree sandbox")
	}
}

func TestBrowserFixtureExport(t *testing.T) {
	raw := os.Getenv("ORI_LIBRARY_BROWSER_SANDBOX")
	if raw == "" {
		t.Skip("opt-in export for the isolated browser host, not an integration pass")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := filepath.EvalSymlinks(filepath.Join(cwd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := browserFixtureSandbox(raw, owner)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(sandbox, "project-library-browser-fixture")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal("refusing to overwrite an existing fixture:", err)
	}
	_, _, fixture := createBrowserFixture(t, root)
	fixture.ProviderSource = writeBrowserProviderFixture(t, root)
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
