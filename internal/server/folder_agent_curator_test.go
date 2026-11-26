package server

import (
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"os"
	"testing"
)

// A plan containing the Filing step finishes even though every installer,
// access writer, scanner and agent provisioner is absent from the host.
func TestFolderHireRecommendsFilingWithoutInstallingOrGranting(t *testing.T) {
	for _, folder := range []string{"Downloads", "Loose files"} {
		t.Run(folder, func(t *testing.T) {
			root := t.TempDir()
			other := t.TempDir()
			side := t.TempDir()
			store := folderagent.NewStore(func(string) (string, error) { return side, nil })
			svc := folderagent.NewService(folderagent.Deps{Store: store, Workspaces: inboxAnchor{root: root}, KnownFolderRoot: func(key string) (string, error) {
				if folder == "Downloads" && key == "downloads" {
					return root, nil
				}
				return other, nil
			}})
			facts := foldermap.Facts{Dump: true, RootLooseFiles: 36}
			rec := folderagent.Record{WorkspaceID: "hired", OwnerUserID: "local", FolderName: folder, WorkspaceName: folder, AgentName: "Manager", State: folderagent.StateReported, Stage: foldermap.StageFolder, Facts: facts, Candidates: folderlook.RequiredCandidates(facts, folderlook.Readiness{})}
			if err := store.Create(&rec); err != nil {
				t.Fatal(err)
			}
			r, err := svc.Record("local", "hired")
			if err != nil {
				t.Fatal(err)
			}
			plan := folderagent.BuildPlan(*r, []string{"file-curator"})
			var lines []personalassistant.FolderPlanLine
			res := (folderAgentSetups{builder: &ServerBuilder{folderAgentService: svc}}).Run(t.Context(), *r, plan, func(v []personalassistant.FolderPlanLine) { lines = v })
			if res.Status != personalassistant.FolderSetupDone {
				t.Fatal(res)
			}
			found := false
			for _, l := range lines {
				if l.Kind == folderagent.FolderPlanFilingAdvice && l.State == personalassistant.FolderLineDone {
					found = true
				}
				if l.Kind == folderagent.FolderPlanCapability {
					t.Fatal("reported an install", l)
				}
			}
			if !found {
				t.Fatal(lines)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatal(entries, err)
			}
		})
	}
}
