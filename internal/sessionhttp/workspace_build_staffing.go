package sessionhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
)

type buildStaffingTarget struct {
	templateID string
	revision   string
	roleID     string
	scope      string
}

type buildStaffingContext struct {
	input   rosterstaffing.Input
	prompt  string
	targets map[string]buildStaffingTarget
	version int64
}

// Staffing shares the ordinary turn and provider. No route, GET, polling,
// roster mutation or second per-role inference is introduced here.
func (h *Handler) workspaceBuildStaffing(ctx context.Context, owner string, session *personalassistant.WorkspaceBuildSession, catalog []WorkspaceBuildCatalogEntry) *buildStaffingContext {
	if h.workspaceBuild == nil || session == nil {
		return nil
	}
	roster := h.workspaceBuildRoster(ctx, owner)
	state := &buildStaffingContext{input: rosterstaffing.Input{Roster: roster, Goal: session.Draft.Mission}, targets: map[string]buildStaffingTarget{}, version: session.Version}
	ranked := relevantCatalog(catalog, session.FirstRequest+" "+session.Draft.Description)
	// Always include the current blueprint before suggestions, without
	// dropping an entire role set halfway through the fixed budget.
	for i, entry := range ranked {
		if entry.Template.ID == session.Draft.TemplateID {
			ranked[0], ranked[i] = ranked[i], ranked[0]
			break
		}
	}
	for _, entry := range ranked {
		roles := buildRoleDeclarations(entry.Template)
		if len(roles) == 0 || len(roles)+len(state.input.Roles) > rosterstaffing.MaxRoles {
			continue
		}
		revision, err := digestJSON(entry.Template)
		if err != nil {
			continue
		}
		for _, declaration := range roles {
			scope, purpose := "workspace", declaration.Label+". "+entry.Template.Description
			if program := entry.Template.AssistantProgram; program != nil {
				scope = "project"
				for _, role := range program.Roles {
					if role.ID == declaration.RoleID {
						purpose = role.Description
						break
					}
				}
			}
			sum := sha256.Sum256([]byte(entry.Template.ID + "\x00" + revision + "\x00" + declaration.RoleID + "\x00" + scope))
			handle := "r_" + hex.EncodeToString(sum[:12])
			state.targets[handle] = buildStaffingTarget{entry.Template.ID, revision, declaration.RoleID, scope}
			state.input.Roles = append(state.input.Roles, rosterstaffing.Role{TargetID: entry.Template.ID, ID: handle, Scope: scope, Purpose: purpose})
		}
	}
	if len(state.input.Roles) == 0 {
		return nil
	}
	bounded, prompt, err := rosterstaffing.Prepare(state.input)
	if err != nil {
		return nil
	}
	state.input, state.prompt = bounded, prompt
	return state
}

func (h *Handler) workspaceBuildRoster(ctx context.Context, owner string) rosterstaffing.Roster {
	var configured func(string, string) bool
	if h.workspaceBuild != nil {
		configured = h.workspaceBuild.ProfileModelConfigured
	}
	return rosterstaffing.Collect(ctx, owner, rosterstaffing.Readers{
		Profiles:        func(ctx context.Context) store.StaffingSnapshot { return store.ReadStaffingSnapshot(ctx, h.agentStore) },
		ModelConfigured: configured,
		// CustomizedIn is not assignment evidence. No reader means unknown.
	})
}

func finishBuildStaffing(session *personalassistant.WorkspaceBuildSession, outcome buildOutcome, state *buildStaffingContext) {
	if state == nil {
		return
	}
	result := &personalassistant.BuildStaffing{Version: 1, ContextVersion: session.ContextVersion,
		TemplateID: session.Draft.TemplateID, State: rosterstaffing.Unavailable, Coverage: state.input.Roster.Coverage}
	input := state.input
	input.Roles = nil
	for _, role := range state.input.Roles {
		target := state.targets[role.ID]
		if target.templateID == session.Draft.TemplateID {
			input.Roles = append(input.Roles, role)
			result.TemplateRevision = target.revision
		}
	}
	session.Staffing = result
	for _, field := range []string{"blueprint_id", "goal", "description", "inputs", "parent_id", "team"} {
		if session.EditedFields[field] > state.version {
			result.State = rosterstaffing.Stale
			return
		}
	}
	if outcome.kind != "reply" || len(input.Roles) == 0 || (!input.Roster.Coverage.Complete && len(input.Roster.Candidates) == 0) {
		return
	}
	validated, err := rosterstaffing.Validate(input, outcome.reply.Staffing)
	if err != nil {
		return
	}
	result.State = validated.State
	byHandle := map[string]rosterstaffing.Candidate{}
	for _, c := range input.Roster.Candidates {
		byHandle[c.Handle] = c
	}
	for _, proposal := range validated.Roles {
		target := state.targets[proposal.RoleID]
		role := personalassistant.BuildStaffingRole{RoleID: target.roleID, Scope: target.scope, State: proposal.State}
		for _, choice := range proposal.Choices {
			candidate := byHandle[choice.Candidate]
			item := personalassistant.BuildStaffingChoice{Handle: candidate.Handle, Name: candidate.Name, Source: candidate.Source,
				Configured: candidate.Ready, SourceWitness: candidate.Witness, Gaps: append([]string(nil), candidate.Gaps...)}
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
		result.Roles = append(result.Roles, role)
	}
}

func buildStaffingInstructions(state *buildStaffingContext) string {
	if state == nil {
		return ""
	}
	return "\nStaffing is advisory and separate from patch.team. Before proposing a new agent, compare the eligible saved profiles below. In staffing.roles return only roles for the blueprint you choose (or the current blueprint when unchanged), using the opaque role handles in this data. target_id identifies its blueprint. Do not return roles for alternatives. A recommendation does not assign anyone: leave existing explicit team choices alone; the user must review the exact action. If the chosen blueprint has no supplied roles, return an empty staffing.roles and ask the user to review Team; do not invent a match. Return the staffing result INSIDE the ordinary build reply's staffing field, not a separate reply.\n" + strings.ReplaceAll(strings.ReplaceAll(state.prompt, "Return only", "The staffing field has shape"), "Return one entry per unoccupied role.", "Return one entry per supplied role of the chosen blueprint only.")
}
