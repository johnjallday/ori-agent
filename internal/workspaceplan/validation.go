package workspaceplan

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Hard bounds on one Plan (FR-42). These are product decisions, not tuning
// knobs: a Plan that needs more than this is a Plan that should be split or
// superseded, and over-limit content is refused whole rather than truncated
// (FR-43).
const (
	// MaxTaskGroups is the most task groups one Plan may contain.
	MaxTaskGroups = 20
	// MaxTaskItems is the most actionable task items one Plan may contain,
	// across all of its groups.
	MaxTaskItems = 200
	// MaxContentBytes is the largest canonical Plan JSON that may be stored.
	MaxContentBytes = 512 * 1024
	// MaxReviewVersions is the most immutable review versions one Plan may
	// retain. Reaching it offers split or supersession; it never deletes
	// history (FR-31).
	MaxReviewVersions = 50
)

// ValidationIssue is one specific reason content was refused. Issues carry a
// stable code and, where possible, the Plan-local ID at fault, so the editor
// can put the user on the exact field rather than showing a wall of prose.
type ValidationIssue struct {
	Code ValidationCode `json:"code"`
	// Field is a dotted path into the Plan content, when one applies.
	Field string `json:"field,omitempty"`
	// ID is the Plan-local ID at fault, when one applies.
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

// ValidationCode is the stable identifier for a class of validation failure.
type ValidationCode string

const (
	IssueMissingObjective      ValidationCode = "missing_objective"
	IssueNoGroups              ValidationCode = "no_groups"
	IssueEmptyGroup            ValidationCode = "empty_group"
	IssueMissingTitle          ValidationCode = "missing_title"
	IssueMissingDescription    ValidationCode = "missing_description"
	IssueDuplicateID           ValidationCode = "duplicate_id"
	IssueDanglingDependency    ValidationCode = "dangling_dependency"
	IssueSelfDependency        ValidationCode = "self_dependency"
	IssueCyclicDependency      ValidationCode = "cyclic_dependency"
	IssueInvalidExecutionMode  ValidationCode = "invalid_execution_mode"
	IssueInvalidEnum           ValidationCode = "invalid_enum"
	IssueTooManyGroups         ValidationCode = "too_many_groups"
	IssueTooManyItems          ValidationCode = "too_many_items"
	IssueContentTooLarge       ValidationCode = "content_too_large"
	IssueUnavailableAssignee   ValidationCode = "unavailable_assignee"
	IssueUnavailableCapability ValidationCode = "unavailable_capability"
	IssueUnsafeArtifactPath    ValidationCode = "unsafe_artifact_path"
	// IssueCredentialInContent marks content carrying something shaped like a
	// credential. It is refused rather than redacted: an immutable version can
	// only hide a token after the fact, never remove it (FR-170).
	IssueCredentialInContent ValidationCode = "credential_in_content"
)

// ValidationResult is the outcome of validating candidate Plan content.
type ValidationResult struct {
	Issues []ValidationIssue `json:"issues,omitempty"`
}

// OK reports whether the content may be persisted as a reviewable version.
func (r ValidationResult) OK() bool { return len(r.Issues) == 0 }

// Error renders the issues as one error carrying the right sentinel, so an
// HTTP caller gets `limit_exceeded` for a bounds failure and
// `validation_failed` for everything else (FR-166).
func (r ValidationResult) Error() error {
	if r.OK() {
		return nil
	}
	sentinel := ErrValidation
	for _, issue := range r.Issues {
		switch issue.Code {
		case IssueTooManyGroups, IssueTooManyItems, IssueContentTooLarge:
			sentinel = ErrLimitExceeded
		case IssueUnavailableAssignee, IssueUnavailableCapability:
			if sentinel == ErrValidation {
				sentinel = ErrUnavailableCapability
			}
		case IssueUnsafeArtifactPath:
			if sentinel == ErrValidation {
				sentinel = ErrUnsafePath
			}
		}
	}
	messages := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		messages = append(messages, issue.Message)
	}
	return fmt.Errorf("%w: %s", sentinel, strings.Join(messages, "; "))
}

// ValidationContext carries what the application knows and the model does not:
// which agents exist right now and which capabilities are available. It is
// optional — structural validation runs without it — but assignment checks
// need it, and the materializer re-runs them immediately before writing Tasks
// (FR-46, FR-48, FR-85).
type ValidationContext struct {
	// AvailableAgents lists agent names that may be assigned. Nil means
	// assignment checking is skipped, not that every agent is available.
	AvailableAgents []string
	// AvailableCapabilities lists capability keys that may be required. Nil
	// means capability checking is skipped.
	AvailableCapabilities []string
}

// ValidatePlanContent checks candidate content against everything the schema
// cannot express (FR-41, FR-42).
//
// It returns every issue it finds rather than the first, because a model
// repair attempt with one issue at a time would burn its bounded retries on a
// response it could have fixed in one pass (FR-44).
func ValidatePlanContent(objective string, content PlanContent, vctx ValidationContext) ValidationResult {
	var result ValidationResult
	add := func(code ValidationCode, id, field, format string, args ...any) {
		result.Issues = append(result.Issues, ValidationIssue{
			Code:    code,
			ID:      id,
			Field:   field,
			Message: fmt.Sprintf(format, args...),
		})
	}

	if strings.TrimSpace(objective) == "" {
		add(IssueMissingObjective, "", "objective", "a plan needs an objective")
	}
	if len(content.Groups) == 0 {
		add(IssueNoGroups, "", "groups", "a plan needs at least one task group")
	}

	// Bounds first, so an enormous candidate is refused without walking every
	// item of it.
	if len(content.Groups) > MaxTaskGroups {
		add(IssueTooManyGroups, "", "groups",
			"a plan may contain at most %d task groups; this one has %d. Split it or supersede it rather than trimming approved scope",
			MaxTaskGroups, len(content.Groups))
	}
	if items := content.ActionableItemCount(); items > MaxTaskItems {
		add(IssueTooManyItems, "", "groups",
			"a plan may contain at most %d task items; this one has %d. Split it or supersede it rather than trimming approved scope",
			MaxTaskItems, items)
	}
	if size, err := CanonicalSize(objective, content); err == nil && size > MaxContentBytes {
		add(IssueContentTooLarge, "", "",
			"plan content is %d bytes; the limit is %d. Split it or supersede it rather than truncating it",
			size, MaxContentBytes)
	}

	// Stable IDs must be unique across the whole Plan, because dependencies
	// reference them by ID alone (FR-8).
	seen := map[string]string{}
	requireUniqueID := func(id, kind, field string) {
		if strings.TrimSpace(id) == "" {
			return
		}
		if priorKind, exists := seen[id]; exists {
			add(IssueDuplicateID, id, field,
				"id %q is used by more than one element (%s and %s); plan-local ids must be unique",
				id, priorKind, kind)
			return
		}
		seen[id] = kind
	}

	groupIDs := map[string]struct{}{}
	itemIDs := map[string]struct{}{}
	for gi, group := range content.Groups {
		field := fmt.Sprintf("groups[%d]", gi)
		requireUniqueID(group.ID, "group", field)
		groupIDs[group.ID] = struct{}{}
		if strings.TrimSpace(group.Title) == "" {
			add(IssueMissingTitle, group.ID, field+".title", "task group %d needs a title", gi+1)
		}
		if len(group.Items) == 0 {
			add(IssueEmptyGroup, group.ID, field+".items",
				"task group %q has no items; remove it or give it work", group.Title)
		}
		for ii, item := range group.Items {
			itemField := fmt.Sprintf("%s.items[%d]", field, ii)
			requireUniqueID(item.ID, "item", itemField)
			itemIDs[item.ID] = struct{}{}
			if strings.TrimSpace(item.Description) == "" {
				add(IssueMissingDescription, item.ID, itemField+".description",
					"task item %d in %q needs a description", ii+1, group.Title)
			}
		}
	}

	for _, assumption := range content.Assumptions {
		requireUniqueID(assumption.ID, "assumption", "assumptions")
	}
	for _, risk := range content.Risks {
		requireUniqueID(risk.ID, "risk", "risks")
		if risk.Severity != "" && risk.Severity != RiskLow && risk.Severity != RiskMedium && risk.Severity != RiskHigh {
			add(IssueInvalidEnum, risk.ID, "risks.severity", "unsupported risk severity %q", risk.Severity)
		}
	}
	for _, source := range content.Sources {
		requireUniqueID(source.ID, "source", "sources")
		if !validSourceKind(source.Kind) {
			add(IssueInvalidEnum, source.ID, "sources.kind", "unsupported source kind %q", source.Kind)
		}
	}
	for _, checkpoint := range content.Validations {
		requireUniqueID(checkpoint.ID, "validation", "validations")
	}

	for ai, artifact := range content.Artifacts {
		field := fmt.Sprintf("artifacts[%d]", ai)
		requireUniqueID(artifact.ID, "artifact", field)
		if !validArtifactKind(artifact.Kind) {
			add(IssueInvalidEnum, artifact.ID, field+".kind", "unsupported artifact kind %q", artifact.Kind)
		}
		// Path safety is re-checked at the write boundary too. Catching it
		// here means an unsafe path never reaches review, rather than failing
		// after a user has approved it (FR-97, FR-169).
		if err := ValidateArtifactPath(artifact.Path); err != nil {
			add(IssueUnsafeArtifactPath, artifact.ID, field+".path", "%s", err.Error())
		}
	}

	validateDependencies(content, groupIDs, itemIDs, add)
	validateAssignments(content, vctx, add)

	if content.Execution.Mode != ExecutionStepThrough && content.Execution.Mode != ExecutionAuto {
		add(IssueInvalidExecutionMode, "", "execution.mode",
			"unsupported execution mode %q; supported modes are %q and %q",
			content.Execution.Mode, ExecutionStepThrough, ExecutionAuto)
	}

	// Credentials are refused here rather than stored and redacted later.
	// Validation is the last point before content can become an immutable
	// version, and after that redaction can only hide a token, never remove it
	// (FR-170).
	result.Issues = append(result.Issues, credentialIssues(FindCredentials(objective, content))...)

	return result
}

type issueFunc func(code ValidationCode, id, field, format string, args ...any)

// validateDependencies checks that every dependency points at something that
// exists and that the graph has no cycle. Both are checked over stable IDs, so
// a reordered Plan validates identically (FR-8, FR-42).
func validateDependencies(content PlanContent, groupIDs, itemIDs map[string]struct{}, add issueFunc) {
	groupEdges := map[string][]string{}
	for _, group := range content.Groups {
		for _, dep := range group.DependsOn {
			if dep == group.ID {
				add(IssueSelfDependency, group.ID, "groups.depends_on",
					"task group %q depends on itself", group.Title)
				continue
			}
			if _, ok := groupIDs[dep]; !ok {
				add(IssueDanglingDependency, group.ID, "groups.depends_on",
					"task group %q depends on %q, which is not a group in this plan", group.Title, dep)
				continue
			}
			groupEdges[group.ID] = append(groupEdges[group.ID], dep)
		}
	}

	itemEdges := map[string][]string{}
	for _, group := range content.Groups {
		for _, item := range group.Items {
			for _, dep := range item.DependsOn {
				if dep == item.ID {
					add(IssueSelfDependency, item.ID, "items.depends_on",
						"task item %q depends on itself", item.Description)
					continue
				}
				if _, ok := itemIDs[dep]; !ok {
					add(IssueDanglingDependency, item.ID, "items.depends_on",
						"task item %q depends on %q, which is not an item in this plan", item.Description, dep)
					continue
				}
				itemEdges[item.ID] = append(itemEdges[item.ID], dep)
			}
		}
	}

	for _, cycle := range findCycles(groupEdges) {
		add(IssueCyclicDependency, cycle[0], "groups.depends_on",
			"task groups form a dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	for _, cycle := range findCycles(itemEdges) {
		add(IssueCyclicDependency, cycle[0], "items.depends_on",
			"task items form a dependency cycle: %s", strings.Join(cycle, " -> "))
	}
}

// findCycles returns each dependency cycle in the graph, as the sequence of
// IDs that closes it. Iteration order is sorted so the reported cycle is the
// same on every run — a validation error that changes wording between attempts
// is a bad error.
func findCycles(edges map[string][]string) [][]string {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string
	var cycles [][]string
	seenCycle := map[string]struct{}{}

	nodes := make([]string, 0, len(edges))
	for node := range edges {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	var visit func(node string)
	visit = func(node string) {
		state[node] = inStack
		stack = append(stack, node)

		next := append([]string(nil), edges[node]...)
		sort.Strings(next)
		for _, dep := range next {
			switch state[dep] {
			case unvisited:
				visit(dep)
			case inStack:
				// Report from where the cycle starts, so the message names the
				// loop rather than the path that reached it.
				start := 0
				for i, entry := range stack {
					if entry == dep {
						start = i
						break
					}
				}
				cycle := append(append([]string(nil), stack[start:]...), dep)
				key := strings.Join(cycle, ">")
				if _, exists := seenCycle[key]; !exists {
					seenCycle[key] = struct{}{}
					cycles = append(cycles, cycle)
				}
			}
		}

		stack = stack[:len(stack)-1]
		state[node] = done
	}

	for _, node := range nodes {
		if state[node] == unvisited {
			visit(node)
		}
	}
	return cycles
}

// validateAssignments checks proposed assignees and capabilities against what
// the workspace actually has. An unavailable assignee blocks review rather than
// being silently dropped or silently replaced (FR-47, FR-48).
func validateAssignments(content PlanContent, vctx ValidationContext, add issueFunc) {
	var agents, capabilities map[string]struct{}
	if vctx.AvailableAgents != nil {
		agents = toSet(vctx.AvailableAgents)
	}
	if vctx.AvailableCapabilities != nil {
		capabilities = toSet(vctx.AvailableCapabilities)
	}
	if agents == nil && capabilities == nil {
		return
	}

	content.EachItem(func(group TaskGroup, item TaskItem) bool {
		// An empty assignee is a legitimate choice: it materializes an
		// unassigned Task rather than a guess (FR-86).
		if agents != nil && item.Assignee != "" {
			if _, ok := agents[item.Assignee]; !ok {
				add(IssueUnavailableAssignee, item.ID, "items.assignee",
					"task item %q is assigned to %q, which is not an available agent in this workspace",
					item.Description, item.Assignee)
			}
		}
		if capabilities != nil {
			for _, capability := range item.RequiredCapabilities {
				if _, ok := capabilities[capability]; !ok {
					add(IssueUnavailableCapability, item.ID, "items.required_capabilities",
						"task item %q requires capability %q, which is not available in this workspace",
						item.Description, capability)
				}
			}
		}
		return true
	})
}

func toSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func validSourceKind(kind SourceKind) bool {
	switch kind {
	case SourceURL, SourceFile, SourceNote, SourceTask, SourceRun, SourceText:
		return true
	default:
		return false
	}
}

func validArtifactKind(kind ArtifactKind) bool {
	switch kind {
	case ArtifactPRD, ArtifactTaskList, ArtifactNote, ArtifactDocument:
		return true
	default:
		return false
	}
}

// CanonicalSize returns the byte size of the Plan's canonical JSON, which is
// what the 512 KiB bound applies to (FR-42).
func CanonicalSize(objective string, content PlanContent) (int, error) {
	encoded, err := json.Marshal(struct {
		Objective string      `json:"objective"`
		Content   PlanContent `json:"content"`
	}{Objective: objective, Content: content})
	if err != nil {
		return 0, fmt.Errorf("measure plan content: %w", err)
	}
	return len(encoded), nil
}

// ValidateArtifactPath rejects a proposed artifact path that is absolute,
// escapes the workspace root, or is not a file. It is deliberately strict and
// deliberately duplicated at the write boundary: a path is untrusted input
// wherever it arrives from (FR-97, FR-169).
func ValidateArtifactPath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("artifact path is required")
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("artifact path %q must be relative to the workspace", path)
	}
	// A Windows-style drive letter is absolute even though it has no leading
	// slash.
	if len(normalized) >= 2 && normalized[1] == ':' {
		return fmt.Errorf("artifact path %q must be relative to the workspace", path)
	}
	if strings.Contains(normalized, "\x00") {
		return fmt.Errorf("artifact path %q contains an invalid character", path)
	}
	if slices.Contains(strings.Split(normalized, "/"), "..") {
		return fmt.Errorf("artifact path %q must not escape the workspace root", path)
	}
	if strings.HasSuffix(normalized, "/") {
		return fmt.Errorf("artifact path %q must name a file, not a directory", path)
	}
	return nil
}
