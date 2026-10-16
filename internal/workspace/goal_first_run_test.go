package workspace

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func TestGoalAdmissionConsentPrecedesReadinessAndBridge(t *testing.T) {
	for _, kind := range []string{"code", "copy", "legacy-profile", "folder", "recipe", "damaged-binding"} {
		t.Run(kind, func(t *testing.T) {
			ts, store, starter, mux, ws := goalStartFixture(t)
			if err := store.Update(ws.ID, func(w *Workspace) error {
				w.GoalFirstRunPending = true
				switch kind {
				case "code":
					w.TemplateProvenance = &TemplateProvenance{TemplateID: "code-project"}
				case "copy":
					w.TemplateProvenance = &TemplateProvenance{TemplateID: "renamed-copy", BehaviorProfile: "software_project"}
				case "legacy-profile":
					w.SharedData = workspacesettings.Store(w.SharedData, workspacesettings.ProfileDefaults("software_project"))
				case "folder":
					w.Tasks = []Task{{Context: map[string]any{"template_id": "folder-digest", "template_starter_task": true}}}
				case "recipe":
					w.Tasks = []Task{{Context: map[string]any{TaskContextFirstTaskRecipe: FirstTaskRecipeSoftwareAssessment}}}
				case "damaged-binding":
					w.SharedData[SoftwareProjectKey] = "damaged"
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			checked := 0
			ts.SetMissionReadinessCheck(func(context.Context, *Workspace) *MissionBlockedError { checked++; return nil })
			base := "/api/workspaces/" + ws.ID + "/mission"
			for range 3 {
				serveJSON(t, mux, http.MethodPost, base+"/first-open", "")
			}
			fresh, _ := store.Get(ws.ID)
			if starter.startCount() != 0 || checked != 0 || fresh.GoalFirstRunPending || fresh.GoalFirstRunState != GoalFirstRunBlocked {
				t.Fatalf("consent was bypassed: starts=%d checks=%d state=%s pending=%t", starter.startCount(), checked, fresh.GoalFirstRunState, fresh.GoalFirstRunPending)
			}
			_, state := serveJSON(t, mux, http.MethodGet, base, "")
			if state["read_consent_required"] != true || state["needs_attention"] != GoalReadConsentReason {
				t.Fatalf("consent disclosure absent: %v", state)
			}
			// A click starts a goal, never changes or starts the first-look task.
			beforeTasks := len(fresh.Tasks)
			if code, body := serveJSON(t, mux, http.MethodPost, base+"/trigger", ""); code != http.StatusAccepted {
				t.Fatalf("explicit start = %d %v", code, body)
			}
			fresh, _ = store.Get(ws.ID)
			if starter.startCount() != 1 || checked != 1 || len(fresh.Tasks) != beforeTasks {
				t.Fatal("Run now must start only the goal")
			}
		})
	}
}

func TestGoalAdmissionFirstSaveDoesNotAuthorizeCodeRead(t *testing.T) {
	_, store, starter, mux, ws := goalStartFixture(t)
	_ = store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = ""
		w.TemplateProvenance = &TemplateProvenance{BehaviorProfile: "software_project"}
		return nil
	})
	_, body := serveJSON(t, mux, http.MethodPut, "/api/workspaces/"+ws.ID+"/mission", `{"mission":"Understand this project"}`)
	first, _ := body["first_run"].(map[string]any)
	if first["code"] != "goal_read_consent_required" || starter.startCount() != 0 {
		t.Fatalf("saving authorized a read: %v", body)
	}
}

func TestGoalAdmissionBlockersRequireExplicitRetry(t *testing.T) {
	for _, kind := range []string{"setup", "missing-check", "no-agent", "no-model", "bindings", "toolbox", "local"} {
		t.Run(kind, func(t *testing.T) {
			ts, store, starter, mux, ws := goalStartFixture(t)
			_ = store.Update(ws.ID, func(w *Workspace) error {
				w.GoalFirstRunPending = true
				switch kind {
				case "missing-check":
					w.TemplateProvenance = &TemplateProvenance{SetupWizard: &SetupWizard{Version: 1}}
				case "no-agent":
					w.AgentInstances = nil
					delete(w.SharedData, sharedDataEntryAgentNameKey)
				case "bindings":
					w.MCPBindings = []MCPBinding{{ID: "unknown", Enabled: true}}
				case "toolbox":
					w.GoalToolboxPolicy = &GoalToolboxPolicy{EntryAgentInstanceID: w.AgentInstances[0].ID, ToolboxID: "missing", ToolboxVersion: 1}
				}
				return nil
			})
			switch kind {
			case "setup":
				ts.SetMissionReadinessCheck(func(context.Context, *Workspace) *MissionBlockedError {
					return &MissionBlockedError{Code: "goal_setup_required", Reason: "Complete workspace setup, then choose Run now"}
				})
			case "no-model":
				ts.SetMissionModelCheck(func(*Workspace) (bool, string) { return false, "" })
			case "local":
				ts.workspaceStore = &executionAdmissionTestStore{Store: store}
			}
			base := "/api/workspaces/" + ws.ID + "/mission"
			serveJSON(t, mux, http.MethodPost, base+"/first-open", "")
			fresh, _ := store.Get(ws.ID)
			if starter.startCount() != 0 || fresh.GoalFirstRunState != GoalFirstRunBlocked || goalRunAttention(fresh, false) == "" {
				t.Fatalf("blocker not retained: starts=%d state=%s", starter.startCount(), fresh.GoalFirstRunState)
			}
			// Repairing does not arm automatic retry.
			ts.workspaceStore = store
			ts.SetMissionReadinessCheck(func(context.Context, *Workspace) *MissionBlockedError { return nil })
			ts.SetMissionModelCheck(nil)
			_ = store.Update(ws.ID, func(w *Workspace) error {
				w.AgentInstances = []AgentInstance{{ID: "writer", Name: "Writer"}}
				w.MCPBindings = nil
				w.GoalToolboxPolicy = nil
				return w.SetEntryAgentName("Writer")
			})
			for range 3 {
				serveJSON(t, mux, http.MethodPost, base+"/first-open", "")
			}
			if starter.startCount() != 0 {
				t.Fatal("repair/reload retried automatically")
			}
			if code, body := serveJSON(t, mux, http.MethodPost, base+"/trigger", ""); code != http.StatusAccepted {
				t.Fatalf("retry = %d %v", code, body)
			}
		})
	}
}

func TestGoalAdmissionManualHonorsLocalPolicy(t *testing.T) {
	ts, store, starter, _, ws := goalStartFixture(t)
	local := &executionAdmissionTestStore{Store: store}
	ts.workspaceStore = local
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); err == nil || starter.startCount() != 0 {
		t.Fatal("manual run bypassed installation-local admission")
	}
	local.manual = true
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); err != nil {
		t.Fatal(err)
	}
}

func TestGoalFirstRunConcurrentAndFastCompletionNeverReplays(t *testing.T) {
	for _, competitor := range []string{"first-open", "manual", "cadence", "event", "first-save"} {
		t.Run(competitor, func(t *testing.T) {
			ts, store, starter, mux, ws := goalStartFixture(t)
			_ = store.Update(ws.ID, func(w *Workspace) error { w.GoalFirstRunPending = true; return nil })
			base := "/api/workspaces/" + ws.ID + "/mission"
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); serveJSON(t, mux, http.MethodPost, base+"/first-open", "") }()
			go func() {
				defer wg.Done()
				switch competitor {
				case "first-open":
					serveJSON(t, mux, http.MethodPost, base+"/first-open", "")
				case "first-save":
					serveJSON(t, mux, http.MethodPut, base, `{"mission":"Edited goal"}`)
				case "manual":
					_, _ = ts.StartMissionRun(ws.ID, MissionOriginManual)
				default:
					_, _ = ts.StartMissionRun(ws.ID, competitor)
				}
			}()
			wg.Wait()
			if starter.startCount() != 1 {
				t.Fatalf("concurrent starts = %d", starter.startCount())
			}
			close(starter.release)
			ts.wg.Wait() // completion between requests must not leave deferred work
			for range 3 {
				serveJSON(t, mux, http.MethodPost, base+"/first-open", "")
			}
			if starter.startCount() != 1 {
				t.Fatal("fast completion rearmed first-open")
			}
			fresh, _ := store.Get(ws.ID)
			if fresh.GoalFirstRunPending || fresh.GoalFirstRunState != GoalFirstRunAccepted {
				t.Fatalf("accepted state = %s / %t", fresh.GoalFirstRunState, fresh.GoalFirstRunPending)
			}
		})
	}
}

func TestGoalFirstRunClearCancelsDuringAdmission(t *testing.T) {
	ts, store, starter, mux, ws := goalStartFixture(t)
	_ = store.Update(ws.ID, func(w *Workspace) error { w.GoalFirstRunPending = true; return nil })
	ts.SetMissionReadinessCheck(func(context.Context, *Workspace) *MissionBlockedError {
		serveJSON(t, mux, http.MethodPut, "/api/workspaces/"+ws.ID+"/mission", `{"mission":""}`)
		return nil
	})
	_, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal)
	if !errors.Is(err, ErrMissionGoalRequired) || starter.startCount() != 0 {
		t.Fatalf("clear did not cancel admission: %v", err)
	}
	fresh, _ := store.Get(ws.ID)
	if fresh.GoalFirstRunPending || fresh.Mission != "" {
		t.Fatal("clear resurrected intent")
	}
}

type failingGoalStore struct {
	Store
	updates        int
	failAt         int
	canonicalError bool
}

func (s *failingGoalStore) Update(id string, fn func(*Workspace) error) error {
	s.updates++
	if s.updates == s.failAt {
		return errors.New("disk full")
	}
	return s.Store.Update(id, fn)
}
func (s *failingGoalStore) GetFolderWorkspace(id string) (*Workspace, error) {
	if s.canonicalError {
		return nil, errors.New("unreadable canonical metadata")
	}
	return s.Store.Get(id)
}

func TestGoalFirstRunPersistenceFailureNeverExecutes(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			ts, store, starter, _, ws := goalStartFixture(t)
			_ = store.Update(ws.ID, func(w *Workspace) error { w.GoalFirstRunPending = true; return nil })
			ts.workspaceStore = &failingGoalStore{Store: store, failAt: failAt}
			if _, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal); err == nil {
				t.Fatal("write failure hidden")
			}
			close(starter.release)
			ts.wg.Wait()
			if len(starter.executed) != 0 {
				t.Fatal("execution after failed persistence")
			}
			if failAt == 2 {
				fresh, _ := store.Get(ws.ID)
				if fresh.GoalFirstRunPending || fresh.GoalFirstRunState != GoalFirstRunStarting || !strings.Contains(goalRunAttention(fresh, false), "interrupted") {
					t.Fatal("ambiguous start must require deliberate recovery")
				}
				if _, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal); !errors.Is(err, ErrMissionFirstRunHandled) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGoalAdmissionUnreadableCanonicalMetadataFailsClosed(t *testing.T) {
	ts, store, starter, _, ws := goalStartFixture(t)
	ts.workspaceStore = &failingGoalStore{Store: store, canonicalError: true}
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); err == nil || starter.startCount() != 0 {
		t.Fatal("unreadable setup treated as absent")
	}
}

type outcomeGoalStarter struct {
	prepareError bool
}

func (s outcomeGoalStarter) TriggerMissionRun(context.Context, string, int) (string, error) {
	return "", errors.New("must use split preparation")
}
func (s outcomeGoalStarter) StartMissionRun(context.Context, string, int, MissionRunOptions) (string, func() error, error) {
	if s.prepareError {
		return "", nil, errors.New("create refused")
	}
	return "accepted-failed", func() error { return errors.New("execution failed") }, nil
}

func TestGoalFirstRunFailureBeforeAndAfterAcceptanceDoesNotReplay(t *testing.T) {
	for _, prepareError := range []bool{true, false} {
		ts, store, _, _, ws := goalStartFixture(t)
		ts.SetMissionTrigger(outcomeGoalStarter{prepareError: prepareError})
		_ = store.Update(ws.ID, func(w *Workspace) error { w.GoalFirstRunPending = true; return nil })
		_, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal)
		if (err != nil) != prepareError {
			t.Fatalf("prepare=%t err=%v", prepareError, err)
		}
		ts.wg.Wait()
		fresh, _ := store.Get(ws.ID)
		want := GoalFirstRunAccepted
		if prepareError {
			want = GoalFirstRunFailed
		}
		if fresh.GoalFirstRunState != want || fresh.GoalFirstRunPending {
			t.Fatalf("state=%s pending=%t", fresh.GoalFirstRunState, fresh.GoalFirstRunPending)
		}
		restarted := NewTaskScheduler(store, SchedulerConfig{})
		restarted.SetMissionTrigger(outcomeGoalStarter{})
		if _, err := restarted.StartMissionRun(ws.ID, MissionOriginFirstGoal); !errors.Is(err, ErrMissionFirstRunHandled) {
			t.Fatal(err)
		}
	}
}

func TestGoalAdmissionResetFenceRefusesBeforeBridge(t *testing.T) {
	ts, _, starter, _, ws := goalStartFixture(t)
	ts.admissionGate = &resetstate.WorkGate{}
	if err := ts.admissionGate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); err == nil || starter.startCount() != 0 {
		t.Fatal("reset fence bypassed")
	}
	if _, running := ts.MissionRunning(ws.ID); running {
		t.Fatal("reset refusal leaked slot")
	}
}

func TestGoalFirstSaveAndManualShareClaim(t *testing.T) {
	ts, store, starter, mux, ws := goalStartFixture(t)
	_ = store.Update(ws.ID, func(w *Workspace) error { w.Mission = ""; return nil })
	entered, release, saved := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ts.SetMissionReadinessCheck(func(context.Context, *Workspace) *MissionBlockedError {
		close(entered)
		<-release
		return nil
	})
	go func() {
		defer close(saved)
		serveJSON(t, mux, http.MethodPut, "/api/workspaces/"+ws.ID+"/mission", `{"mission":"First saved goal"}`)
	}()
	<-entered
	_, manualErr := ts.StartMissionRun(ws.ID, MissionOriginManual)
	close(release)
	<-saved
	if !errors.Is(manualErr, ErrMissionRunInFlight) || starter.startCount() != 1 {
		t.Fatalf("starts=%d manual=%v", starter.startCount(), manualErr)
	}
	close(starter.release)
	ts.wg.Wait()
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal); !errors.Is(err, ErrMissionFirstRunHandled) {
		t.Fatal(err)
	}
}

func TestGoalAdmissionImplicitToolboxAllowsAdvisoryButNotBrokenAssignment(t *testing.T) {
	ws := newRecommendationFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: "tbx-lean"}); err != nil {
		t.Fatal(err)
	}
	for i := range ws.Toolboxes {
		if ws.Toolboxes[i].ID == "tbx-lean" {
			for j := range ws.Toolboxes[i].MCPBindings {
				ws.Toolboxes[i].MCPBindings[j].InheritsBindingTools = true
			}
		}
	}
	for i := range ws.MCPBindings {
		ws.MCPBindings[i].AllowedTools = nil
	}
	if result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds()); !result.OK {
		t.Fatalf("advisory blocked legacy behavior: %+v", result)
	}
	ws.Toolboxes = nil
	if result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds()); result.OK {
		t.Fatal("broken current assignment was accepted")
	}
}

func TestGoalCadenceConsumesDeferredIntentAndPreservesConsentChoice(t *testing.T) {
	ts, store, trigger := newSchedulerWithStub(t)
	ws := readyCadenceWorkspace(t)
	past := time.Now().Add(-time.Hour)
	ws.GoalFirstRunPending = true
	ws.MissionEnabled = true
	ws.Cadence = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
	ws.NextMissionRunAt = &past
	ws.TemplateProvenance = &TemplateProvenance{BehaviorProfile: "software_project"}
	_ = store.Save(ws)
	ts.checkMissionCadence(ws, time.Now())
	if trigger.callCount() != 1 {
		t.Fatal("explicit cadence was incorrectly gated on first-open consent")
	}
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginFirstGoal); !errors.Is(err, ErrMissionFirstRunHandled) {
		t.Fatal(err)
	}
	if trigger.callCount() != 1 {
		t.Fatal("cadence left a deferred run")
	}
}
