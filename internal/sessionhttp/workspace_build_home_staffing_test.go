package sessionhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type buildHomeStaffingFixture struct {
	*fakeBuildFolders
	handler *Handler
	inputs  []personalassistant.FolderHomeStaffingInput
	during  func(context.Context)
}

func (f *buildHomeStaffingFixture) ReviewHomeStaffing(ctx context.Context, draft foldercontext.Target, id, observation, candidate, name string, input personalassistant.FolderHomeStaffingInput) (personalassistant.FolderOfferView, error) {
	f.inputs = append(f.inputs, input)
	target := personalassistant.BuildReviewTarget(draft, id)
	offer := personalassistant.FolderOffer{ID: "staffing-offer", Status: personalassistant.FolderOfferPending,
		ConversationReview: &personalassistant.FolderConversationReview{Target: target, ObservationID: observation, CandidateID: candidate, WorkspaceName: name}}
	if f.during != nil {
		f.during(ctx)
	}
	if err := f.handler.workspaceBuildReviewGuard(ctx, offer); err != nil {
		return personalassistant.FolderOfferView{}, err
	}
	return personalassistant.FolderOfferView{ID: offer.ID, Status: offer.Status}, nil
}

func TestWorkspaceBuildHomeStaffingSetupActionIsDraftOnlyAndDoesNotRequireRankingReadiness(t *testing.T) {
	f := newBuildFixture(t, true, plannedTeammateReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	owner := &buildHomeStaffingFixture{fakeBuildFolders: folders, handler: f.handler}
	f.handler.workspaceBuild.Folders = owner
	f.handler.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
		t.Fatal("manual action resolved a ranking model")
		return WorkspaceBuildModel{}, nil
	}
	calls := f.provider.calls
	for _, action := range []string{"prepare_staffing", "choose_staffing", "clear_staffing"} {
		input := map[string]any{"request_id": action, "action": action}
		if action == "choose_staffing" {
			input["context_digest"] = strings.Repeat("c", 64)
			input["choice"] = map[string]any{"role_id": "manager", "mode": "assign", "name": "Aster"}
		}
		status, body := f.setUp(t, build, map[string]any{"home_staffing": input})
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", action, status, body)
		}
		build = sessionOf(t, body)
		if build.Setup != nil || build.Status != personalassistant.WorkspaceBuildOpen || build.Folder.Recognition.OfferID != "staffing-offer" {
			t.Fatal("draft action claimed or started setup")
		}
	}
	if len(owner.inputs) != 3 || len(folders.started) != 0 || len(folders.reviewed) != 0 || f.provider.calls != calls {
		t.Fatal("draft action entered execution or ranking")
	}
	for name, body := range map[string]map[string]any{
		"null":         {"home_staffing": nil},
		"mixed":        {"plan_digest": songDigest, "home_staffing": map[string]any{"action": "recommend_staffing"}},
		"source proof": {"home_staffing": map[string]any{"action": "choose_staffing", "choice": map[string]any{"role_id": "manager", "mode": "assign", "name": "Aster", "source_witness": "private"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if status, _ := f.setUp(t, build, body); status != http.StatusBadRequest {
				t.Fatal(status)
			}
		})
	}
	if len(owner.inputs) != 3 || len(folders.started) != 0 {
		t.Fatal("invalid staffing input downgraded into execution")
	}
}

func TestWorkspaceBuildHomeStaffingOriginalBuildFenceRefusesLateChangeWithoutHoldingLease(t *testing.T) {
	f := newBuildFixture(t, true, plannedTeammateReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	owner := &buildHomeStaffingFixture{fakeBuildFolders: folders, handler: f.handler}
	f.handler.workspaceBuild.Folders = owner
	owner.during = func(ctx context.Context) {
		target := personalassistant.BuildReviewTarget(buildFolderTarget("local", f.assistant, build.ID), build.ID)
		release, ok := f.handler.buildReviews.enter(target)
		if !ok {
			t.Fatal("model boundary held the build review lease")
		}
		release()
		_, err := f.store.Mutate(ctx, "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
			current := doc.Session(build.ID)
			current.Draft.Name = "Explicit newer name"
			current.Touch(f.store.Now())
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	status, _ := f.setUp(t, build, map[string]any{"home_staffing": map[string]any{"request_id": "late", "action": "recommend_staffing", "context_digest": strings.Repeat("c", 64)}})
	current := f.read(t, build.ID)
	if status != http.StatusConflict || current.Draft.Name != "Explicit newer name" || current.Setup != nil || current.Folder.Recognition.OfferID != "" || len(folders.started) != 0 {
		t.Fatal("late staffing action changed/claimed the newer build", status)
	}
}
