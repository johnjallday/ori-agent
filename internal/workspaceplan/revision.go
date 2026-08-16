package workspaceplan

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Targeted revision: regenerate part of a Plan and leave the rest alone.
//
// The prompt asks the model to preserve untouched sections and their ids, but
// asking is not a guarantee. What makes FR-55 true is the merge below: the
// generated content is used only for the sections the user targeted, and every
// other section is copied from the current draft. A model that rewrites the
// whole plan anyway cannot change anything outside the target.
//
// What the merge cannot prevent is collateral: regenerating a group can drop an
// item that work elsewhere depends on. That is disclosed before the replacement
// happens rather than discovered after (FR-56).

// Revisable section names. A revision targets either whole sections by name or
// individual task groups by their stable id.
const (
	SectionObjective   = "objective"
	SectionScope       = "scope"
	SectionNonGoals    = "non_goals"
	SectionAssumptions = "assumptions"
	SectionRisks       = "risks"
	SectionSources     = "sources"
	SectionArtifacts   = "artifacts"
	SectionGroups      = "groups"
	SectionValidations = "validations"
	SectionExecution   = "execution"
)

// AllSections lists the named sections a revision may target.
func AllSections() []string {
	return []string{
		SectionObjective, SectionScope, SectionNonGoals, SectionAssumptions,
		SectionRisks, SectionSources, SectionArtifacts, SectionGroups,
		SectionValidations, SectionExecution,
	}
}

// RevisionDisclosure is what the user is shown before a targeted revision
// replaces anything (FR-56).
type RevisionDisclosure struct {
	// Sections are the named sections that will be regenerated.
	Sections []string `json:"sections"`
	// GroupIDs are the individual task groups that will be regenerated.
	GroupIDs []string `json:"group_ids,omitempty"`
	// ReplacedItems lists the task items inside the target that will be
	// replaced. Their ids may not survive: the model decides what the revised
	// section contains.
	ReplacedItems []RevisionItemRef `json:"replaced_items,omitempty"`
	// UserAuthored flags the replaced items a person wrote. Losing a model's
	// suggestion is cheap; losing someone's own words is not, so they are
	// called out separately (FR-55, FR-57).
	UserAuthored []RevisionItemRef `json:"user_authored,omitempty"`
	// Collateral lists work OUTSIDE the target that depends on something
	// inside it. Regenerating may remove what it depends on, which is exactly
	// the "what else will be affected" this disclosure exists to answer
	// (FR-56).
	Collateral []RevisionItemRef `json:"collateral,omitempty"`
	// Whole is true when the revision regenerates everything, in which case
	// nothing is preserved and the item-level detail is noise.
	Whole bool `json:"whole"`
}

// RevisionItemRef identifies one element in a disclosure.
type RevisionItemRef struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// GroupID locates an item; it is empty for a group-level reference.
	GroupID string `json:"group_id,omitempty"`
}

// NeedsConfirmation reports whether the disclosure contains anything the user
// should see before it happens. A revision that replaces only model-generated
// content with no collateral is unremarkable; one that discards a person's
// writing or breaks a dependency is not.
func (d RevisionDisclosure) NeedsConfirmation() bool {
	return len(d.UserAuthored) > 0 || len(d.Collateral) > 0
}

// DiscloseRevision reports what a targeted revision will replace, before the
// model is called (FR-56).
//
// No target at all means a full revision: everything is regenerated, so there
// is nothing to preserve and nothing to disclose item by item.
//
// Targets that match nothing are NOT the same thing. A mistyped section name
// must never widen the revision to the whole plan — that would turn a typo into
// a silent rewrite of work the user meant to keep — so it produces an empty,
// non-whole disclosure that Revise refuses.
func DiscloseRevision(content PlanContent, sections []string) RevisionDisclosure {
	named, groupIDs := splitTargets(content, sections)
	if len(named) == 0 && len(groupIDs) == 0 {
		return RevisionDisclosure{Whole: !hasTarget(sections)}
	}

	disclosure := RevisionDisclosure{Sections: named, GroupIDs: groupIDs}
	regeneratesAllGroups := slices.Contains(named, SectionGroups)

	// Which items live inside the target.
	inTarget := map[string]struct{}{}
	for _, group := range content.Groups {
		if !regeneratesAllGroups && !slices.Contains(groupIDs, group.ID) {
			continue
		}
		for _, item := range group.Items {
			inTarget[item.ID] = struct{}{}
			ref := RevisionItemRef{ID: item.ID, Description: item.Description, GroupID: group.ID}
			disclosure.ReplacedItems = append(disclosure.ReplacedItems, ref)
			if item.Author == AuthorUser {
				disclosure.UserAuthored = append(disclosure.UserAuthored, ref)
			}
		}
	}

	// Work outside the target that depends on something inside it. Those
	// dependencies are what a regeneration can silently break.
	for _, group := range content.Groups {
		insideTarget := regeneratesAllGroups || slices.Contains(groupIDs, group.ID)
		for _, item := range group.Items {
			if insideTarget {
				continue
			}
			for _, dep := range item.DependsOn {
				if _, targeted := inTarget[dep]; targeted {
					disclosure.Collateral = append(disclosure.Collateral, RevisionItemRef{
						ID: item.ID, Description: item.Description, GroupID: group.ID,
					})
					break
				}
			}
		}
	}

	return disclosure
}

// hasTarget reports whether the caller asked for a targeted revision at all,
// as opposed to asking for one whose targets happen to match nothing.
func hasTarget(sections []string) bool {
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			return true
		}
	}
	return false
}

// splitTargets separates named sections from task-group ids, dropping anything
// that names neither. A target the Plan does not contain is ignored rather than
// silently widening the revision.
func splitTargets(content PlanContent, targets []string) (sections []string, groupIDs []string) {
	known := toSet(AllSections())
	groups := map[string]struct{}{}
	for _, group := range content.Groups {
		groups[group.ID] = struct{}{}
	}

	for _, target := range targets {
		trimmed := strings.TrimSpace(target)
		if trimmed == "" {
			continue
		}
		if _, ok := known[trimmed]; ok {
			if !slices.Contains(sections, trimmed) {
				sections = append(sections, trimmed)
			}
			continue
		}
		if _, ok := groups[trimmed]; ok {
			if !slices.Contains(groupIDs, trimmed) {
				groupIDs = append(groupIDs, trimmed)
			}
		}
	}
	sort.Strings(sections)
	sort.Strings(groupIDs)
	return sections, groupIDs
}

// MergeTargetedRevision keeps generated content for the targeted sections and
// the current draft's content for everything else.
//
// This is the enforcement half of FR-55. The prompt asks the model to preserve
// untouched sections; this makes it so regardless of what the model returned.
func MergeTargetedRevision(current, generated PlanContent, currentObjective, generatedObjective string, sections []string) (string, PlanContent) {
	named, groupIDs := splitTargets(current, sections)
	if len(named) == 0 && len(groupIDs) == 0 {
		// A full revision replaces everything.
		return generatedObjective, generated
	}

	merged := current.Clone()
	targeted := toSet(named)

	if _, ok := targeted[SectionObjective]; ok {
		currentObjective = generatedObjective
	}
	if _, ok := targeted[SectionScope]; ok {
		merged.InScope = cloneStrings(generated.InScope)
		merged.Rationale = generated.Rationale
	}
	if _, ok := targeted[SectionNonGoals]; ok {
		merged.NonGoals = cloneStrings(generated.NonGoals)
	}
	if _, ok := targeted[SectionAssumptions]; ok {
		merged.Assumptions = preserveSkipAssumptions(current.Assumptions, generated.Assumptions)
	}
	if _, ok := targeted[SectionRisks]; ok {
		merged.Risks = append([]Risk(nil), generated.Risks...)
	}
	if _, ok := targeted[SectionSources]; ok {
		merged.Sources = append([]Source(nil), generated.Sources...)
	}
	if _, ok := targeted[SectionArtifacts]; ok {
		merged.Artifacts = append([]ProposedArtifact(nil), generated.Artifacts...)
	}
	if _, ok := targeted[SectionValidations]; ok {
		merged.Validations = append([]ValidationCheckpoint(nil), generated.Validations...)
	}
	if _, ok := targeted[SectionExecution]; ok {
		merged.Execution = generated.Execution
		merged.Execution.Preconditions = cloneStrings(generated.Execution.Preconditions)
	}

	if _, ok := targeted[SectionGroups]; ok {
		merged.Groups = generated.Clone().Groups
		return currentObjective, merged
	}

	// Group-level targeting: replace only the named groups, in place, so the
	// order of untouched groups is preserved too.
	if len(groupIDs) > 0 {
		generatedByID := map[string]TaskGroup{}
		for _, group := range generated.Groups {
			generatedByID[group.ID] = group
		}
		for i, group := range merged.Groups {
			if !slices.Contains(groupIDs, group.ID) {
				continue
			}
			if replacement, ok := generatedByID[group.ID]; ok {
				merged.Groups[i] = replacement
			}
			// A targeted group the model did not return is left as it was.
			// Dropping it would be a deletion the user never asked for.
		}
	}

	return currentObjective, merged
}

// preserveSkipAssumptions keeps assumptions that record a skipped clarification.
// Those are the application's record of a user's decision, not the model's
// suggestion, so a regeneration must not drop them (FR-28).
func preserveSkipAssumptions(current, generated []Assumption) []Assumption {
	var kept []Assumption
	for _, assumption := range current {
		if assumption.ClarificationID != "" {
			kept = append(kept, assumption)
		}
	}
	for _, assumption := range generated {
		if assumption.ClarificationID == "" {
			kept = append(kept, assumption)
		}
	}
	return kept
}

// ReviseInput is one revision request.
type ReviseInput struct {
	// Sections targets the revision. Empty means revise the whole Plan (FR-54).
	Sections []string
	// Confirmed acknowledges a disclosure that needed it. A revision that
	// would discard user-authored content or break a dependency is refused
	// until the user has seen what it will do (FR-56).
	Confirmed bool
	Actor     string
	Guidance  GuidanceInput
	// Validation carries the agents and capabilities that actually exist.
	Validation       ValidationContext
	WorkspaceContext string
	ExpectedRevision int64
}

// RevisionRequiredError reports that a revision needs confirmation, and carries
// the disclosure explaining why (FR-56).
type RevisionRequiredError struct {
	Disclosure RevisionDisclosure
}

func (e *RevisionRequiredError) Error() string {
	if e == nil {
		return "revision requires confirmation"
	}
	parts := make([]string, 0, 2)
	if count := len(e.Disclosure.UserAuthored); count > 0 {
		parts = append(parts, fmt.Sprintf("%d item(s) you wrote will be replaced", count))
	}
	if count := len(e.Disclosure.Collateral); count > 0 {
		parts = append(parts, fmt.Sprintf("%d item(s) outside this section depend on it", count))
	}
	return "this revision needs confirmation: " + strings.Join(parts, "; ")
}

// DiscloseRevisionFor returns what a revision would replace, without changing
// anything.
func (s *Service) DiscloseRevisionFor(ctx context.Context, workspaceID, planID string, sections []string) (RevisionDisclosure, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return RevisionDisclosure{}, err
	}
	return DiscloseRevision(plan.Draft, sections), nil
}

// Revise regenerates part or all of a Plan's draft (FR-54).
//
// A targeted revision merges: only the targeted sections come from the model,
// and everything else — including every id — is carried over from the current
// draft (FR-55). When the disclosure shows the revision would discard
// user-authored content or break a dependency, it is refused until the caller
// confirms (FR-56).
func (s *Service) Revise(ctx context.Context, workspaceID, planID string, input ReviseInput) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if s.generator == nil || !s.generator.Available() {
		return nil, ErrModelUnavailable
	}
	if plan.Status != StatusDraft && plan.Status != StatusNeedsInput {
		return nil, fmt.Errorf("%w: a %s plan cannot be revised in place; create a new draft",
			ErrInvalidTransition, plan.Status)
	}

	disclosure := DiscloseRevision(plan.Draft, input.Sections)
	// Targets that match nothing are refused rather than treated as "revise
	// everything". A mistyped section name must not become a full rewrite.
	if !disclosure.Whole && len(disclosure.Sections) == 0 && len(disclosure.GroupIDs) == 0 {
		return nil, fmt.Errorf("%w: none of the requested sections (%s) exist in this plan",
			ErrValidation, strings.Join(input.Sections, ", "))
	}
	if disclosure.NeedsConfirmation() && !input.Confirmed {
		return nil, &RevisionRequiredError{Disclosure: disclosure}
	}

	outcome, err := s.generator.GenerateDraft(ctx, GenerationInput{
		Request:          plan.OriginalRequest,
		Objective:        plan.Objective,
		Content:          plan.Draft,
		Answers:          plan.Draft.Clarifications,
		Guidance:         input.Guidance,
		Validation:       input.Validation,
		WorkspaceContext: input.WorkspaceContext,
		Sections:         input.Sections,
	})
	if err != nil {
		return nil, &GenerationError{Err: err, Issues: outcome.Issues, Attempts: outcome.Attempts}
	}

	objective, merged := MergeTargetedRevision(
		plan.Draft, outcome.Content, plan.Objective, outcome.Objective, input.Sections)

	// The merge can leave a dependency pointing at an item the regenerated
	// section no longer contains. That is the collateral the disclosure warned
	// about; refusing here keeps a broken graph out of the draft.
	if result := ValidatePlanContent(objective, merged, input.Validation); !result.OK() {
		return nil, &GenerationError{Err: result.Error(), Issues: result.Issues, Attempts: outcome.Attempts}
	}

	if _, err := s.store.UpdatePlanDraft(ctx, workspaceID, planID, input.ExpectedRevision, DraftUpdate{
		Title:     plan.Title,
		Objective: objective,
		Content:   merged,
		Intent:    plan.DraftIntent,
		UpdatedAt: s.now(),
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}
