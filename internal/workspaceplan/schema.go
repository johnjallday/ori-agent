package workspaceplan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file publishes the one structured-output schema every provider adapter
// requests, and the decoder that turns a model response into typed Plan
// content (FR-40).
//
// The schema is the contract, and it is deliberately narrow:
//
//   - Actionable structure — groups, items, dependencies, assignees,
//     capabilities, validations, execution policy — lives in typed fields.
//     Explanatory prose lives only in `explanation`, which nothing reads for
//     meaning. A model cannot express a dependency, an assignment, or an
//     approval effect in prose and have it take effect (FR-8, FR-53).
//   - Nothing the model returns is trusted on its word. Schema conformance is
//     checked here, and everything a schema cannot express — dependency
//     targets existing, cycles, bounds, agent availability — is checked by
//     validation.go before the content can become a reviewable version
//     (FR-41, FR-47).
//   - No provider response object is ever persisted as the canonical schema.
//     A response is decoded into PlanContent and the response itself is
//     discarded (FR-18).

// PlanContentSchemaVersion identifies the structured-output contract. It is
// stored with generated content so a later change to the schema is visible in
// provenance rather than silently reinterpreting old output.
const PlanContentSchemaVersion = "workspace-plan/v1"

// PlanContentSchema returns the JSON Schema for model-generated Plan content.
//
// It is returned as a fresh map on each call because provider adapters mutate
// what they are handed (adding `strict`, wrapping in a response-format
// envelope), and a shared map would let one provider's requirements leak into
// another's request.
func PlanContentSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"objective", "groups"},
		"properties": map[string]any{
			"objective": map[string]any{
				"type":        "string",
				"description": "One sentence stating the outcome this plan delivers.",
			},
			"rationale": map[string]any{
				"type":        "string",
				"description": "Why this approach was proposed.",
			},
			"in_scope": stringArraySchema("Outcomes this plan commits to delivering."),
			"non_goals": stringArraySchema(
				"What this plan explicitly will not do."),
			"assumptions": objectArraySchema(
				"Premises the plan takes as given.",
				[]any{"statement"},
				map[string]any{
					"id":        idSchema("Stable plan-local id. Omit for new assumptions."),
					"statement": map[string]any{"type": "string"},
				}),
			"risks": objectArraySchema(
				"Known ways this plan can go wrong.",
				[]any{"statement"},
				map[string]any{
					"id":        idSchema("Stable plan-local id. Omit for new risks."),
					"statement": map[string]any{"type": "string"},
					"severity": map[string]any{
						"type": "string",
						"enum": []any{string(RiskLow), string(RiskMedium), string(RiskHigh)},
					},
					"mitigation": map[string]any{"type": "string"},
				}),
			"sources": objectArraySchema(
				"References this plan draws on. Paths and URLs are validated by the application before use.",
				[]any{"kind", "ref"},
				map[string]any{
					"id": idSchema("Stable plan-local id. Omit for new sources."),
					"kind": map[string]any{
						"type": "string",
						"enum": []any{
							string(SourceURL), string(SourceFile), string(SourceNote),
							string(SourceTask), string(SourceRun), string(SourceText),
						},
					},
					"ref":     map[string]any{"type": "string"},
					"title":   map[string]any{"type": "string"},
					"excerpt": map[string]any{"type": "string"},
				}),
			"artifacts": objectArraySchema(
				"Documents approval would authorize writing. Paths must be workspace-relative.",
				[]any{"kind", "path"},
				map[string]any{
					"id": idSchema("Stable plan-local id. Omit for new artifacts."),
					"kind": map[string]any{
						"type": "string",
						"enum": []any{
							string(ArtifactPRD), string(ArtifactTaskList),
							string(ArtifactNote), string(ArtifactDocument),
						},
					},
					"path":        map[string]any{"type": "string"},
					"title":       map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
				}),
			"groups": map[string]any{
				"type":        "array",
				"description": "Proposed task groups, in execution order.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"title", "items"},
					"properties": map[string]any{
						"id":      idSchema("Stable plan-local id. Reuse it exactly when revising an existing group; omit it for a new one."),
						"title":   map[string]any{"type": "string"},
						"outcome": map[string]any{"type": "string"},
						"notes":   map[string]any{"type": "string"},
						"depends_on": stringArraySchema(
							"Ids of other groups that must finish first. Use group ids, never titles or positions."),
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"required":             []any{"description"},
								"properties": map[string]any{
									"id":          idSchema("Stable plan-local id. Reuse it exactly when revising an existing item; omit it for a new one."),
									"description": map[string]any{"type": "string"},
									"details":     map[string]any{"type": "string"},
									"assignee": map[string]any{
										"type":        "string",
										"description": "Name of an available agent. Leave empty rather than guessing; an unassigned item creates an unassigned task.",
									},
									"required_capabilities": stringArraySchema(
										"Capability keys that must be available before this item can run."),
									"depends_on": stringArraySchema(
										"Ids of items that must succeed first. Use item ids, never descriptions or positions."),
									"expected_result": map[string]any{
										"type":        "string",
										"description": "How a reviewer will know this item succeeded.",
									},
									"reference_url": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
			"validations": objectArraySchema(
				"Checks that must pass before the plan may complete.",
				[]any{"title"},
				map[string]any{
					"id":    idSchema("Stable plan-local id. Omit for new checkpoints."),
					"title": map[string]any{"type": "string"},
					"applies_to": stringArraySchema(
						"Group or item ids this check gates. Empty means the whole plan."),
					"expectation": map[string]any{"type": "string"},
					"required":    map[string]any{"type": "boolean"},
				}),
			"execution": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "How approved work should run.",
				"properties": map[string]any{
					"mode": map[string]any{
						"type": "string",
						"enum": []any{string(ExecutionStepThrough), string(ExecutionAuto)},
					},
					"preconditions": stringArraySchema(
						"Enforcement adapter keys that must pass before code-oriented work begins. Only keys the application offers are honored."),
				},
			},
			"explanation": map[string]any{
				"type":        "string",
				"description": "Optional prose for the reader. Carries no dependency, assignment, or approval meaning.",
			},
		},
	}
}

// ClarificationSchema returns the JSON Schema for a round of clarification
// questions (FR-23, FR-24). It is separate from the content schema because
// asking and drafting are different outcomes: a model returns one or the other,
// never both.
func ClarificationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"questions"},
		"properties": map[string]any{
			"questions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"prompt"},
					"properties": map[string]any{
						"id":     idSchema("Stable id. Reuse it exactly when re-asking an existing question."),
						"prompt": map[string]any{"type": "string"},
						"detail": map[string]any{"type": "string"},
						"options": stringArraySchema(
							"Optional suggested answers. The user may always answer freely."),
						"required": map[string]any{
							"type":        "boolean",
							"description": "True only when the plan cannot be drafted without this answer.",
						},
					},
				},
			},
		},
	}
}

func idSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func stringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func objectArraySchema(description string, required []any, properties map[string]any) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             required,
			"properties":           properties,
		},
	}
}

// generatedContent mirrors the schema above. It exists so decoding is a typed
// operation rather than map-walking, and so a field the schema does not declare
// cannot arrive as Plan content by accident.
type generatedContent struct {
	Objective   string                `json:"objective"`
	Rationale   string                `json:"rationale"`
	InScope     []string              `json:"in_scope"`
	NonGoals    []string              `json:"non_goals"`
	Assumptions []generatedNamed      `json:"assumptions"`
	Risks       []generatedRisk       `json:"risks"`
	Sources     []generatedSource     `json:"sources"`
	Artifacts   []generatedArtifact   `json:"artifacts"`
	Groups      []generatedGroup      `json:"groups"`
	Validations []generatedCheckpoint `json:"validations"`
	Execution   generatedExecution    `json:"execution"`
	Explanation string                `json:"explanation"`
}

type generatedNamed struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type generatedRisk struct {
	ID         string `json:"id"`
	Statement  string `json:"statement"`
	Severity   string `json:"severity"`
	Mitigation string `json:"mitigation"`
}

type generatedSource struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

type generatedArtifact struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type generatedGroup struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Outcome   string          `json:"outcome"`
	Notes     string          `json:"notes"`
	DependsOn []string        `json:"depends_on"`
	Items     []generatedItem `json:"items"`
}

type generatedItem struct {
	ID                   string   `json:"id"`
	Description          string   `json:"description"`
	Details              string   `json:"details"`
	Assignee             string   `json:"assignee"`
	RequiredCapabilities []string `json:"required_capabilities"`
	DependsOn            []string `json:"depends_on"`
	ExpectedResult       string   `json:"expected_result"`
	ReferenceURL         string   `json:"reference_url"`
}

type generatedCheckpoint struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	AppliesTo   []string `json:"applies_to"`
	Expectation string   `json:"expectation"`
	Required    bool     `json:"required"`
}

type generatedExecution struct {
	Mode          string   `json:"mode"`
	Preconditions []string `json:"preconditions"`
}

// DecodePlanContent turns a model's structured response into typed Plan
// content and the objective that goes with it.
//
// Decoding assigns stable Plan-local IDs to anything the model left without
// one, and preserves the IDs it reused — that is what lets a targeted revision
// keep untouched sections identified across regenerations (FR-52, FR-55).
//
// Every generated element is marked AuthorModel so version provenance can show
// which parts a user wrote and which a model produced (FR-57).
//
// Decoding validates shape only. Dependency targets, cycles, bounds, and agent
// availability are validation.go's job, and nothing may become a reviewable
// version without passing it (FR-41).
func DecodePlanContent(raw []byte) (objective string, content PlanContent, err error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", PlanContent{}, fmt.Errorf("%w: model returned no plan content", ErrValidation)
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	// A field the schema never declared is a signal the response is not the
	// contract we asked for, so it is refused rather than quietly dropped.
	decoder.DisallowUnknownFields()

	var generated generatedContent
	if decodeErr := decoder.Decode(&generated); decodeErr != nil {
		return "", PlanContent{}, fmt.Errorf("%w: model output did not match the plan schema: %v",
			ErrValidation, decodeErr)
	}

	content = PlanContent{
		Rationale:   strings.TrimSpace(generated.Rationale),
		InScope:     trimAll(generated.InScope),
		NonGoals:    trimAll(generated.NonGoals),
		Explanation: generated.Explanation,
		Execution: ExecutionPolicy{
			Mode:          normalizeExecutionMode(generated.Execution.Mode),
			Preconditions: trimAll(generated.Execution.Preconditions),
		},
	}

	for _, assumption := range generated.Assumptions {
		content.Assumptions = append(content.Assumptions, Assumption{
			ID:        orNewID(assumption.ID, NewAssumptionID),
			Statement: strings.TrimSpace(assumption.Statement),
			Author:    AuthorModel,
		})
	}
	for _, risk := range generated.Risks {
		content.Risks = append(content.Risks, Risk{
			ID:         orNewID(risk.ID, NewRiskID),
			Statement:  strings.TrimSpace(risk.Statement),
			Severity:   normalizeSeverity(risk.Severity),
			Mitigation: strings.TrimSpace(risk.Mitigation),
			Author:     AuthorModel,
		})
	}
	for _, source := range generated.Sources {
		content.Sources = append(content.Sources, Source{
			ID:      orNewID(source.ID, NewSourceID),
			Kind:    SourceKind(strings.TrimSpace(source.Kind)),
			Ref:     strings.TrimSpace(source.Ref),
			Title:   strings.TrimSpace(source.Title),
			Excerpt: source.Excerpt,
			Author:  AuthorModel,
		})
	}
	for _, artifact := range generated.Artifacts {
		content.Artifacts = append(content.Artifacts, ProposedArtifact{
			ID:          orNewID(artifact.ID, NewArtifactID),
			Kind:        ArtifactKind(strings.TrimSpace(artifact.Kind)),
			Path:        strings.TrimSpace(artifact.Path),
			Title:       strings.TrimSpace(artifact.Title),
			Description: strings.TrimSpace(artifact.Description),
			// Whether an artifact is actually written is a policy decision the
			// application makes, never something the model turns on (FR-95).
			Enabled: false,
		})
	}
	for _, group := range generated.Groups {
		decoded := TaskGroup{
			ID:        orNewID(group.ID, NewGroupID),
			Title:     strings.TrimSpace(group.Title),
			Outcome:   strings.TrimSpace(group.Outcome),
			Notes:     strings.TrimSpace(group.Notes),
			DependsOn: trimAll(group.DependsOn),
			Author:    AuthorModel,
		}
		for _, item := range group.Items {
			decoded.Items = append(decoded.Items, TaskItem{
				ID:                   orNewID(item.ID, NewItemID),
				Description:          strings.TrimSpace(item.Description),
				Details:              strings.TrimSpace(item.Details),
				Assignee:             strings.TrimSpace(item.Assignee),
				RequiredCapabilities: trimAll(item.RequiredCapabilities),
				DependsOn:            trimAll(item.DependsOn),
				ExpectedResult:       strings.TrimSpace(item.ExpectedResult),
				ReferenceURL:         strings.TrimSpace(item.ReferenceURL),
				Author:               AuthorModel,
			})
		}
		content.Groups = append(content.Groups, decoded)
	}
	for _, checkpoint := range generated.Validations {
		content.Validations = append(content.Validations, ValidationCheckpoint{
			ID:          orNewID(checkpoint.ID, NewValidationID),
			Title:       strings.TrimSpace(checkpoint.Title),
			AppliesTo:   trimAll(checkpoint.AppliesTo),
			Expectation: strings.TrimSpace(checkpoint.Expectation),
			Required:    checkpoint.Required,
			Author:      AuthorModel,
		})
	}

	return strings.TrimSpace(generated.Objective), content, nil
}

// decodeStrictJSON decodes into dest and refuses any field the target type does
// not declare, so a response that is not the contract we asked for is rejected
// rather than partially accepted.
func decodeStrictJSON(raw string, dest any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dest)
}

// marshalIndent renders Plan content for inclusion in a prompt.
func marshalIndent(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

func orNewID(id string, mint func() string) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return mint()
}

func trimAll(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeExecutionMode refuses to guess. An unrecognized mode becomes the
// safe one rather than the one that starts work, and validation reports the
// mismatch separately (FR-42).
func normalizeExecutionMode(mode string) ExecutionMode {
	switch ExecutionMode(strings.TrimSpace(mode)) {
	case ExecutionAuto:
		return ExecutionAuto
	case ExecutionStepThrough:
		return ExecutionStepThrough
	default:
		return ExecutionStepThrough
	}
}

func normalizeSeverity(severity string) RiskSeverity {
	switch RiskSeverity(strings.ToLower(strings.TrimSpace(severity))) {
	case RiskHigh:
		return RiskHigh
	case RiskMedium:
		return RiskMedium
	case RiskLow:
		return RiskLow
	default:
		return ""
	}
}
