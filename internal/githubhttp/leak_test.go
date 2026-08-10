package githubhttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The token must never reach anything that is persisted unencrypted or
// returned over HTTP. A live audit of the demo sandbox confirmed this holds in
// practice (nothing under the data dir contained the token, and
// mcp_registry.json carried only the opaque auth_ref); these assertions keep it
// true as the shapes change.

// mcp_registry.json is a serialized []ServerConfig. If a token could reach a
// ServerConfig field it would land there in plaintext.
func TestServerConfig_CannotCarryTheToken(t *testing.T) {
	store := withFakeStore(t)
	ctx := context.Background()
	cfg := MCPServerConfig()

	if err := mcp.SaveStaticBearerToken(ctx, cfg, testToken); err != nil {
		t.Fatalf("SaveStaticBearerToken: %v", err)
	}

	// The config as the registry would serialize it, with a credential
	// stored for it.
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), testToken) {
		t.Fatalf("the server config serialized the token: %s", encoded)
	}
	// It carries the opaque reference instead.
	if !strings.Contains(string(encoded), mcp.GitHubAuthRef) {
		t.Fatalf("expected the auth ref in the serialized config: %s", encoded)
	}

	// And the credential really was stored, so the absence above is
	// redaction rather than a no-op.
	if _, ok, _ := mcp.LoadStaticBearerToken(ctx, cfg); !ok {
		t.Fatal("expected the token to be stored")
	}
	_ = store
}

// The workspace snapshot is written to workspace.json. The GitHub binding
// carries the repo and the tool policy -- never the credential.
func TestWorkspaceSnapshot_CannotCarryTheToken(t *testing.T) {
	withFakeStore(t)
	ws := &agentworkspace.Workspace{ID: "ws-1", Name: "GitHub Ops"}
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}

	binding, ok := FindGitHubBinding(ws)
	if !ok {
		t.Fatal("expected a github binding")
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), testToken) {
		t.Fatalf("the workspace binding serialized a token: %s", encoded)
	}
	// It should carry the repo scope, which is the only GitHub state a
	// workspace legitimately holds.
	if !strings.Contains(string(encoded), "octocat/demo") {
		t.Fatalf("expected the bound repo in the binding: %s", encoded)
	}
}

// A proposal is returned over HTTP and rendered in the browser. Nothing about
// it should be able to carry a credential.
func TestProposal_CannotCarryTheToken(t *testing.T) {
	withFakeStore(t)
	broker, _ := newBroker(t)
	p, err := broker.Propose("ws-1", commentChange())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), testToken) {
		t.Fatalf("a proposal serialized a token: %s", encoded)
	}
}

// Status is the connection surface's own response, and the one most likely to
// be tempted into echoing what it just read.
func TestStatus_CannotCarryTheToken(t *testing.T) {
	store := withFakeStore(t)
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef: authRef, AccessToken: testToken, TokenType: mcp.StaticBearerTokenType,
	}
	conn, _ := newFakeGitHub(t, okUser())

	encoded, err := json.Marshal(conn.Status(context.Background()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), testToken) {
		t.Fatalf("status serialized the token: %s", encoded)
	}
	if !strings.Contains(string(encoded), "octocat") {
		t.Fatalf("expected the login in status: %s", encoded)
	}
}

// The linked-workspaces payload names workspaces and repos, nothing else.
func TestLinkedWorkspaces_CarryNoCredential(t *testing.T) {
	withFakeStore(t)
	ws := &agentworkspace.Workspace{ID: "ws-1", Name: "GitHub Ops"}
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	encoded, err := json.Marshal(LinkedWorkspace{ID: ws.ID, Name: ws.Name, Repo: "octocat/demo"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), testToken) {
		t.Fatalf("linked workspaces serialized a token: %s", encoded)
	}
}
