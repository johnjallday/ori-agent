package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestInboxPlacementSelection_CopiesExactScopeWithoutRescanOrLifetimeExtension(t *testing.T) {
	f, service, source := observationFixture(t)
	ctx := context.Background()
	source.ConversationID, source.DraftID = "conversation", ""
	observation, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	service.digest.deps.ScanObservation = func(string) (folderdigest.Result, error) {
		t.Fatal("handoff rescanned source")
		return folderdigest.Result{}, nil
	}
	f.now = f.now.Add(10 * time.Minute)
	copied, err := service.copyInboxPlacementHold(ctx, source, observation.ID, "build-a")
	if err != nil || copied.ID == observation.ID || !copied.ScannedAt.Equal(observation.ScannedAt) || copied.Folder != observation.Folder {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
	target := source
	target.ConversationID, target.DraftID = "", "build:build-a"
	root, err := service.ResolveRoot(ctx, target, copied.ID)
	if err != nil || root != filepath.Join(f.home, "Desktop") {
		t.Fatalf("destination root = %q, %v", root, err)
	}
	if _, err := service.ResolveRoot(ctx, source, copied.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("destination handle leaked back into source scope")
	}
	if _, err := service.ResolveRoot(ctx, target, observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("original handle gained build authority")
	}
	if _, err := service.ResolveRoot(ctx, source, observation.ID); err != nil {
		t.Fatal("copy destroyed source recovery")
	}
	copied.Tree.Nodes[0].Name = "tampered"
	replay, err := service.copyInboxPlacementHold(ctx, source, observation.ID, "build-a")
	if err != nil || replay.ID != copied.ID || replay.Tree.Nodes[0].Name == "tampered" {
		t.Fatalf("replay changed identity/evidence: %+v, %v", replay, err)
	}
	f.now = observation.ScannedAt.Add(foldercontext.SelectionTTL)
	if _, err := service.ResolveRoot(ctx, target, copied.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("copy extended lifetime")
	}
	if _, err := service.copyInboxPlacementHold(ctx, source, observation.ID, "build-a"); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("copy revived expired source")
	}
}

func TestInboxPlacementSelection_RefusesForeignChangedAndRestartedSelections(t *testing.T) {
	for _, kind := range []string{"foreign-owner", "foreign-conversation", "foreign-hq", "foreign-agent", "draft", "replaced", "restart"} {
		t.Run(kind, func(t *testing.T) {
			f, service, source := observationFixture(t)
			ctx := context.Background()
			source.ConversationID, source.DraftID = "conversation", ""
			observation, err := service.Observe(ctx, source, "chip", "desktop")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "foreign-owner":
				source.UserID = "someone-else"
			case "foreign-conversation":
				source.ConversationID = "another"
			case "foreign-hq":
				source.WorkspaceID = "another"
			case "foreign-agent":
				source.AgentName = "another"
			case "draft":
				source.ConversationID, source.DraftID = "", "staging"
			case "replaced":
				root := filepath.Join(f.home, "Desktop")
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o750); err != nil {
					t.Fatal(err)
				}
			case "restart":
				service = NewFolderObservationService(f.service)
			}
			if _, err := service.copyInboxPlacementHold(ctx, source, observation.ID, "build-a"); err == nil {
				t.Fatal("unowned or lost selection gained authority")
			}
		})
	}
}
