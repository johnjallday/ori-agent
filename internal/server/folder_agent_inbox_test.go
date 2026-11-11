package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type inboxAnchor struct {
	folderagent.Workspaces
	root string
}

func (w inboxAnchor) AnchorRoot(string) (string, error) { return w.root, nil }

func TestFolderAgentDownloads_ProductionRefusesBeforeAnyMutationDispatch(t *testing.T) {
	for _, exists := range []bool{false, true} {
		name := "new-home"
		if exists {
			name = "existing-home"
		}
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "Downloads")
			if err := os.Mkdir(root, 0o750); err != nil {
				t.Fatal(err)
			}
			sidecar := t.TempDir()
			store := folderagent.NewStore(func(string) (string, error) { return sidecar, nil })
			facts := foldermap.Facts{Shapes: []foldermap.ShapeCount{{Shape: "audio", Format: "reaper", Count: 3}}, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 3,
				HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}}
			for _, path := range []string{"One", "Two", "Three"} {
				facts.Members = append(facts.Members, foldermap.Project{Path: path, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
			}
			rec := folderagent.Record{WorkspaceID: "inbox", OwnerUserID: "u", FolderName: "Downloads", WorkspaceName: "Downloads",
				State: folderagent.StateReported, Stage: foldermap.StageHome, Facts: facts,
				Candidates: folderlook.RequiredCandidates(facts, folderlook.Readiness{HomeExists: func(string) bool { return exists }})}
			if err := store.Create(&rec); err != nil {
				t.Fatal(err)
			}
			// Build genuinely valid, anchor-bound old plans first, then change
			// known-folder identity. Empty/forged digests alone would not prove
			// that production dispatch revalidates the current root policy.
			known := t.TempDir()
			service := folderagent.NewService(folderagent.Deps{Store: store, Workspaces: inboxAnchor{root: root},
				KnownFolderRoot: func(string) (string, error) { return known, nil }})
			before, err := service.Record("u", rec.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			plans := []folderagent.Plan{
				folderagent.BuildPlan(*before, []string{"group", "reaper-tools"}),
				folderagent.BuildPlan(*before, []string{"music-production-home", "reaper-tools"}),
				folderagent.BuildPlan(*before, []string{"reaper-tools"}),
			}
			for _, plan := range plans {
				if err := service.CheckPlan(rec.WorkspaceID, plan); err != nil {
					t.Fatalf("fixture plan was not valid: %v", err)
				}
			}
			known = root
			// Every mutation dependency is deliberately nil: reaching any
			// dispatch is a failure, not a provider failing after side effects.
			b := &ServerBuilder{folderAgentService: service}
			for _, plan := range plans {
				updates := 0
				got := (folderAgentSetups{builder: b}).Run(t.Context(), rec, plan, func([]personalassistant.FolderPlanLine) { updates++ })
				if got.Status != personalassistant.FolderSetupStopped || got.StopReason != personalassistant.FolderStopPlanChanged || updates != 0 {
					t.Fatalf("result=%+v updates=%d", got, updates)
				}
			}
		})
	}
}
