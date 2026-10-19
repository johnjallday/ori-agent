package personalassistant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// seedAlbums lays out the collection the feature was reported on: five album
// folders, each a REAPER session with its media, and one sketch folder that no
// marker names. Every file is two weeks old.
func seedAlbums(t *testing.T, root string) {
	t.Helper()
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
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
}

func scanAlbums(t *testing.T) (folderdigest.Result, folderdigest.Verdict, time.Time) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Albums")
	seedAlbums(t, root)
	now := time.Now()
	result, err := folderdigest.Scan(root, folderdigest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result, folderdigest.Decide(result, now), now
}

func TestBuildFolderStructure_ACollectionIsAGroupWithAWorkspacePerSubfolder(t *testing.T) {
	result, verdict, now := scanAlbums(t)
	plain := BuildFolderStructure(result, verdict, now, false)
	if plain == nil {
		t.Fatal("a folder of five projects was not described as a collection")
	}
	if err := plain.Validate(); err != nil {
		t.Fatal(err)
	}
	if plain.Group.Type != FolderGroupPlain || plain.Group.Name != "Albums" || plain.Group.DestinationState != "new" ||
		plain.Group.HomeName != "Music Production Home" || plain.Group.HomeAvailable {
		t.Fatalf("plain group: %+v", plain.Group)
	}
	sessions, others := 0, 0
	for _, member := range plain.Members {
		if !member.Included || member.Files < 1 || member.LastChanged == "" || member.Name != member.Folder || member.RelPath == "" {
			t.Fatalf("member: %+v", member)
		}
		switch member.TypeLabel {
		case "REAPER session":
			sessions++
			if !member.Project || member.Shape != string(folderdigest.ShapeAudio) || member.MarkerName != "*.rpp" {
				t.Fatalf("session member: %+v", member)
			}
		case "Plain workspace":
			others++
			if member.Project || member.Name != "Logic Sketch" {
				t.Fatalf("plain member: %+v", member)
			}
		default:
			t.Fatalf("unexpected type label %q", member.TypeLabel)
		}
	}
	if len(plain.Members) != 6 || sessions != 5 || others != 1 || plain.Projects != 5 || plain.Omitted != 0 || plain.MinMembers != 2 {
		t.Fatalf("members: %d sessions, %d others, %+v", sessions, others, plain)
	}

	home := BuildFolderStructure(result, verdict, now, true)
	if home == nil || home.Validate() != nil || home.Group.Type != FolderGroupHome ||
		home.Group.Name != "Music Production Home" || !home.Group.HomeAvailable || len(home.Members) != 6 {
		t.Fatalf("home group: %+v", home)
	}
}

func TestBuildFolderStructure_TheViewIsPathFreeAndSaysEachUnavailabilityOnce(t *testing.T) {
	result, verdict, now := scanAlbums(t)
	structure := BuildFolderStructure(result, verdict, now, false)
	structure.Group.Availability = FolderAvailabilityProviderUnverified
	for i := range structure.Members {
		if structure.Members[i].Project {
			structure.Members[i].Integration = "REAPER"
			structure.Members[i].Availability = FolderAvailabilityIntegrationNotInstalled
		}
	}
	view := structure.view()
	want := []string{
		"Music Production Home could not be verified right now; a plain group is proposed instead.",
		"REAPER integration is not installed; it can be added later.",
	}
	if len(view.Notes) != len(want) || view.Notes[0] != want[0] || view.Notes[1] != want[1] {
		t.Fatalf("notes: %q", view.Notes)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{result.Root, filepath.Dir(result.Root), "rel_path", "marker_name", "dominant_ext", "identity"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the view carries %q: %s", forbidden, encoded)
		}
	}
	// The stored record keeps what the run needs and none of the display copy.
	stored := structure.stored()
	if stored.Members[0].RelPath == "" || len(stored.Notes) != 0 || len(structure.Notes) != 0 {
		t.Fatalf("stored record: %+v", stored)
	}
	// An unchecked member's note is not shown.
	for i := range structure.Members {
		if structure.Members[i].Project {
			structure.Members[i].Included = false
		}
	}
	if notes := structure.view().Notes; len(notes) != 1 || notes[0] != want[0] {
		t.Fatalf("notes after unchecking: %q", notes)
	}
}

func TestBuildFolderStructure_OnlyAFolderOfSeveralProjectsIsACollection(t *testing.T) {
	write := func(t *testing.T, root, rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]struct {
		files      []string
		collection bool
	}{
		"one project and a leftover folder": {files: []string{"Song/Song.rpp", "Notes/ideas.txt"}},
		"a session folder with its backups": {files: []string{"Song.rpp", "Old/v1.rpp", "Alt/alt.rpp"}},
		"a repository with packages":        {files: []string{"go.mod", "api/go.mod", "web/package.json"}},
		"plain folders only":                {files: []string{"A/a.txt", "B/b.txt", "C/c.txt"}},
		"two projects":                      {files: []string{"One/One.rpp", "Two/Two.rpp"}, collection: true},
		"projects of different kinds":       {files: []string{"Song/Song.rpp", "Thesis/main.tex", "Notes/ideas.txt"}, collection: true},
		// Software projects are set up one at a time, each with its own review.
		"a folder of repositories":      {files: []string{"api/go.mod", "web/package.json", "cli/Cargo.toml"}},
		"one project and a repository":  {files: []string{"Song/Song.rpp", "Site/package.json"}},
		"two projects and a repository": {files: []string{"One/One.rpp", "Two/Two.rpp", "Site/package.json"}, collection: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "Root")
			for _, file := range tc.files {
				write(t, root, file)
			}
			result, err := folderdigest.Scan(root, folderdigest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			structure := BuildFolderStructure(result, folderdigest.Decide(result, now), now, true)
			if (structure != nil) != tc.collection {
				t.Fatalf("collection = %v, want %v: %+v", structure != nil, tc.collection, structure)
			}
			if structure == nil {
				return
			}
			// Fewer than five same-kind projects is no portfolio, so no Home.
			if structure.Validate() != nil || structure.Group.Type != FolderGroupPlain || structure.Group.Name != "Root" || structure.Group.HomeName != "" {
				t.Fatalf("structure: %+v", structure)
			}
		})
	}
}

func TestBuildFolderStructure_BoundsTheMemberListAndKeepsProjectsFirst(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Everything")
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Thirty plain folders sort before the three projects.
	for n := range 30 {
		write(fmt.Sprintf("Archive-%02d/note.txt", n))
	}
	for n := range 3 {
		write(fmt.Sprintf("Song-%d/Song.rpp", n))
	}
	write("Empty/.keep")
	result, err := folderdigest.Scan(root, folderdigest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	structure := BuildFolderStructure(result, folderdigest.Decide(result, now), now, false)
	if structure == nil || structure.Validate() != nil {
		t.Fatalf("structure: %+v", structure)
	}
	if len(structure.Members) != FolderStructureMaxMembers || structure.Projects != 3 || structure.Omitted != 9 {
		t.Fatalf("bounded list: %d members, %d projects, %d omitted", len(structure.Members), structure.Projects, structure.Omitted)
	}
	if structure.Member("candidate-0") != nil {
		t.Fatal("the root was listed as a member")
	}
	for _, member := range structure.Members {
		if member.Folder == "Empty" {
			t.Fatal("a folder with no files was listed as a member")
		}
	}
}

func TestFolderAvailabilityCopy_OneSentencePerReason(t *testing.T) {
	cases := []struct{ reason, subject, want string }{
		{FolderAvailabilityIntegrationNotInstalled, "REAPER", "REAPER integration is not installed; it can be added later."},
		{FolderAvailabilityProviderNotInstalled, "Music Production Home", "Music Production Home is not installed; a plain group is proposed instead."},
		{FolderAvailabilityProviderUnverified, "Music Production Home", "Music Production Home could not be verified right now; a plain group is proposed instead."},
		// An earlier decline is said on Home's card with Show, never as a note.
		{FolderAvailabilityDeclinedEarlier, "REAPER", ""},
		{FolderAvailabilityPlanUnavailable, "REAPER", "REAPER setup is unavailable right now; it can be added later."},
		{FolderAvailabilityPlanUnavailable, "", "One-click setup is unavailable right now; it can be added later."},
		{"", "REAPER", ""},
		{"something_else", "REAPER", ""},
	}
	for _, tc := range cases {
		if got := FolderAvailabilityCopy(tc.reason, tc.subject); got != tc.want {
			t.Fatalf("%s/%s: %q", tc.reason, tc.subject, got)
		}
	}
}

func TestFolderStructure_ValidateRefusesPathsAndUnknownValues(t *testing.T) {
	result, verdict, now := scanAlbums(t)
	base := BuildFolderStructure(result, verdict, now, false)
	mutations := map[string]func(*FolderStructure){
		"a path as the group name":      func(s *FolderStructure) { s.Group.Name = "/Users/riley/Albums" },
		"an unknown group type":         func(s *FolderStructure) { s.Group.Type = "library" },
		"a Home that cannot be chosen":  func(s *FolderStructure) { s.Group.Type = FolderGroupHome },
		"a plain group that exists":     func(s *FolderStructure) { s.Group.DestinationState = "existing" },
		"an unknown availability":       func(s *FolderStructure) { s.Members[0].Availability = "broken" },
		"a path as a member name":       func(s *FolderStructure) { s.Members[0].Name = "Albums/Album-1" },
		"a nested member folder":        func(s *FolderStructure) { s.Members[0].RelPath = "Albums/Album-1" },
		"a repeated candidate":          func(s *FolderStructure) { s.Members[1].CandidateID = s.Members[0].CandidateID },
		"a date with a time":            func(s *FolderStructure) { s.Members[0].LastChanged = "2026-09-29T10:00:00Z" },
		"too few members":               func(s *FolderStructure) { s.Members = s.Members[:1] },
		"more projects than members":    func(s *FolderStructure) { s.Projects = len(s.Members) + 1 },
		"another recognition threshold": func(s *FolderStructure) { s.MinMembers = 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			structure := base.clone()
			mutate(structure)
			if structure.Validate() == nil {
				t.Fatal("an invalid structure was accepted")
			}
		})
	}
}
