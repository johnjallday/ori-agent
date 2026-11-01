package secondbrain

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/vaultfixture"
)

func treePaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

// TestFixtureCreated_HasTheCreatorsLayoutAndOriNotesReadBackAsOris keeps the
// fixture honest: it cannot import this package, so a drift from what the
// Creator makes, or from the marker format, fails here.
func TestFixtureCreated_HasTheCreatorsLayoutAndOriNotesReadBackAsOris(t *testing.T) {
	vault, err := vaultfixture.Build(canonicalTemp(t), vaultfixture.Created, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	made, err := Creator{}.Create(canonicalTemp(t), vault.Name, Seeds{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, rel := range treePaths(t, vault.Path) {
		have[rel] = true
	}
	for _, rel := range treePaths(t, made.Path) {
		if !have[rel] {
			t.Errorf("the Creator makes %s; the created fixture lacks it", rel)
		}
	}

	if !IsObsidianVault(vault.Path) || vault.Origin != OriginCreatedByOri {
		t.Fatalf("vault = %+v", vault)
	}
	for _, rel := range treePaths(t, vault.Path) {
		content, err := os.ReadFile(filepath.Join(vault.Path, filepath.FromSlash(rel))) // #nosec G304 -- the test's own temp tree
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(string(content), "---\nori: ") {
			if mark := ReadMark(content); !mark.OwnedByOri {
				t.Errorf("%s does not read back as Ori's: %+v", rel, mark)
			}
		}
	}
}

func TestFixtureCreated_ReadsAsDocumentedAndSeededItemsAreNeverSuggested(t *testing.T) {
	vault, err := vaultfixture.Build(canonicalTemp(t), vaultfixture.Created, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	reading, err := Read(context.Background(), vault.Path, ReadOptions{})
	if err != nil || reading.Partial {
		t.Fatalf("read: %+v, %v", reading, err)
	}
	signals := Extract(reading, fixtureNow)
	if signals.Notes != vault.Notes || len(signals.OpenLoops) != vault.OpenItems || signals.NotesWithOpen != vault.NotesWithOpen ||
		signals.Dated != vault.Dated || signals.Done != vault.Done || signals.DailyNotes != vault.DailyNotes || signals.DroppedSecrets != vault.Secrets {
		t.Fatalf("notes %d open %d withOpen %d dated %d done %d daily %d; fixture says %+v",
			signals.Notes, len(signals.OpenLoops), signals.NotesWithOpen, signals.Dated, signals.Done, signals.DailyNotes, vault)
	}

	source := createdSource(vault.Path)
	source.Kind = KindObsidian
	look := fixedLooker().Run(context.Background(), source, nil, "look-1", "").Look
	seeded, suggested := map[string]bool{}, map[string]bool{}
	for _, item := range look.Items {
		if item.Seeded {
			seeded[item.Note] = true
		}
		if item.Suggested {
			suggested[item.Text] = true
		}
	}
	// Two seeds from setup and a follow-up Ori copied later: the user has all
	// three as follow-ups already, so none is suggested back (FR3).
	if len(seeded) != 3 || !seeded["Inbox/call-the-distributor.md"] || !seeded["Inbox/renew-the-lease.md"] || !seeded["Inbox/mail-the-masters.md"] {
		t.Fatalf("seeded = %v", seeded)
	}
	if len(suggested) != 2 || !suggested["Buy strings"] || !suggested["Book the venue"] {
		t.Fatalf("suggested = %v", suggested)
	}
}
