package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func workspaceResolverFixture(t *testing.T) (*AssistantWorkspaceResolver, *workspace.InMemoryStore, *workspace.Workspace, *workspace.Workspace, *workspace.Workspace) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	create := func(name, slug, kind, parent string) *workspace.Workspace {
		t.Helper()
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		ws.FolderSlug, ws.Kind, ws.ParentID, ws.OwnerUserID = slug, kind, parent, "local"
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
		ws, err := store.Get(ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ws
	}
	home := create("Music Home", "music-home", "group", "")
	alpha := create("Album-1", "album-1", "", home.ID)
	beta := create("Second Project", "second-project", "", "")
	return &AssistantWorkspaceResolver{Source: store, Now: func() time.Time { return time.Unix(1700000000, 0) }}, store, home, alpha, beta
}

func TestAssistantWorkspaceResolver_ReferencePrecedence(t *testing.T) {
	r, store, home, alpha, beta := workspaceResolverFixture(t)
	foreign := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Private foreign name"})
	foreign.OwnerUserID, foreign.FolderSlug = "another-user", "foreign"
	if err := store.Save(foreign); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, prompt              string
		refs                      HomeAssistantRouteContext
		location, subject, reason string
	}{
		{"canonical ID", "hello", HomeAssistantRouteContext{WorkspaceID: alpha.ID}, alpha.ID, alpha.ID, ""},
		{"canonical slug", "hello", HomeAssistantRouteContext{WorkspaceSlug: alpha.FolderSlug}, alpha.ID, alpha.ID, ""},
		{"published page", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1/canvas", WorkspaceID: alpha.ID, WorkspaceSlug: "album-1", ContextVersion: 1}, alpha.ID, alpha.ID, ""},
		{"legacy guide slug", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1", WorkspaceID: "album-1"}, alpha.ID, alpha.ID, ""},
		{"versioned slug is not ID", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1", WorkspaceID: "album-1", ContextVersion: 1}, "", "", "workspace_reference_conflict"},
		{"stale map selection cannot override page", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1/assistant", SelectionWorkspaceID: beta.ID}, alpha.ID, alpha.ID, ""},
		{"conflicting canonical page ID", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1", WorkspaceID: beta.ID}, "", "", "workspace_reference_conflict"},
		{"Home selection", "hello", HomeAssistantRouteContext{PagePath: "/", SelectionWorkspaceID: home.ID}, home.ID, home.ID, ""},
		{"unselected app Home", "hello", HomeAssistantRouteContext{PagePath: "/"}, "", "", ""},
		{"app-wide clears stale page", "hello", HomeAssistantRouteContext{PagePath: "/settings", WorkspaceID: alpha.ID}, "", "", ""},
		{"workspaces list with a trailing slash", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/", WorkspaceID: alpha.ID}, "", "", ""},
		{"explicit unique name", "What should I do in Second Project?", HomeAssistantRouteContext{PagePath: "/workspaces/album-1"}, alpha.ID, beta.ID, ""},
		{"word boundary", "Translate Album-10", HomeAssistantRouteContext{}, "", "", ""},
		{"explicit subject reference", "hello", HomeAssistantRouteContext{WorkspaceID: alpha.ID, SubjectWorkspaceID: beta.ID}, alpha.ID, beta.ID, ""},
		{"missing ID", "hello", HomeAssistantRouteContext{WorkspaceID: "gone"}, "", "", "workspace_unavailable"},
		{"ID never used as slug fallback", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/" + alpha.ID}, "", "", "workspace_unavailable"},
		{"foreign ID", "hello", HomeAssistantRouteContext{WorkspaceID: foreign.ID}, "", "", "workspace_denied"},
		{"foreign explicit subject", "hello", HomeAssistantRouteContext{SubjectWorkspaceID: foreign.ID}, "", "", "subject_denied"},
		{"absolute page URL", "hello", HomeAssistantRouteContext{PagePath: "https://example.invalid/workspaces/album-1"}, "", "", "page_reference_invalid"},
		{"encoded traversal", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/%2E%2E"}, "", "", "page_reference_invalid"},
		{"encoded slash", "hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1%2Fother"}, "", "", "page_reference_invalid"},
		{"unknown version", "hello", HomeAssistantRouteContext{ContextVersion: 50}, "", "", "context_version_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(context.Background(), "local", tc.prompt, &tc.refs)
			id := func(ref *assistantcontext.WorkspaceRef) string {
				if ref == nil {
					return ""
				}
				return ref.ID
			}
			if got.Reason != tc.reason || id(got.Location) != tc.location || id(got.Subject) != tc.subject {
				t.Fatalf("resolved: %+v", got)
			}
			if tc.reason != "" && got.Overview != nil {
				t.Fatal("unresolved reference retained readable overview")
			}
			body, err := json.Marshal(got)
			if err != nil || strings.Contains(string(body), foreign.Name) {
				t.Fatal("foreign name or serialization failure", err)
			}
		})
	}
	// Resolving a subject never mutates next-turn page references/defaults.
	refs := &HomeAssistantRouteContext{WorkspaceID: alpha.ID}
	first := r.Resolve(context.Background(), "local", "Second Project", refs)
	second := r.Resolve(context.Background(), "local", "Hello", refs)
	if first.Subject.ID != beta.ID || second.Subject.ID != alpha.ID || refs.SubjectWorkspaceID != "" {
		t.Fatal("explicit subject rebound location default")
	}
}

func TestAssistantWorkspaceResolver_DuplicateNamesAndFreshAccess(t *testing.T) {
	r, store, _, alpha, beta := workspaceResolverFixture(t)
	if err := store.Update(beta.ID, func(ws *workspace.Workspace) error { ws.Name = alpha.Name; return nil }); err != nil {
		t.Fatal(err)
	}
	got := r.Resolve(context.Background(), "local", "Discuss Album-1", &HomeAssistantRouteContext{WorkspaceID: alpha.ID})
	if got.Reason != "subject_ambiguous" || got.Subject != nil || len(got.Choices) != 2 || got.Location.ID != alpha.ID {
		t.Fatalf("duplicate names: %+v", got)
	}
	refs := &HomeAssistantRouteContext{WorkspaceID: alpha.ID}
	for _, status := range []workspace.WorkspaceStatus{workspace.StatusTrashed, workspace.StatusMissing} {
		if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.Status = status; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := r.Resolve(context.Background(), "local", "hello", refs); got.Subject != nil || got.Reason != "workspace_denied" {
			t.Fatal("deleted workspace stayed readable", got)
		}
	}
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error {
		ws.Status = workspace.StatusActive
		ws.OwnerUserID = "foreign"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(context.Background(), "local", "hello", refs); got.Overview != nil {
		t.Fatal("ownership revocation ignored")
	}
	if got := r.Resolve(context.Background(), "foreign", "hello", refs); got.Reason != "principal_unsupported" {
		t.Fatal("implicitly adopted non-local principal")
	}
}

func TestAssistantWorkspaceResolver_SelectedTaskOwnership(t *testing.T) {
	r, store, _, alpha, beta := workspaceResolverFixture(t)
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = []workspace.Task{{ID: "task-a", WorkspaceID: alpha.ID, Description: "Selected current task", Status: workspace.TaskStatusPending}, {ID: "forged", WorkspaceID: beta.ID}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, refs := range []HomeAssistantRouteContext{
		{WorkspaceID: alpha.ID, TaskID: "task-a"},
		{PagePath: "/workspaces/album-1/tasks/task-a"},
	} {
		got := r.Resolve(context.Background(), "local", "hello", &refs)
		if got.Overview == nil || got.Overview.SelectedTask == nil || got.Overview.SelectedTask.ID != "task-a" || len(got.Overview.Tasks) != 1 {
			t.Fatalf("task: %+v", got)
		}
	}
	for _, refs := range []HomeAssistantRouteContext{
		{WorkspaceID: beta.ID, TaskID: "task-a"},
		{WorkspaceID: alpha.ID, TaskID: "forged"},
		{PagePath: "/workspaces/album-1/tasks/task-a", TaskID: "task-b"},
		{TaskID: "task-a"},
	} {
		got := r.Resolve(context.Background(), "local", "hello", &refs)
		if got.Status == assistantcontext.Available || got.Overview != nil {
			t.Fatal("foreign/conflicting task accepted", got)
		}
	}
	// Another workspace named from a task page. The page's task belongs to the
	// page: it is not looked up in the named workspace, and the turn resolves.
	for name, turn := range map[string]struct {
		prompt string
		refs   HomeAssistantRouteContext
	}{
		"named in the request": {"hello", HomeAssistantRouteContext{PagePath: "/workspaces/album-1/task/task-a", SubjectWorkspaceID: beta.ID}},
		"named with a task ID": {"hello", HomeAssistantRouteContext{WorkspaceID: alpha.ID, TaskID: "task-a", SubjectWorkspaceID: beta.ID}},
		"named in the message": {"What should I do in Second Project?", HomeAssistantRouteContext{PagePath: "/workspaces/album-1/task/task-a"}},
	} {
		got := r.Resolve(context.Background(), "local", turn.prompt, &turn.refs)
		if got.Status != assistantcontext.Available || got.Location == nil || got.Location.ID != alpha.ID || got.Subject == nil || got.Subject.ID != beta.ID ||
			!got.SubjectExplicit || got.Overview == nil || got.Overview.SelectedTask != nil {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}

// failingChildRead cannot read some workspaces that the listing still names.
type failingChildRead struct {
	AssistantWorkspaceSource
	unreadable map[string]bool
}

func (f failingChildRead) Get(id string) (*workspace.Workspace, error) {
	if f.unreadable[id] {
		return nil, errors.New("store unavailable at /Users/person/private")
	}
	return f.AssistantWorkspaceSource.Get(id)
}

// A project that is listed under a Home but cannot be read right now is missing
// from the count. The overview says so; it does not call the Home empty.
func TestAssistantWorkspaceResolver_UnreadChildIsNotAnEmptyHome(t *testing.T) {
	r, store, home, alpha, _ := workspaceResolverFixture(t)
	sibling := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album-2"})
	sibling.FolderSlug, sibling.ParentID, sibling.OwnerUserID = "album-2", home.ID, "local"
	if err := store.Save(sibling); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		unreadable []string
		status     assistantcontext.Availability
		count      int
	}{
		"every child unreadable": {[]string{alpha.ID, sibling.ID}, assistantcontext.Unavailable, 0},
		"one child unreadable":   {[]string{alpha.ID}, assistantcontext.Partial, 1},
	} {
		source := failingChildRead{AssistantWorkspaceSource: store, unreadable: map[string]bool{}}
		for _, id := range test.unreadable {
			source.unreadable[id] = true
		}
		got := (&AssistantWorkspaceResolver{Source: source, Now: r.Now}).Resolve(context.Background(), "local", "hello", &HomeAssistantRouteContext{WorkspaceID: home.ID})
		if got.Overview == nil {
			t.Fatalf("%s: the Home itself was lost: %+v", name, got)
		}
		children := got.Overview.Sources["children"]
		if children.Status != test.status || children.Reason != "child_unavailable" || children.Count != test.count || len(got.Overview.Children) != test.count {
			t.Fatalf("%s: %+v", name, children)
		}
		if encoded, _ := json.Marshal(got); strings.Contains(string(encoded), "/Users/") {
			t.Fatalf("%s: the store's error leaked", name)
		}
	}
	whole := r.Resolve(context.Background(), "local", "hello", &HomeAssistantRouteContext{WorkspaceID: home.ID})
	if children := whole.Overview.Sources["children"]; children.Status != assistantcontext.Available || children.Count != 2 || children.Reason != "" {
		t.Fatalf("a readable Home: %+v", children)
	}
}

type failingWorkspaceListing struct{ AssistantWorkspaceSource }

func (failingWorkspaceListing) ListActive() ([]*workspace.Workspace, error) {
	return nil, errors.New("private /Users/person/path and secret")
}

func TestAssistantWorkspaceResolver_MetadataBoundsAndAvailability(t *testing.T) {
	r, store, home, alpha, _ := workspaceResolverFixture(t)
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error {
		ws.Description = strings.Repeat("<界>", 10000)
		ws.Tasks = nil
		for range 20 {
			ws.Tasks = append(ws.Tasks, workspace.Task{ID: "task-" + strings.Repeat("a", len(ws.Tasks)+1), WorkspaceID: ws.ID, Description: strings.Repeat("界<", 500)})
			ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{Name: strings.Repeat("界", 300), Role: "Attached role"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Resolve(context.Background(), "local", "Hello", &HomeAssistantRouteContext{WorkspaceID: alpha.ID})
	if got.Overview == nil || got.Overview.Parent.ID != home.ID || !got.Overview.Truncated || len(got.Overview.Tasks) > 5 || len(got.Overview.Agents) > 5 {
		t.Fatal("overview missing/bounds", got)
	}
	encoded, err := json.Marshal(got.Overview)
	if err != nil || !json.Valid(encoded) || utf8.RuneCount(encoded) > assistantcontext.OverviewLimit {
		t.Fatal("serialized overview exceeds budget", len(encoded), err)
	}
	if got.GroupCount != 1 || len(got.Groups) != 1 || got.Groups[0].ID != home.ID || got.ProjectCount != 2 || got.Overview.ProgramLink != nil {
		t.Fatal("physical parent fabricated program link or counts changed")
	}
	for _, status := range got.Overview.Sources {
		if status.ContentRead {
			t.Fatal("metadata lookup claimed content read")
		}
	}
	after, err := store.Get(alpha.ID)
	if err != nil || after.Version != before.Version || after.UpdatedAt != before.UpdatedAt {
		t.Fatal("resolver mutated canonical state", err)
	}
	failed := (&AssistantWorkspaceResolver{Source: failingWorkspaceListing{store}}).Resolve(context.Background(), "local", "Hello", &HomeAssistantRouteContext{WorkspaceID: alpha.ID})
	if failed.Overview == nil || failed.Discovery.Status != assistantcontext.Unavailable || failed.Overview.Sources["children"].Status != assistantcontext.Unavailable {
		t.Fatal("failed listing reported empty or destroyed valid location", failed)
	}
	encoded, _ = json.Marshal(failed)
	if strings.Contains(string(encoded), "/Users/") || strings.Contains(string(encoded), "private ") {
		t.Fatal("source error leaked")
	}
	if got := workspaceContextText("Work on /Users/person/project and C:\\private\\project", 500); strings.Contains(got, "/Users/") || strings.Contains(got, "C:\\") {
		t.Fatal("metadata paths leaked", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := r.Resolve(ctx, "local", "hello", nil); got.Reason != "request_cancelled" {
		t.Fatal("cancelled request performed reads")
	}
}
