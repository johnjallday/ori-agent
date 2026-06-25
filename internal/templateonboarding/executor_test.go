package templateonboarding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestExecutorNoneInstantiatesSkeletonAndSucceeds(t *testing.T) {
	session := readyExecutorSession(t)
	session.Spec.Completion.Type = ActionNone
	session.Spec.Completion.Inputs = nil

	saver := &recordingSessionSaver{}
	instantiator := &fakeProjectInstantiator{returnPath: "project-x"}
	executor := NewExecutor(saver, WithProjectInstantiator(instantiator))

	result, err := executor.Complete(context.Background(), session, "Producer")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", session.Status)
	}
	if result == nil || result.ProjectPath != "project-x" {
		t.Fatalf("result = %+v, want project path", result)
	}
	if len(instantiator.calls) != 1 {
		t.Fatalf("instantiator calls = %d, want 1", len(instantiator.calls))
	}
	if instantiator.calls[0].values["song_name"] != "Night Drive" {
		t.Fatalf("instantiator values = %#v", instantiator.calls[0].values)
	}
	assertSavedStatuses(t, saver, StatusRunning, StatusRunning, StatusSucceeded)
}

func TestExecutorTaskDispatchSubstitutesInputsAndCapturesRun(t *testing.T) {
	session := readyExecutorSession(t)
	session.Spec.Completion.InstantiateSkeleton = false
	session.Spec.Completion.SkillRefs = []string{"reaper-session-setup"}
	session.Spec.Dependencies = []Dependency{{Type: DependencySkill, Ref: "workspace-planning"}}
	session.Spec.Completion.Instructions = "Create the REAPER song.\nUse the selected key."
	session.Spec.Completion.Inputs = map[string]string{
		"bpm":     "${fields.bpm}",
		"name":    "${fields.song_name}",
		"summary": "Song ${fields.song_name} at ${fields.bpm} BPM",
	}

	saver := &recordingSessionSaver{}
	handler := &fakeRunAwareTaskHandler{returnResult: workspace.TaskRunResult{Result: "done", RunID: "run-1"}}
	resolver := &fakeRuntimeResolver{runtime: &workspace.ResolvedAgentRuntime{
		EffectiveSkills: []workspace.ResolvedSkill{
			{Name: "reaper-session-setup", Enabled: true},
			{Name: "workspace-planning", Enabled: true},
		},
	}}
	executor := NewExecutor(saver, WithTaskHandler(handler), WithRuntimeResolver(resolver))

	result, err := executor.Complete(context.Background(), session, "Producer")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", session.Status)
	}
	if result.RunID != "run-1" || result.TaskID == "" {
		t.Fatalf("result = %+v, want run/task IDs", result)
	}
	if len(handler.tasks) != 1 {
		t.Fatalf("task calls = %d, want 1", len(handler.tasks))
	}
	task := handler.tasks[0]
	if task.To != "Producer" || task.WorkspaceID != "ws-1" {
		t.Fatalf("task routing = %+v", task)
	}
	if task.Description != "Create the REAPER song." || !strings.Contains(task.Details, "selected key") {
		t.Fatalf("task instructions = description %q details %q", task.Description, task.Details)
	}
	if task.Context["bpm"] != float64(128) || task.Context["name"] != "Night Drive" || task.Context["summary"] != "Song Night Drive at 128 BPM" {
		t.Fatalf("task context = %#v", task.Context)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
}

func TestExecutorInstantiateFailureMarksFailedAndSkipsTask(t *testing.T) {
	session := readyExecutorSession(t)
	saver := &recordingSessionSaver{}
	instantiator := &fakeProjectInstantiator{err: errors.New("copy failed")}
	handler := &fakeRunAwareTaskHandler{}
	executor := NewExecutor(saver, WithProjectInstantiator(instantiator), WithTaskHandler(handler))

	result, err := executor.Complete(context.Background(), session, "Producer")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil on failed execution", result)
	}
	if session.Status != StatusFailed || !strings.Contains(session.ActionError, "copy failed") {
		t.Fatalf("session = %+v, want failed copy error", session)
	}
	if len(handler.tasks) != 0 {
		t.Fatalf("task should not run after instantiate failure: %d calls", len(handler.tasks))
	}
	assertSavedStatuses(t, saver, StatusRunning, StatusFailed)
}

func TestExecutorBlocksMissingSkillPrecondition(t *testing.T) {
	session := readyExecutorSession(t)
	session.Spec.Completion.InstantiateSkeleton = false
	session.Spec.Completion.SkillRefs = []string{"reaper-session-setup"}

	saver := &recordingSessionSaver{}
	handler := &fakeRunAwareTaskHandler{}
	resolver := &fakeRuntimeResolver{runtime: &workspace.ResolvedAgentRuntime{
		EffectiveSkills: []workspace.ResolvedSkill{{Name: "other", Enabled: true}},
	}}
	executor := NewExecutor(saver, WithTaskHandler(handler), WithRuntimeResolver(resolver))

	result, err := executor.Complete(context.Background(), session, "Producer")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil on blocked execution", result)
	}
	if session.Status != StatusBlocked || !containsString(session.Blockers, `skill "reaper-session-setup" must be bound and enabled on entry agent "Producer" before completion`) {
		t.Fatalf("session blockers = %#v status=%q", session.Blockers, session.Status)
	}
	if len(handler.tasks) != 0 {
		t.Fatalf("task should not run while blocked: %d calls", len(handler.tasks))
	}
}

func TestExecutorBlocksReservedActionType(t *testing.T) {
	session := readyExecutorSession(t)
	session.Spec.Completion.Type = ActionTool
	session.Spec.Completion.Ref = "future-tool"

	saver := &recordingSessionSaver{}
	executor := NewExecutor(saver)

	if _, err := executor.Complete(context.Background(), session, "Producer"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session.Status != StatusBlocked || !strings.Contains(session.ActionError, `completion action type "tool" is not supported in this version`) {
		t.Fatalf("session = %+v, want blocked unsupported tool", session)
	}
}

func TestExecutorRetrySkipsAlreadyInstantiatedSkeleton(t *testing.T) {
	session := readyExecutorSession(t)
	saver := &recordingSessionSaver{}
	instantiator := &fakeProjectInstantiator{returnPath: "project-x"}
	handler := &fakeRunAwareTaskHandler{err: errors.New("task failed")}
	executor := NewExecutor(saver, WithProjectInstantiator(instantiator), WithTaskHandler(handler))

	if _, err := executor.Complete(context.Background(), session, "Producer"); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if session.Status != StatusFailed || session.ProjectPath != "project-x" {
		t.Fatalf("after failure session = %+v, want failed with project path", session)
	}

	handler.err = nil
	handler.returnResult = workspace.TaskRunResult{Result: "created", RunID: "run-2"}
	if _, err := executor.Complete(context.Background(), session, "Producer"); err != nil {
		t.Fatalf("retry Complete: %v", err)
	}
	if session.Status != StatusSucceeded || session.ActionResult.RunID != "run-2" {
		t.Fatalf("after retry session = %+v, want success", session)
	}
	if len(instantiator.calls) != 1 {
		t.Fatalf("instantiator calls = %d, want only initial skeleton creation", len(instantiator.calls))
	}
}

func readyExecutorSession(t *testing.T) *Session {
	t.Helper()
	session, err := NewSession("ws-1", testSpec(), StatusCollecting)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.TemplateID = "demo-template"
	session.ProjectName = "Project X"
	if _, err := session.MergeValues(map[string]any{"song_name": "Night Drive", "bpm": 128}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	if _, err := session.MarkReadyToComplete(); err != nil {
		t.Fatalf("MarkReadyToComplete: %v", err)
	}
	return session
}

type recordingSessionSaver struct {
	saved []*Session
}

func (s *recordingSessionSaver) Save(ctx context.Context, session *Session) error {
	_ = ctx
	cloned, err := session.Clone()
	if err != nil {
		return err
	}
	s.saved = append(s.saved, cloned)
	return nil
}

type fakeProjectInstantiator struct {
	returnPath string
	err        error
	calls      []projectInstantiateCall
}

type projectInstantiateCall struct {
	workspaceID  string
	templateID   string
	templatePath string
	projectName  string
	values       map[string]any
}

func (f *fakeProjectInstantiator) InstantiateProject(ctx context.Context, workspaceID, templateID, templatePath, projectName string, fieldValues map[string]any) (string, error) {
	_ = ctx
	f.calls = append(f.calls, projectInstantiateCall{
		workspaceID:  workspaceID,
		templateID:   templateID,
		templatePath: templatePath,
		projectName:  projectName,
		values:       cloneContext(fieldValues),
	})
	if f.err != nil {
		return "", f.err
	}
	return f.returnPath, nil
}

type fakeRunAwareTaskHandler struct {
	returnResult workspace.TaskRunResult
	err          error
	tasks        []workspace.Task
}

func (h *fakeRunAwareTaskHandler) ExecuteTask(ctx context.Context, agentName string, task workspace.Task) (string, error) {
	result, err := h.ExecuteTaskRun(ctx, agentName, task)
	return result.Result, err
}

func (h *fakeRunAwareTaskHandler) ExecuteTaskRun(ctx context.Context, agentName string, task workspace.Task) (workspace.TaskRunResult, error) {
	_ = ctx
	_ = agentName
	h.tasks = append(h.tasks, task)
	if h.err != nil {
		return workspace.TaskRunResult{}, h.err
	}
	return h.returnResult, nil
}

type fakeRuntimeResolver struct {
	runtime *workspace.ResolvedAgentRuntime
	err     error
	calls   int
}

func (r *fakeRuntimeResolver) ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error) {
	_, _, _ = agentName, workspaceID, nodeID
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.runtime, nil
}

func assertSavedStatuses(t *testing.T, saver *recordingSessionSaver, want ...Status) {
	t.Helper()
	if len(saver.saved) != len(want) {
		t.Fatalf("saved count = %d, want %d (%#v)", len(saver.saved), len(want), saver.saved)
	}
	for i, status := range want {
		if saver.saved[i].Status != status {
			t.Fatalf("saved[%d] status = %q, want %q", i, saver.saved[i].Status, status)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
