package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// writeFolderFiles creates each named file (relative to root) with one byte.
func writeFolderFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// observeSelected observes an arbitrary folder the way the build's typed path
// does: through a selection token the resolver turns into that folder.
func observeSelected(t *testing.T, f *folderDigestFixture, service *FolderObservationService, target foldercontext.Target, root string) *foldercontext.Observation {
	t.Helper()
	f.service.deps.ResolveSelection = func(string) (string, error) { return root, nil }
	observation, err := service.Observe(context.Background(), target, FolderObserveSelection, "token")
	if err != nil || observation == nil {
		t.Fatalf("observe %s: %+v %v", root, observation, err)
	}
	return observation
}

func TestFolderRecognition_AREAPERSongFolderIsTheProjectTheDigestWouldOffer(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	song := filepath.Join(f.home, "Music", "Ori Music Demo")
	writeFolderFiles(t, song, "Ori Music Demo.rpp", "Ori Music Demo-take2.rpp", "Ori Music Demo-mix.rpp", "Audio/vox.wav", "Audio/gtr.wav")
	var asked []string
	// Installed or not, the shape's blueprint is named by the table's own id;
	// the build decides what it can offer from its catalog.
	f.service.deps.BlueprintAvailable = func(string) bool { return false }
	f.service.deps.HomeExists = func(_ context.Context, userID, provider string) (bool, error) {
		asked = append(asked, userID+"|"+provider)
		return true, nil
	}
	observation := observeSelected(t, f, service, target, song)
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	got, err := service.Recognize(ctx, target, observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := BuildFolderRecognition{
		Verdict: BuildFolderProject, Shape: "audio", ProjectCount: 1, CandidateID: "candidate-0",
		Label: "REAPER project", MarkerNoun: "session", MarkerCount: 3,
		BlueprintID: "reaper-song", BlueprintLabel: "REAPER song",
		IntegrationKey: "ori_reaper", IntegrationName: "REAPER",
		HomeProviderKey: "music_project_management", HomeProviderName: "Music Project Management", HomeName: "Music Production Home",
		HomeExists: true,
	}
	if got == nil || *got != want {
		t.Fatalf("recognition = %+v\nwant          %+v", got, want)
	}
	if len(asked) != 1 || asked[0] != "local|music_project_management" {
		t.Fatalf("the Home is looked up for its owner and provider: %v", asked)
	}
	// Reading creates nothing: no offer, no decision, no new document version.
	after, err := f.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version || len(after.Offers) != len(before.Offers) {
		t.Fatalf("recognizing wrote the digest: before %d/%d after %d/%d %v", before.Version, len(before.Offers), after.Version, len(after.Offers), err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), f.home) {
		t.Fatalf("a recognition names no path: %s", encoded)
	}

	// A Home that cannot be read is said to be absent.
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return false, errors.New("unreadable") }
	got, err = service.Recognize(ctx, target, observation.ID)
	if err != nil || got.BlueprintID != "reaper-song" || got.HomeExists {
		t.Fatalf("degraded recognition = %+v %v", got, err)
	}

	// A Logic Pro session is an audio project no reviewed tool owns: it is
	// named by its marker and suggests no blueprint.
	logic := filepath.Join(f.home, "Music", "Bounce")
	writeFolderFiles(t, logic, "Bounce.als", "Samples/kick.wav")
	ableton := observeSelected(t, f, service, target, logic)
	got, err = service.Recognize(ctx, target, ableton.ID)
	if err != nil || got.Verdict != BuildFolderProject || got.Label != "Ableton Live set" || got.BlueprintID != "" || got.IntegrationKey != "" {
		t.Fatalf("an unowned audio project = %+v %v", got, err)
	}
}

func TestFolderRecognition_EverythingElseSaysWhatItIs(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()

	// Desktop holds two loose files: no project of a kind Ori knows, so it is
	// an inbox folder, set up as a File Janitor workspace by the host's own row.
	desktop, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	inbox := BuildFolderRecognition{
		Verdict: BuildFolderProject, Shape: "inbox", ProjectCount: 1, CandidateID: "candidate-0",
		Label: "inbox folder", BlueprintID: "file-janitor", BlueprintLabel: "File Janitor",
	}
	if got, err := service.Recognize(ctx, target, desktop.ID); err != nil || *got != inbox {
		t.Fatalf("loose files = %+v %v", got, err)
	}

	// A folder with a marker of a kind Ori knows but no blueprint for is that
	// kind, never an inbox.
	vault := filepath.Join(f.home, "Notes", "Vault")
	writeFolderFiles(t, vault, ".obsidian/app.json", "Inbox.md")
	notes := observeSelected(t, f, service, target, vault)
	if got, err := service.Recognize(ctx, target, notes.ID); err != nil || got.Verdict != BuildFolderProject || got.Shape != "notes" || got.Label != "Obsidian vault" {
		t.Fatalf("notes vault = %+v %v", got, err)
	}

	// Several songs side by side are a collection, which a build never makes
	// into one workspace.
	songs := filepath.Join(f.home, "Music", "Songs")
	writeFolderFiles(t, songs, "Night Drive/Night Drive.rpp", "Harbor Lights/Harbor Lights.rpp", "Low Tide/Low Tide.rpp")
	collection := observeSelected(t, f, service, target, songs)
	got, err := service.Recognize(ctx, target, collection.ID)
	if err != nil || got.Verdict != BuildFolderCollection || got.ProjectCount != 3 || got.CandidateID != "" || got.Label != "" {
		t.Fatalf("collection = %+v %v", got, err)
	}

	// A software project is recognized by its own host recipe.
	repo := filepath.Join(f.home, "Code", "ori-site")
	writeFolderFiles(t, repo, "go.mod", "main.go", "README.md")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	software := observeSelected(t, f, service, target, repo)
	got, err = service.Recognize(ctx, target, software.ID)
	if err != nil || got.Verdict != BuildFolderProject || got.Shape != "code" || got.Label != "software project" || got.IntegrationKey != "" || got.MarkerCount != 0 {
		t.Fatalf("software = %+v %v", got, err)
	}
}

func TestFolderRecognition_ADeclinedFolderAndALostPickAreNotRecognized(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	song := filepath.Join(f.home, "Music", "Ori Music Demo")
	writeFolderFiles(t, song, "Ori Music Demo.rpp")
	observation := observeSelected(t, f, service, target, song)

	// A folder the user said never to ask about again stays unasked here.
	if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		d.Tombstones = append(d.Tombstones, FolderTombstone{Key: FolderKey(song), Name: "Ori Music Demo", CreatedAt: f.now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Recognize(ctx, target, observation.ID); err != nil || got.Verdict != BuildFolderUnrecognized {
		t.Fatalf("tombstoned = %+v %v", got, err)
	}

	other := target
	other.DraftID = "build:other"
	if _, err := service.Recognize(ctx, other, observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("another build's pick: %v", err)
	}
	f.now = f.now.Add(foldercontext.SelectionTTL)
	if _, err := service.Recognize(ctx, target, observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("an expired pick: %v", err)
	}
}

func TestWorkspaceBuildRecognition_IsBoundedAndNewInSchemaThree(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	doc, err := store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	observation := foldercontext.Observation{
		Version: foldercontext.Version, ID: "obs-1", Folder: "Ori Music Demo", ScannedAt: *now, Entries: 1, Files: 1,
		Kinds: []foldercontext.Kind{}, Projects: []foldercontext.Project{{ID: "candidate-0", Name: "Ori Music Demo", Files: 1, Root: true}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3},
	}
	build := openBuild("song-build", *now)
	recognized := &BuildFolderRecognition{Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0", Label: "REAPER project"}
	build.Folder = &BuildFolder{Observation: observation, Recognition: recognized}
	doc.Version, doc.Sessions = 1, []WorkspaceBuildSession{build}
	encode := func(schema int) []byte {
		doc.SchemaVersion = schema
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if loaded, err := decodeWorkspaceBuild(encode(WorkspaceBuildSchemaVersion), doc.Owner); err != nil || !loaded.Open().Folder.Recognition.Recognized() {
		t.Fatalf("schema 3 keeps the recognition: %+v %v", loaded, err)
	}
	// An older document never carried one; one that does is malformed.
	for _, schema := range []int{1, 2} {
		if _, err := decodeWorkspaceBuild(encode(schema), doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("schema %d accepted a recognition: %v", schema, err)
		}
	}
	build.Folder.Recognition = nil
	doc.Sessions = []WorkspaceBuildSession{build}
	if loaded, err := decodeWorkspaceBuild(encode(2), doc.Owner); err != nil || loaded.SchemaVersion != WorkspaceBuildSchemaVersion || loaded.Open().Folder == nil {
		t.Fatalf("a genuine schema 2 build still loads: %+v %v", loaded, err)
	}

	for name, bad := range map[string]BuildFolderRecognition{
		"an undisclosed candidate":    {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-9", Label: "REAPER project"},
		"a project without a label":   {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0"},
		"a collection of one":         {Verdict: BuildFolderCollection, ProjectCount: 1},
		"an unrecognized with facts":  {Verdict: BuildFolderUnrecognized, Label: "REAPER project"},
		"an unknown verdict":          {Verdict: "maybe"},
		"a path in an id":             {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0", Label: "x", OfferID: "../offer"},
		"a negative marker count":     {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0", Label: "x", MarkerCount: -1},
		"a line break in a name":      {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0", Label: "REAPER\nproject"},
		"an oversized home name":      {Verdict: BuildFolderProject, ProjectCount: 1, CandidateID: "candidate-0", Label: "x", HomeName: strings.Repeat("h", 201)},
		"a collection naming a setup": {Verdict: BuildFolderCollection, ProjectCount: 3, OfferID: "offer-1"},
	} {
		recognition := bad
		build.Folder.Recognition = &recognition
		doc.SchemaVersion, doc.Sessions = WorkspaceBuildSchemaVersion, []WorkspaceBuildSession{build}
		if err := validateWorkspaceBuild(doc); !errors.Is(err, ErrWorkspaceBuildInvalid) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}
