package folderpath

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFind_FindsAFolderWrittenInsideASentence(t *testing.T) {
	f := newFixture(t)
	songs := filepath.Join(f.home, "Music", "REAPER Projects")
	for name, message := range map[string]string{
		"alone":                 "~/Music/REAPER Projects",
		"absolute":              songs,
		"leads the message":     "~/Music/REAPER Projects is where my songs live",
		"mid sentence":          "My songs are in ~/Music/REAPER Projects and I want to know which is newest",
		"ends a sentence":       "Can you look at ~/Music/REAPER Projects?",
		"followed by a comma":   "Look in ~/Music/REAPER Projects, then tell me what is there.",
		"in double quotes":      `Please open "~/Music/REAPER Projects" for me`,
		"in backticks":          "the folder is `~/Music/REAPER Projects`.",
		"in typographic quotes": "the folder is “~/Music/REAPER Projects” I think",
		"in brackets":           "my songs (~/Music/REAPER Projects) need sorting",
		"on its own line":       "Here it is:\n~/Music/REAPER Projects\nthanks",
		"trailing separator":    "see ~/Music/REAPER Projects/ please",
	} {
		t.Run(name, func(t *testing.T) {
			found, ok := Find(message, f.guards)
			if !ok || !found.Usable() {
				t.Fatalf("Find(%q) = %+v, %v", message, found, ok)
			}
			if found.Path != songs || found.Name != "REAPER Projects" || found.Warning != "" {
				t.Fatalf("Find(%q) = %+v, want %q", message, found.Checked, songs)
			}
			if message[found.Start:found.End] != found.Text || !strings.Contains(found.Text, "REAPER Projects") {
				t.Fatalf("range %d:%d = %q, text %q", found.Start, found.End, message[found.Start:found.End], found.Text)
			}
			if rest := found.Without(message); strings.Contains(rest, "REAPER Projects") || strings.Contains(rest, "Music/") {
				t.Fatalf("Without left the path in: %q", rest)
			}
		})
	}
}

// The longest existing folder is the one named: a parent that also exists must
// not win just because its spelling is a prefix.
func TestFind_PrefersTheLongestExistingFolder(t *testing.T) {
	f := newFixture(t)
	found, ok := Find("open ~/Music/REAPER Projects/Night Drive and list it", f.guards)
	if !ok || found.Name != "Night Drive" {
		t.Fatalf("found = %+v, %v", found, ok)
	}
	// Words after the path that name nothing are not part of it.
	found, ok = Find("open ~/Music and nothing else", f.guards)
	if !ok || found.Path != filepath.Join(f.home, "Music") || found.Text != "~/Music" {
		t.Fatalf("found = %+v, %v", found, ok)
	}
}

func TestFind_IgnoresWhatIsNotAnExistingFolderPath(t *testing.T) {
	f := newFixture(t)
	for name, message := range map[string]string{
		"no path":             "which of my songs is the newest?",
		"missing folder":      "look at ~/Music/Nope please",
		"a file, not a dir":   "read ~/Music/notes.txt",
		"relative path":       "it is in Music/REAPER Projects",
		"slash inside a word": "drums and/or bass, 4/4 time",
		"a link":              "see https://example.com/Music/REAPER Projects",
		"slash command":       "/help",
		"lone slash":          "either / or",
		"tilde without slash": "about ~20 songs",
		"empty":               "",
	} {
		t.Run(name, func(t *testing.T) {
			if found, ok := Find(message, f.guards); ok {
				t.Fatalf("Find(%q) = %+v, want nothing", message, found)
			}
		})
	}
}

func TestFind_AFolderOutsideHomeCarriesTheWarning(t *testing.T) {
	f := newFixture(t)
	found, ok := Find("my sessions are on "+f.elsewhere+" these days", f.guards)
	if !ok || !found.Usable() || found.Path != f.elsewhere || found.Warning != WarningOutsideHome {
		t.Fatalf("found = %+v, %v", found, ok)
	}
}

// A real folder Ori will not open is reported with the reason, never skipped in
// favour of its parent and never offered.
func TestFind_ARefusedFolderSaysWhy(t *testing.T) {
	f := newFixture(t)
	for name, tc := range map[string]struct{ message, reason string }{
		"keys":          {"read ~/.ssh/keys for me", "passwords or keys"},
		"keychains":     {"look at ~/Library/Keychains", "passwords or keys"},
		"ori data":      {"open " + filepath.Join(f.data, "vaults"), "Ori's own data"},
		"the home root": {"look at " + f.home + " please", ""},
	} {
		t.Run(name, func(t *testing.T) {
			found, ok := Find(tc.message, f.guards)
			if !ok || found.Usable() || found.Path != "" || found.Refusal == "" {
				t.Fatalf("Find(%q) = %+v, %v; want a refusal", tc.message, found, ok)
			}
			if !strings.Contains(found.Refusal, tc.reason) {
				t.Fatalf("refusal = %q, want it to mention %q", found.Refusal, tc.reason)
			}
		})
	}
}

// A usable folder later in the message wins over a refused one before it.
func TestFind_AUsableFolderWinsOverARefusedOne(t *testing.T) {
	f := newFixture(t)
	found, ok := Find("not ~/.ssh/keys but ~/Music/REAPER Projects", f.guards)
	if !ok || !found.Usable() || found.Name != "REAPER Projects" {
		t.Fatalf("found = %+v, %v", found, ok)
	}
}

func TestFind_IsBoundedOnAMessageFullOfSlashes(t *testing.T) {
	f := newFixture(t)
	message := strings.Repeat("/nope/nothing here at all ", 200) + "~/Music/REAPER Projects"
	// The real path is past the bound: bounded work matters more than finding it.
	if found, ok := Find(message, f.guards); ok {
		t.Fatalf("searched past the bound: %+v", found)
	}
}
