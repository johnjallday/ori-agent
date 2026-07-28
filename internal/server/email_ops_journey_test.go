package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The whole journey, in one test: connected identity → healthy read-only Gmail
// grant → Email Ops linked to the canonical account → Postmaster coordinating →
// Inbox reading natively. Each group proved its own piece; this proves they
// compose, because the bug this feature fixes lived in the seams between them.

type journeyFixture struct {
	conns      *connections.Store
	vaults     *vault.Store
	workspaces workspace.Store
	readiness  *emailReadinessEvaluator
	account    *vault.EmailAccount
	vaultID    string
}

func newJourneyFixture(t *testing.T) *journeyFixture {
	t.Helper()
	vaults := newTestVaultStore(t)
	created := createTestVault(t, vaults, "Personal")
	account := createAccount(t, vaults, vault.EmailAccountInput{
		VaultID: created.ID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})

	conns := connections.NewStore(t.TempDir())
	if err := conns.Save(&connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "me@example.com", VaultID: created.ID,
		Grants: map[connections.ProductKey]*connections.ProductGrant{
			connections.ProductGmail: {
				ConnectionID: "c1", Product: connections.ProductGmail,
				CredentialRef: account.ID,
				// The exact granted scope set: read-only, no send.
				GrantedScopes: []string{"openid", "email", "profile", connections.GmailReadonlyScope},
				Health:        connections.HealthHealthy,
			},
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	workspaces := workspace.NewInMemoryStore()
	return &journeyFixture{
		conns:      conns,
		vaults:     vaults,
		workspaces: workspaces,
		readiness:  newEmailReadinessEvaluator(conns, newConnectionVaultCatalog(vaults), workspaces, vaults),
		account:    account,
		vaultID:    created.ID,
	}
}

// emailOpsWithRoster is an Email Ops workspace as the blueprint creates it,
// before any mailbox is linked.
func (f *journeyFixture) emailOpsWithRoster(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws := &workspace.Workspace{
		ID: "ws-email-ops", Name: "Email Ops",
		AgentInstances: []workspace.AgentInstance{
			{ID: "postmaster-id", Name: "Postmaster", EntryPoint: true},
			{ID: "inbox-id", Name: "Inbox"},
		},
	}
	if err := f.workspaces.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

// FR 87: the complete path, asserted at each hop.
func TestJourney_ConnectedIdentityToInboxRead(t *testing.T) {
	ctx := context.Background()
	f := newJourneyFixture(t)
	ws := f.emailOpsWithRoster(t)

	// 1. Before linking: everything upstream is healthy, but THIS workspace is
	//    not connected, and the repair says exactly that.
	before := f.readiness.Evaluate(ctx, ws.ID)
	if before.Ready {
		t.Fatal("an unlinked workspace must not report ready")
	}
	if before.Reason != workspace.BlockedReasonNotLinkedToWorkspace {
		t.Fatalf("reason = %q, want not_linked_to_workspace", before.Reason)
	}

	// 2. Link — a plain application operation, referencing the canonical account.
	linker := newMailboxLinkerService(nil, f.workspaces, f.vaults, nil)
	linker.readiness = f.readiness
	status, err := linker.LinkWorkspaceMailbox(ctx, "", ws.ID, f.account.ID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !status.Connected || status.Setup == nil || !status.Setup.Ready {
		t.Fatalf("status after link = %+v, want connected and ready", status)
	}

	// 3. The binding references the AUTHORITATIVE credential — not a copy.
	if got := boundAccountID(t, f.workspaces, ws.ID); got != f.account.ID {
		t.Fatalf("binding references %q, want the connection's credential %q", got, f.account.ID)
	}
	accounts, err := f.vaults.ListEmailAccounts(ctx, "", "")
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("linking produced %d credential records, want exactly 1", len(accounts))
	}

	// 4. Postmaster coordinates but cannot read; Inbox can.
	access := newMailboxAccess(f.workspaces, f.vaults, stubMailProvider{})
	if access.CanAccess(ws.ID, "Postmaster") {
		t.Fatal("Postmaster must never receive the native mail tools")
	}
	if !access.CanAccess(ws.ID, "Inbox") {
		t.Fatal("Inbox must be authorized once the mailbox is linked")
	}
	acc, err := access.AuthorizedAccount(ctx, ws.ID, "Inbox")
	if err != nil {
		t.Fatalf("Inbox authorization: %v", err)
	}
	if acc.ID != f.account.ID {
		t.Fatalf("Inbox resolved account %q, want %q", acc.ID, f.account.ID)
	}

	// 5. A mail-dependent task may now run.
	if blocked := f.readiness.CheckTaskCapabilities(ws.ID, []string{workspace.CapabilityEmail}); blocked != nil {
		t.Fatalf("a healthy workspace blocked its triage task: %+v", blocked)
	}

	// 6. The grant is read-only: no send scope was requested or granted.
	conn, _ := f.conns.Load()
	grant, _ := conn.Grant(connections.ProductGmail)
	for _, scope := range grant.GrantedScopes {
		if strings.Contains(scope, "gmail.send") {
			t.Fatalf("the journey granted a send scope: %v", grant.GrantedScopes)
		}
	}
}

// FR 90, the hard regression: nothing in a linked Email Ops workspace may look
// up, launch, or fail on a `gmail` MCP server.
func TestJourney_NoGmailMCPServerIsEverRequested(t *testing.T) {
	ctx := context.Background()
	f := newJourneyFixture(t)
	ws := f.emailOpsWithRoster(t)

	linker := newMailboxLinkerService(nil, f.workspaces, f.vaults, nil)
	linker.readiness = f.readiness
	if _, err := linker.LinkWorkspaceMailbox(ctx, "", ws.ID, f.account.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Give the workspace a real MCP binding too, so the resolver has work to do.
	if err := workspace.CanonicalUpdate(f.workspaces, ws.ID, func(fresh *workspace.Workspace) error {
		return fresh.UpsertMCPBinding(workspace.MCPBinding{
			ID: "b-fs", ServerName: "filesystem", Enabled: true,
			Config: map[string]any{"roots": []any{t.TempDir()}},
		})
	}); err != nil {
		t.Fatalf("add filesystem binding: %v", err)
	}

	templates := &journeyTemplateLookup{servers: map[string]mcp.ServerConfig{
		"filesystem": {Name: "filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}},
	}}
	registry := &journeyRegistry{}
	resolver := workspace.NewAgentRuntimeResolver(
		&journeyAgentStore{agents: map[string]*agent.Agent{"Inbox": {}, "Postmaster": {}}},
		f.workspaces, registry, templates,
	)

	for _, agentName := range []string{"Postmaster", "Inbox"} {
		resolved, err := resolver.ResolveAgentForWorkspace(agentName, ws.ID, "")
		if err != nil {
			t.Fatalf("resolve %s: %v", agentName, err)
		}
		for _, server := range resolved.MCPServers {
			if strings.Contains(strings.ToLower(server), "gmail") {
				t.Fatalf("%s resolved a gmail MCP server: %s", agentName, server)
			}
		}
	}

	for _, asked := range templates.asked {
		if strings.EqualFold(asked, "gmail") {
			t.Fatalf("a template lookup for %q happened; there is no such server", asked)
		}
	}
	for name := range registry.configs {
		if strings.Contains(strings.ToLower(name), "gmail") {
			t.Fatalf("a gmail runtime server was registered: %s", name)
		}
	}
	if templates.notFoundFor("gmail") {
		t.Fatal(`"server gmail not found" was produced`)
	}
}

// FR 89: repairing a blocker does not execute anything. The user's explicit
// action is what starts work.
func TestJourney_RepairDoesNotExecuteTheTask(t *testing.T) {
	ctx := context.Background()
	f := newJourneyFixture(t)
	ws := f.emailOpsWithRoster(t)

	// Seed a triage task that is blocked because nothing is linked yet.
	if err := workspace.CanonicalUpdate(f.workspaces, ws.ID, func(fresh *workspace.Workspace) error {
		return fresh.AddTasks([]workspace.Task{{
			ID: "task-triage", WorkspaceID: ws.ID, To: "Postmaster",
			Description:          "Triage today's inbox",
			Status:               workspace.TaskStatusPending,
			RequiredCapabilities: []string{workspace.CapabilityEmail},
		}})
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	blocked := f.readiness.CheckTaskCapabilities(ws.ID, []string{workspace.CapabilityEmail})
	if blocked == nil {
		t.Fatal("the triage task should be gated before linking")
	}
	if blocked.Repair == nil || blocked.Repair.Code != emailActionLinkAccount {
		t.Fatalf("repair = %+v, want link_account", blocked.Repair)
	}

	// Repair it.
	linker := newMailboxLinkerService(nil, f.workspaces, f.vaults, nil)
	linker.readiness = f.readiness
	if _, err := linker.LinkWorkspaceMailbox(ctx, "", ws.ID, f.account.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	// The gate now permits execution — but nothing has executed.
	if blocked := f.readiness.CheckTaskCapabilities(ws.ID, []string{workspace.CapabilityEmail}); blocked != nil {
		t.Fatalf("still gated after repair: %+v", blocked)
	}
	saved, _ := f.workspaces.Get(ws.ID)
	task, err := saved.GetTask("task-triage")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status == workspace.TaskStatusInProgress || task.StartedAt != nil {
		t.Fatalf("repairing the link started the task (status %q); it must wait for an explicit run", task.Status)
	}
}

// FR 50-52, 84: a provider quota failure stops after one attempt and points at
// provider billing — never at Gmail, and never at an automatic model switch.
func TestJourney_QuotaFailureStopsAndBlamesTheProvider(t *testing.T) {
	quota := llm.ClassifyProviderError("openai",
		errNamed("You exceeded your current quota, please check your plan and billing details"),
		http.StatusTooManyRequests, "insufficient_quota")

	if quota.Retryable {
		t.Fatal("a quota failure must not be retryable")
	}
	if quota.Action != llm.ActionCheckBilling {
		t.Fatalf("action = %q, want provider billing", quota.Action)
	}
	message := strings.ToLower(quota.Message)
	for _, wrong := range []string{"gmail", "email", "vault", "switch model", "another model"} {
		if strings.Contains(message, wrong) {
			t.Fatalf("quota message mentions %q: %s", wrong, quota.Message)
		}
	}
	if !strings.Contains(message, "quota") && !strings.Contains(message, "credit") {
		t.Fatalf("quota message must name the problem: %s", quota.Message)
	}
}

// FR 19, 20, 91: nothing token-bearing may reach a browser-facing surface.
func TestJourney_NoSurfaceLeaksCredentialMaterial(t *testing.T) {
	ctx := context.Background()
	f := newJourneyFixture(t)
	ws := f.emailOpsWithRoster(t)

	linker := newMailboxLinkerService(nil, f.workspaces, f.vaults, nil)
	linker.readiness = f.readiness
	status, err := linker.LinkWorkspaceMailbox(ctx, "", ws.ID, f.account.ID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	// Every browser-facing projection from this journey.
	conn, _ := f.conns.Load()
	surfaces := map[string]any{
		"mailbox status":        status,
		"connection projection": connections.Project(conn),
		"readiness verdict":     f.readiness.Evaluate(ctx, ws.ID),
	}
	for name, surface := range surfaces {
		blob := strings.ToLower(jsonOf(t, surface))
		for _, forbidden := range []string{
			"fixture-refresh-token", "access_token", "refresh_token",
			"client_secret", "id_token", "password",
		} {
			if strings.Contains(blob, forbidden) {
				t.Fatalf("%s leaked %q: %s", name, forbidden, blob)
			}
		}
	}
}

// --- fixtures ----------------------------------------------------------------

type journeyTemplateLookup struct {
	servers  map[string]mcp.ServerConfig
	asked    []string
	notFound []string
}

func (j *journeyTemplateLookup) GetServer(name string) (*mcp.ServerConfig, error) {
	j.asked = append(j.asked, name)
	cfg, ok := j.servers[name]
	if !ok {
		j.notFound = append(j.notFound, name)
		return nil, errNamed("server " + name + " not found")
	}
	clone := cfg
	return &clone, nil
}

func (j *journeyTemplateLookup) notFoundFor(name string) bool {
	for _, n := range j.notFound {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

type journeyRegistry struct{ configs map[string]mcp.ServerConfig }

func (r *journeyRegistry) UpsertServer(config mcp.ServerConfig) error {
	if r.configs == nil {
		r.configs = map[string]mcp.ServerConfig{}
	}
	r.configs[config.Name] = config
	return nil
}

type journeyAgentStore struct{ agents map[string]*agent.Agent }

func (s *journeyAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}
func (s *journeyAgentStore) ListAgents() []string {
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	return names
}
func (s *journeyAgentStore) DeleteAgent(name string) error {
	delete(s.agents, name)
	return nil
}
func (s *journeyAgentStore) SetAgent(name string, ag *agent.Agent) error {
	s.agents[name] = ag
	return nil
}
func (s *journeyAgentStore) UpdateAgent(string, func(*agent.Agent) error) error { return nil }
func (s *journeyAgentStore) CreateAgent(name string, _ *store.CreateAgentConfig) error {
	s.agents[name] = &agent.Agent{}
	return nil
}
func (s *journeyAgentStore) ClearAgents() error { return nil }
func (s *journeyAgentStore) Save() error        { return nil }

var _ store.Store = (*journeyAgentStore)(nil)

// errNamed is a plain error with an exact message, used where a test asserts on
// the classifier's reading of provider text.
func errNamed(msg string) error { return &namedError{msg} }

type namedError struct{ msg string }

func (e *namedError) Error() string { return e.msg }

// jsonOf renders a value the way an API response would, so a leak test sees
// exactly what a browser would.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal surface: %v", err)
	}
	return string(encoded)
}
