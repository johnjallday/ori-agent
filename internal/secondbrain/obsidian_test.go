package secondbrain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type detectFixture struct {
	home, appData, data, workspaces string
	guards                          filejanitor.RootGuards
}

// newDetectFixture lays out a temporary home with Ori's data directory and
// workspace root inside it, the way a sandboxed server sees its own HOME.
func newDetectFixture(t *testing.T) detectFixture {
	t.Helper()
	// macOS temp dirs sit behind a symlink; detection returns canonical paths.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := detectFixture{
		home:       filepath.Join(base, "home"),
		appData:    filepath.Join(base, "home", "AppData", "Roaming"),
		data:       filepath.Join(base, "home", "Library", "Application Support", "OriAgent"),
		workspaces: filepath.Join(base, "home", "Ori Workspaces"),
	}
	for _, dir := range []string{f.home, f.data, f.workspaces} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	f.guards = filejanitor.RootGuards{HomeDir: f.home, DataDir: f.data, WorkspaceRoot: f.workspaces}
	return f
}

// vault makes a folder under the home; withObsidian adds Obsidian's folder.
func (f detectFixture) vault(t *testing.T, rel string, withObsidian bool) string {
	t.Helper()
	dir := filepath.Join(f.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if withObsidian {
		if err := os.MkdirAll(filepath.Join(dir, ObsidianFolder), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Inbox.md"), []byte("- [ ] one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeList writes obsidian.json where goos keeps it, in Obsidian's shape.
func (f detectFixture) writeList(t *testing.T, goos string, entries map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"vaults": entries})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRaw(t, goos, data)
}

func (f detectFixture) writeRaw(t *testing.T, goos string, data []byte) {
	t.Helper()
	path := VaultListPath(goos, f.home, f.appData)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f detectFixture) detector(goos string) *Detector {
	return &Detector{Home: f.home, AppData: f.appData, GOOS: goos, Guards: f.guards}
}

func entry(path string, ts int64) map[string]any {
	return map[string]any{"path": path, "ts": ts, "open": true}
}

func names(vaults []Vault) []string {
	out := make([]string, 0, len(vaults))
	for _, vault := range vaults {
		out = append(out, vault.Name)
	}
	return out
}

func TestVaultListPath_PerPlatform(t *testing.T) {
	home := filepath.Join("/", "Users", "sam")
	cases := []struct {
		goos, appData, want string
	}{
		{"darwin", "", filepath.Join(home, "Library", "Application Support", "obsidian", "obsidian.json")},
		{"linux", "", filepath.Join(home, ".config", "obsidian", "obsidian.json")},
		{"windows", filepath.Join("/", "Roaming"), filepath.Join("/", "Roaming", "obsidian", "obsidian.json")},
		{"windows", "", filepath.Join(home, "AppData", "Roaming", "obsidian", "obsidian.json")},
	}
	for _, tc := range cases {
		if got := VaultListPath(tc.goos, home, tc.appData); got != tc.want {
			t.Errorf("VaultListPath(%q, home, %q) = %q, want %q", tc.goos, tc.appData, got, tc.want)
		}
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if got := VaultListPath(goos, "", ""); got != "" {
			t.Errorf("VaultListPath(%q) with no home = %q, want \"\"", goos, got)
		}
	}
}

func TestDetector_ReadsEachPlatformsListUnderTheServersHome(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			f := newDetectFixture(t)
			brain := f.vault(t, "Documents/Second Brain", true)
			f.writeList(t, goos, map[string]any{"a1b2c3d4e5f60708": entry(brain, 1760000000000)})

			vaults := f.detector(goos).Vaults()
			if len(vaults) != 1 {
				t.Fatalf("vaults = %v, want one", names(vaults))
			}
			got := vaults[0]
			if got.Name != "Second Brain" || got.Path != brain {
				t.Fatalf("vault = %+v, want Second Brain at %s", got, brain)
			}
			if got.ID != SourceID(brain) || !strings.HasPrefix(got.ID, "v_") {
				t.Fatalf("ID = %q, want Ori's handle for the path, not Obsidian's key", got.ID)
			}
			if !got.LastUsed.Equal(time.UnixMilli(1760000000000)) {
				t.Fatalf("LastUsed = %v", got.LastUsed)
			}
		})
	}
}

func TestDetector_OrdersByMostRecentlyUsed(t *testing.T) {
	f := newDetectFixture(t)
	f.writeList(t, "darwin", map[string]any{
		"old":    entry(f.vault(t, "Notes/Archive", true), 1000),
		"newest": entry(f.vault(t, "Notes/Work", true), 3000),
		"middle": entry(f.vault(t, "Notes/Home", true), 2000),
	})
	got := strings.Join(names(f.detector("darwin").Vaults()), ",")
	if got != "Work,Home,Archive" {
		t.Fatalf("order = %s, want Work,Home,Archive", got)
	}
}

func TestDetector_OffersOnlyVaultsThatPassTheFolderRules(t *testing.T) {
	f := newDetectFixture(t)
	good := f.vault(t, "Documents/Good", true)
	noObsidian := f.vault(t, "Documents/Plain", false)
	insideData := filepath.Join(f.data, "Vaulted")
	if err := os.MkdirAll(filepath.Join(insideData, ObsidianFolder), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.home, "Documents", "Link to Good")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	fakeMarker := f.vault(t, "Documents/Fake Marker", false)
	if err := os.Symlink(filepath.Join(good, ObsidianFolder), filepath.Join(fakeMarker, ObsidianFolder)); err != nil {
		t.Fatal(err)
	}
	fileVault := filepath.Join(f.home, "Documents", "file.md")
	if err := os.WriteFile(fileVault, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.home, ObsidianFolder), 0o750); err != nil {
		t.Fatal(err)
	}

	f.writeList(t, "darwin", map[string]any{
		"good":        entry(good, 10),
		"no-obsidian": entry(noObsidian, 20),
		"missing":     entry(filepath.Join(f.home, "Gone"), 30),
		"relative":    entry("Documents/Good", 40),
		"in-data":     entry(insideData, 50),
		"link":        entry(link, 60),
		"fake-marker": entry(fakeMarker, 70),
		"file":        entry(fileVault, 80),
		"home":        entry(f.home, 90),
		"empty":       entry("", 100),
	})
	vaults := f.detector("darwin").Vaults()
	if len(vaults) != 1 || vaults[0].Path != good {
		t.Fatalf("vaults = %+v, want only %s", vaults, good)
	}
}

func TestDetector_KeepsOneEntryPerFolder(t *testing.T) {
	f := newDetectFixture(t)
	brain := f.vault(t, "Brain", true)
	f.writeList(t, "darwin", map[string]any{
		"first":  entry(brain, 100),
		"second": entry(brain+string(filepath.Separator), 500),
	})
	vaults := f.detector("darwin").Vaults()
	if len(vaults) != 1 || !vaults[0].LastUsed.Equal(time.UnixMilli(500)) {
		t.Fatalf("vaults = %+v, want one entry with the newer ts", vaults)
	}
}

func TestDetector_MissingOrUnparsableListMeansNoVaults(t *testing.T) {
	cases := map[string][]byte{
		"bad json":      []byte("{\"vaults\": {"),
		"not an object": []byte("[1, 2, 3]"),
		"vaults list":   []byte("{\"vaults\": [\"/Users/sam/Brain\"]}"),
		"empty":         {},
		"oversized":     []byte("{\"vaults\": {}, \"pad\": \"" + strings.Repeat("x", maxVaultListBytes) + "\"}"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDetectFixture(t)
			f.vault(t, "Brain", true)
			f.writeRaw(t, "darwin", data)
			if vaults := f.detector("darwin").Vaults(); len(vaults) != 0 {
				t.Fatalf("vaults = %v, want none", names(vaults))
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		f := newDetectFixture(t)
		if vaults := f.detector("darwin").Vaults(); len(vaults) != 0 {
			t.Fatalf("vaults = %v, want none", names(vaults))
		}
	})
	t.Run("no home", func(t *testing.T) {
		if vaults := (&Detector{GOOS: "darwin"}).Vaults(); len(vaults) != 0 {
			t.Fatalf("vaults = %v, want none", names(vaults))
		}
	})
}

func TestDetector_DropsOnlyAMalformedEntry(t *testing.T) {
	f := newDetectFixture(t)
	brain := f.vault(t, "Brain", true)
	f.writeRaw(t, "darwin", []byte(`{"vaults": {
		"bad": {"path": 42, "ts": "yesterday"},
		"good": {"path": `+mustJSON(t, brain)+`, "ts": 1760000000000.0}
	}}`))
	if got := names(f.detector("darwin").Vaults()); len(got) != 1 || got[0] != "Brain" {
		t.Fatalf("vaults = %v, want [Brain]", got)
	}
}

func TestDetector_ReusesAResultForFiveMinutes(t *testing.T) {
	f := newDetectFixture(t)
	first := f.vault(t, "First", true)
	f.writeList(t, "darwin", map[string]any{"first": entry(first, 100)})
	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	detector := f.detector("darwin")
	detector.Now = func() time.Time { return clock }

	if got := names(detector.Vaults()); len(got) != 1 {
		t.Fatalf("first detection = %v", got)
	}
	f.writeList(t, "darwin", map[string]any{
		"first":  entry(first, 100),
		"second": entry(f.vault(t, "Second", true), 200),
	})

	clock = clock.Add(DetectionTTL - time.Second)
	if got := names(detector.Vaults()); len(got) != 1 {
		t.Fatalf("within the TTL = %v, want the cached single vault", got)
	}
	if _, ok := detector.Lookup(SourceID(filepath.Join(f.home, "Second"))); ok {
		t.Fatal("Lookup found a vault the cached result does not hold")
	}

	clock = clock.Add(2 * time.Second)
	if got := strings.Join(names(detector.Vaults()), ","); got != "Second,First" {
		t.Fatalf("after the TTL = %s, want Second,First", got)
	}

	if err := os.Remove(VaultListPath("darwin", f.home, f.appData)); err != nil {
		t.Fatal(err)
	}
	detector.Forget()
	if got := names(detector.Vaults()); len(got) != 0 {
		t.Fatalf("after Forget with the list gone = %v, want none", got)
	}
}

func TestDetector_LookupFindsADetectedVaultByID(t *testing.T) {
	f := newDetectFixture(t)
	brain := f.vault(t, "Brain", true)
	f.writeList(t, "darwin", map[string]any{"x": entry(brain, 1)})
	detector := f.detector("darwin")
	vault, ok := detector.Lookup(SourceID(brain))
	if !ok || vault.Path != brain {
		t.Fatalf("Lookup = %+v, %v", vault, ok)
	}
	if _, ok := detector.Lookup("x"); ok {
		t.Fatal("Lookup accepted Obsidian's own key")
	}
	if _, ok := detector.Lookup(""); ok {
		t.Fatal("Lookup accepted an empty id")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
