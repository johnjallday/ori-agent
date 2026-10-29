package sessionhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// A source-owner seam contract, not evidence of real inspection or creation.
// The production-wired server and browser suites cover those separately.
type softwareReviewFixture struct {
	*fakeBuildFolders
	calls   int
	inspect bool
	during  func()
}

func (f *softwareReviewFixture) ReviewSoftware(_ context.Context, _ foldercontext.Target, _, _, _, _, _ string, inspect bool) (personalassistant.FolderOfferView, error) {
	f.calls++
	f.inspect = inspect
	if f.during != nil {
		f.during()
	}
	return personalassistant.FolderOfferView{ID: "pending-code-review"}, nil
}

func TestWorkspaceBuildSoftware_ReviewRequiresCurrentEligibleSourceAndNeverAcceptsSetup(t *testing.T) {
	for _, scenario := range []string{"current", "stale", "closed", "plugin", "unknown-mode", "adjust-before-review", "changed-during-review"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBuildFixture(t, true, plannedTeammateReply)
			folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
			build := f.plannedBuild(t, folders)
			_, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
				current := doc.Session(build.ID)
				current.Folder.Recognition.Shape = "code"
				current.Folder.Recognition.IntegrationKey = ""
				if scenario == "closed" {
					current.Status = personalassistant.WorkspaceBuildAbandoned
				}
				if scenario == "plugin" {
					current.Folder.Recognition.Shape = "audio"
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			reviewer := &softwareReviewFixture{fakeBuildFolders: folders}
			f.handler.workspaceBuild.Folders = reviewer
			version := build.Version
			mode := "review"
			if scenario == "stale" {
				version--
			}
			if scenario == "unknown-mode" {
				mode = "guess"
			}
			if scenario == "adjust-before-review" {
				mode = "adjust"
			}
			if scenario == "changed-during-review" {
				reviewer.during = func() {
					_, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
						doc.Session(build.ID).Touch(f.store.Now())
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := f.provider.calls
			status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/software-review", map[string]any{"version": version, "mode": mode})
			after := f.read(t, build.ID)
			if f.provider.calls != calls || after.Setup != nil || after.CreatedWorkspaceID != "" {
				t.Fatal("review invoked inference, accepted setup or created a resource")
			}
			if scenario == "current" {
				if status != http.StatusOK || reviewer.calls != 1 || !reviewer.inspect || after.Folder.Recognition.OfferID != "pending-code-review" || after.Status != personalassistant.WorkspaceBuildOpen || after.Version <= build.Version {
					t.Fatalf("review=%d %v; build=%+v calls=%d", status, body, after, reviewer.calls)
				}
			} else {
				if status < 400 || after.Folder.Recognition.OfferID != "" {
					t.Fatalf("refusal=%d %v; source changed", status, body)
				}
				wantCalls := 0
				if scenario == "changed-during-review" {
					wantCalls = 1
				}
				if reviewer.calls != wantCalls {
					t.Fatalf("source inspection calls=%d want=%d", reviewer.calls, wantCalls)
				}
			}
		})
	}
}
