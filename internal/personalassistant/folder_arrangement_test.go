package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestFolderArrangement_OnlyHeldEvidenceNoPathResolutionOrRescan(t *testing.T) {
	f, service, target := observationFixture(t)
	for i := 0; i < 30; i++ {
		folder := filepath.Join(f.home, "Desktop", fmt.Sprintf("Song %02d", i))
		if err := os.Mkdir(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "song.rpp"), []byte("PRIVATE CONTENT"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	observation, err := service.Observe(t.Context(), target, FolderObserveChip, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	f.service.deps.ValidateRoot = func(string) (string, error) { t.Fatal("query resolved a source path"); return "", nil }
	f.service.deps.ScanObservation = func(string) (folderdigest.Result, error) { t.Fatal("query scanned"); return folderdigest.Result{}, nil }
	before, err := f.store.Read(t.Context(), target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	// The directory is now offline. Held, dated metadata remains discussable;
	// the eventual review, not a query, checks current filesystem identity.
	if err := os.Rename(filepath.Join(f.home, "Desktop"), filepath.Join(f.home, "Offline")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.ArrangementEvidence(t.Context(), target, observation.ID)
	if err != nil || len(snapshot.Items) != 31 || len(observation.Projects) != foldercontext.MaxNames {
		t.Fatalf("held tail lost: %d %v", len(snapshot.Items), err)
	}
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return service.ArrangementEvidence(ctx, target, observation.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.Overview(t.Context()); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{f.home, "PRIVATE CONTENT", "identity", "relative_folder"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("private %q leaked", forbidden)
		}
	}
	snapshot.Items[0].Tree[0].Name = "tampered"
	again, err := service.ArrangementEvidence(t.Context(), target, observation.ID)
	if err != nil || again.Items[0].Tree[0].Name == "tampered" {
		t.Fatal("query aliased held tree")
	}
	other := target
	other.DraftID = "other"
	if _, err := service.ArrangementEvidence(t.Context(), other, observation.ID); !errors.Is(err, workspacearrangement.ErrStale) {
		t.Fatal("cross-draft evidence shared")
	}
	service.BindSaved(target, observation.ID, "saved-conversation")
	if _, err := service.ArrangementEvidence(t.Context(), target, observation.ID); !errors.Is(err, workspacearrangement.ErrStale) {
		t.Fatal("consumed draft retained authority")
	}
	target.DraftID, target.ConversationID = "", "saved-conversation"
	if _, err := service.ArrangementEvidence(t.Context(), target, observation.ID); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Read(t.Context(), target.UserID)
	if err != nil || before.Version != after.Version {
		t.Fatal("evidence query mutated setup state")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ArrangementEvidence(ctx, target, observation.ID); err == nil {
		t.Fatal("cancelled query returned evidence")
	}
	f.now = f.now.Add(foldercontext.SelectionTTL)
	if _, err := service.ArrangementEvidence(context.Background(), target, observation.ID); !errors.Is(err, workspacearrangement.ErrStale) {
		t.Fatal("expired source remained live")
	}
}
