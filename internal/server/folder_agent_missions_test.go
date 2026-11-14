package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type missionFolderWorkspaces struct{ inboxAnchor }

func (missionFolderWorkspaces) IDs() ([]string, error) { return []string{"folder"}, nil }
func (missionFolderWorkspaces) Route(string) string    { return "/workspaces/folder" }

func TestFolderAgentMissions_ReportedHookIsLocalAndIdempotent(t *testing.T) {
	completions := 0
	engine := progression.New(nil, progression.WithGraph(progression.PersonalAssistantGraph()),
		progression.WithOnComplete(func(progression.Quest) { completions++ }))
	builder := &ServerBuilder{progressionEngine: engine}
	builder.completeFolderAgentMissions(t.Context(), "another-user", "folder")
	if completions != 0 {
		t.Fatal("another user's report completed local missions")
	}
	for range 2 {
		builder.completeFolderAgentMissions(t.Context(), userprofile.LocalUserID, "folder")
	}
	if completions != 2 || !engine.HasCompleted(progression.ShowFolderQuestID) || !engine.HasCompleted(progression.FolderFirstLookQuestID) {
		t.Fatalf("expected each folder mission once, got %d completions", completions)
	}
}

func TestFolderAgentMissions_RestartUsesOnlyOwnedDurableReports(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state folderagent.State
		owner string
		want  bool
	}{
		{"reported", folderagent.StateReported, userprofile.LocalUserID, true},
		{"kept", folderagent.StateKept, userprofile.LocalUserID, true},
		{"setting up", folderagent.StateSettingUp, userprofile.LocalUserID, true},
		{"set up", folderagent.StateSetUp, userprofile.LocalUserID, true},
		{"looking", folderagent.StateLooking, userprofile.LocalUserID, false},
		{"failed", folderagent.StateFailed, userprofile.LocalUserID, false},
		{"other user", folderagent.StateReported, "another-user", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := folderagent.NewStore(func(string) (string, error) { return root, nil })
			if err := store.Create(&folderagent.Record{WorkspaceID: "folder", OwnerUserID: tc.owner, State: tc.state}); err != nil {
				t.Fatal(err)
			}
			service := folderagent.NewService(folderagent.Deps{Store: store,
				Workspaces:      missionFolderWorkspaces{inboxAnchor{root: root}},
				KnownFolderRoot: func(string) (string, error) { return "", nil },
			})
			engine := progression.New(nil, progression.WithGraph(progression.PersonalAssistantGraph()))
			builder := &ServerBuilder{folderAgentService: service, progressionEngine: engine}
			builder.reconcileFolderAgentMissions(t.Context())
			for _, id := range []string{progression.ShowFolderQuestID, progression.FolderFirstLookQuestID} {
				if got := engine.HasCompleted(id); got != tc.want {
					t.Fatalf("%s completed=%v want %v", id, got, tc.want)
				}
			}
		})
	}
}
