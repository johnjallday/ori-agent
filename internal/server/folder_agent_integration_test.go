package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// fakeIntegrationQuest is the host's install quest for one reviewed
// integration: install, then enable, then complete.
type fakeIntegrationQuest struct {
	revision           int64
	installed, enabled bool
	commitErr          error
	unresolved         bool
}

func (f *fakeIntegrationQuest) Child(context.Context, int64, string) (*setupjourney.JourneyProjection, error) {
	return nil, errors.New("no child runs")
}

func (f *fakeIntegrationQuest) Read(context.Context, string) (*setupjourney.JourneyProjection, error) {
	step := setupjourney.StepProjection{ID: "integration", Kind: specialist.SetupStepIntegrationInstall, Status: setupjourney.StepActive}
	switch {
	case f.installed && f.enabled:
		step.Status = setupjourney.StepComplete
	case f.installed:
		step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewEnable}}
	default:
		step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewInstall}}
	}
	return &setupjourney.JourneyProjection{RunID: "install-run", StateRevision: f.revision,
		Steps: []setupjourney.StepProjection{step}, Busy: f.unresolved, ReconciliationRequired: f.unresolved}, nil
}

func (f *fakeIntegrationQuest) Mutate(_ context.Context, _ string, action setupjourney.ActionID, _ setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	disclosure := &setupjourney.IntegrationProjection{PluginID: "reaper-plugin", ExpectedVersion: "0.9.0", InstalledVersion: "0.9.0"}
	switch action {
	case setupjourney.ActionReviewInstall:
		return &setupjourney.ActionResult{Review: &setupjourney.ReviewProjection{Token: "t", CommitAction: setupjourney.ActionInstall, Integration: disclosure}}, nil
	case setupjourney.ActionReviewEnable:
		return &setupjourney.ActionResult{Review: &setupjourney.ReviewProjection{Token: "t", CommitAction: setupjourney.ActionEnable, Integration: disclosure}}, nil
	}
	if f.commitErr != nil {
		f.unresolved = true
		return nil, f.commitErr
	}
	if action == setupjourney.ActionInstall {
		f.installed = true
	} else {
		f.enabled = true
	}
	f.revision++
	return &setupjourney.ActionResult{}, nil
}

// integrationRun drives runFolderAgentIntegration over the Tools-stage lines
// with the same set the real runner uses, and keeps every published snapshot.
type integrationRun struct {
	lines     []personalassistant.FolderPlanLine
	snapshots [][]personalassistant.FolderPlanLine
}

func newIntegrationRun() *integrationRun {
	return &integrationRun{lines: []personalassistant.FolderPlanLine{
		{Kind: personalassistant.FolderPlanIntegration, Name: "Installs REAPER tools"},
		{Kind: personalassistant.FolderPlanWorkspace, Name: "Connects REAPER tools to One Song"},
	}}
}

func (r *integrationRun) set(kind, state, progress string) {
	for i := range r.lines {
		if r.lines[i].Kind == kind && r.lines[i].State != personalassistant.FolderLineDone {
			r.lines[i].Mark(state, progress, time.Now().UTC())
		}
	}
	r.snapshots = append(r.snapshots, append([]personalassistant.FolderPlanLine(nil), r.lines...))
}

func (r *integrationRun) progressOf(kind string) []string {
	var seen []string
	for _, snap := range r.snapshots {
		for _, line := range snap {
			if line.Kind == kind && line.State == personalassistant.FolderLineWorking &&
				(len(seen) == 0 || seen[len(seen)-1] != line.Progress) {
				seen = append(seen, line.Progress)
			}
		}
	}
	return seen
}

func reaperStep(t *testing.T, quest *fakeIntegrationQuest, state string, connectErr error) folderAgentIntegration {
	t.Helper()
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("the REAPER entry is not reviewed")
	}
	return folderAgentIntegration{
		words: integrationWords{plugin: entry.PluginName(), tools: entry.DisplayName, workspace: "One Song"},
		facts: func(context.Context) (foldersetup.Plugin, error) {
			return foldersetup.Plugin{PluginID: "reaper-plugin", State: state, Version: "0.9.0"}, nil
		},
		install: func(context.Context) (foldersetup.Journey, error) { return quest, nil },
		connect: func() error { return connectErr },
	}
}

func TestFolderAgentIntegration_EachLineShowsItsPhaseTimesAndReceipt(t *testing.T) {
	run := newIntegrationRun()
	kind, reason, err := runFolderAgentIntegration(context.Background(),
		reaperStep(t, &fakeIntegrationQuest{revision: 1}, personalassistant.FolderInstallInstall, nil), run.set)
	if err != nil {
		t.Fatalf("integration stopped at %s (%s): %v", kind, reason, err)
	}
	wantInstall := []string{
		"Checking the reviewed release",
		"Downloading Ori REAPER Plugin 0.9.0",
		"Verifying Ori REAPER Plugin 0.9.0",
		"Enabling Ori REAPER Plugin 0.9.0",
		"Verifying Ori REAPER Plugin 0.9.0",
	}
	if got := run.progressOf(personalassistant.FolderPlanIntegration); strings.Join(got, "|") != strings.Join(wantInstall, "|") {
		t.Fatalf("install progress = %q, want %q", got, wantInstall)
	}
	if got := run.progressOf(personalassistant.FolderPlanWorkspace); strings.Join(got, "|") != "Enabling it in One Song" {
		t.Fatalf("connect progress = %q", got)
	}
	install, connect := run.lines[0], run.lines[1]
	if install.State != personalassistant.FolderLineDone || install.Progress != "Installed Ori REAPER Plugin 0.9.0" {
		t.Fatalf("install line = %+v", install)
	}
	if connect.State != personalassistant.FolderLineDone || connect.Progress != "Connected REAPER tools to One Song" {
		t.Fatalf("connect line = %+v", connect)
	}
	for _, line := range run.lines {
		if line.StartedAt.IsZero() || line.FinishedAt.IsZero() || line.FinishedAt.Before(line.StartedAt) {
			t.Fatalf("line %q times = %v → %v", line.Name, line.StartedAt, line.FinishedAt)
		}
	}
	// No line is working while another one is.
	for _, snap := range run.snapshots {
		working := 0
		for _, line := range snap {
			if line.State == personalassistant.FolderLineWorking {
				working++
			}
		}
		if working > 1 {
			t.Fatalf("two lines working at once: %+v", snap)
		}
	}
	// The consent digest is unchanged by the run's words and times.
	if personalassistant.FolderPlanDigest(run.lines) != personalassistant.FolderPlanDigest(newIntegrationRun().lines) {
		t.Fatal("live words changed the plan digest")
	}
}

func TestFolderAgentIntegration_AReadyPluginInstallsNothingAndSaysSo(t *testing.T) {
	run := newIntegrationRun()
	quest := &fakeIntegrationQuest{revision: 1}
	if _, _, err := runFolderAgentIntegration(context.Background(), reaperStep(t, quest, personalassistant.FolderInstallReady, nil), run.set); err != nil {
		t.Fatalf("integration: %v", err)
	}
	if quest.installed || quest.enabled {
		t.Fatal("a ready plugin was installed again")
	}
	if run.lines[0].State != personalassistant.FolderLineDone || run.lines[0].Progress != "" {
		t.Fatalf("install line of a ready plugin = %+v", run.lines[0])
	}
}

func TestFolderAgentIntegration_AFailureMarksOnlyTheLineThatFailed(t *testing.T) {
	run := newIntegrationRun()
	quest := &fakeIntegrationQuest{revision: 1, commitErr: errors.New("download refused")}
	kind, reason, err := runFolderAgentIntegration(context.Background(), reaperStep(t, quest, personalassistant.FolderInstallInstall, nil), run.set)
	if err == nil || kind != personalassistant.FolderPlanIntegration || reason != personalassistant.FolderStopInstallFailed {
		t.Fatalf("failure = %s %s %v", kind, reason, err)
	}
	if run.lines[0].State != personalassistant.FolderLineWorking || run.lines[0].Progress != "Downloading Ori REAPER Plugin 0.9.0" {
		t.Fatalf("install line before the stop = %+v", run.lines[0])
	}
	if run.lines[1].State != "" || !run.lines[1].StartedAt.IsZero() {
		t.Fatalf("the connect line moved: %+v", run.lines[1])
	}

	run = newIntegrationRun()
	kind, _, err = runFolderAgentIntegration(context.Background(),
		reaperStep(t, &fakeIntegrationQuest{revision: 1}, personalassistant.FolderInstallInstall, errors.New("no plugin manager")), run.set)
	if err == nil || kind != personalassistant.FolderPlanWorkspace || run.lines[0].State != personalassistant.FolderLineDone {
		t.Fatalf("connect failure = %s %v lines=%+v", kind, err, run.lines)
	}
}
