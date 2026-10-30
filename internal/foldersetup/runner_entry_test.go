package foldersetup

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

type entryReviewJourney struct {
	*fakeJourney
	rejectNamed bool
	reviews     []string
}

func (j *entryReviewJourney) Mutate(ctx context.Context, id string, action setupjourney.ActionID, mutation setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	if action == setupjourney.ActionReviewExistingProject {
		var request projectconnection.Request
		if err := json.Unmarshal(mutation.Input, &request); err != nil {
			return nil, err
		}
		j.reviews = append(j.reviews, request.EntryName)
		if j.rejectNamed && request.EntryName != "" {
			return nil, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, j.revision)
		}
	}
	return j.fakeJourney.Mutate(ctx, id, action, mutation)
}

func TestRunProjectEntry_ConfirmsOnlyTheChosenFileOrAsksAgain(t *testing.T) {
	for _, test := range []struct {
		name, shown, reason string
		candidates          []string
		rejectNamed         bool
	}{
		{name: "preselected", shown: "My Song.rpp", candidates: []string{"My Song.rpp", "Alternate.rpp"}},
		{name: "another file added keeps the selection", shown: "My Song.rpp", candidates: []string{"New.rpp", "My Song.rpp", "Alternate.rpp"}},
		{name: "removed selection", candidates: []string{"Alternate.rpp"}, reason: personalassistant.FolderStopNeedsChoice},
		{name: "missing selection rejected by real adapter contract", shown: "Alternate.rpp", candidates: []string{"Alternate.rpp"}, rejectNamed: true, reason: personalassistant.FolderStopNeedsChoice},
		{name: "unrelated invalid request is not a file question", shown: "My Song.rpp", candidates: []string{"My Song.rpp", "Alternate.rpp"}, rejectNamed: true, reason: personalassistant.FolderStopFailed},
		{name: "different default cannot override a listed choice", shown: "Alternate.rpp", candidates: []string{"My Song.rpp", "Alternate.rpp"}, reason: personalassistant.FolderStopPlanChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			journey := &entryReviewJourney{fakeJourney: newFake(), rejectNamed: test.rejectNamed}
			journey.shownEntry, journey.candidates = test.shown, test.candidates
			progress := &recorder{}
			runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: progress}
			cfg := testConfig()
			cfg.EntryName = "My Song.rpp"
			cfg.Plan = personalassistant.WithFolderSetupEntry(cfg.Plan, cfg.EntryName, []string{"My Song.rpp", "Alternate.rpp"})
			result, err := runner.Run(t.Context(), cfg)
			if err != nil || result.StopReason != test.reason {
				t.Fatal(result, err)
			}
			if test.rejectNamed && !reflect.DeepEqual(journey.reviews, []string{"My Song.rpp", ""}) {
				t.Fatal("fallback did not read a fresh unselected review", journey.reviews)
			}
			if test.reason != "" {
				for _, call := range journey.calls {
					if call == "connect_existing_project" {
						t.Fatal("committed a changed file")
					}
				}
				if test.reason == personalassistant.FolderStopNeedsChoice && !reflect.DeepEqual(progress.last().EntryCandidates, test.candidates) {
					t.Fatal("missing fresh question", progress.last())
				}
				return
			}
			if result.Status != personalassistant.FolderSetupDone || progress.last().EntryName != cfg.EntryName {
				t.Fatal("selected file lost", result, progress.last())
			}
			for _, request := range journey.projectRequests {
				if request.EntryName != cfg.EntryName {
					t.Fatal("review/commit changed file", request)
				}
			}
			found := false
			for _, line := range progress.last().Lines {
				if line.Name == "Opens My Song.rpp" && line.State == personalassistant.FolderLineDone {
					found = true
				}
			}
			if !found {
				t.Fatal("finished lines omitted the file", progress.last())
			}
		})
	}
}

type removedEntryAdapter struct {
	stepAdapter
	seen []string
}

func (a *removedEntryAdapter) Review(ctx context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, input json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	var request projectconnection.Request
	if err := json.Unmarshal(input, &request); err != nil {
		return setupjourney.ActionReviewMaterial{}, err
	}
	a.seen = append(a.seen, request.EntryName)
	if request.EntryName == "Gone.rpp" {
		return setupjourney.ActionReviewMaterial{}, setupjourney.ErrInvalid
	}
	material, err := a.stepAdapter.Review(ctx, scope, action, input)
	if err == nil {
		material.ProjectConnection.EntryName, material.ProjectConnection.EntryCandidates = "Remaining.rpp", []string{"Remaining.rpp"}
	}
	return material, err
}

func TestRunProjectEntry_RealJourneyReturnsFreshChoiceAfterItsTypedInputFailure(t *testing.T) {
	owner := &owners{}
	adapter := &removedEntryAdapter{stepAdapter: stepAdapter{owners: owner, kind: specialist.SetupStepProjectConnect}}
	service := contractServiceWith(t, owner, map[specialist.SetupStepKind]contractStep{specialist.SetupStepProjectConnect: adapter})
	progress := &recorder{}
	runner := &Runner{Journey: contractJourney{service}, Selections: fakeSelections{}, Progress: progress}
	cfg := testConfig()
	cfg.EntryName = "Gone.rpp"
	cfg.Plan = personalassistant.WithFolderSetupEntry(cfg.Plan, cfg.EntryName, nil)
	result, err := runner.Run(t.Context(), cfg)
	if err != nil || result.StopReason != personalassistant.FolderStopNeedsChoice || !reflect.DeepEqual(adapter.seen, []string{"Gone.rpp", ""}) {
		t.Fatal(result, err, adapter.seen)
	}
	if owner.project || len(owner.commits) != 0 || !reflect.DeepEqual(progress.last().EntryCandidates, []string{"Remaining.rpp"}) {
		t.Fatal("fallback committed or lost the fresh choice", owner, progress.last())
	}
}

func TestRunProjectEntry_LegacySingleFileRecordsTheCommittedName(t *testing.T) {
	result, progress := runWith(t, newFake(), fakeSelections{})
	if result.Status != personalassistant.FolderSetupDone || progress.last().EntryName != "My Song.rpp" {
		t.Fatal(result, progress.last())
	}
}
