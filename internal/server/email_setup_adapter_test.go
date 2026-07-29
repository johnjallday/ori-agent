package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func emailStepRequest(kind string) setupwizard.StepRequest {
	return setupwizard.StepRequest{
		WorkspaceID: "ws-1",
		Step: workspace.SetupWizardStep{
			ID:       kind,
			Kind:     kind,
			Required: true,
			Adapter:  emailSetupAdapterID,
		},
	}
}

// TestEmailStepReadiness_KeepsTheLayersApart is the whole point of the Email
// adapter: an account connection, a mail permission on it, a credential still in
// the vault, and a mailbox linked to *this* workspace all look like "email is
// set up" from outside, and the repair for each is different.
//
// The table is the contract. What matters is not the wording but which of two
// things each state is: something the user has not done yet, or something that
// was working and broke.
func TestEmailStepReadiness_KeepsTheLayersApart(t *testing.T) {
	cases := []struct {
		name     string
		in       EmailReadiness
		ready    bool
		blocked  bool
		category string
	}{
		{
			name:     "no account connected",
			in:       EmailReadiness{Action: emailActionConnectGoogle, Message: "Connect an account first."},
			category: setupwizard.ErrorCategoryPermissionRequired,
		},
		{
			name:     "account connected without mail permission",
			in:       EmailReadiness{Action: emailActionEnableGmail, Message: "Enable mail for this account."},
			category: setupwizard.ErrorCategoryPermissionRequired,
		},
		{
			name:     "account healthy but this workspace has no mailbox",
			in:       EmailReadiness{Action: emailActionLinkAccount, Message: "Connect your email account to this workspace."},
			category: setupwizard.ErrorCategoryNotConfigured,
		},
		{
			name:     "credential gone from the vault",
			in:       EmailReadiness{Action: emailActionRepairVault, Message: "Your credential is no longer available."},
			blocked:  true,
			category: setupwizard.ErrorCategoryPermissionRequired,
		},
		{
			name:     "linked account needs reconnecting",
			in:       EmailReadiness{Action: emailActionReconnect, Message: "Reconnect the linked account."},
			blocked:  true,
			category: setupwizard.ErrorCategoryPermissionRequired,
		},
		{
			name:  "ready",
			in:    EmailReadiness{Ready: true, AccountID: "acct-1", EmailAddress: "someone@example.test"},
			ready: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := emailStepReadiness(tc.in)
			if got.Ready != tc.ready {
				t.Errorf("Ready = %v, want %v", got.Ready, tc.ready)
			}
			if got.Blocked != tc.blocked {
				t.Errorf("Blocked = %v, want %v (a repair and an unfinished step are different states)", got.Blocked, tc.blocked)
			}
			if got.ErrorCategory != tc.category {
				t.Errorf("category = %q, want %q", got.ErrorCategory, tc.category)
			}
			if got.Summary == "" {
				t.Error("every state must give the user something to act on")
			}
		})
	}
}

// TestEmailStepReadiness_SummaryNamesNoAddress covers the safety half: the
// wizard's payload travels into logs and analytics, so it describes state and
// never who the mailbox belongs to.
func TestEmailStepReadiness_SummaryNamesNoAddress(t *testing.T) {
	ready := emailStepReadiness(EmailReadiness{
		Ready:        true,
		AccountID:    "acct-1",
		EmailAddress: "someone@example.test",
	})
	if strings.Contains(ready.Summary, "@") || strings.Contains(ready.Summary, "acct-1") {
		t.Fatalf("the summary must not carry the address or account id: %q", ready.Summary)
	}
}

func TestEmailSetupAdapter_UnavailableEvaluatorBlocksRatherThanPanics(t *testing.T) {
	var adapter *emailSetupAdapter
	readiness, err := adapter.Evaluate(context.Background(), emailStepRequest(workspace.SetupStepKindAccountLink))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !readiness.Blocked || readiness.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatalf("an unwired adapter blocks with a safe category: %+v", readiness)
	}
	if _, err := adapter.Confirm(context.Background(), emailStepRequest(workspace.SetupStepKindAccountLink), setupwizard.StepAction{}); err == nil {
		t.Fatal("an unwired adapter must refuse to act")
	}
}

// TestEmailSetupAdapter_EveryStepReadsOneVerdict pins that the link, readiness,
// and summary steps agree by construction: they are the same evaluation, so a
// workspace cannot be linked according to one and unlinked according to another.
func TestEmailSetupAdapter_EveryStepReadsOneVerdict(t *testing.T) {
	adapter := newEmailSetupAdapter(&emailReadinessEvaluator{})
	ctx := context.Background()

	var seen []setupwizard.StepReadiness
	for _, kind := range []string{
		workspace.SetupStepKindAccountLink,
		workspace.SetupStepKindReadiness,
		workspace.SetupStepKindSummary,
	} {
		readiness, err := adapter.Evaluate(ctx, emailStepRequest(kind))
		if err != nil {
			t.Fatalf("Evaluate(%s): %v", kind, err)
		}
		seen = append(seen, readiness)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i].Ready != seen[0].Ready || seen[i].Blocked != seen[0].Blocked {
			t.Fatalf("steps disagree about the same workspace: %+v vs %+v", seen[0], seen[i])
		}
	}
}
