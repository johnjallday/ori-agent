package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssistantFolderRepick_ResumesBeforeCanonicalRetirement(t *testing.T) {
	for _, move := range []string{"unchanged", "root", "child", "replacement", "different"} {
		t.Run(move, func(t *testing.T) {
			f := newDraftServerFixture(t)
			id, revision, offerID := reviewFolder(t, f, stageReviewFolder(t, f))
			root := filepath.Join(os.Getenv("HOME"), "Documents")
			switch move {
			case "root":
				if err := os.Rename(root, root+"-renamed"); err != nil {
					t.Fatal(err)
				}
				root += "-renamed"
			case "child", "replacement":
				child := filepath.Join(root, "Chosen")
				if err := os.Rename(child, child+"-original"); err != nil {
					t.Fatal(err)
				}
				if move == "replacement" {
					if err := os.Mkdir(child, 0750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(child, "notes.txt"), []byte("replacement"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "different":
				root = filepath.Join(os.Getenv("HOME"), "Other folder")
				if err := os.Mkdir(root, 0750); err != nil {
					t.Fatal(err)
				}
			}
			status, picked := f.call(t, http.MethodPost, "http://localhost/api/folder-picker/select-path", map[string]any{"path": root})
			if status != http.StatusOK {
				t.Fatalf("pick: %d %v", status, picked)
			}
			status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{
				"conversation_id": id, "revision": revision, "mode": "selection", "selection_token": picked["selection_token"],
			})
			if move == "replacement" {
				if status != http.StatusConflict || body["needs_pick_reason"] != "changed" {
					t.Fatalf("replacement accepted: %d %v", status, body)
				}
			} else if status != http.StatusOK {
				t.Fatalf("select: %d %v", status, body)
			}
			_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
			state := history["folder_context"].(map[string]any)
			if move == "different" {
				if body["resumed_review"] != nil || state["offer_id"] == offerID {
					t.Fatal("different folder inherited review")
				}
				return
			}
			if state["offer_id"] != offerID || state["revision"] != revision || len(history["messages"].([]any)) != 1 {
				t.Fatal("canonical review was retired/replaced", history)
			}
			if move != "replacement" {
				view := body["resumed_review"].(map[string]any)
				if view["id"] != offerID || view["needs_pick"] == true {
					t.Fatal("review did not resume", body)
				}
				if state["authority"] != "lost" || body["folder_context"].(map[string]any)["historical"] != true {
					t.Fatal("original snapshot became fresh")
				}
				encoded, _ := json.Marshal(body)
				if strings.Contains(string(encoded), os.Getenv("HOME")) {
					t.Fatal("private path leaked")
				}
			}
		})
	}
}
