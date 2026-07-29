package reapersetup

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func reaperStep(kind, selected string) setupwizard.StepRequest {
	return setupwizard.StepRequest{
		WorkspaceID: "ws-1",
		Step: workspace.SetupWizardStep{
			ID:       kind,
			Kind:     kind,
			Required: true,
			Adapter:  SetupAdapterID,
		},
		Plugin:         "reaper-plugin",
		SelectedOption: selected,
	}
}

// evaluateWith exercises the adapter's pure translation directly. The resolver
// itself is covered by readiness_test.go; what matters here is what each status
// means to a user standing in front of the wizard.
func evaluateWith(kind string, readiness Readiness, selected string) setupwizard.StepReadiness {
	if kind == workspace.SetupStepKindPluginReadiness {
		return modeReadiness(readiness, selected)
	}
	return overallReadiness(readiness, selected)
}

// TestSetupAdapter_FileOnlyIsAFinishedAnswer is the property this blueprint's
// setup exists for: choosing the simpler mode completes setup outright. Nothing
// is installed, enabled, attached, assigned, or granted — and the user is not
// left looking at an unfinished workspace for having wanted less.
func TestSetupAdapter_FileOnlyIsAFinishedAnswer(t *testing.T) {
	// The worst case for the claim: nothing is installed at all.
	bare := Readiness{Identified: true, Status: StatusPluginMissing, LiveVerification: "not_checked"}

	mode := evaluateWith(workspace.SetupStepKindPluginReadiness, bare, ModeFileOnly)
	if !mode.Ready {
		t.Fatalf("file-only is a complete answer: %+v", mode)
	}
	overall := evaluateWith(workspace.SetupStepKindReadiness, bare, ModeFileOnly)
	if !overall.Ready {
		t.Fatalf("file-only setup is finished with no plugin at all: %+v", overall)
	}
	// And it says what it did not check, in the sentence that reports success.
	if !strings.Contains(strings.ToLower(overall.Summary), "not checked whether reaper is running") {
		t.Errorf("the file-only summary must not imply a live session was verified: %q", overall.Summary)
	}
}

// TestSetupAdapter_UnansweredModeIsTheOutstandingStep pins that the choice is
// the gate: before the user answers, setup is unfinished no matter what the
// machine happens to have installed.
func TestSetupAdapter_UnansweredModeIsTheOutstandingStep(t *testing.T) {
	fullyReady := Readiness{Identified: true, Status: StatusOriReady, LiveVerification: "not_checked"}

	mode := evaluateWith(workspace.SetupStepKindPluginReadiness, fullyReady, "")
	if mode.Ready {
		t.Fatalf("an unanswered choice cannot be satisfied: %+v", mode)
	}
	if len(mode.Options) != 2 {
		t.Fatalf("the step must offer both supported modes: %+v", mode.Options)
	}
	var ids []string
	for _, option := range mode.Options {
		ids = append(ids, option.ID)
		if option.Selected {
			t.Errorf("nothing is chosen yet, but %q reports selected", option.ID)
		}
		if option.Description == "" {
			t.Errorf("option %q must say what it does", option.ID)
		}
	}
	if strings.Join(ids, ",") != ModeFileOnly+","+ModeOriAssisted {
		t.Fatalf("options = %v", ids)
	}
	// The file-only option states what it does *not* do, where the user chooses.
	if !strings.Contains(strings.ToLower(mode.Options[0].Description), "no plugin") {
		t.Errorf("file-only must state that it installs nothing: %q", mode.Options[0].Description)
	}
}

// TestSetupAdapter_ChoosingAModeAnswersTheModeStep pins where the blocking
// happens. The mode step asks a question; answering it finishes it, even when
// the answer has prerequisites that are nowhere near satisfied. Blocking here
// instead would park the user on a step whose only control is the choice they
// already made, one step short of the controls that fix it.
func TestSetupAdapter_ChoosingAModeAnswersTheModeStep(t *testing.T) {
	bare := Readiness{Identified: true, Status: StatusPluginMissing}

	mode := evaluateWith(workspace.SetupStepKindPluginReadiness, bare, ModeOriAssisted)
	if !mode.Ready {
		t.Fatalf("choosing a mode answers the mode step: %+v", mode)
	}
	if !strings.Contains(mode.Summary, "next step") {
		t.Errorf("the step must say where the prerequisites are checked: %q", mode.Summary)
	}
	// The prerequisites are still outstanding — on the step that checks them.
	if out := evaluateWith(workspace.SetupStepKindReadiness, bare, ModeOriAssisted); out.Ready {
		t.Fatalf("setup is not finished with the plugin missing: %+v", out)
	}
}

// TestSetupAdapter_OriAssistedNeedsItsPrerequisites walks the blockers in the
// order a user hits them, and pins that a missing permission is called what it
// is rather than lumped in with "not configured".
func TestSetupAdapter_OriAssistedNeedsItsPrerequisites(t *testing.T) {
	cases := []struct {
		status   Status
		ready    bool
		category string
	}{
		{StatusPluginMissing, false, setupwizard.ErrorCategoryNotConfigured},
		{StatusPluginDisabled, false, setupwizard.ErrorCategoryNotConfigured},
		{StatusPluginDetached, false, setupwizard.ErrorCategoryNotConfigured},
		{StatusCLIAgentRequired, false, setupwizard.ErrorCategoryNotConfigured},
		{StatusNativeCLIAccessRequired, false, setupwizard.ErrorCategoryPermissionRequired},
		{StatusOriReady, true, ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			readiness := Readiness{Identified: true, Status: tc.status, LiveVerification: "not_checked"}
			check := evaluateWith(workspace.SetupStepKindReadiness, readiness, ModeOriAssisted)
			if check.Ready != tc.ready {
				t.Fatalf("ready = %v, want %v (%+v)", check.Ready, tc.ready, check)
			}
			if check.ErrorCategory != tc.category {
				t.Errorf("category = %q, want %q", check.ErrorCategory, tc.category)
			}
			if check.Summary == "" {
				t.Error("every state must name what is outstanding")
			}
		})
	}
}

// TestSetupAdapter_OriReadyNeverClaimsALiveSession is the honesty requirement in
// one assertion: the most positive thing this adapter can say still does not
// say REAPER is running.
func TestSetupAdapter_OriReadyNeverClaimsALiveSession(t *testing.T) {
	readiness := Readiness{Identified: true, Status: StatusOriReady, LiveVerification: "not_checked"}
	overall := evaluateWith(workspace.SetupStepKindSummary, readiness, ModeOriAssisted)
	if !overall.Ready {
		t.Fatalf("ori_ready with the assisted mode chosen is ready: %+v", overall)
	}
	lower := strings.ToLower(overall.Summary)
	for _, forbidden := range []string{"reaper is running", "connected to reaper", "web remote is ready"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("the summary claims a live session (%q): %q", forbidden, overall.Summary)
		}
	}
	if !strings.Contains(lower, "checked separately") {
		t.Errorf("the summary must point at the separate live check: %q", overall.Summary)
	}
}

func TestSetupAdapter_ConfirmRefusesAnUnsupportedMode(t *testing.T) {
	adapter := NewSetupAdapter(NewResolver(nil, nil))
	_, err := adapter.Confirm(
		context.Background(),
		reaperStep(workspace.SetupStepKindPluginReadiness, ""),
		setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "take_over_my_daw"},
	)
	if err == nil {
		t.Fatal("only the two supported modes may be chosen")
	}
	if !strings.Contains(err.Error(), "supported REAPER mode") {
		t.Fatalf("the refusal should name the problem: %v", err)
	}
}

func TestSetupAdapter_UnavailableResolverBlocksRatherThanPanics(t *testing.T) {
	var adapter *SetupAdapter
	readiness, err := adapter.Evaluate(context.Background(), reaperStep(workspace.SetupStepKindReadiness, ModeFileOnly))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !readiness.Blocked || readiness.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatalf("an unwired adapter blocks with a safe category: %+v", readiness)
	}
	if _, err := adapter.Confirm(context.Background(), reaperStep(workspace.SetupStepKindPluginReadiness, ""), setupwizard.StepAction{}); err == nil {
		t.Fatal("an unwired adapter must refuse to act")
	}
}

// TestSetupAdapter_SummariesNameNoProjectFile keeps the wizard's payload free of
// the user's filenames, which travel into logs and analytics.
func TestSetupAdapter_SummariesNameNoProjectFile(t *testing.T) {
	readiness := Readiness{
		Identified:  true,
		Status:      StatusOriReady,
		Explanation: "Everything is attached.",
	}
	for _, selected := range []string{ModeFileOnly, ModeOriAssisted} {
		for _, kind := range []string{workspace.SetupStepKindPluginReadiness, workspace.SetupStepKindReadiness} {
			summary := evaluateWith(kind, readiness, selected).Summary
			if strings.Contains(summary, ".rpp") || strings.Contains(summary, "/") {
				t.Errorf("%s/%s leaked a project path: %q", kind, selected, summary)
			}
		}
	}
}

// TestSetupAdapter_LaterStepsHonorTheEarlierChoice pins the join between steps.
// The mode is asked once; the steps that check its prerequisites are different
// steps with no selection of their own, and reading only their own would leave
// setup permanently stuck on "choose a mode first".
func TestSetupAdapter_LaterStepsHonorTheEarlierChoice(t *testing.T) {
	bare := Readiness{Identified: true, Status: StatusPluginMissing}

	req := reaperStep(workspace.SetupStepKindReadiness, "")
	req.Selections = map[string]string{"mode": ModeFileOnly}
	if mode := chosenMode(req); mode != ModeFileOnly {
		t.Fatalf("chosenMode = %q, want the answer recorded on the mode step", mode)
	}
	if out := overallReadiness(bare, chosenMode(req)); !out.Ready {
		t.Fatalf("a step that inherits file-only is satisfied: %+v", out)
	}

	// An unrelated recorded choice is not an answer to this question.
	other := reaperStep(workspace.SetupStepKindReadiness, "")
	other.Selections = map[string]string{"folder": "downloads"}
	if mode := chosenMode(other); mode != "" {
		t.Fatalf("chosenMode = %q, want no answer", mode)
	}
	if out := overallReadiness(bare, chosenMode(other)); out.Ready {
		t.Fatalf("nothing was chosen, so nothing is satisfied: %+v", out)
	}
}

// TestSetupAdapter_BlockersDoNotInheritTheResolversModeWording is a copy bug
// worth a test: the resolver calls a plugin-less workspace a "file-only
// project", which is the wrong thing to say to someone who just chose the
// assisted path — and it names the project file, which the wizard keeps out of
// its payload.
func TestSetupAdapter_BlockersDoNotInheritTheResolversModeWording(t *testing.T) {
	readiness := Readiness{
		Identified:  true,
		Status:      StatusPluginMissing,
		Explanation: "File-only project: the reaper-plugin is not installed. The project and its .rpp file are intact.",
	}
	summary := evaluateWith(workspace.SetupStepKindReadiness, readiness, ModeOriAssisted).Summary
	if strings.Contains(strings.ToLower(summary), "file-only") {
		t.Errorf("the assisted path must not be described as file-only: %q", summary)
	}
	if strings.Contains(summary, ".rpp") {
		t.Errorf("the summary named the project file: %q", summary)
	}
	if !strings.Contains(summary, "not installed") {
		t.Errorf("the summary must still name the blocker: %q", summary)
	}
}

// TestSetupAdapter_AnAlreadyAssistedWorkspaceNeedsNoAnswer covers the workspaces
// that existed before this wizard did. Someone who installed the plugin,
// attached it, assigned a compatible agent, and granted both permissions has
// already answered the question this step asks; making them answer it again
// would be the migration talking, not a real choice.
func TestSetupAdapter_AnAlreadyAssistedWorkspaceNeedsNoAnswer(t *testing.T) {
	fullyReady := Readiness{Identified: true, Status: StatusOriReady}
	nothing := reaperStep(workspace.SetupStepKindPluginReadiness, "")

	if mode := effectiveMode(fullyReady, nothing); mode != ModeOriAssisted {
		t.Fatalf("effectiveMode = %q, want the mode the workspace demonstrably took", mode)
	}
	if out := modeReadiness(fullyReady, effectiveMode(fullyReady, nothing)); !out.Ready {
		t.Fatalf("an already-assisted workspace is not asked to choose: %+v", out)
	}
	if out := overallReadiness(fullyReady, effectiveMode(fullyReady, nothing)); !out.Ready {
		t.Fatalf("its prerequisites are in place, so setup is finished: %+v", out)
	}

	// Everything short of that stays a real question: no plugin is equally
	// consistent with "chose file-only" and "never finished", and inferring
	// either would put words in the user's mouth.
	for _, status := range []Status{StatusPluginMissing, StatusPluginDisabled, StatusPluginDetached, StatusCLIAgentRequired, StatusNativeCLIAccessRequired} {
		readiness := Readiness{Identified: true, Status: status}
		if mode := effectiveMode(readiness, nothing); mode != "" {
			t.Errorf("%s inferred %q; only full readiness is evidence", status, mode)
		}
		if modeReadiness(readiness, effectiveMode(readiness, nothing)).Ready {
			t.Errorf("%s must still ask the user to choose", status)
		}
	}
}
