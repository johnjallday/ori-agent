package foldersetup

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// ReviewedHomeStaffing consumes an exact private choice through the canonical
// role owner. Legacy Missing/ModelReady/Staff must never consume this intent:
// they may allocate a free name and know nothing about selected source identity.
// The returned outcome is observed from the role owner's binding/receipt, not
// a second executor journal. No recommendation or default-model resolution is
// allowed here.
type ReviewedHomeStaffing interface {
	StaffReviewed(context.Context, string, rosterstaffing.RoleIntent, string) (string, error)
}

// HomeRoleOutcome is bounded receipt input, not authority to perform a role.
// ChoiceDigest lets the receipt reader verify the same canonical outcome; no
// source/configuration witness enters the displayed FolderReceiptRow.
type HomeRoleOutcome struct {
	RoleID, Name, Action, Outcome, ChoiceDigest string
}

func (s *portfolioRun) staffReviewedHome(ctx context.Context, team *rosterstaffing.TeamIntent) error {
	owner, ok := s.runner.Staffing.(ReviewedHomeStaffing)
	if !ok {
		return ErrPortfolioNotWired // also checked before any runner effect
	}
	if err := s.working(ctx, personalassistant.FolderPlanAgents); err != nil {
		return err
	}
	roles := team.Clone().Roles
	for i, role := range roles {
		if err := s.guard(ctx); err != nil {
			return err
		}
		if role.Mode == "create" && (role.Provider == "" || role.Model == "") {
			s.recordUnfinishedHomeRoles(roles[i:])
			return &stop{reason: personalassistant.FolderStopNeedsModel}
		}
		outcome, err := owner.StaffReviewed(ctx, s.homeID, role, s.cfg.Plan.Digest)
		valid := outcome == "already_present" || (role.Mode == "assign" && outcome == "assigned") || (role.Mode == "create" && outcome == "created")
		if err != nil || !valid {
			s.recordUnfinishedHomeRoles(roles[i:])
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "a reviewed Home role could not be completed; review the remaining team"}
		}
		result := HomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, Outcome: outcome, ChoiceDigest: role.StepDigest(s.cfg.Plan.Digest, s.homeID)}
		s.homeRoles = append(s.homeRoles, result)
		s.receipt = append(s.receipt, HomeRoleReceipt(result))
		if err := s.record(ctx, personalassistant.FolderSetupRunning, ""); err != nil {
			return err
		}
	}
	s.done(personalassistant.FolderPlanAgents)
	return s.record(ctx, personalassistant.FolderSetupRunning, "")
}

func (s *portfolioRun) recordUnfinishedHomeRoles(roles []rosterstaffing.RoleIntent) {
	for _, role := range roles {
		s.receipt = append(s.receipt, HomeRoleReceipt(HomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, Outcome: "not_completed"}))
	}
}

// HomeRoleReceipt projects only the outcome supplied by the canonical owner.
// Callers must verify current binding identity before presenting it as current.
func HomeRoleReceipt(role HomeRoleOutcome) personalassistant.FolderReceiptRow {
	operation := "reuse"
	if role.Action == "create" {
		operation = "create"
	}
	row := personalassistant.FolderReceiptRow{Kind: "agent", Name: role.Name, Operation: operation}
	switch role.Outcome {
	case "assigned":
		row.Detail, row.Assigned = "reused saved agent", true
	case "created":
		row.Detail, row.Assigned = "created", true
	case "already_present":
		row.Detail, row.Assigned = "already present", true
	default:
		row.Detail = "not completed; review this role"
	}
	return row
}
