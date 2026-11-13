package folderagent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func knownOnly(id, root string) func(string) (string, error) {
	return func(key string) (string, error) {
		if key == id {
			return root, nil
		}
		return "", errors.New("not present")
	}
}

func TestKnownFolders_PhysicalIdentityAndMissingPeers(t *testing.T) {
	for _, tc := range []struct {
		id, name string
		policy   folderlook.RootPolicy
	}{{"desktop", "Desktop", folderlook.RootDesktop}, {"documents", "Documents", folderlook.RootDocuments}, {"downloads", "Downloads", folderlook.RootDownloads}} {
		t.Run(tc.id, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, tc.name)
			for _, path := range []string{root, filepath.Join(root, "Inside"), root + "-Old", filepath.Join(home, "other", tc.name)} {
				if err := os.MkdirAll(path, 0750); err != nil {
					t.Fatal(err)
				}
			}
			alias := filepath.Join(home, "alias")
			if err := os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			known := knownOnly(tc.id, root)
			for _, path := range []string{root, root + string(filepath.Separator), alias} {
				if got := ResolveRootIdentity(path, known); got.Policy != tc.policy || len(got.Binding) != 64 {
					t.Fatalf("identity %s = %+v", path, got)
				}
			}
			for _, path := range []string{filepath.Join(root, "Inside"), root + "-Old", filepath.Join(home, "other", tc.name)} {
				if got := ResolveRootIdentity(path, known); got.Policy != folderlook.RootOrdinary {
					t.Fatalf("recognized name or child: %+v", got)
				}
			}
			lower := strings.ToLower(root)
			if info, err := os.Stat(lower); err == nil {
				original, _ := os.Stat(root)
				got := ResolveRootIdentity(lower, known)
				if (got.Policy == tc.policy) != os.SameFile(original, info) {
					t.Fatal("lexical rather than filesystem case policy", got)
				}
			}
			if got := ResolveRootIdentity(filepath.Join(home, "gone"), known); got.Policy != folderlook.RootUnresolved {
				t.Fatal("unavailable fed root not refused", got)
			}
			if got := ResolveRootIdentity(root, knownOnly("absent", "")); got.Policy != folderlook.RootOrdinary || got.Binding == "" {
				t.Fatal("missing known root blocked an available folder", got)
			}
		})
	}
}

func TestKnownFolders_ChooseDefaultsAndReportPlans(t *testing.T) {
	for _, id := range []string{"downloads", "desktop", "documents"} {
		t.Run(id, func(t *testing.T) {
			root := demo(t)
			h := newHarness(t, root)
			h.svc.deps.KnownFolderRoot = knownOnly(id, root)
			chosen, err := h.svc.Choose(t.Context(), "u1", personalassistant.FolderChoice{Picker: true})
			if err != nil || chosen.Purpose == "" || chosen.Brief == DefaultBrief || chosen.Hire.Mandate != chosen.Brief {
				t.Fatal("missing before-hire defaults", chosen, err)
			}
			if len(h.ws.anchors) != 0 {
				t.Fatal("choosing created a workspace")
			}
			v, err := h.svc.Start(t.Context(), "u1", chosen.Token, "Custom brief", Hire{})
			if err != nil {
				t.Fatal(err)
			}
			h.svc.Wait()
			view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
			if view.Brief != "Custom brief" || view.Premise != nil || !view.ExploreFolder || len(view.Plan.Steps) != 1 {
				t.Fatal("catch-all report lost role or got a premise", view)
			}
			if view.Plan.Runnable != (id != "documents") || view.Plan.Steps[0].Chosen != (id != "documents") {
				t.Fatal("incorrect default plan", view.Plan)
			}
			if id == "documents" {
				if !strings.Contains(view.StageLine, "Documents stays a folder with its agent") {
					t.Fatal(view.StageLine)
				}
				plan, err := h.svc.Plan("u1", v.WorkspaceID, []string{"file-curator"})
				if err != nil || !plan.Runnable {
					t.Fatal("optional Janitor cannot be selected", plan, err)
				}
			}
		})
	}
}

func TestKnownFolders_WaitingProjectionAndCompletedReceipts(t *testing.T) {
	for _, id := range []string{"desktop", "documents"} {
		t.Run(id, func(t *testing.T) {
			root := demo(t)
			h := newHarness(t, root)
			v := h.start(t, "")
			old, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
			before, _ := h.svc.deps.Store.Read(v.WorkspaceID)
			h.svc.deps.KnownFolderRoot = knownOnly(id, root)
			restarted := NewService(h.svc.deps)
			view, _ := restarted.View(t.Context(), "u1", v.WorkspaceID)
			after, _ := h.svc.deps.Store.Read(v.WorkspaceID)
			if !reflect.DeepEqual(before, after) || view.RootPolicy == folderlook.RootOrdinary || len(view.Plan.Steps) != 1 {
				t.Fatal("waiting projection wrote or kept old plan")
			}
			if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: old.Plan.Digest, Selected: old.Plan.Selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
				t.Fatal("stale confirmation accepted", err)
			}
			_, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
				r.State = StateSetUp
				r.Setup = &Setup{Status: personalassistant.FolderSetupDone, Stage: before.Stage, Lines: old.Plan.Lines, Selected: old.Plan.Selected,
					Receipt: &SetupReceipt{Home: &WorkspaceRef{ID: "home", Name: "Music Home"}}}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ = h.svc.deps.Store.Read(v.WorkspaceID)
			view, _ = restarted.View(t.Context(), "u1", v.WorkspaceID)
			after, _ = h.svc.deps.Store.Read(v.WorkspaceID)
			if !reflect.DeepEqual(before, after) || view.Setup.Receipt.Home.ID != "home" || view.RootPolicy != folderlook.RootOrdinary {
				t.Fatal("changed completed organization")
			}
		})
	}
}

// Negative admission is tested before any card changes. Project evidence and
// an existing Home never lend authority to an entire catch-all folder.
func TestKnownFolders_RejectProjectSetupBeforeDispatch(t *testing.T) {
	for _, id := range []string{"desktop", "documents"} {
		for _, homeExists := range []bool{false, true} {
			t.Run(id+map[bool]string{false: "/new home", true: "/existing home"}[homeExists], func(t *testing.T) {
				root := demo(t)
				h := newHarness(t, root)
				h.svc.deps.KnownFolderRoot = knownOnly(id, root)
				// Readiness cannot introduce a Home candidate under this policy.
				ready := folderlook.Readiness{HomeExists: func(string) bool { return homeExists }}
				turns := &scriptedTurns{responses: []*llm.ChatResponse{{Content: "What it is: Songs and loose files.\nPlan: Create [music-production-home] and [group], connect [reaper-tools].\nSuggested goal: Release songs.\nNext: Work on songs."}}}
				withTurns(h, turns)
				v := h.start(t, "")
				view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
				if view.SuggestedGoal != "" || view.FirstTicket != "" || strings.Contains(turns.references[0], "[group]") || strings.Contains(turns.references[0], "[reaper-tools]") {
					t.Fatal("hostile model restored project setup", view)
				}
				rec, err := h.svc.Record("u1", v.WorkspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if candidates := folderlook.CandidatesForPolicy(rec.RootPolicy, rec.Facts, ready); len(candidates) != 1 || candidates[0].Kind != folderlook.KindFileCurator {
					t.Fatal("project candidates escaped policy", candidates)
				}
				for _, forbidden := range []string{"group", "music-production-home", "home:ori_music", "integration:ori_reaper", "reaper-tools"} {
					selected := []string{"file-curator", forbidden}
					plan, err := h.svc.Plan("u1", v.WorkspaceID, selected)
					if err != nil || plan.Runnable {
						t.Fatal("forbidden plan runnable", plan, err)
					}
					if _, err := h.svc.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: plan.Digest, Selected: selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
						t.Fatal("forged selection accepted", err)
					}
				}
				if len(h.setups.runs) != 0 || len(h.tickets.backlog) != 0 || len(h.ws.goals) != 0 {
					t.Fatal("forbidden request mutated workspace")
				}
			})
		}
	}
}
