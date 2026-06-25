package templateonboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SessionSaver is the persistence dependency the executor needs. Store
// satisfies it; tests can use an in-memory implementation.
type SessionSaver interface {
	Save(ctx context.Context, session *Session) error
}

// ProjectInstantiator creates the deferred template skeleton after the user
// confirms onboarding completion.
type ProjectInstantiator interface {
	InstantiateProject(ctx context.Context, workspaceID, templateID, templatePath, projectName string, fieldValues map[string]any) (string, error)
}

// RuntimeResolver is the part of workspace.AgentRuntimeResolver needed for
// dependency preflight checks.
type RuntimeResolver interface {
	ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error)
}

// MemoryAppender appends a durable workspace memory entry. workspace.MemoryStore
// satisfies it.
type MemoryAppender interface {
	Append(workspaceID string, entry workspace.MemoryEntry) error
}

// Executor runs the completion action for a template-onboarding session and
// owns all running/terminal state persistence.
type Executor struct {
	store               SessionSaver
	projectInstantiator ProjectInstantiator
	taskHandler         workspace.TaskHandler
	runtimeResolver     RuntimeResolver
	memory              MemoryAppender
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithProjectInstantiator wires deferred skeleton instantiation.
func WithProjectInstantiator(instantiator ProjectInstantiator) ExecutorOption {
	return func(e *Executor) {
		e.projectInstantiator = instantiator
	}
}

// WithTaskHandler wires synchronous task dispatch.
func WithTaskHandler(handler workspace.TaskHandler) ExecutorOption {
	return func(e *Executor) {
		e.taskHandler = handler
	}
}

// WithRuntimeResolver wires dependency preflight checks.
func WithRuntimeResolver(resolver RuntimeResolver) ExecutorOption {
	return func(e *Executor) {
		e.runtimeResolver = resolver
	}
}

// WithMemoryAppender wires optional MEMORY.md summary writes.
func WithMemoryAppender(memory MemoryAppender) ExecutorOption {
	return func(e *Executor) {
		e.memory = memory
	}
}

// NewExecutor creates a completion executor.
func NewExecutor(store SessionSaver, opts ...ExecutorOption) *Executor {
	executor := &Executor{store: store}
	for _, opt := range opts {
		if opt != nil {
			opt(executor)
		}
	}
	return executor
}

// Complete transitions the session to running, executes the configured action,
// persists the resulting terminal/blocked state, and returns the action result
// for compatibility with HTTP handlers. Action failures are recorded on the
// session as failed/blocked and do not return a Go error after persistence
// succeeds.
func (e *Executor) Complete(ctx context.Context, session *Session, entryAgentName string) (*ActionResult, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("%w: template onboarding executor store is required", ErrInvalidSession)
	}
	if session == nil {
		return nil, ErrInvalidSession
	}
	entryAgentName = strings.TrimSpace(entryAgentName)
	if entryAgentName == "" {
		return nil, fmt.Errorf("%w: entry agent is required", ErrSessionMissingInput)
	}
	if session.Status == StatusRunning {
		return nil, ErrSessionRunning
	}
	if session.Status == StatusSucceeded {
		return session.ActionResult, nil
	}
	if _, err := session.StartCompletion(); err != nil {
		return nil, err
	}
	if err := e.store.Save(ctx, session); err != nil {
		return nil, err
	}

	if blockers := e.preconditionBlockers(session, entryAgentName); len(blockers) > 0 {
		if _, err := session.Block(blockers...); err != nil {
			return nil, err
		}
		if err := e.store.Save(ctx, session); err != nil {
			return nil, err
		}
		return nil, nil
	}

	result, err := e.execute(ctx, session, entryAgentName)
	if err != nil {
		if _, markErr := session.MarkFailed(err.Error()); markErr != nil {
			return nil, markErr
		}
		if saveErr := e.store.Save(ctx, session); saveErr != nil {
			return nil, saveErr
		}
		return nil, nil
	}
	if _, err := session.MarkSucceeded(result); err != nil {
		return nil, err
	}
	if err := e.store.Save(ctx, session); err != nil {
		return nil, err
	}
	e.appendMemorySummary(session, result)
	return result, nil
}

func (e *Executor) execute(ctx context.Context, session *Session, entryAgentName string) (*ActionResult, error) {
	action := session.Spec.Completion
	inputs, err := substituteInputs(action.Inputs, session.Values)
	if err != nil {
		return nil, err
	}

	projectPath := strings.TrimSpace(session.ProjectPath)
	if action.InstantiateSkeleton && projectPath == "" {
		if e.projectInstantiator == nil {
			return nil, fmt.Errorf("project template instantiator is unavailable")
		}
		projectPath, err = e.projectInstantiator.InstantiateProject(ctx, session.WorkspaceID, session.TemplateID, session.TemplatePath, session.ProjectName, session.Values)
		if err != nil {
			return nil, err
		}
		session.ProjectPath = strings.TrimSpace(projectPath)
		if err := e.store.Save(ctx, session); err != nil {
			return nil, err
		}
	}
	if projectPath != "" {
		inputs["project_path"] = projectPath
	}

	switch action.Type {
	case ActionNone:
		result := "Template onboarding completed."
		if projectPath != "" {
			result = fmt.Sprintf("Template onboarding completed. Project created at %s.", projectPath)
		}
		return &ActionResult{Result: result, ProjectPath: projectPath}, nil
	case ActionTask:
		if e.taskHandler == nil {
			return nil, fmt.Errorf("template onboarding task handler is unavailable")
		}
		task := onboardingTask(session, entryAgentName, inputs)
		taskRun, err := workspace.ExecuteTaskWithRunMetadata(ctx, e.taskHandler, entryAgentName, task)
		if err != nil {
			return nil, err
		}
		return &ActionResult{
			Result:      taskRun.Result,
			RunID:       taskRun.RunID,
			TaskID:      task.ID,
			ProjectPath: projectPath,
		}, nil
	default:
		return nil, fmt.Errorf("completion action type %q is not supported in this version", action.Type)
	}
}

func onboardingTask(session *Session, entryAgentName string, inputs map[string]any) workspace.Task {
	instructions := strings.TrimSpace(session.Spec.Completion.Instructions)
	description := firstInstructionLine(instructions)
	if description == "" {
		description = "Complete template onboarding"
	}
	return workspace.Task{
		ID:          "template-onboarding-" + uuid.NewString(),
		WorkspaceID: session.WorkspaceID,
		From:        "system:template-onboarding",
		To:          entryAgentName,
		Description: description,
		Details:     instructions,
		Context:     cloneContext(inputs),
		Priority:    3,
		Status:      workspace.TaskStatusPending,
		CreatedAt:   time.Now(),
	}
}

func firstInstructionLine(instructions string) string {
	for _, line := range strings.Split(instructions, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (e *Executor) preconditionBlockers(session *Session, entryAgentName string) []string {
	action := session.Spec.Completion
	switch action.Type {
	case ActionNone, ActionTask:
	case ActionTool, ActionWorkflowTemplate:
		return []string{fmt.Sprintf("completion action type %q is not supported in this version", action.Type)}
	default:
		return []string{fmt.Sprintf("completion action type %q is not supported", action.Type)}
	}

	var blockers []string
	requiredSkills := append([]string(nil), action.SkillRefs...)
	for _, dep := range session.Spec.Dependencies {
		switch dep.Type {
		case DependencySkill:
			requiredSkills = append(requiredSkills, dep.Ref)
		case DependencyMCPServer:
			// Checked below after runtime resolution.
		case DependencyTool, DependencyWorkflowTemplate:
			blockers = append(blockers, fmt.Sprintf("%s dependency %q is not supported in this version", dep.Type, dep.Ref))
		default:
			blockers = append(blockers, fmt.Sprintf("dependency %q is not supported in this version", dep.Ref))
		}
	}

	requiredSkills = dedupeTrimmed(requiredSkills)
	needsRuntime := len(requiredSkills) > 0 || hasMCPDependency(session.Spec.Dependencies)
	if !needsRuntime {
		return blockers
	}
	if e.runtimeResolver == nil {
		return append(blockers, "entry-agent dependencies cannot be verified because runtime resolution is unavailable")
	}
	runtime, err := e.runtimeResolver.ResolveAgentForWorkspace(entryAgentName, session.WorkspaceID, "")
	if err != nil {
		return append(blockers, fmt.Sprintf("entry agent %q cannot be resolved: %v", entryAgentName, err))
	}
	if runtime == nil {
		return append(blockers, fmt.Sprintf("entry agent %q cannot be resolved", entryAgentName))
	}
	for _, skill := range missingEnabledSkills(requiredSkills, runtime.EffectiveSkills) {
		blockers = append(blockers, fmt.Sprintf("skill %q must be bound and enabled on entry agent %q before completion", skill, entryAgentName))
	}
	for _, dep := range session.Spec.Dependencies {
		if dep.Type == DependencyMCPServer && !containsFold(runtime.MCPServers, dep.Ref) {
			blockers = append(blockers, fmt.Sprintf("MCP server %q must be enabled for entry agent %q before completion", dep.Ref, entryAgentName))
		}
	}
	return blockers
}

func substituteInputs(inputs map[string]string, values map[string]any) (map[string]any, error) {
	if len(inputs) == 0 {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(inputs))
	for key, raw := range inputs {
		resolved, err := substituteInput(raw, values)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", key, err)
		}
		out[key] = resolved
	}
	return out, nil
}

func substituteInput(raw string, values map[string]any) (any, error) {
	refs := fieldRefPattern.FindAllStringSubmatch(raw, -1)
	if len(refs) == 0 {
		return raw, nil
	}
	if len(refs) == 1 && refs[0][0] == raw {
		value, ok := values[refs[0][1]]
		if !ok || value == nil {
			return nil, fmt.Errorf("%w: field %q has no value", ErrSessionMissingInput, refs[0][1])
		}
		return cloneJSONValue(value), nil
	}
	resolved := raw
	for _, ref := range refs {
		value, ok := values[ref[1]]
		if !ok || value == nil {
			return nil, fmt.Errorf("%w: field %q has no value", ErrSessionMissingInput, ref[1])
		}
		resolved = strings.ReplaceAll(resolved, ref[0], fmt.Sprint(value))
	}
	return resolved, nil
}

func cloneContext(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func missingEnabledSkills(required []string, effective []workspace.ResolvedSkill) []string {
	if len(required) == 0 {
		return nil
	}
	available := make(map[string]bool, len(effective))
	for _, skill := range effective {
		name := strings.ToLower(strings.TrimSpace(skill.Name))
		if name != "" && skill.Enabled {
			available[name] = true
		}
	}
	var missing []string
	for _, skill := range required {
		if !available[strings.ToLower(skill)] {
			missing = append(missing, skill)
		}
	}
	return missing
}

func hasMCPDependency(deps []Dependency) bool {
	for _, dep := range deps {
		if dep.Type == DependencyMCPServer {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func dedupeTrimmed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (e *Executor) appendMemorySummary(session *Session, result *ActionResult) {
	if e == nil || e.memory == nil || session == nil || result == nil {
		return
	}
	parts := []string{"Template onboarding completed"}
	if result.ProjectPath != "" {
		parts = append(parts, "project "+result.ProjectPath)
	}
	if result.RunID != "" {
		parts = append(parts, "run "+result.RunID)
	}
	text := strings.Join(parts, "; ") + "."
	cleaned, err := workspace.ValidateMemoryText(text)
	if err != nil {
		return
	}
	_ = e.memory.Append(session.WorkspaceID, workspace.MemoryEntry{
		Type:       workspace.MemoryTypeDecision,
		Date:       nowFunc().Format("2006-01-02"),
		Provenance: "template-onboarding",
		Text:       cleaned,
	})
}
