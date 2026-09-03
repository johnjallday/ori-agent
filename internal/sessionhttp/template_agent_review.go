package sessionhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
)

const templateAgentReviewVersion = 1

var (
	errTemplateAgentReviewMalformed = errors.New("invalid template agent review")
	errTemplateAgentReviewStale     = errors.New("template agent plan changed")
)

type templateAgentReview struct {
	Version      int                              `json:"version"`
	PlanRevision string                           `json:"plan_revision"`
	Expectations []templateAgentReviewExpectation `json:"expectations"`
}

type templateAgentReviewExpectation struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

type templateAgentReviewValidationError struct {
	Kind           error
	Message        string
	FreshPlan      templateAgentPlan
	Index          *int
	Name           string
	ExpectedAction string
	ActualAction   string
}

func (e *templateAgentReviewValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *templateAgentReviewValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// templateAgentPlanRevision hashes only the normalized plan contract that was
// shown to the user. Warnings are explanatory copies of fields already present,
// so they are excluded; template identity, defaults, create/reuse resolution,
// and each effective definition remain part of the digest.
func templateAgentPlanRevision(plan templateAgentPlan) string {
	agents := append([]templateAgentPlanItem(nil), plan.Agents...)
	for index := range agents {
		agents[index].Warning = ""
	}
	canonical := struct {
		TemplateID            string                  `json:"template_id"`
		TemplateName          string                  `json:"template_name"`
		EntryAgentName        string                  `json:"entry_agent_name"`
		SystemProvider        string                  `json:"system_provider"`
		SystemModel           string                  `json:"system_model"`
		SystemModelConfigured bool                    `json:"system_model_configured"`
		Agents                []templateAgentPlanItem `json:"agents"`
	}{
		TemplateID:            strings.TrimSpace(plan.TemplateID),
		TemplateName:          strings.TrimSpace(plan.TemplateName),
		EntryAgentName:        strings.TrimSpace(plan.EntryAgentName),
		SystemProvider:        strings.TrimSpace(plan.SystemProvider),
		SystemModel:           strings.TrimSpace(plan.SystemModel),
		SystemModelConfigured: plan.SystemModelConfigured,
		Agents:                agents,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func finalizeTemplateAgentPlan(plan templateAgentPlan) templateAgentPlan {
	plan.Revision = templateAgentPlanRevision(plan)
	return plan
}

func malformedTemplateAgentReview(message string, fresh templateAgentPlan) error {
	return &templateAgentReviewValidationError{
		Kind:      errTemplateAgentReviewMalformed,
		Message:   message,
		FreshPlan: fresh,
	}
}

func staleTemplateAgentReview(message string, fresh templateAgentPlan) error {
	return &templateAgentReviewValidationError{
		Kind:      errTemplateAgentReviewStale,
		Message:   message,
		FreshPlan: fresh,
	}
}

func staleTemplateAgentExpectation(message string, fresh templateAgentPlan, index int, name, expectedAction, actualAction string) error {
	return &templateAgentReviewValidationError{
		Kind:           errTemplateAgentReviewStale,
		Message:        message,
		FreshPlan:      fresh,
		Index:          &index,
		Name:           name,
		ExpectedAction: expectedAction,
		ActualAction:   actualAction,
	}
}

// validateTemplateAgentReview validates the complete reviewed roster before any
// agent or workspace write. rawTemplate is the plan the browser reviewed;
// effectiveTemplate has request overrides applied and is what strict seeding
// will create.
func (h *Handler) validateTemplateAgentReview(review *templateAgentReview, rawTemplate, effectiveTemplate projecttemplates.Template) error {
	if review == nil {
		return nil
	}
	fresh := h.buildTemplateAgentPlan(rawTemplate)
	if review.Version != templateAgentReviewVersion {
		return malformedTemplateAgentReview("template_agent_review.version must be 1", fresh)
	}
	if strings.TrimSpace(review.PlanRevision) == "" {
		return malformedTemplateAgentReview("template_agent_review.plan_revision is required", fresh)
	}
	if len(effectiveTemplate.Agents) == 0 {
		return malformedTemplateAgentReview("template_agent_review requires an included blueprint agent roster", fresh)
	}
	if h == nil || h.agentStore == nil {
		return malformedTemplateAgentReview("template agent storage is unavailable", fresh)
	}
	if review.PlanRevision != fresh.Revision {
		return staleTemplateAgentReview("The blueprint agent plan changed. Review the updated team before creating the workspace.", fresh)
	}
	if len(review.Expectations) != len(effectiveTemplate.Agents) {
		return malformedTemplateAgentReview("template_agent_review.expectations must contain one entry for every included blueprint agent", fresh)
	}

	seen := make(map[int]struct{}, len(review.Expectations))
	for _, expectation := range review.Expectations {
		if expectation.Index < 0 || expectation.Index >= len(effectiveTemplate.Agents) {
			return malformedTemplateAgentReview(fmt.Sprintf("template agent expectation index %d is out of range", expectation.Index), fresh)
		}
		if _, duplicate := seen[expectation.Index]; duplicate {
			return malformedTemplateAgentReview(fmt.Sprintf("template agent expectation index %d is duplicated", expectation.Index), fresh)
		}
		seen[expectation.Index] = struct{}{}

		spec := effectiveTemplate.Agents[expectation.Index]
		if expectation.Name != strings.TrimSpace(spec.Name) {
			return malformedTemplateAgentReview(fmt.Sprintf("template agent expectation %d name does not match the reviewed setup", expectation.Index), fresh)
		}
		action := strings.ToLower(strings.TrimSpace(expectation.Action))
		if action != expectation.Action || (action != "create" && action != "reuse") {
			return malformedTemplateAgentReview(fmt.Sprintf("template agent expectation %d action must be create or reuse", expectation.Index), fresh)
		}
		_, exists := h.agentStore.GetAgent(spec.Name)
		currentAction := "create"
		if exists {
			currentAction = "reuse"
		}
		if action != currentAction {
			return staleTemplateAgentExpectation(
				fmt.Sprintf("Agent %q changed since Team was reviewed.", spec.Name),
				fresh,
				expectation.Index,
				spec.Name,
				action,
				currentAction,
			)
		}
	}
	return nil
}

func respondTemplateAgentReviewError(w http.ResponseWriter, err error) {
	var validation *templateAgentReviewValidationError
	if !errors.As(err, &validation) {
		_ = orihttp.RespondInternalError(w, "Failed to validate template agent review")
		return
	}
	if errors.Is(validation, errTemplateAgentReviewStale) {
		conflict := map[string]any{"type": "template_agent_plan"}
		if validation.Index != nil {
			conflict["index"] = *validation.Index
			conflict["name"] = validation.Name
			conflict["expected_action"] = validation.ExpectedAction
			conflict["actual_action"] = validation.ActualAction
		}
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":               validation.Message,
			"conflict":            conflict,
			"template_agent_plan": validation.FreshPlan,
		})
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{
		"error":    validation.Message,
		"conflict": map[string]any{"type": "template_agent_review_invalid"},
	})
}

func respondTemplateAgentOverrideValidationError(w http.ResponseWriter, err error) bool {
	var validation *templateAgentOverrideValidationError
	if !errors.As(err, &validation) {
		return false
	}
	_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{
		"error": validation.Error(),
		"conflict": map[string]any{
			"type":  "template_agent_override",
			"index": validation.Index,
			"field": validation.Field,
		},
	})
	return true
}

type strictTemplateAgentSeedError struct {
	Index          int
	Name           string
	Kind           string
	ExpectedAction string
	ActualAction   string
	Cause          error
	Fresh          templateAgentPlan
}

func (e *strictTemplateAgentSeedError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("template agent %q could not be %s: %v", e.Name, e.Kind, e.Cause)
}

func (e *strictTemplateAgentSeedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// seedTemplateAgentsStrict rechecks every expectation immediately before its
// operation. Any failure is fatal; the caller rolls back OwnedNames before
// returning, so workspace persistence is reached only with the full roster.
func (h *Handler) seedTemplateAgentsStrict(ws *session.Workspace, tpl projecttemplates.Template, freshTemplate projecttemplates.Template, review templateAgentReview) (seedAgentsResult, error) {
	var result seedAgentsResult
	if h == nil || h.agentStore == nil || ws == nil {
		return result, &strictTemplateAgentSeedError{Index: 0, Kind: "created", Cause: errors.New("agent store is unavailable")}
	}

	expectations := make(map[int]templateAgentReviewExpectation, len(review.Expectations))
	for _, expectation := range review.Expectations {
		expectations[expectation.Index] = expectation
	}
	for index, spec := range tpl.Agents {
		expectation := expectations[index]
		_, exists := h.agentStore.GetAgent(spec.Name)
		wantReuse := expectation.Action == "reuse"
		if exists != wantReuse {
			actualAction := "create"
			if exists {
				actualAction = "reuse"
			}
			return result, &strictTemplateAgentSeedError{
				Index:          index,
				Name:           spec.Name,
				Kind:           "reconciled",
				ExpectedAction: expectation.Action,
				ActualAction:   actualAction,
				Cause:          errTemplateAgentReviewStale,
				Fresh:          h.buildTemplateAgentPlan(freshTemplate),
			}
		}
		if !exists {
			cfg, _ := h.templateAgentCreateConfig(spec)
			if err := h.agentStore.CreateAgent(spec.Name, cfg); err != nil {
				return result, &strictTemplateAgentSeedError{
					Index: index,
					Name:  spec.Name,
					Kind:  "created",
					Cause: err,
				}
			}
			result.OwnedNames = append(result.OwnedNames, spec.Name)
			if !spec.Tools.IsEmpty() {
				result.Created = append(result.Created, createdAgent{Name: spec.Name, Tools: spec.Tools})
			}
		} else {
			result.ReuseNotices = append(result.ReuseNotices,
				fmt.Sprintf("Reusing existing agent %q - its saved prompt, model, and tools are used, not the template's.", spec.Name))
		}

		if index == 0 {
			setWorkspaceEntryAgent(ws, spec.Name)
			result.EntrySet = true
		} else {
			attachWorkspaceSpecialist(ws, spec.Name)
		}
	}
	return result, nil
}

func (h *Handler) respondStrictTemplateAgentSeedError(w http.ResponseWriter, seed seedAgentsResult, err error) {
	cleanupErrors := h.rollbackSeededAgents(seed)
	var strictErr *strictTemplateAgentSeedError
	if !errors.As(err, &strictErr) {
		_ = orihttp.RespondInternalError(w, "Failed to create reviewed template agents")
		return
	}
	if errors.Is(strictErr, errTemplateAgentReviewStale) {
		message := "The blueprint agent plan changed while the workspace was being created. Nothing was created."
		if len(cleanupErrors) > 0 {
			message = "The blueprint agent plan changed while the workspace was being created. The workspace was not created, but some agent cleanup failed."
		}
		response := map[string]any{
			"error": message,
			"conflict": map[string]any{
				"type":            "template_agent_plan",
				"index":           strictErr.Index,
				"name":            strictErr.Name,
				"expected_action": strictErr.ExpectedAction,
				"actual_action":   strictErr.ActualAction,
			},
			"template_agent_plan": strictErr.Fresh,
		}
		if len(cleanupErrors) > 0 {
			response["cleanup_errors"] = cleanupErrors
		}
		_ = orihttp.RespondJSON(w, http.StatusConflict, response)
		return
	}
	message := fmt.Sprintf("Agent %q could not be created. Nothing was created.", strictErr.Name)
	if len(cleanupErrors) > 0 {
		message = fmt.Sprintf("Agent %q could not be created. The workspace was not created, but some agent cleanup failed.", strictErr.Name)
	}
	response := map[string]any{
		"error": message,
		"conflict": map[string]any{
			"type":  "template_agent_create",
			"index": strictErr.Index,
			"name":  strictErr.Name,
		},
	}
	if len(cleanupErrors) > 0 {
		response["cleanup_errors"] = cleanupErrors
	}
	_ = orihttp.RespondJSON(w, http.StatusInternalServerError, response)
}
