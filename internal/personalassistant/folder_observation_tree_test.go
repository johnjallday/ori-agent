package personalassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderObservation_TreeByteBoundAndHeldEvidenceCopies(t *testing.T) {
	f, service, target := observationFixture(t)
	root := filepath.Join(f.home, "Desktop")
	for index := 0; index < 80; index++ {
		// Genuine synthetic filenames, escaped expansively by JSON. No real user
		// state or contents are used to exercise the unchanged storage byte limit.
		name := fmt.Sprintf("%02d-%s.txt", index, strings.Repeat("<", 85))
		if err := os.WriteFile(filepath.Join(root, name), []byte("TREE_CONTENT_SENTINEL"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	observation, err := service.Observe(context.Background(), target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(observation)
	if err != nil || len(encoded) > foldercontext.MaxBytes || observation.Validate() != nil {
		t.Fatalf("invalid bounded snapshot: %d %v", len(encoded), err)
	}
	if observation.Tree == nil || len(observation.Tree.Nodes) == 0 || len(observation.Tree.Nodes) >= 64 || observation.Tree.Omitted == 0 {
		t.Fatal("byte limit did not trim/declare omitted entries", observation.Tree)
	}
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "TREE_CONTENT_SENTINEL") {
		t.Fatal("path or contents exposed")
	}
	observation.Tree.Nodes[0].Name = "tampered"
	held, err := service.Resolve(context.Background(), target, observation.ID)
	if err != nil || held.Tree.Nodes[0].Name == "tampered" {
		t.Fatal("Observe exposed mutable held tree", err)
	}
	event := foldercontext.Event{Version: 1, Observation: held, FocusIDs: []string{held.Tree.Nodes[0].ID}}
	if event.Validate() != nil {
		t.Fatal("bounded tree did not fit canonical event")
	}
}
