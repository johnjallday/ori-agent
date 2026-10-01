package foldersetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// fakeInstall stands in for the host's install quest: one step that offers
// install, then enable, and is complete once both have happened.
type fakeInstall struct {
	revision  int64
	installed bool
	enabled   bool
	// shownVersion is the release the review discloses.
	shownVersion string
	pluginID     string
	commitErr    error
	unresolved   bool
	calls        *[]string
}

func newFakeInstall(log *[]string) *fakeInstall {
	return &fakeInstall{revision: 1, shownVersion: "0.9.0", pluginID: "reaper-plugin", calls: log}
}

func (f *fakeInstall) Child(context.Context, int64, string) (*setupjourney.JourneyProjection, error) {
	return nil, errors.New("the install quest has no child runs")
}

func (f *fakeInstall) Read(context.Context, string) (*setupjourney.JourneyProjection, error) {
	step := setupjourney.StepProjection{ID: "integration", Kind: specialist.SetupStepIntegrationInstall, Status: setupjourney.StepActive}
	switch {
	case f.installed && f.enabled:
		step.Status = setupjourney.StepComplete
	case f.installed:
		step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewEnable}}
	default:
		step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewInstall}}
	}
	return &setupjourney.JourneyProjection{
		RunID: "install-run", StateRevision: f.revision, Steps: []setupjourney.StepProjection{step},
		// A commit that failed leaves the quest unresolved, as the real journey does.
		Busy: f.unresolved, ReconciliationRequired: f.unresolved,
	}, nil
}

func (f *fakeInstall) Mutate(_ context.Context, _ string, action setupjourney.ActionID, _ setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	*f.calls = append(*f.calls, "install:"+string(action))
	disclosure := &setupjourney.IntegrationProjection{PluginID: f.pluginID, ExpectedVersion: f.shownVersion, InstalledVersion: f.shownVersion}
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

type fakeProvider struct {
	ready      bool
	pluginID   string
	version    string
	installErr error
	calls      *[]string
}

func (p *fakeProvider) Preview(context.Context) (ProviderPreview, error) {
	*p.calls = append(*p.calls, "provider:preview")
	return ProviderPreview{Ready: p.ready, PluginID: p.pluginID, Version: p.version}, nil
}

func (p *fakeProvider) Install(_ context.Context, version string) error {
	*p.calls = append(*p.calls, "provider:install:"+version)
	if p.installErr != nil {
		return p.installErr
	}
	p.ready = true
	return nil
}

// installConfig is a confirmed plan whose integration and provider are in the
// given states.
func installConfig(integration, provider string) Config {
	cfg := testConfig()
	cfg.Plan.Lines = append([]personalassistant.FolderPlanLine{
		{Kind: personalassistant.FolderPlanIntegration, Name: "Installs and enables the reviewed REAPER integration 0.9.0"},
		{Kind: personalassistant.FolderPlanProvider, Name: "Installs the reviewed Home provider 0.1.1"},
	}, cfg.Plan.Lines[1:]...)
	cfg.Plan.Intent = personalassistant.FolderSetupIntent{
		Integration: integration, IntegrationPlugin: "reaper-plugin", IntegrationVersion: "0.9.0",
		Provider: provider, ProviderPlugin: "music-project-management", ProviderVersion: "0.1.1",
	}
	return cfg
}

type installHarness struct {
	log      []string
	install  *fakeInstall
	provider *fakeProvider
	project  *fakeJourney
	opened   int
}

func newInstallHarness() *installHarness {
	h := &installHarness{project: newFake()}
	h.install = newFakeInstall(&h.log)
	h.provider = &fakeProvider{pluginID: "music-project-management", version: "0.1.1", calls: &h.log}
	return h
}

func (h *installHarness) run(t *testing.T, cfg Config) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{
		Install: h.install, Providers: h.provider, Selections: fakeSelections{}, Progress: progress,
		OpenProject: func(context.Context) (Journey, error) {
			h.opened++
			h.log = append(h.log, "open-project")
			return h.project, nil
		},
	}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func (h *installHarness) projectTouched() bool { return len(h.project.calls) > 0 }

func TestRunInstallsThenEnablesBeforeAnyProjectStep(t *testing.T) {
	h := newInstallHarness()
	result, progress := h.run(t, installConfig(personalassistant.FolderInstallInstall, personalassistant.FolderInstallInstall))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	want := []string{
		"install:review_install", "install:install", "install:review_enable", "install:enable",
		"provider:preview", "provider:install:0.1.1", "provider:preview", "open-project",
	}
	if len(h.log) != len(want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
	for i := range want {
		if h.log[i] != want[i] {
			t.Fatalf("log = %v, want %v", h.log, want)
		}
	}
	states := lineStates(progress.last())
	if states[personalassistant.FolderPlanIntegration] != personalassistant.FolderLineDone || states[personalassistant.FolderPlanProvider] != personalassistant.FolderLineDone {
		t.Fatalf("states = %v", states)
	}
	// The integration line was seen in progress while the download ran.
	sawWorking := false
	for _, update := range progress.updates {
		sawWorking = sawWorking || lineStates(update)[personalassistant.FolderPlanIntegration] == personalassistant.FolderLineWorking
	}
	if !sawWorking {
		t.Fatal("the integration line never showed working")
	}
}

func TestRunEnablesAnInstalledButDisabledIntegration(t *testing.T) {
	h := newInstallHarness()
	h.install.installed, h.provider.ready = true, true
	result, _ := h.run(t, installConfig(personalassistant.FolderInstallEnable, personalassistant.FolderInstallReady))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	if h.log[0] != "install:review_enable" || h.log[1] != "install:enable" {
		t.Fatalf("log = %v", h.log)
	}
}

func TestRunSkipsAnAlreadyReadyIntegrationAndProvider(t *testing.T) {
	h := newInstallHarness()
	h.install.installed, h.install.enabled, h.provider.ready = true, true, true
	result, _ := h.run(t, installConfig(personalassistant.FolderInstallReady, personalassistant.FolderInstallReady))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	for _, call := range h.log {
		if call == "install:review_install" || call == "provider:install:0.1.1" {
			t.Fatalf("a finished step was repeated: %v", h.log)
		}
	}
}

func TestRunStopsWhenTheReviewDisclosesAnotherRelease(t *testing.T) {
	cases := map[string]func(*installHarness){
		"another version": func(h *installHarness) { h.install.shownVersion = "0.9.1" },
		"another plugin":  func(h *installHarness) { h.install.pluginID = "evil-plugin" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newInstallHarness()
			mutate(h)
			result, _ := h.run(t, installConfig(personalassistant.FolderInstallInstall, personalassistant.FolderInstallInstall))
			if result.StopReason != personalassistant.FolderStopPlanChanged {
				t.Fatalf("result = %+v", result)
			}
			for _, call := range h.log {
				if call == "install:install" {
					t.Fatalf("installed something the plan did not describe: %v", h.log)
				}
			}
			if h.opened != 0 || h.projectTouched() {
				t.Fatal("nothing after a refused install may run")
			}
		})
	}
}

func TestRunStopsWhenTheIntegrationNeedsMoreThanThePlanSaid(t *testing.T) {
	h := newInstallHarness()
	// The plan promised "already installed", but the quest now offers an install.
	result, _ := h.run(t, installConfig(personalassistant.FolderInstallReady, personalassistant.FolderInstallReady))
	if result.StopReason != personalassistant.FolderStopPlanChanged {
		t.Fatalf("result = %+v", result)
	}
	for _, call := range h.log {
		if call == "install:install" || call == "install:review_install" {
			t.Fatalf("acted on an install the plan did not promise: %v", h.log)
		}
	}
}

func TestRunStopsOnAFailedDownloadAndRunsNothingAfterIt(t *testing.T) {
	h := newInstallHarness()
	h.install.commitErr = errors.New("download failed")
	result, progress := h.run(t, installConfig(personalassistant.FolderInstallInstall, personalassistant.FolderInstallInstall))
	if result.StopReason != personalassistant.FolderStopInstallFailed || result.Cause == nil {
		t.Fatalf("result = %+v", result)
	}
	if h.opened != 0 || h.projectTouched() {
		t.Fatalf("the project steps ran after a failed install: %v", h.log)
	}
	for _, call := range h.log {
		if call == "provider:install:0.1.1" {
			t.Fatal("the provider installed after the integration failed")
		}
	}
	if got := lineStates(progress.last())[personalassistant.FolderPlanIntegration]; got != personalassistant.FolderLineFailed {
		t.Fatalf("the failing line = %s, want failed", got)
	}
}

// A failed install leaves the install quest unresolved, and the journey will not
// commit again until the plugin is seen installed. The run says so instead of
// pressing on, and a retry continues once the plugin is installed another way.
func TestRunAfterAFailedInstallWaitsForThePluginInsteadOfCommittingAgain(t *testing.T) {
	h := newInstallHarness()
	h.install.commitErr = errors.New("download failed")
	h.provider.ready = true
	cfg := installConfig(personalassistant.FolderInstallInstall, personalassistant.FolderInstallReady)
	if result, _ := h.run(t, cfg); result.StopReason != personalassistant.FolderStopInstallFailed {
		t.Fatalf("first attempt = %+v", result)
	}

	// Try again with the plugin still missing: no second install is attempted.
	before := len(h.log)
	result, _ := h.run(t, cfg)
	if result.StopReason != personalassistant.FolderStopInstallFailed || result.Cause == nil {
		t.Fatalf("retry = %+v", result)
	}
	for _, call := range h.log[before:] {
		if call == "install:review_install" || call == "install:install" {
			t.Fatalf("committed again over an unresolved install: %v", h.log[before:])
		}
	}

	// The user installs it from Plugins; the step now reads complete, and Try
	// again carries on from there.
	h.install.installed, h.install.enabled = true, true
	result, _ = h.run(t, cfg)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("retry after the plugin was installed = %+v cause=%v", result, result.Cause)
	}
}

func TestRunHoldsTheHomeProviderToThePlannedRelease(t *testing.T) {
	t.Run("another release", func(t *testing.T) {
		h := newInstallHarness()
		h.install.installed, h.install.enabled = true, true
		h.provider.version = "0.2.0"
		result, _ := h.run(t, installConfig(personalassistant.FolderInstallReady, personalassistant.FolderInstallInstall))
		if result.StopReason != personalassistant.FolderStopPlanChanged {
			t.Fatalf("result = %+v", result)
		}
		for _, call := range h.log {
			if call == "provider:install:0.2.0" {
				t.Fatal("installed a release the plan did not name")
			}
		}
	})
	t.Run("a failed install", func(t *testing.T) {
		h := newInstallHarness()
		h.install.installed, h.install.enabled = true, true
		h.provider.installErr = errors.New("home upgrade required")
		result, _ := h.run(t, installConfig(personalassistant.FolderInstallReady, personalassistant.FolderInstallInstall))
		if result.StopReason != personalassistant.FolderStopInstallFailed || h.opened != 0 {
			t.Fatalf("result = %+v opened=%d", result, h.opened)
		}
	})
}

func TestRunStopsWhenTheInstalledPluginHasNoQuest(t *testing.T) {
	h := newInstallHarness()
	h.install.installed, h.install.enabled, h.provider.ready = true, true, true
	progress := &recorder{}
	runner := &Runner{
		Install: h.install, Providers: h.provider, Selections: fakeSelections{}, Progress: progress,
		OpenProject: func(context.Context) (Journey, error) { return nil, errors.New("no single quest") },
	}
	result, err := runner.Run(context.Background(), installConfig(personalassistant.FolderInstallReady, personalassistant.FolderInstallReady))
	if err != nil || result.StopReason != personalassistant.FolderStopFailed {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}
