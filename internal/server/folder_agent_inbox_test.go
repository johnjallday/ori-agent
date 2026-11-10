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
	root := filepath.Join(t.TempDir(), "Downloads")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	sidecar := t.TempDir()
	store := folderagent.NewStore(func(string) (string, error) { return sidecar, nil })
	rec := folderagent.Record{WorkspaceID: "inbox", OwnerUserID: "u", FolderName: "Downloads", WorkspaceName: "Downloads", State: folderagent.StateReported}
	if err := store.Create(&rec); err != nil {
		t.Fatal(err)
	}
	service := folderagent.NewService(folderagent.Deps{Store: store, Workspaces: inboxAnchor{root: root}, KnownFolderRoot: func(string) (string, error) { return root, nil }})
	// Every mutation dependency is deliberately nil: reaching any dispatch is a
	// test failure (or panic), not merely a provider install failing later.
	b := &ServerBuilder{folderAgentService: service}
	for _, plan := range []folderagent.Plan{
		{Stage: foldermap.StageGroup, Selected: []string{"group"}, Runnable: true},
		{Stage: foldermap.StageHome, Candidates: []folderlook.Candidate{{ID: "home:music", Kind: folderlook.KindHomeProvider}}, Runnable: true},
		{Stage: foldermap.StageTools, Candidates: []folderlook.Candidate{{ID: "integration:ori_reaper", Kind: folderlook.KindIntegration}}, Runnable: true},
	} {
		updates := 0
		got := (folderAgentSetups{builder: b}).Run(t.Context(), rec, plan, func([]personalassistant.FolderPlanLine) { updates++ })
		if got.Status != personalassistant.FolderSetupStopped || got.StopReason != personalassistant.FolderStopPlanChanged || updates != 0 {
			t.Fatalf("result=%+v updates=%d", got, updates)
		}
	}
}
