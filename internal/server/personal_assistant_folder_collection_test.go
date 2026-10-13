package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedAlbumsCollection lays out, in the sandboxed Music folder, the collection
// the feature was reported on: five album folders that each hold a session file
// and its media, and one sketch folder no marker names. Every file is two weeks
// old. It returns the folder.
func seedAlbumsCollection(t *testing.T) string {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), "Music") // sandboxed by the fixture, never the user's home
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("source must stay unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().AddDate(0, 0, -14)
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for n := 1; n <= 5; n++ {
		write(fmt.Sprintf("Album-%d/Song.rpp", n))
		write(fmt.Sprintf("Album-%d/Media/drums.wav", n))
	}
	write("Album-3/Song.rpp-bak")
	write("Album-5/Alternate.rpp")
	for _, name := range []string{"ideas.txt", "sketch.mid", "bounce.wav"} {
		write("Logic Sketch/" + name)
	}
	return root
}

// assertProposedAlbums checks a structure as a browser receives it: the whole
// collection proposed as a plain group, because this server has neither the
// Home's provider nor the integration installed. Both are known from what is on
// this computer, so the answer does not depend on a release lookup.
func assertProposedAlbums(t *testing.T, structure map[string]any, root string) {
	t.Helper()
	group := structure["group"].(map[string]any)
	if group["type"] != "group" || group["name"] != "Music" || group["destination_state"] != "new" ||
		group["home_name"] != "Music Production Home" || group["availability"] != "provider_not_installed" || group["home_available"] != nil {
		t.Fatalf("group: %v", group)
	}
	members := structure["members"].([]any)
	sessions := 0
	for _, item := range members {
		member := item.(map[string]any)
		if member["included"] != true || member["name"] == "" || member["candidate_id"] == "" {
			t.Fatalf("member: %v", member)
		}
		if member["type_label"] == "REAPER session" {
			sessions++
			if member["availability"] != "integration_not_installed" || member["blueprint_id"] != nil {
				t.Fatalf("a session with nothing installed must be a plain workspace for now: %v", member)
			}
		} else if member["type_label"] != "Plain workspace" || member["name"] != "Logic Sketch" || member["availability"] != nil {
			t.Fatalf("plain member: %v", member)
		}
	}
	if len(members) != 6 || sessions != 5 || structure["projects"] != float64(5) {
		t.Fatalf("%d members, %d sessions, projects %v", len(members), sessions, structure["projects"])
	}
	notes := structure["notes"].([]any)
	if len(notes) != 2 || notes[0] != "Music Production Home is not installed; a plain group is proposed instead." ||
		notes[1] != "REAPER integration is not installed; it can be added later." {
		t.Fatalf("notes: %v", notes)
	}
	encoded, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{root, os.Getenv("HOME"), "rel_path", "identity", "marker_name"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the structure carries %q: %s", forbidden, encoded)
		}
	}
}

func TestAssistantFolderCollection_AttachAndScanCarryTheProposedStructure(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	root := seedAlbumsCollection(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())

	// Attaching in the composer: the proposal arrives with the observation,
	// before any message is sent.
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, body)
	}
	structure, ok := body["structure"].(map[string]any)
	if !ok {
		t.Fatalf("the attach response carries no structure: %v", body)
	}
	assertProposedAlbums(t, structure, root)

	// A single folder attaches exactly as before, with no structure.
	single := filepath.Join(os.Getenv("HOME"), "Desktop")
	if err := os.MkdirAll(single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(single, "todo.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "desktop"})
	if _, present := body["structure"]; status != http.StatusOK || present {
		t.Fatalf("a single folder was proposed as a group: %d %v", status, body)
	}

	// Home's Explore a folder: the scan's offer carries the same record.
	status, body = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	offer := body["offer"].(map[string]any)
	scanned, ok := offer["structure"].(map[string]any)
	if !ok {
		t.Fatalf("the scan's offer carries no structure: %v", offer)
	}
	assertProposedAlbums(t, scanned, root)
	// The Home-plus-library plan the same offer still carries says why it has
	// none, from what is on this computer, instead of a bare "unavailable".
	if offer["plan"] != nil || offer["setup_unavailable_reason"] != "home_provider_unavailable" || offer["availability"] != "provider_not_installed" {
		t.Fatalf("collection plan: reason %v, availability %v", offer["setup_unavailable_reason"], offer["availability"])
	}

	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(provider.requests) != 0 {
		t.Fatal("describing a collection created a workspace or called a model")
	}
}
