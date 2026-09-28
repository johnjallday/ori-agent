package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type questSink struct {
	calls   int
	refs    []string
	account string
	err     error
}

func (s *questSink) LinkGmailToWorkspace(_ context.Context, credentialRef, vaultID, workspaceID string) (string, error) {
	s.calls++
	s.refs = append(s.refs, credentialRef+"|"+vaultID+"|"+workspaceID)
	if s.err != nil {
		return "", s.err
	}
	return s.account, nil
}

type questWizard struct{ steps []string }

func (w *questWizard) Confirm(_ context.Context, workspaceID, stepID string, action setupwizard.StepAction) (setupwizard.Status, error) {
	w.steps = append(w.steps, workspaceID+"|"+stepID+"|"+action.Type)
	return setupwizard.Status{}, nil
}

type noRelationship struct{}

func (noRelationship) GetState(context.Context, string) (*personalassistant.State, error) {
	return nil, errors.New("host quests need no assistant relationship")
}

func emailOpsQuestWorkspaceStore(t *testing.T, linked bool) *workspace.InMemoryStore {
	t.Helper()
	ws := &workspace.Workspace{
		ID: "ws-email", Name: "Email Ops", FolderSlug: "email-ops", Status: workspace.StatusActive,
		TemplateProvenance: &workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true},
	}
	if linked {
		if err := ws.UpsertMCPBinding(workspace.MCPBinding{
			ID: "b-mail", ServerName: "gmail", Enabled: true, RuntimeKind: workspace.RuntimeKindNativeEmail,
			Config: map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return store
}

func accountLinkScope() setupjourney.ReadScope {
	return setupjourney.ReadScope{
		OwnerUserID: "local", Shape: specialist.SetupJourneyShapeAccountLink,
		ExpectedBlueprintID: workspace.EmailOpsTemplateID, RunKind: setupjourney.RunKindRoot,
		ProjectWorkspaceID: "ws-email",
	}
}

func lockedVaults() stubReadinessVaults {
	return stubReadinessVaults{vaults: []connections.VaultRef{{ID: "v-1", Name: "Personal", Availability: connections.VaultLocked}}}
}

// FR 27-29: step 2 reports the connection-level verdict. Unfinished first-time
// setup is an active step; a missing OAuth client, a broken grant, or a locked
// vault blocks with its own reason. Both offers are navigation only.
func TestEmailOpsAccountConnectReaderMapsEveryConnectionVerdict(t *testing.T) {
	ctx := context.Background()
	navigation := []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings, setupjourney.ActionRecheckConnection}
	noIdentity := &connections.Connection{ID: "c1", Provider: connections.ProviderGoogle}
	notEnabled := connectedWithGmail(connections.HealthHealthy)
	notEnabled.Grants = nil
	cases := []struct {
		name       string
		conn       *connections.Connection
		vaults     connections.VaultCatalog
		configured bool
		complete   bool
		reason     setupjourney.ReasonCode
		health     string
		email      string
	}{
		{"no oauth client", nil, healthyVaults(), false, false, setupjourney.ReasonAccountConnectionNotConfigured, setupjourney.AccountHealthUnconfigured, ""},
		{"not connected yet", nil, healthyVaults(), true, false, "", setupjourney.AccountHealthNotConnected, ""},
		{"identity handshake incomplete", noIdentity, healthyVaults(), true, false, "", setupjourney.AccountHealthNotConnected, ""},
		{"gmail not enabled", notEnabled, healthyVaults(), true, false, "", setupjourney.AccountHealthNotEnabled, "me@example.com"},
		{"gmail not enabled, client since removed", notEnabled, healthyVaults(), false, false, "", setupjourney.AccountHealthNotEnabled, "me@example.com"},
		{"grant needs reconnect", connectedWithGmail(connections.HealthReconnectRequired), healthyVaults(), true, false, setupjourney.ReasonAccountReconnectRequired, setupjourney.AccountHealthUnhealthy, "me@example.com"},
		{"vault locked", connectedWithGmail(connections.HealthHealthy), lockedVaults(), true, false, setupjourney.ReasonAccountVaultRepairRequired, setupjourney.AccountHealthVaultUnavailable, "me@example.com"},
		{"healthy", connectedWithGmail(connections.HealthHealthy), healthyVaults(), true, true, "", setupjourney.AccountHealthHealthy, "me@example.com"},
		{"healthy without a current client", connectedWithGmail(connections.HealthHealthy), healthyVaults(), false, true, "", setupjourney.AccountHealthHealthy, "me@example.com"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			evaluator := newEmailReadinessEvaluator(connectionStore(t, test.conn), test.vaults, unlinkedWorkspace(t), healthyAccount())
			reader := emailOpsAccountConnectReader{readiness: evaluator, clientConfigured: func() bool { return test.configured }}
			read, err := reader.Read(ctx, accountLinkScope())
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if read.Complete != test.complete || read.BlockedReason != test.reason || read.AccountConnect == nil ||
				read.AccountConnect.GmailHealth != test.health || read.AccountConnect.IdentityEmail != test.email {
				t.Fatalf("read = %+v projection = %+v", read, read.AccountConnect)
			}
			if !test.complete && !reflect.DeepEqual(read.AvailableActions, navigation) {
				t.Fatalf("actions = %v, want navigation only", read.AvailableActions)
			}
			if test.complete && len(read.AvailableActions) != 0 {
				t.Fatalf("a complete connection offered actions: %v", read.AvailableActions)
			}
			if route := read.AccountConnect.ActionURL; route != "" && !strings.HasPrefix(route, "/settings") {
				t.Fatalf("repair route %q is not a Settings route", route)
			}
			if test.reason == setupjourney.ReasonAccountVaultRepairRequired && !strings.Contains(read.AccountConnect.ActionURL, "gc_action=unlock") {
				t.Fatalf("vault repair lost the evaluator's exact route: %q", read.AccountConnect.ActionURL)
			}
		})
	}
	if _, err := (emailOpsAccountConnectReader{}).Read(ctx, accountLinkScope()); err == nil {
		t.Fatal("a reader without the readiness owner claimed a verdict")
	}
}

// FR 30: step 3 is complete exactly when the workspace is Ready, pending with a
// review when only the link is missing, and blocked for account repairs.
func TestEmailOpsAccountLinkReaderFollowsWorkspaceReadiness(t *testing.T) {
	ctx := context.Background()
	gone := fakeAccounts{err: errors.New("record not found")}
	expired := fakeAccounts{acc: &vault.EmailAccount{ID: "acct-1", EmailAddress: "me@example.com"}}
	cases := []struct {
		name     string
		linked   bool
		accounts emailAccountResolver
		complete bool
		reason   setupjourney.ReasonCode
		actions  []setupjourney.ActionID
		email    string
	}{
		{"not linked", false, healthyAccount(), false, "", []setupjourney.ActionID{setupjourney.ActionReviewMailboxLink}, "me@example.com"},
		{"ready", true, healthyAccount(), true, "", nil, "me@example.com"},
		{"linked account missing", true, gone, false, setupjourney.ReasonMailboxAccountUnavailable, []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings}, ""},
		{"linked credential expired", true, expired, false, setupjourney.ReasonAccountReconnectRequired, []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings}, "me@example.com"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := emailOpsQuestWorkspaceStore(t, test.linked)
			evaluator := newEmailReadinessEvaluator(connectionStore(t, connectedWithGmail(connections.HealthHealthy)), healthyVaults(), store, test.accounts)
			read, err := emailOpsAccountLinkReader{readiness: evaluator, workspaces: store}.Read(ctx, accountLinkScope())
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if read.Complete != test.complete || read.BlockedReason != test.reason ||
				!reflect.DeepEqual(read.AvailableActions, test.actions) || read.AccountLink == nil ||
				read.AccountLink.AccountEmail != test.email || read.AccountLink.Linked != test.linked ||
				read.AccountLink.WorkspaceLabel != "Email Ops" || read.AccountLink.Ready != test.complete {
				t.Fatalf("read = %+v projection = %+v", read, read.AccountLink)
			}
		})
	}

	store := emailOpsQuestWorkspaceStore(t, false)
	evaluator := newEmailReadinessEvaluator(connectionStore(t, connectedWithGmail(connections.HealthHealthy)), healthyVaults(), store, healthyAccount())
	scope := accountLinkScope()
	scope.ProjectWorkspaceID = ""
	if read, err := (emailOpsAccountLinkReader{readiness: evaluator, workspaces: store}).Read(ctx, scope); err != nil ||
		read.Complete || len(read.AvailableActions) != 0 || read.AccountLink != nil {
		t.Fatalf("no workspace read = %+v err = %v", read, err)
	}
}

type emailOpsQuestHarness struct {
	service  *setupjourney.Service
	db       *database.DB
	store    *workspace.InMemoryStore
	conns    *connections.Store
	sink     *questSink
	wizard   *questWizard
	adapter  *emailOpsMailboxLinkAdapter
	accounts fakeAccounts
}

func newEmailOpsQuestHarness(t *testing.T, conn *connections.Connection) *emailOpsQuestHarness {
	t.Helper()
	ctx := context.Background()
	db := newFixtureDatabase(t)
	store := emailOpsQuestWorkspaceStore(t, false)
	conns := connectionStore(t, conn)
	accounts := healthyAccount()
	evaluator := newEmailReadinessEvaluator(conns, healthyVaults(), store, accounts)
	linker := newMailboxLinkerService(nil, store, accounts, nil)
	linker.readiness = evaluator
	sink := &questSink{account: "acct-1"}
	wizard := &questWizard{}
	adapter := &emailOpsMailboxLinkAdapter{
		readiness: evaluator, resolver: store, workspaces: store, sink: sink, linker: linker, wizard: wizard,
	}
	readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader)
	for _, kind := range specialist.SetupStepKinds() {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	readers[specialist.SetupStepWorkspaceCreate] = emailOpsWorkspaceCreateReader{source: store}
	readers[specialist.SetupStepAccountConnect] = emailOpsAccountConnectReader{readiness: evaluator, clientConfigured: func() bool { return true }}
	readers[specialist.SetupStepAccountLink] = emailOpsAccountLinkReader{readiness: evaluator, workspaces: store}
	readers[specialist.SetupStepSummary] = setupSummaryReader{}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := setupjourney.NewService(setupjourney.NewSQLiteStore(db), noRelationship{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(setupjourney.NewHostQuestCatalog(hostquests.All()))
	if err := service.SetActionAdapter(specialist.SetupStepAccountLink, adapter); err != nil {
		t.Fatal(err)
	}
	scoped, err := service.ForHostQuest(ctx, "local", hostquests.EmailOpsSetupQuestID)
	if err != nil {
		t.Fatal(err)
	}
	return &emailOpsQuestHarness{service: scoped, db: db, store: store, conns: conns, sink: sink, wizard: wizard, adapter: adapter, accounts: accounts}
}

func (h *emailOpsQuestHarness) step(t *testing.T, projection *setupjourney.JourneyProjection, id string) setupjourney.StepProjection {
	t.Helper()
	for _, step := range projection.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("step %q missing", id)
	return setupjourney.StepProjection{}
}

func (h *emailOpsQuestHarness) review(t *testing.T, projection *setupjourney.JourneyProjection, key string) *setupjourney.ActionResult {
	t.Helper()
	result, err := h.service.Mutate(context.Background(), "local", projection.RunID, setupjourney.ActionReviewMailboxLink,
		setupjourney.ActionMutation{IfRevision: projection.StateRevision, IdempotencyKey: key, Input: json.RawMessage(`{}`)})
	if err != nil || result.Review == nil {
		t.Fatalf("review = %+v err = %v", result, err)
	}
	return result
}

func (h *emailOpsQuestHarness) link(projection *setupjourney.JourneyProjection, review *setupjourney.ReviewProjection, key string) (*setupjourney.ActionResult, error) {
	return h.service.Mutate(context.Background(), "local", projection.RunID, setupjourney.ActionLinkMailbox,
		setupjourney.ActionMutation{IfRevision: projection.StateRevision, IdempotencyKey: key, ReviewToken: review.Token, Input: json.RawMessage(`{}`)})
}

// FR 31-33, FR 35, FR 52: the reviewed link runs sink → linker → wizard once,
// makes the quest ready, replays without re-linking, and leaves no secret in
// any journey table.
func TestEmailOpsQuestReviewedLinkReachesReadyWithoutPersistingSecrets(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))

	before, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if before.CurrentStepID != "mailbox" || h.step(t, before, "team").Status != setupjourney.StepComplete ||
		h.step(t, before, "connect").Status != setupjourney.StepComplete || h.step(t, before, "mailbox").Status != setupjourney.StepActive {
		t.Fatalf("before link = %+v", before)
	}
	if h.sink.calls != 0 {
		t.Fatal("reading the quest linked a mailbox")
	}

	reviewed := h.review(t, before, "review-1")
	disclosure := reviewed.Review.AccountLink
	if reviewed.Review.CommitAction != setupjourney.ActionLinkMailbox || disclosure == nil ||
		disclosure.WorkspaceLabel != "Email Ops" || disclosure.AccountEmail != "me@example.com" || disclosure.Linked {
		t.Fatalf("review = %+v", reviewed.Review)
	}
	if h.sink.calls != 0 {
		t.Fatal("reviewing linked a mailbox")
	}

	linked, err := h.link(reviewed.Journey, reviewed.Review, "link-1")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if linked.Journey.Lifecycle != setupjourney.LifecycleReady || linked.Journey.FirstCompletedAt == nil {
		t.Fatalf("after link = %+v", linked.Journey)
	}
	if h.sink.calls != 1 || h.sink.refs[0] != "vault://email/acct-1|v-1|ws-email" {
		t.Fatalf("sink calls = %d refs = %v", h.sink.calls, h.sink.refs)
	}
	if !reflect.DeepEqual(h.wizard.steps, []string{"ws-email|mailbox|confirm"}) {
		t.Fatalf("wizard confirms = %v", h.wizard.steps)
	}
	saved, _ := h.store.Get("ws-email")
	bindings := 0
	for _, binding := range saved.MCPBindings {
		if binding.IsNativeEmail() {
			bindings++
			if fmt.Sprint(binding.Config["allowed_actions"]) != "[read search]" {
				t.Fatalf("binding permits %v, want read/search only", binding.Config["allowed_actions"])
			}
		}
	}
	if bindings != 1 {
		t.Fatalf("workspace has %d mail bindings, want 1", bindings)
	}

	// Replaying the same confirmed request never links again.
	replay, err := h.link(reviewed.Journey, reviewed.Review, "link-1")
	if err != nil || replay.Journey.Lifecycle != setupjourney.LifecycleReady || h.sink.calls != 1 {
		t.Fatalf("replay = %+v err = %v sink calls = %d", replay, err, h.sink.calls)
	}

	summary := h.step(t, linked.Journey, "summary")
	if summary.Status != setupjourney.StepComplete || len(summary.Actions) != 2 ||
		summary.Actions[0].ID != setupjourney.ActionOpenWorkspace || summary.Actions[1].ID != setupjourney.ActionOpenModelSettings {
		t.Fatalf("summary without a model = %+v", summary)
	}

	for _, table := range []string{"setup_journey_run", "setup_journey_operation_receipt", "setup_journey_review_receipt"} {
		dump := dumpQuestTable(t, h.db, table)
		for _, secret := range []string{"me@example.com", "vault://email/acct-1", "acct-1", "sub-1", "\"v-1\""} {
			if strings.Contains(dump, secret) {
				t.Fatalf("%s persisted %q: %s", table, secret, dump)
			}
		}
	}
}

// FR 31, FR 34, FR 51: a review is bound to the account, grant, and binding it
// described. A connection change or a link made through the workspace's own
// CTA in between makes the review stale, and a CTA link completes the step.
func TestEmailOpsQuestStaleReviewsAndConcurrentWorkspaceLinks(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	start, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}

	// The connection's credential changed after review.
	reviewed := h.review(t, start, "review-a")
	changed := connectedWithGmail(connections.HealthHealthy)
	changed.Grants[connections.ProductGmail].CredentialRef = "vault://email/acct-2"
	if err := h.conns.Save(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := h.link(reviewed.Journey, reviewed.Review, "link-a"); !isQuestFailure(err, setupjourney.ReasonReviewStale) {
		t.Fatalf("changed credential commit error = %v, want review_stale", err)
	}
	if h.sink.calls != 0 {
		t.Fatal("a stale review linked a mailbox")
	}
	if err := h.conns.Save(connectedWithGmail(connections.HealthHealthy)); err != nil {
		t.Fatal(err)
	}

	// The workspace's own "Connect email" CTA links between review and commit.
	fresh, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	second := h.review(t, fresh, "review-b")
	if _, err := h.adapter.linker.LinkWorkspaceMailbox(ctx, "local", "ws-email", "acct-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.link(second.Journey, second.Review, "link-b"); err == nil {
		t.Fatal("a review taken before the CTA link was still committed")
	}
	after, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := h.store.Get("ws-email")
	if after.Lifecycle != setupjourney.LifecycleReady || h.sink.calls != 0 || len(saved.MCPBindings) != 1 {
		t.Fatalf("after CTA link lifecycle = %s sink = %d bindings = %d", after.Lifecycle, h.sink.calls, len(saved.MCPBindings))
	}
}

// FR 33: an interrupted commit reconciles to already_current from the owner
// read instead of re-linking.
func TestEmailOpsQuestInterruptedLinkReconcilesWithoutRelinking(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	start, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := h.review(t, start, "review-i")
	// The link lands, then the wizard step "fails" to report back: model a
	// commit whose response was lost by failing after the linker ran.
	h.adapter.wizard = failingAfterLink{}
	h.adapter.sink = &questSink{account: "acct-1"}
	realLinker := h.adapter.linker
	h.adapter.linker = linkThenFail{inner: realLinker}
	if _, err := h.link(reviewed.Journey, reviewed.Review, "link-i"); err == nil {
		t.Fatal("interrupted commit reported success")
	}
	recovered, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Lifecycle != setupjourney.LifecycleReady || recovered.Busy {
		t.Fatalf("interrupted link did not reconcile: %+v", recovered)
	}
	calls := h.adapter.sink.(*questSink).calls
	if _, err := h.link(reviewed.Journey, reviewed.Review, "link-i"); err != nil {
		t.Fatalf("replay after reconcile: %v", err)
	}
	if h.adapter.sink.(*questSink).calls != calls {
		t.Fatal("replay after reconcile linked again")
	}
}

type failingAfterLink struct{}

func (failingAfterLink) Confirm(context.Context, string, string, setupwizard.StepAction) (setupwizard.Status, error) {
	return setupwizard.Status{}, nil
}

type linkThenFail struct{ inner questMailboxLinker }

func (l linkThenFail) LinkWorkspaceMailbox(ctx context.Context, userID, workspaceID, accountID string) (personalhqhttp.MailboxStatus, error) {
	if _, err := l.inner.LinkWorkspaceMailbox(ctx, userID, workspaceID, accountID); err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return personalhqhttp.MailboxStatus{}, errors.New("response lost after the link was saved")
}

// FR 38, success metric 2: the quest is ready exactly when the workspace email
// status says setup is ready, for every readiness verdict.
func TestEmailOpsQuestReadyMatchesWorkspaceEmailStatus(t *testing.T) {
	ctx := context.Background()
	notEnabled := connectedWithGmail(connections.HealthHealthy)
	notEnabled.Grants = nil
	cases := []struct {
		name     string
		conn     *connections.Connection
		vaults   connections.VaultCatalog
		linked   bool
		accounts emailAccountResolver
	}{
		{"not connected", nil, healthyVaults(), true, healthyAccount()},
		{"gmail not enabled", notEnabled, healthyVaults(), true, healthyAccount()},
		{"grant needs reconnect", connectedWithGmail(connections.HealthReconnectRequired), healthyVaults(), true, healthyAccount()},
		{"vault locked", connectedWithGmail(connections.HealthHealthy), lockedVaults(), true, healthyAccount()},
		{"not linked", connectedWithGmail(connections.HealthHealthy), healthyVaults(), false, healthyAccount()},
		{"linked account missing", connectedWithGmail(connections.HealthHealthy), healthyVaults(), true, fakeAccounts{err: errors.New("gone")}},
		{"linked credential expired", connectedWithGmail(connections.HealthHealthy), healthyVaults(), true, fakeAccounts{acc: &vault.EmailAccount{ID: "acct-1", EmailAddress: "me@example.com"}}},
		{"ready", connectedWithGmail(connections.HealthHealthy), healthyVaults(), true, healthyAccount()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := emailOpsQuestWorkspaceStore(t, test.linked)
			evaluator := newEmailReadinessEvaluator(connectionStore(t, test.conn), test.vaults, store, test.accounts)
			linker := newMailboxLinkerService(nil, store, test.accounts, nil)
			linker.readiness = evaluator
			status, err := linker.WorkspaceMailboxStatus(ctx, "local", "ws-email")
			if err != nil || status.Setup == nil {
				t.Fatalf("workspace email status = %+v err = %v", status, err)
			}
			journey := questLifecycleFor(t, store, evaluator)
			if (journey == setupjourney.LifecycleReady) != status.Setup.Ready {
				t.Fatalf("quest lifecycle %s but workspace setup.ready = %v (%s)", journey, status.Setup.Ready, status.Setup.Reason)
			}
		})
	}
}

func questLifecycleFor(t *testing.T, store *workspace.InMemoryStore, evaluator *emailReadinessEvaluator) setupjourney.LifecycleState {
	t.Helper()
	ctx := context.Background()
	db := newFixtureDatabase(t)
	readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader)
	for _, kind := range specialist.SetupStepKinds() {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{}, nil
		})
	}
	readers[specialist.SetupStepWorkspaceCreate] = emailOpsWorkspaceCreateReader{source: store}
	readers[specialist.SetupStepAccountConnect] = emailOpsAccountConnectReader{readiness: evaluator, clientConfigured: func() bool { return true }}
	readers[specialist.SetupStepAccountLink] = emailOpsAccountLinkReader{readiness: evaluator, workspaces: store}
	readers[specialist.SetupStepSummary] = setupSummaryReader{}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := setupjourney.NewService(setupjourney.NewSQLiteStore(db), noRelationship{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(setupjourney.NewHostQuestCatalog(hostquests.All()))
	scoped, err := service.ForHostQuest(ctx, "local", hostquests.EmailOpsSetupQuestID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := scoped.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	return projection.Lifecycle
}

func isQuestFailure(err error, reason setupjourney.ReasonCode) bool {
	var failure *setupjourney.Failure
	return errors.As(err, &failure) && failure.ReasonCode == reason
}

func dumpQuestTable(t *testing.T, db *database.DB, table string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT * FROM "+table) // #nosec G202 -- fixed table names in a test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			fmt.Fprintf(&dump, "%q ", fmt.Sprint(value))
		}
		dump.WriteString("\n")
	}
	return dump.String()
}

// FR 6, 3.4: without the mailbox runtime the account steps stay fail-closed and
// no link adapter is registered; with every owner present all three readers
// and the adapter are real.
func TestEmailOpsQuestReadersDegradeWithoutTheMailboxRuntime(t *testing.T) {
	stub := setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
	})
	fresh := func() map[specialist.SetupStepKind]setupjourney.CanonicalReader {
		readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader)
		for _, kind := range specialist.SetupStepKinds() {
			readers[kind] = stub
		}
		return readers
	}

	readers := fresh()
	if adapter := (&ServerBuilder{}).emailOpsQuestReaders(readers); adapter != nil {
		t.Fatal("a builder without stores registered a link adapter")
	}
	for _, kind := range []specialist.SetupStepKind{specialist.SetupStepAccountConnect, specialist.SetupStepAccountLink} {
		if _, stillStub := readers[kind].(setupjourney.CanonicalReaderFunc); !stillStub {
			t.Fatalf("%s left fail-closed stub without its owner", kind)
		}
	}
	if _, err := setupjourney.NewReaderRegistry(readers); err != nil {
		t.Fatalf("degraded registry: %v", err)
	}
}
