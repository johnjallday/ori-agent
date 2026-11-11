package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestDownloads_StoppedLegacyRetryNeedsFreshChoicesAndDoesNotReviveGoal(t *testing.T) {
	root := demo(t)
	h := newHarness(t, root)
	v := h.start(t, "")
	old, _ := h.svc.Record("u1", v.WorkspaceID)
	oldPlan := BuildPlan(*old, nil)
	_, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.State = StateSetUp
		r.Sections = &folderlook.Sections{Plan: "Create [music-production-home]", PlanSteps: []string{"music-production-home"}, Goal: "Release songs", Next: "Work on songs"}
		r.Setup = &Setup{Status: personalassistant.FolderSetupStopped, Stage: foldermap.StageHome, Selected: oldPlan.Selected, Lines: oldPlan.Lines}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.deps.KnownFolderRoot = func(string) (string, error) { return root, nil }
	restarted := NewService(h.svc.deps)
	report, _ := restarted.View(t.Context(), "u1", v.WorkspaceID)
	assertInbox(t, report)
	if report.Setup != nil || report.State != StateReported {
		t.Fatal("offered unsafe retry")
	}
	if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: oldPlan.Digest, Selected: oldPlan.Selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal(err)
	}
	if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest, Selected: report.Plan.Selected}); err != nil {
		t.Fatal(err)
	}
	restarted.Wait()
	done, _ := restarted.View(t.Context(), "u1", v.WorkspaceID)
	if done.GoalOffer != nil || done.NextTicket != nil || len(h.tickets.backlog) != 0 || len(done.Plan.Steps) != 1 || done.Plan.Steps[0].Kind != folderlook.StepFileCurator {
		t.Fatalf("legacy revived after completion: %+v", done)
	}
	// Another tab cannot reuse the confirmation of the now-completed run.
	if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, ErrWrongState) {
		t.Fatal(err)
	}
	if len(h.setups.runs) != 1 {
		t.Fatal("second tab dispatched")
	}
	raw, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	if raw.Sections.Goal != "" || raw.Sections.Next != "" {
		t.Fatal("old project recommendations retained as current setup")
	}
	if done.Plan.Runnable || strings.Contains(done.StageLine, "group") || strings.Contains(done.StageLine, "Home") {
		t.Fatal("completed receipt became a new structural offer")
	}
}

func TestDownloads_FutureRecordIsNeverDownWritten(t *testing.T) {
	root := demo(t)
	h := downloadsHarness(t, root)
	v := h.start(t, "")
	rec, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	rec.Version = RecordVersion + 1
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path, err := h.svc.deps.Store.path(v.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(h.svc.deps)
	if _, err := restarted.View(t.Context(), "u1", v.WorkspaceID); err == nil {
		t.Fatal("read future version")
	}
	if _, err := restarted.Keep(t.Context(), "u1", v.WorkspaceID); err == nil {
		t.Fatal("wrote future version")
	}
	if _, err := restarted.LookFurther(t.Context(), "u1", v.WorkspaceID, nil, ""); err == nil {
		t.Fatal("continued future version")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("future record changed", err)
	}
}

func TestDownloads_KeepAndThresholdCrossingLookFurtherRetainPolicyAndFacts(t *testing.T) {
	root := demo(t)
	h := downloadsHarness(t, root)
	h.svc.deps.Walk = foldermap.Options{MaxEntries: 4}
	v := h.start(t, "")
	before, _ := h.svc.Record("u1", v.WorkspaceID)
	if len(before.Facts.Unopened) == 0 {
		t.Fatal("fixture did not exercise unopened state")
	}
	if _, err := h.svc.Keep(t.Context(), "u1", v.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	kept, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	assertInbox(t, kept)
	h.svc.deps.Walk = foldermap.Options{}
	if _, err := h.svc.LookFurther(t.Context(), "u1", v.WorkspaceID, []string{"Albums"}, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	after, _ := h.svc.Record("u1", v.WorkspaceID)
	report, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	assertInbox(t, report)
	if after.Facts.Portfolio == nil || len(after.Facts.Members) <= len(before.Facts.Members) {
		t.Fatal("did not preserve newly discovered portfolio facts")
	}
	if strings.Contains(report.StageLine, "Home") || strings.Contains(report.StageLine, "group") {
		t.Fatal(report.StageLine)
	}
}

func TestDownloads_WorkspaceLabelDoesNotEstablishIdentity(t *testing.T) {
	h := newHarness(t, demo(t))
	v := h.start(t, "")
	_, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.WorkspaceName, r.FolderName = "Downloads", "Downloads"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	if report.PolicyNote != "" || report.ExploreFolder || len(report.Plan.Steps) < 2 {
		t.Fatal("label was mistaken for trusted root identity")
	}
}

type policyToolTurns struct{ sawPolicy bool }

func (p *policyToolTurns) TokenBudget() int { return 0 }
func (p *policyToolTurns) Model(_ctx context.Context, _ string, _ string) (folderlook.Chat, folderlook.ModelRef, error) {
	calls := 0
	return func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		if calls == 1 {
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "open", Name: folderlook.LookToolName, Arguments: `{"folder":"Albums"}`}}}, nil
		}
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool {
				var payload struct {
					Policy string `json:"root_policy"`
				}
				if json.Unmarshal([]byte(m.Content), &payload) == nil && strings.Contains(payload.Policy, "Downloads stays an inbox") {
					p.sawPolicy = true
				}
			}
		}
		return &llm.ChatResponse{Content: "What it is: Projects are saved here.\nPlan: Create [group] and [music-production-home]."}, nil
	}, folderlook.ModelRef{Provider: "fixture", Model: "fixture"}, nil
}
func TestDownloads_ModelLookReplyRetainsHostPolicy(t *testing.T) {
	root := demo(t)
	h := downloadsHarness(t, root)
	turns := &policyToolTurns{}
	h.svc.deps.Turns = turns
	v := h.start(t, "")
	report, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	assertInbox(t, report)
	if !turns.sawPolicy {
		t.Fatal("look reply lost root policy")
	}
}
