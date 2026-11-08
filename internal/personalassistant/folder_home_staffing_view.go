package personalassistant

import "github.com/johnjallday/ori-agent/internal/rosterstaffing"

// FolderHomeStaffingView extends the existing setup plan's review, not its
// execution contract. Only the host's private exact intent can authorize work.
type FolderHomeStaffingView struct {
	ContextDigest  string                      `json:"context_digest"`
	Revision       int64                       `json:"revision"`
	State          string                      `json:"state"`
	Problem        string                      `json:"problem,omitempty"`
	Roles          []FolderHomeRoleView        `json:"roles"`
	Choices        []rosterstaffing.RoleChoice `json:"choices,omitempty"`
	Advice         *rosterstaffing.Advice      `json:"advice,omitempty"`
	ChoiceProblems map[string]string           `json:"choice_problems,omitempty"`
}

// FolderHomeRoleView contains declared public requirements and current held
// names only. Prompts, source/configuration witnesses and grants stay private.
type FolderHomeRoleView struct {
	RoleID      string   `json:"role_id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Primary     bool     `json:"primary,omitempty"`
	HeldName    string   `json:"held_name,omitempty"`
	Skills      []string `json:"skills,omitempty"`
}

// Keep exact choices visible/clearable even while the provider cannot supply
// a fresh declaration. This fallback never reports readiness or renews advice.
func folderHomeStaffingFallback(review *FolderHomeStaffingReview) *FolderHomeStaffingView {
	if review == nil {
		return nil
	}
	view := &FolderHomeStaffingView{ContextDigest: review.ContextDigest, Revision: review.Revision, State: "unavailable",
		Problem: "The Home declaration is unavailable. Your choices are kept; you can clear them or review again when it is available.", Choices: review.Team.Choices(), ChoiceProblems: map[string]string{}}
	for _, choice := range view.Choices {
		view.Roles = append(view.Roles, FolderHomeRoleView{RoleID: choice.RoleID, Label: choice.RoleID})
		view.ChoiceProblems[choice.RoleID] = "Review this role before confirming."
	}
	return view
}

func (v *FolderHomeStaffingView) Clone() *FolderHomeStaffingView {
	if v == nil {
		return nil
	}
	copy := *v
	copy.Advice = v.Advice.Public()
	copy.Roles = append([]FolderHomeRoleView(nil), v.Roles...)
	for i := range copy.Roles {
		copy.Roles[i].Skills = append([]string(nil), v.Roles[i].Skills...)
	}
	copy.Choices = append([]rosterstaffing.RoleChoice(nil), v.Choices...)
	for i := range copy.Choices {
		copy.Choices[i].Appearance = v.Choices[i].Appearance.Clone()
	}
	if v.ChoiceProblems != nil {
		copy.ChoiceProblems = make(map[string]string, len(v.ChoiceProblems))
		for role, problem := range v.ChoiceProblems {
			copy.ChoiceProblems[role] = problem
		}
	}
	return &copy
}
