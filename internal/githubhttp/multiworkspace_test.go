package githubhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// multiWorkspaceStore serves several workspaces from one store, so a test can
// exercise two GitHub Ops workspaces against a single connection.
type multiWorkspaceStore struct {
	byID map[string]*agentworkspace.Workspace
}

func (m *multiWorkspaceStore) GetFolderWorkspace(id string) (*agentworkspace.Workspace, error) {
	return m.byID[id], nil
}

func (m *multiWorkspaceStore) Save(*agentworkspace.Workspace) error { return nil }

// The reuse promise the PRD is built on: connecting GitHub is a one-time cost,
// not a per-workspace one. A second workspace must find the connection already
// there and never ask for a token again.
func TestSecondWorkspace_ReusesTheConnectionWithoutReprompting(t *testing.T) {
	store := withFakeStore(t)
	first := &agentworkspace.Workspace{ID: "ws-1"}
	second := &agentworkspace.Workspace{ID: "ws-2"}
	workspaces := &multiWorkspaceStore{byID: map[string]*agentworkspace.Workspace{
		"ws-1": first, "ws-2": second,
	}}

	conn, _ := newFakeGitHub(t, okUser("octocat"))
	adapter := NewSetupAdapter(conn, workspaces)
	connectToken(t, store)

	// The first workspace is already set up.
	if err := BindRepo(first, "octocat/alpha"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}

	// The second, brand new, finds the account step already satisfied.
	got, err := adapter.Evaluate(context.Background(), setupwizard.StepRequest{
		WorkspaceID: "ws-2",
		Step: agentworkspace.SetupWizardStep{
			ID: "account", Kind: agentworkspace.SetupStepKindAccountLink,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !got.Ready {
		t.Fatalf("a second workspace must reuse the existing connection, got %+v", got)
	}
	if !strings.Contains(got.Summary, "@octocat") {
		t.Fatalf("it should report the account already connected: %s", got.Summary)
	}
	// Nothing about the second workspace asks for a credential.
	if strings.Contains(strings.ToLower(got.Summary), "token") {
		t.Fatalf("a second workspace must not mention supplying a token: %s", got.Summary)
	}

	// And exactly one credential backs both.
	if len(store.byRef) != 1 {
		t.Fatalf("expected one shared credential, got %d", len(store.byRef))
	}
}

// Two workspaces on one connection stay bound to their own repositories. The
// connection is shared; the repository is not.
func TestTwoWorkspaces_KeepSeparateRepositories(t *testing.T) {
	withFakeStore(t)
	first := &agentworkspace.Workspace{ID: "ws-1"}
	second := &agentworkspace.Workspace{ID: "ws-2"}

	if err := BindRepo(first, "octocat/alpha"); err != nil {
		t.Fatalf("BindRepo first: %v", err)
	}
	if err := BindRepo(second, "octocat/beta"); err != nil {
		t.Fatalf("BindRepo second: %v", err)
	}

	if repo, ok := BoundRepo(first); !ok || repo != "octocat/alpha" {
		t.Fatalf("first workspace bound to %q", repo)
	}
	if repo, ok := BoundRepo(second); !ok || repo != "octocat/beta" {
		t.Fatalf("second workspace bound to %q", repo)
	}

	// Rebinding one must not disturb the other.
	if err := BindRepo(first, "octocat/gamma"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if repo, _ := BoundRepo(second); repo != "octocat/beta" {
		t.Fatalf("rebinding one workspace changed the other's repo to %q", repo)
	}
}

// Neither workspace's agent can route a change into the other's repository.
// The broker resolves the binding per workspace, so a proposal naming a repo
// this workspace is not bound to is refused before it is ever recorded --
// which matters because the shared token, being fine-grained, can read every
// public repository regardless of scoping.
func TestBroker_RefusesTheOtherWorkspacesRepo(t *testing.T) {
	withFakeStore(t)
	first := &agentworkspace.Workspace{ID: "ws-1"}
	second := &agentworkspace.Workspace{ID: "ws-2"}
	if err := BindRepo(first, "octocat/alpha"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	if err := BindRepo(second, "octocat/beta"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	workspaces := &multiWorkspaceStore{byID: map[string]*agentworkspace.Workspace{
		"ws-1": first, "ws-2": second,
	}}

	exec := &recordingExecutor{}
	broker := NewBroker(NewRepoResolver(func() WorkspaceStore { return workspaces }), exec)

	// ws-1 tries to comment on ws-2's repository.
	if _, err := broker.Propose("ws-1", Change{
		Kind: ProposalComment, Repo: "octocat/beta", Issue: 1, Body: "hello",
	}); err == nil {
		t.Fatal("a workspace must not propose a change to another workspace's repo")
	}
	if exec.count() != 0 {
		t.Fatal("nothing may reach GitHub")
	}
	if len(broker.List("ws-1")) != 0 {
		t.Fatal("a refused proposal must not be recorded")
	}

	// Its own repository is fine, and lands only against itself.
	if _, err := broker.Propose("ws-1", Change{
		Kind: ProposalComment, Repo: "octocat/alpha", Issue: 1, Body: "hello",
	}); err != nil {
		t.Fatalf("a workspace must be able to propose against its own repo: %v", err)
	}
	if len(broker.List("ws-2")) != 0 {
		t.Fatal("ws-1's proposal must not appear in ws-2's list")
	}
}

// One shared credential really is shared: a token stored once is readable
// through the config every workspace's materialized server copy resolves to.
// This is the AuthRef pin, exercised the way multiple workspaces exercise it.
func TestOneCredentialBacksEveryWorkspace(t *testing.T) {
	store := withFakeStore(t)
	ctx := context.Background()
	cfg := MCPServerConfig()

	if err := mcp.SaveStaticBearerToken(ctx, cfg, testToken); err != nil {
		t.Fatalf("SaveStaticBearerToken: %v", err)
	}

	for _, workspaceID := range []string{"ws-1", "ws-2", "ws-3"} {
		workspaceCopy := cfg
		workspaceCopy.Name = "ws:" + workspaceID + ":mcp:github:binding"
		got, ok, err := mcp.LoadStaticBearerToken(ctx, workspaceCopy)
		if err != nil || !ok || got != testToken {
			t.Fatalf("%s could not read the shared token: ok=%v err=%v", workspaceID, ok, err)
		}
	}
	if len(store.byRef) != 1 {
		t.Fatalf("expected exactly one stored credential, got %d", len(store.byRef))
	}
}
