package foldersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

type exactWorldStaffing struct {
	worldStaffing
	calls    []rosterstaffing.RoleIntent
	digests  []string
	failRole string
	replay   bool
}

func (*exactWorldStaffing) Missing(context.Context, string) ([]HomeRole, error) {
	panic("exact team entered legacy name allocation")
}
func (*exactWorldStaffing) ModelReady(context.Context) bool {
	panic("exact team consulted new-agent default")
}
func (*exactWorldStaffing) Staff(context.Context, string, HomeRole) error {
	panic("exact team entered legacy creation")
}
func (s *exactWorldStaffing) StaffReviewed(_ context.Context, _ string, role rosterstaffing.RoleIntent, digest string) (string, error) {
	s.calls = append(s.calls, role)
	s.digests = append(s.digests, digest)
	if role.RoleID == s.failRole {
		return "", errors.New("changed source")
	}
	if s.replay {
		return "already_present", nil
	}
	if role.Mode == "assign" {
		return "assigned", nil
	}
	return "created", nil
}

func exactPortfolioPlan() personalassistant.FolderSetupPlan {
	destination := &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "home-1", Name: "Music Production Home", Kind: "home", OwnerUserID: "local",
		ProviderPlugin: "fixture", ProviderVersion: "1.0.0", ProgramID: "collection", DeclarationDigest: strings.Repeat("c", 64), CompatibilityHash: strings.Repeat("d", 64)}
	return (personalassistant.FolderSetupPlan{Destination: destination,
		Lines: []personalassistant.FolderPlanLine{{Kind: personalassistant.FolderPlanAgents, Name: "Reuse Aster; create Cedar"}},
		Intent: personalassistant.FolderSetupIntent{Portfolio: true, StaffsHome: true, Placement: "grouped", HomeTemplate: "plugin-home:fixture:collection", ProviderPlugin: "fixture", ProviderVersion: "1.0.0",
			HomeStaffing: &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{
				{RoleID: "manager", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: "original-source"},
				{RoleID: "helper", Scope: "home", Mode: "create", Name: "Cedar", Provider: "fixture", Model: "model"},
			}}}}).Stamped()
}

func TestPortfolioReviewedHomeUsesExactActionsAndCanonicalReplayWithoutNewAgentDefaults(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "replay"}[replay], func(t *testing.T) {
			plan := exactPortfolioPlan()
			if plan.Digest == "" {
				t.Fatal("invalid fixture")
			}
			w := newPortfolioWorld()
			w.homeID, w.initialized, w.rootID, w.scanned, w.modelReady = "home-1", true, "root-1", true, false
			staffing := &exactWorldStaffing{worldStaffing: worldStaffing{w}, replay: replay}
			result, _ := runPortfolio(t, w, plan, func(r *PortfolioRunner, cfg *PortfolioConfig) { r.Staffing, cfg.HomeID = staffing, "home-1" })
			if result.Status != personalassistant.FolderSetupDone || len(staffing.calls) != 2 || len(w.receiptFacts.HomeRoles) != 2 || len(w.receiptFacts.AddedAgents) != 0 {
				t.Fatal("exact roles were not consumed", result, staffing.calls)
			}
			for i, call := range staffing.calls {
				if call.Name != plan.Intent.HomeStaffing.Roles[i].Name || call.Mode != plan.Intent.HomeStaffing.Roles[i].Mode || staffing.digests[i] != plan.Digest {
					t.Fatal("execution substituted or renewed a choice")
				}
				role := w.receiptFacts.HomeRoles[i]
				want := []string{"assigned", "created"}[i]
				if replay {
					want = "already_present"
				}
				if role.Outcome != want || role.ChoiceDigest != call.StepDigest(plan.Digest, "home-1") || !HomeRoleReceipt(role).Assigned {
					t.Fatal("untruthful role receipt", role)
				}
			}
		})
	}
}

func TestPortfolioReviewedHomeStopsAfterChangedRoleAndPreservesCompletedReceipt(t *testing.T) {
	plan := exactPortfolioPlan()
	plan.Intent.HomeStaffing.Roles = append(plan.Intent.HomeStaffing.Roles, rosterstaffing.RoleIntent{RoleID: "later", Scope: "home", Mode: "create", Name: "Elm", Provider: "fixture", Model: "model"})
	plan = plan.Stamped()
	w := newPortfolioWorld()
	staffing := &exactWorldStaffing{worldStaffing: worldStaffing{w}, failRole: "helper"}
	result, progress := runPortfolio(t, w, plan, func(r *PortfolioRunner, cfg *PortfolioConfig) { r.Staffing, cfg.HomeID = staffing, "home-1" })
	if result.StopReason != personalassistant.FolderStopPlanChanged || len(staffing.calls) != 2 || w.called("library:on") || w.called("consent:grant") {
		t.Fatal("partial team continued effects", result, w.log)
	}
	last := progress.updates[len(progress.updates)-1]
	if len(last.Receipt) != 3 || !last.Receipt[0].Assigned || last.Receipt[0].Detail != "reused saved agent" || last.Receipt[1].Assigned || last.Receipt[2].Assigned {
		t.Fatal("partial outcome was erased or fabricated", last.Receipt)
	}
}

func TestPortfolioUndecidedHomeTeamCannotFallBackToLegacyEffects(t *testing.T) {
	w := newPortfolioWorld()
	runner := &PortfolioRunner{Homes: w, Staffing: &exactWorldStaffing{worldStaffing: worldStaffing{w}}, Library: worldLibrary{w}, Receipts: worldReceipts{w}, Progress: &recorder{}}
	plan := exactPortfolioPlan()
	plan.Intent.RequiresHomeStaffing, plan.Intent.HomeStaffing = true, nil
	_, err := runner.Run(t.Context(), PortfolioConfig{Plan: plan})
	if !errors.Is(err, personalassistant.ErrFolderPlanChanged) || len(w.log) != 0 {
		t.Fatal("undecided team reached legacy effects", err, w.log)
	}
}

func TestPortfolioReviewedHomeCannotDowngradeIntoLegacyOrChangedPlan(t *testing.T) {
	for _, changed := range []bool{false, true} {
		w := newPortfolioWorld()
		runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Receipts: worldReceipts{w}, Progress: &recorder{}}
		plan := exactPortfolioPlan()
		if changed {
			runner.Staffing = &exactWorldStaffing{worldStaffing: worldStaffing{w}}
			plan.Intent.HomeStaffing.Roles[0].Name = "Substituted"
		}
		_, err := runner.Run(t.Context(), PortfolioConfig{Plan: plan})
		want := ErrPortfolioNotWired
		if changed {
			want = personalassistant.ErrFolderPlanChanged
		}
		if !errors.Is(err, want) || len(w.log) != 0 {
			t.Fatal("unsafe team reached effects", err, w.log)
		}
	}
}
