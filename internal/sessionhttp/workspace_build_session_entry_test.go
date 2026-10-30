package sessionhttp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type choosingBuildFolders struct {
	*fakeBuildFolders
	chosen int
}

func (f *choosingBuildFolders) ChooseSetupEntry(_ context.Context, draft foldercontext.Target, id, observationID, candidateID, name string, input personalassistant.FolderSetupEntryInput) (personalassistant.FolderOfferView, error) {
	f.chosen++
	f.plan.EntryName, f.plan.Digest = input.EntryName, strings.Repeat("b", 64)
	return personalassistant.FolderOfferView{ID: "offer-1"}, nil
}

func TestWorkspaceBuildEntry_ChoiceChangesNoDraftOrRunAndRejectsStaleVersions(t *testing.T) {
	f := newBuildFixture(t, true, plannedTeammateReply)
	folders := &choosingBuildFolders{fakeBuildFolders: &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}}
	folders.plan.EntryName, folders.plan.EntryCandidates = "Song.rpp", []string{"Song.rpp", "Alternate.rpp"}
	build := f.plannedBuild(t, folders.fakeBuildFolders)
	f.handler.workspaceBuild.Folders = folders
	calls := f.provider.calls
	url := "/api/workspaces/build-sessions/" + build.ID + "/entry"
	status, body := f.do(t, http.MethodPost, url, map[string]any{"version": build.Version, "plan_digest": songDigest, "entry_name": "Alternate.rpp"})
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	after := f.read(t, build.ID)
	if after.Setup != nil || !reflect.DeepEqual(after.Draft, build.Draft) || !reflect.DeepEqual(after.Transcript, build.Transcript) || f.provider.calls != calls || len(folders.started) != 0 {
		t.Fatal("choosing changed draft, conversation or execution", after)
	}
	plan := body["session"].(map[string]any)["plan"].(map[string]any)
	if plan["entry_name"] != "Alternate.rpp" || plan["digest"] == songDigest {
		t.Fatal("choice not returned", plan)
	}
	for _, bad := range []map[string]any{
		{"version": build.Version, "plan_digest": songDigest, "entry_name": "Song.rpp"},
		{"version": after.Version, "plan_digest": songDigest, "entry_name": "Song.rpp"},
		{"version": after.Version, "plan_digest": folders.plan.Digest, "entry_name": "Song.rpp", "open": true},
		{"version": after.Version, "plan_digest": folders.plan.Digest, "entry_name": "Song.rpp", "path": "/private"},
	} {
		if status, body := f.do(t, http.MethodPost, url, bad); status < 400 {
			t.Fatal(status, body)
		}
	}
	if folders.chosen != 1 {
		t.Fatal("stale/extra authority reached editor", folders.chosen)
	}
}

func TestWorkspaceBuildEntry_RetryUsesTheSavedRunsReboundDigest(t *testing.T) {
	f := newBuildFixture(t, true, plannedReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	if status, body := f.setUp(t, build, map[string]any{"plan_digest": songDigest}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	stopped := resolvedSongOffer(build.ID)
	stopped.Status, stopped.Outcome = personalassistant.FolderOfferAwaitingOutcome, nil
	stopped.Setup.Status, stopped.Setup.StopReason = personalassistant.FolderSetupStopped, personalassistant.FolderStopNeedsModel
	stopped.Setup.PlanDigest, stopped.Setup.EntryName = strings.Repeat("b", 64), "Alternate.rpp"
	f.handler.WorkspaceBuildSetupSettled(t.Context(), "local", stopped)
	open := f.read(t, build.ID)
	if open.Setup.PlanDigest != stopped.Setup.PlanDigest {
		t.Fatal("claim kept the pre-choice digest", open.Setup)
	}
	// Also recover an older claim after a crash before its digest was updated.
	folders.run = &personalassistant.FolderSetupView{PlanDigest: strings.Repeat("c", 64), EntryName: "Alternate.rpp", Status: personalassistant.FolderSetupStopped}
	if status, body := f.setUp(t, open, map[string]any{"plan_digest": songDigest}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	if folders.started[len(folders.started)-1].digest != folders.run.PlanDigest {
		t.Fatal("retry ignored canonical run", folders.started)
	}
}
