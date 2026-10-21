package secondbrain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/testutil/vaultfixture"
)

var fixtureNow = time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)

// TestFixtureVaults_ReadAndExtractAsDocumented runs the reader and the
// extractor over every fixture folder and checks the facts the fixture says a
// correct reader finds.
func TestFixtureVaults_ReadAndExtractAsDocumented(t *testing.T) {
	vaults, err := vaultfixture.BuildAll(canonicalTemp(t), fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, vault := range vaults {
		t.Run(vault.Flavour, func(t *testing.T) {
			reading, err := Read(context.Background(), vault.Path, ReadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if reading.Partial {
				t.Fatalf("partial read: %s", reading.PartialReason)
			}
			signals := Extract(reading, fixtureNow)
			if signals.Notes != vault.Notes || len(signals.OpenLoops) != vault.OpenItems || signals.NotesWithOpen != vault.NotesWithOpen ||
				signals.Dated != vault.Dated || signals.Done != vault.Done || signals.DailyNotes != vault.DailyNotes || signals.DroppedSecrets != vault.Secrets {
				t.Fatalf("notes %d open %d withOpen %d dated %d done %d daily %d secrets %d; fixture says %+v",
					signals.Notes, len(signals.OpenLoops), signals.NotesWithOpen, signals.Dated, signals.Done, signals.DailyNotes, signals.DroppedSecrets, vault)
			}
			if len(signals.Decisions) != 1 || signals.Decisions[0].Note != vault.Decision {
				t.Fatalf("decisions = %+v, want %s", signals.Decisions, vault.Decision)
			}
			if len(signals.Stale) != 1 || signals.Stale[0].Note != vault.Stale {
				t.Fatalf("stale = %+v, want %s", signals.Stale, vault.Stale)
			}
			if len(signals.People) == 0 || signals.People[0].Name != vault.TopPerson || signals.People[0].Mentions != vault.TopMentions {
				t.Fatalf("people = %+v, want %s first with %d", signals.People, vault.TopPerson, vault.TopMentions)
			}
			for _, loop := range signals.OpenLoops {
				if strings.Contains(loop.Text, "never read") || strings.Contains(loop.Text, "shell comment") {
					t.Fatalf("open loop %q comes from a skipped place", loop.Text)
				}
				if strings.Contains(loop.Text, vaultfixture.CredentialShapedValue()) {
					t.Fatalf("open loop %q holds a secret-like value", loop.Text)
				}
			}
		})
	}
}

func TestFixtureVaultList_IsFoundByTheDetector(t *testing.T) {
	base := canonicalTemp(t)
	home := filepath.Join(base, "home")
	documents := filepath.Join(home, "Documents")
	if err := os.MkdirAll(documents, 0o750); err != nil {
		t.Fatal(err)
	}
	vaults, err := vaultfixture.BuildAll(documents, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := vaultfixture.WriteVaultList(VaultListPath(runtime.GOOS, home, ""), vaults...); err != nil {
		t.Fatal(err)
	}
	detector := NewDetector(home, filejanitor.RootGuards{HomeDir: home, DataDir: filepath.Join(base, "data"), WorkspaceRoot: filepath.Join(home, "Ori Workspaces")})
	detector.AppData = ""
	got := names(detector.Vaults())
	if strings.Join(got, ",") != "Second Brain,Engineering Notes" {
		t.Fatalf("detected = %v, want Second Brain then Engineering Notes", got)
	}
}
