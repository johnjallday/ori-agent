package setupjourney

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

func accountLinkDeclarationFixture(t *testing.T) *specialist.SetupJourney {
	t.Helper()
	declaration, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1, ID: "account_setup_fixture",
		Title: "Set up an account workspace", Description: "Create a workspace, connect an account, and link it.",
		ExpectedBlueprintID: "account-blueprint",
		Steps: []specialist.SetupJourneyStep{
			{ID: "team", Kind: specialist.SetupStepWorkspaceCreate, Title: "Review your team", Description: "Create the workspace."},
			{ID: "connect", Kind: specialist.SetupStepAccountConnect, Title: "Connect the account", Description: "Connect in Settings."},
			{ID: "mailbox", Kind: specialist.SetupStepAccountLink, Title: "Link the mailbox", Description: "Confirm the link."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Ready", Description: "Review what is ready."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return declaration
}

func accountLinkServiceFixture(t *testing.T, reads map[specialist.SetupStepKind]CanonicalStepRead, scopes map[specialist.SetupStepKind][]ReadScope) *Service {
	t.Helper()
	_, store := openTestStore(t)
	declaration := accountLinkDeclarationFixture(t)
	entry := specialist.Entry{Slug: "account_fixture", DisplayName: "account fixture", SetupJourney: declaration}
	relationship := acceptedRelationship()
	relationship.SpecialistSlug = entry.Slug
	service, err := newService(store, &relationshipStub{state: relationship}, readerRegistryStub(t, reads, nil, nil, scopes),
		func(slug string) (specialist.Entry, bool) { return entry, slug == entry.Slug }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func stepByID(t *testing.T, projection *JourneyProjection, id string) StepProjection {
	t.Helper()
	for _, step := range projection.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("step %q missing from %#v", id, projection.Steps)
	return StepProjection{}
}

// The account-link shape runs through the unchanged generic reconciler:
// pending → active → complete → ready, a blocked owner surfaces its reason, and
// a later regression needs attention without a second completion timestamp.
func TestAccountLinkShapeReconcilesThroughEveryLifecycle(t *testing.T) {
	ctx := context.Background()
	reads := map[specialist.SetupStepKind]CanonicalStepRead{
		specialist.SetupStepWorkspaceCreate: {
			AvailableActions: []ActionID{ActionReviewTeam},
			WorkspaceCreate:  &WorkspaceCreateProjection{TemplateTitle: "Email Ops"},
		},
	}
	scopes := make(map[specialist.SetupStepKind][]ReadScope)
	service := accountLinkServiceFixture(t, reads, scopes)

	fresh, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	team := stepByID(t, fresh, "team")
	if fresh.Lifecycle != LifecycleNotStarted || fresh.CurrentStepID != "team" || team.Status != StepActive ||
		len(team.Actions) != 1 || team.Actions[0].ID != ActionReviewTeam || team.Actions[0].Label != "Review your team" ||
		team.WorkspaceCreate == nil || team.WorkspaceCreate.TemplateTitle != "Email Ops" {
		t.Fatalf("fresh account-link projection = %#v", fresh)
	}
	if fresh.Receipts.ProjectWorkspaceID != "" {
		t.Fatalf("fresh read invented a workspace receipt: %#v", fresh.Receipts)
	}
	opened, err := service.Open(ctx, "local", fresh.RunID, PresentationMutation{IfRevision: fresh.StateRevision, IdempotencyKey: "account-open"})
	if err != nil || opened.Lifecycle != LifecycleInProgress {
		t.Fatalf("open = %#v, err = %v", opened, err)
	}

	// Step 1 completes with the workspace receipt; step 2 is blocked by its owner.
	reads[specialist.SetupStepWorkspaceCreate] = CanonicalStepRead{
		Complete: true, AvailableActions: []ActionID{ActionOpenWorkspace},
		Result: CanonicalResult{ProjectWorkspaceID: "workspace-email-ops"},
		WorkspaceCreate: &WorkspaceCreateProjection{
			TemplateTitle: "Email Ops", WorkspaceID: "workspace-email-ops", WorkspaceLabel: "Email Ops", WorkspaceRoute: "/workspaces/email-ops",
		},
	}
	reads[specialist.SetupStepAccountConnect] = CanonicalStepRead{
		BlockedReason:    ReasonAccountReconnectRequired,
		AvailableActions: []ActionID{ActionOpenAccountSettings, ActionRecheckConnection},
		AccountConnect: &AccountConnectProjection{
			Configured: true, IdentityEmail: "person@example.com", GmailHealth: AccountHealthUnhealthy,
			ActionLabel: "Reconnect Gmail", ActionURL: "/settings#google-account",
		},
	}
	blocked, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	connect := stepByID(t, blocked, "connect")
	if blocked.Receipts.ProjectWorkspaceID != "workspace-email-ops" || blocked.CurrentStepID != "connect" ||
		connect.Status != StepBlocked || connect.ReasonCode != ReasonAccountReconnectRequired || connect.Guidance == "" ||
		len(connect.Actions) != 2 || blocked.Lifecycle != LifecycleNeedsAttention {
		t.Fatalf("blocked account-link projection = %#v", blocked)
	}
	if mailbox := stepByID(t, blocked, "mailbox"); mailbox.Status != StepPending || len(mailbox.Actions) != 0 {
		t.Fatalf("a later step published actions out of order: %#v", mailbox)
	}
	lastConnect := scopes[specialist.SetupStepAccountConnect][len(scopes[specialist.SetupStepAccountConnect])-1]
	if lastConnect.Shape != specialist.SetupJourneyShapeAccountLink || lastConnect.ProjectWorkspaceID != "workspace-email-ops" {
		t.Fatalf("later readers did not receive the shape and workspace receipt: %#v", lastConnect)
	}

	// Step 2 completes; step 3 is the one gap the quest can close itself.
	reads[specialist.SetupStepAccountConnect] = CanonicalStepRead{
		Complete: true,
		AccountConnect: &AccountConnectProjection{
			Configured: true, IdentityEmail: "person@example.com", GmailHealth: AccountHealthHealthy,
		},
	}
	reads[specialist.SetupStepAccountLink] = CanonicalStepRead{
		AvailableActions: []ActionID{ActionReviewMailboxLink},
		AccountLink:      &AccountLinkProjection{WorkspaceLabel: "Email Ops", AccountEmail: "person@example.com"},
	}
	linking, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if mailbox := stepByID(t, linking, "mailbox"); linking.Lifecycle != LifecycleInProgress || linking.CurrentStepID != "mailbox" ||
		mailbox.Status != StepActive || len(mailbox.Actions) != 1 || mailbox.Actions[0].Effect != ActionEffectReview {
		t.Fatalf("link-pending projection = %#v", linking)
	}

	// Everything before the summary is complete: the run is ready once.
	reads[specialist.SetupStepAccountLink] = CanonicalStepRead{
		Complete:    true,
		AccountLink: &AccountLinkProjection{WorkspaceLabel: "Email Ops", AccountEmail: "person@example.com", Linked: true, Ready: true},
	}
	reads[specialist.SetupStepSummary] = CanonicalStepRead{AvailableActions: []ActionID{ActionOpenWorkspace, ActionOpenModelSettings}}
	ready, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	summary := stepByID(t, ready, "summary")
	if ready.Lifecycle != LifecycleReady || ready.CurrentStepID != "" || ready.FirstCompletedAt == nil ||
		summary.Status != StepComplete || len(summary.Actions) != 2 || summary.Actions[0].Label != "Open Email Ops" {
		t.Fatalf("ready account-link projection = %#v", ready)
	}
	completedAt := *ready.FirstCompletedAt

	// A revoked grant after completion regresses to needs_attention, hides the
	// summary offers, and keeps the original completion time.
	reads[specialist.SetupStepAccountConnect] = CanonicalStepRead{
		BlockedReason:    ReasonAccountReconnectRequired,
		AvailableActions: []ActionID{ActionOpenAccountSettings, ActionRecheckConnection},
	}
	regressed, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if regressed.Lifecycle != LifecycleNeedsAttention || regressed.CurrentStepID != "connect" ||
		regressed.FirstCompletedAt == nil || !regressed.FirstCompletedAt.Equal(completedAt) {
		t.Fatalf("regressed account-link projection = %#v", regressed)
	}
	if summary := stepByID(t, regressed, "summary"); summary.Status != StepPending || len(summary.Actions) != 0 {
		t.Fatalf("regressed summary kept its offers: %#v", summary)
	}
}

// Typed projections and receipts stay pinned to their own kind: a reader that
// returns another kind's projection, or an account/credential receipt, is
// treated as an unavailable owner rather than trusted.
func TestAccountLinkShapeRejectsForeignProjectionsAndReceipts(t *testing.T) {
	cases := map[string]struct {
		kind specialist.SetupStepKind
		read CanonicalStepRead
	}{
		"connect projection on team": {specialist.SetupStepWorkspaceCreate, CanonicalStepRead{
			AccountConnect: &AccountConnectProjection{GmailHealth: AccountHealthUnconfigured},
		}},
		"link projection on connect": {specialist.SetupStepAccountConnect, CanonicalStepRead{
			AccountLink: &AccountLinkProjection{WorkspaceLabel: "Email Ops"},
		}},
		"team projection on link": {specialist.SetupStepAccountLink, CanonicalStepRead{
			WorkspaceCreate: &WorkspaceCreateProjection{TemplateTitle: "Email Ops"},
		}},
		"account projection on a specialist kind": {specialist.SetupStepProjectConnect, CanonicalStepRead{
			AccountLink: &AccountLinkProjection{WorkspaceLabel: "Email Ops"},
		}},
		"workspace receipt on connect": {specialist.SetupStepAccountConnect, CanonicalStepRead{
			Complete: true, Result: CanonicalResult{ProjectWorkspaceID: "workspace-email-ops"},
		}},
		"receipt id on link": {specialist.SetupStepAccountLink, CanonicalStepRead{
			Complete: true, Result: CanonicalResult{CanonicalReceiptID: "account-1"},
		}},
		"home receipt on team": {specialist.SetupStepWorkspaceCreate, CanonicalStepRead{
			Complete: true, Result: CanonicalResult{HomeWorkspaceID: "home-1"},
		}},
		"specialist action on team": {specialist.SetupStepWorkspaceCreate, CanonicalStepRead{
			AvailableActions: []ActionID{ActionOpenProject},
		}},
		"commit action on connect": {specialist.SetupStepAccountConnect, CanonicalStepRead{
			AvailableActions: []ActionID{ActionLinkMailbox},
		}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if validCanonicalRead(test.kind, test.read) {
				t.Fatalf("validCanonicalRead accepted %#v for %s", test.read, test.kind)
			}
		})
	}
	valid := map[specialist.SetupStepKind]CanonicalStepRead{
		specialist.SetupStepWorkspaceCreate: {Complete: true, Result: CanonicalResult{ProjectWorkspaceID: "workspace-email-ops"}},
		specialist.SetupStepAccountConnect:  {AvailableActions: []ActionID{ActionOpenAccountSettings, ActionRecheckConnection}},
		specialist.SetupStepAccountLink:     {AvailableActions: []ActionID{ActionReviewMailboxLink, ActionOpenAccountSettings}},
		specialist.SetupStepSummary:         {AvailableActions: []ActionID{ActionOpenWorkspace, ActionStartInboxTriage, ActionOpenModelSettings}},
	}
	for kind, read := range valid {
		if !validCanonicalRead(kind, read) {
			t.Errorf("validCanonicalRead rejected a well-formed %s read: %#v", kind, read)
		}
	}
}

func TestAccountProjectionValidators(t *testing.T) {
	longLabel := strings.Repeat("a", maxAccountProjectionLabelBytes+1)
	controlLabel := "Email" + string(rune(7)) + "Ops"
	createCases := map[string]struct {
		value *WorkspaceCreateProjection
		ok    bool
	}{
		"nil":                 {nil, true},
		"pending":             {&WorkspaceCreateProjection{TemplateTitle: "Email Ops"}, true},
		"found":               {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "Email Ops", WorkspaceRoute: "/workspaces/email-ops"}, true},
		"found without route": {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "Email Ops"}, true},
		"blank title":         {&WorkspaceCreateProjection{TemplateTitle: " "}, false},
		"long label":          {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: longLabel}, false},
		"label without id":    {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceLabel: "Email Ops"}, false},
		"route without id":    {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceRoute: "/workspaces/email-ops"}, false},
		"external route":      {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "E", WorkspaceRoute: "https://example.com/workspaces/x"}, false},
		"traversal route":     {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "E", WorkspaceRoute: "/workspaces/../settings"}, false},
		"nested route":        {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "E", WorkspaceRoute: "/workspaces/a/b"}, false},
		"query route":         {&WorkspaceCreateProjection{TemplateTitle: "Email Ops", WorkspaceID: "ws-1", WorkspaceLabel: "E", WorkspaceRoute: "/workspaces/a?panel=x"}, false},
		"control in title":    {&WorkspaceCreateProjection{TemplateTitle: controlLabel}, false},
	}
	for name, test := range createCases {
		if got := validWorkspaceCreateProjection(test.value); got != test.ok {
			t.Errorf("workspace create %q: valid = %v, want %v", name, got, test.ok)
		}
	}

	connectCases := map[string]struct {
		value *AccountConnectProjection
		ok    bool
	}{
		"unconfigured":                     {&AccountConnectProjection{GmailHealth: AccountHealthUnconfigured, ActionLabel: "Open Google Account", ActionURL: "/settings#google-account"}, true},
		"healthy":                          {&AccountConnectProjection{Configured: true, IdentityEmail: "person@example.com", GmailHealth: AccountHealthHealthy}, true},
		"vault query":                      {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthVaultUnavailable, ActionLabel: "Unlock vault", ActionURL: "/settings#google-account?gc_action=unlock"}, true},
		"unknown health":                   {&AccountConnectProjection{Configured: true, GmailHealth: "maybe"}, false},
		"unconfigured but set":             {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthUnconfigured}, false},
		"configured health without client": {&AccountConnectProjection{GmailHealth: AccountHealthNotConnected}, false},
		"healthy without email":            {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthHealthy}, false},
		"bad email":                        {&AccountConnectProjection{Configured: true, IdentityEmail: "not an address", GmailHealth: AccountHealthNotEnabled}, false},
		"two at signs":                     {&AccountConnectProjection{Configured: true, IdentityEmail: "a@b@c", GmailHealth: AccountHealthNotEnabled}, false},
		"external url":                     {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthNotEnabled, ActionURL: "https://accounts.google.com"}, false},
		"protocol-relative":                {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthNotEnabled, ActionURL: "//evil.example/settings"}, false},
		"non-settings route":               {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthNotEnabled, ActionURL: "/api/connections/google/start"}, false},
		"quoted route":                     {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthNotEnabled, ActionURL: "/settings\"onload=x"}, false},
		"long action label":                {&AccountConnectProjection{Configured: true, GmailHealth: AccountHealthNotEnabled, ActionLabel: strings.Repeat("a", maxAccountProjectionActionBytes+1)}, false},
	}
	for name, test := range connectCases {
		if got := validAccountConnectProjection(test.value); got != test.ok {
			t.Errorf("account connect %q: valid = %v, want %v", name, got, test.ok)
		}
	}

	linkCases := map[string]struct {
		value *AccountLinkProjection
		ok    bool
	}{
		"review":                 {&AccountLinkProjection{WorkspaceLabel: "Email Ops", AccountEmail: "person@example.com"}, true},
		"ready":                  {&AccountLinkProjection{WorkspaceLabel: "Email Ops", AccountEmail: "person@example.com", Linked: true, Ready: true}, true},
		"linked account missing": {&AccountLinkProjection{WorkspaceLabel: "Email Ops", Linked: true}, true},
		"ready without link":     {&AccountLinkProjection{WorkspaceLabel: "Email Ops", AccountEmail: "person@example.com", Ready: true}, false},
		"ready without email":    {&AccountLinkProjection{WorkspaceLabel: "Email Ops", Linked: true, Ready: true}, false},
		"blank label":            {&AccountLinkProjection{WorkspaceLabel: ""}, false},
		"control in label":       {&AccountLinkProjection{WorkspaceLabel: controlLabel}, false},
	}
	for name, test := range linkCases {
		if got := validAccountLinkProjection(test.value); got != test.ok {
			t.Errorf("account link %q: valid = %v, want %v", name, got, test.ok)
		}
	}
}

// Every new reason code is closed, carries compiled guidance, and that
// guidance never names an address, path, vault, or URL.
func TestAccountLinkReasonGuidanceIsSafe(t *testing.T) {
	for _, code := range []ReasonCode{
		ReasonWorkspaceRequired, ReasonAccountConnectionNotConfigured,
		ReasonAccountReconnectRequired, ReasonAccountVaultRepairRequired,
		ReasonMailboxLinkRequired, ReasonMailboxAccountUnavailable,
	} {
		guidance := safeGuidance[code]
		if !validateReasonCode(code, false) || guidance == "" {
			t.Fatalf("reason %q is not a closed guided code", code)
		}
		if strings.ContainsAny(guidance, "@/\\<>") || strings.Contains(guidance, "://") {
			t.Fatalf("reason %q guidance is not plain safe text: %q", code, guidance)
		}
	}
}
