package sessionhttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// groupHomeStaffingAdvice is reached only by an explicit recommendation action
// on the existing Home review. It uses the ordinary assistant's isolated model,
// the common ranker and the existing review receipt, not a second draft owner.
func (h *Handler) groupHomeStaffingAdvice(ctx context.Context, input grouprequirements.Input) *rosterstaffing.Advice {
	current := h.groupRequirements.Evaluate(input)
	if current.State != grouprequirements.StateReadyGrouped {
		return &rosterstaffing.Advice{Version: 2, TemplateID: input.GroupTemplateID, TemplateRevision: input.GroupTemplateRevision, State: rosterstaffing.Unavailable}
	}
	return h.RecommendHomeStaffing(ctx, groupHomeStaffingTarget(input, current))
}

// RecommendHomeStaffing serves explicit recommendation actions of existing
// review owners. Call outside mutation locks; only the returned bounded advice
// may be persisted. It never selects roles, creates a review or performs work.
func (h *Handler) RecommendHomeStaffing(ctx context.Context, target HomeStaffingTarget) *rosterstaffing.Advice {
	out := &rosterstaffing.Advice{Version: 2, TemplateID: target.TemplateID, TemplateRevision: target.TemplateRevision, State: rosterstaffing.Unavailable}
	if h == nil || h.workspaceBuild == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, rosterstaffing.RankingTimeout)
	defer cancel()
	_, occupied, err := h.homeStaffingOccupancy(target)
	if err != nil {
		return out
	}
	ranking := rosterstaffing.Input{Goal: target.Declaration.StationDescription}
	unoccupied := 0
	requestedSkills := []string{}
	seenSkills := map[string]bool{}
	for _, role := range target.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			continue
		}
		if !occupied[role.ID] {
			unoccupied++
		}
		ranking.Roles = append(ranking.Roles, rosterstaffing.Role{TargetID: target.TemplateID, ID: role.ID, Scope: "home", Purpose: role.Description,
			RequiredSkills: append([]string(nil), role.Skills...), Occupied: occupied[role.ID]})
		for _, name := range role.Skills {
			if !seenSkills[name] {
				seenSkills[name] = true
				requestedSkills = append(requestedSkills, name)
			}
		}
	}
	if len(ranking.Roles) == 0 {
		return out
	}
	if unoccupied == 0 {
		out.State = rosterstaffing.NeedsChoice
		return out
	}
	readers := rosterstaffing.Readers{Profiles: func(ctx context.Context) store.StaffingSnapshot { return store.ReadStaffingSnapshot(ctx, h.agentStore) },
		ModelConfigured: h.workspaceBuild.ProfileModelConfigured, Assignments: h.reviewedHomeAssignments}
	if len(requestedSkills) > 0 && h.staffingSkillReader != nil {
		readers.Skills = func(ctx context.Context, profile store.StaffingProfile) rosterstaffing.SkillMembership {
			return h.staffingSkillReader(ctx, store.StaffingSelection{Name: profile.Name, Witness: profile.Witness}, requestedSkills, target.Personal)
		}
	}
	ranking.Roster = rosterstaffing.Collect(ctx, target.OwnerUserID, readers)
	bounded, _, err := rosterstaffing.Prepare(ranking)
	if err != nil {
		return out
	}
	out.Coverage = bounded.Roster.Coverage
	readiness := h.workspaceBuildReadiness(ctx, target.OwnerUserID)
	if !readiness.available || readiness.model.Provider == nil || !readiness.model.Provider.Capabilities().SupportsIsolatedChat {
		return out
	}
	result := rosterstaffing.Recommend(ctx, bounded, func(ctx context.Context, prompt string) ([]byte, error) {
		response, err := readiness.model.Provider.Chat(ctx, llm.ChatRequest{Model: readiness.model.Model, SystemPrompt: prompt,
			Messages: []llm.Message{{Role: "user", Content: "Recommend suitable saved profiles for the unoccupied Home roles."}}, Isolated: true, Temperature: 0.2, MaxTokens: 2000})
		if err != nil {
			return nil, err
		}
		return []byte(response.Content), nil
	})
	out.State, out.Coverage = result.State, result.Coverage
	candidates := map[string]rosterstaffing.Candidate{}
	for _, candidate := range bounded.Roster.Candidates {
		candidates[candidate.Handle] = candidate
	}
	for _, proposal := range result.Roles {
		role := rosterstaffing.RoleAdvice{RoleID: proposal.RoleID, Scope: "home", State: proposal.State}
		for _, choice := range proposal.Choices {
			candidate := candidates[choice.Candidate]
			item := rosterstaffing.AdviceChoice{Handle: candidate.Handle, Name: candidate.Name, Source: candidate.Source,
				Configured: candidate.Ready, SourceWitness: candidate.Witness, CapabilityWitness: candidate.CapabilityWitness, Gaps: append([]string(nil), candidate.Gaps...), Assignments: candidate.AssignmentEvidence()}
			for _, id := range choice.Evidence {
				for _, fact := range candidate.Evidence {
					if fact.ID == id {
						item.Reasons = append(item.Reasons, fact)
						break
					}
				}
			}
			role.Choices = append(role.Choices, item)
		}
		out.Roles = append(out.Roles, role)
	}
	return out
}
