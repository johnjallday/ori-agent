package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestResetPreviewUsesLateBoundOwnersAndCapturedInstallation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	builder := &ServerBuilder{resetHandler: settingshttp.NewResetHandler(nil, nil, p.DataDir)}
	resolve := builder.resetPreviewOwners
	if early := resolve(); early.Config != nil || early.Database != nil {
		t.Fatal("missing owners were reconstructed")
	}
	builder.configManager = config.NewManagerWithSecretStore(filepath.Join(p.WorkDir, "actual-settings.json"), f.Secrets())
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	requireResetNoError(t, err)
	builder.sessionStore = session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() { requireResetNoError(t, builder.sessionStore.Close()) })
	// A later environment change must not substitute another installation for
	// the data directory already attached to this handler/runtime.
	t.Setenv("ORI_DATA_DIR", p.WorkDir)
	owners := resolve()
	if owners.DataDir != p.DataDir || owners.Config != builder.configManager || owners.Database != db {
		t.Fatal("preview did not use the actual late-bound owners")
	}
	if owners.CheckLifecycle != nil {
		t.Fatal("builder advertised unsupported restart admission")
	}
}

// A usage file left in $HOME/.ori-agent by a pre-v0.0.111 build is named in
// the preview and left alone; it no longer stops Start Fresh.
func TestStartFreshKeepsLegacyHomeUsageAndNamesIt(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "Library", "Application Support", "OriAgent")
	legacy := filepath.Join(home, ".ori-agent", "usage_data", "usage_records.json")

	if kept := keptLegacyUsage(home, dataDir); len(kept) != 0 {
		t.Fatalf("no legacy file, kept = %+v", kept)
	}
	requireResetNoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
	requireResetNoError(t, os.WriteFile(legacy, []byte(`[]`), 0o600))

	kept := keptLegacyUsage(home, dataDir)
	if len(kept) != 1 || kept[0].Category != settingsreset.CategoryActivity || kept[0].Location.DisplayPath != legacy {
		t.Fatalf("legacy usage kept = %+v", kept)
	}
	if !strings.Contains(kept[0].Location.Reason, "Start Fresh leaves") {
		t.Fatalf("reason does not say the file is left in place: %q", kept[0].Location.Reason)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("inspection touched the legacy file: %v", err)
	}
	// Where the data dir is $HOME/.ori-agent, that file is the live usage
	// store, removed as usage_records, so it is not disclosed as kept.
	if kept := keptLegacyUsage(home, filepath.Join(home, ".ori-agent")); len(kept) != 0 {
		t.Fatalf("live usage file disclosed as legacy: %+v", kept)
	}
}

// Generated ori-ws-* Codex profiles are counted and named by their exact
// glob; every other file in the Codex folder is ignored and nothing is removed.
func TestStartFreshKeepsCodexProfilesAndNamesThem(t *testing.T) {
	codexHome := t.TempDir()
	if kept := keptCodexProfiles(filepath.Join(codexHome, "missing")); len(kept) != 0 {
		t.Fatalf("missing Codex folder, kept = %+v", kept)
	}
	for _, name := range []string{"auth.json", "config.toml", "ori-ws-notes.toml"} {
		requireResetNoError(t, os.WriteFile(filepath.Join(codexHome, name), []byte("x"), 0o600))
	}
	if kept := keptCodexProfiles(codexHome); len(kept) != 0 {
		t.Fatalf("no generated profiles, kept = %+v", kept)
	}

	requireResetNoError(t, os.WriteFile(filepath.Join(codexHome, "ori-ws-a.config.toml"), []byte("x"), 0o600))
	kept := keptCodexProfiles(codexHome)
	if len(kept) != 1 || !strings.HasPrefix(kept[0].Location.Reason, "1 Codex profile Ori wrote") {
		t.Fatalf("one profile kept = %+v", kept)
	}
	requireResetNoError(t, os.WriteFile(filepath.Join(codexHome, "ori-ws-b.config.toml"), []byte("x"), 0o600))
	kept = keptCodexProfiles(codexHome)
	if len(kept) != 1 || kept[0].Category != settingsreset.CategoryRuntimeCache ||
		kept[0].Location.DisplayPath != filepath.Join(codexHome, "ori-ws-*.config.toml") ||
		!strings.HasPrefix(kept[0].Location.Reason, "2 Codex profiles Ori wrote") {
		t.Fatalf("two profiles kept = %+v", kept)
	}
	entries, err := os.ReadDir(codexHome)
	requireResetNoError(t, err)
	if len(entries) != 5 {
		t.Fatalf("inspection changed the Codex folder: %d entries", len(entries))
	}

	// A Codex home that cannot be listed is named as unchecked, not a blocker.
	notADir := filepath.Join(codexHome, "auth.json")
	kept = keptCodexProfiles(notADir)
	if len(kept) != 1 || kept[0].Location.DisplayPath != notADir || !strings.HasPrefix(kept[0].Location.Reason, "Could not be checked") {
		t.Fatalf("unreadable Codex home kept = %+v", kept)
	}
}
