package downloadsjanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeResolver maps workspace ids to folders under a temp root.
type fakeResolver struct {
	root    string
	missing map[string]bool
}

func (r fakeResolver) GetFolderPath(workspaceID string) (string, error) {
	if r.missing[workspaceID] {
		return "", fmt.Errorf("workspace %s not found", workspaceID)
	}
	return filepath.Join(r.root, workspaceID), nil
}

func newTestStore(t *testing.T) (*Store, fakeResolver) {
	t.Helper()
	root := t.TempDir()
	for _, id := range []string{"ws-1", "ws-2"} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	resolver := fakeResolver{root: root}
	return NewStore(resolver), resolver
}

func TestLoadSettings_MissingStateLoadsAsSetupRequired(t *testing.T) {
	store, _ := newTestStore(t)

	got, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.IsSetUp() {
		t.Fatalf("a workspace with no state must not report as set up: %+v", got)
	}
	if got.ContentMode != ContentModeMetadataOnly {
		t.Fatalf("content inspection must default to off, got %q", got.ContentMode)
	}
	if got.FilingRootName != DefaultFilingRootName || got.DailyScanLocalTime != DefaultDailyScanLocalTime {
		t.Fatalf("defaults not applied: %+v", got)
	}
	if got.RootPath != "" || got.DirectoryReferenceID != "" {
		t.Fatalf("no folder may be configured before setup: %+v", got)
	}
	if DeriveReadinessState(got.IsSetUp(), nil) != ReadinessSetupRequired {
		t.Fatal("an unconfigured workspace must report Setup required")
	}
}

func configuredSettings(t *testing.T, workspaceID string) JanitorSettings {
	t.Helper()
	s := NewSettings(workspaceID)
	s.RootPath = t.TempDir()
	s.DirectoryReferenceID = "dir-ref-1"
	s.SetupCompletedAt = time.Now()
	return s
}

func TestSaveAndLoadSettings_RoundTripsAndSurvivesRestart(t *testing.T) {
	store, resolver := newTestStore(t)

	want := configuredSettings(t, "ws-1")
	want.Timezone = "America/New_York"
	want.DailyScanLocalTime = "07:30"
	if err := store.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// A brand-new Store instance stands in for a restarted process: disk, not
	// memory, must hold the answer.
	restarted := NewStore(resolver)
	got, err := restarted.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings after restart: %v", err)
	}
	if !got.IsSetUp() {
		t.Fatalf("settings did not survive a restart: %+v", got)
	}
	if got.RootPath != want.RootPath || got.DirectoryReferenceID != "dir-ref-1" {
		t.Fatalf("root not persisted: %+v", got)
	}
	if got.Timezone != "America/New_York" || got.DailyScanLocalTime != "07:30" {
		t.Fatalf("schedule not persisted: %+v", got)
	}
	if got.FilingRootPath() != filepath.Join(want.RootPath, DefaultFilingRootName) {
		t.Fatalf("filing root = %q", got.FilingRootPath())
	}
}

func TestLoadSettings_RejectsAnotherWorkspacesRecord(t *testing.T) {
	store, _ := newTestStore(t)

	foreign := configuredSettings(t, "ws-2")
	if err := store.SaveSettings(foreign); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	// Move ws-2's record under ws-1's folder, simulating a copied workspace
	// folder or a tampered state file.
	src, err := store.settingsPath("ws-2")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadSettings("ws-1"); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("expected ErrWorkspaceMismatch, got %v", err)
	}
}

func TestLoadSettings_OlderAndCorruptRecordsDegradeToSetupRequired(t *testing.T) {
	store, _ := newTestStore(t)
	path, err := store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		// A record from a build that predates today's fields.
		"older schema": `{"workspace_id":"ws-1"}`,
		// A truncated/garbled file.
		"corrupt": `{"workspace_id":"ws-1"`,
		// A half-written setup: a root with no directory reference is not a
		// configured workspace, and must not be treated as one.
		"half-written setup": `{"workspace_id":"ws-1","root_path":"/tmp/somewhere","setup_completed_at":"2026-01-01T00:00:00Z"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := store.LoadSettings("ws-1")
			if err != nil {
				t.Fatalf("LoadSettings: %v", err)
			}
			if got.IsSetUp() {
				t.Fatalf("record must load as Setup required, got %+v", got)
			}
			if got.ContentMode.ReadsFileContent() {
				t.Fatalf("degraded record must not enable content reads: %+v", got)
			}
		})
	}
}

func TestLoadSettings_UnknownContentModeFailsClosed(t *testing.T) {
	store, _ := newTestStore(t)
	path, err := store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"workspace_id":"ws-1","content_mode":"everything"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.ContentMode != ContentModeMetadataOnly {
		t.Fatalf("unknown content mode must fail closed to metadata-only, got %q", got.ContentMode)
	}
}

func TestSaveSettings_RejectsInvalidRecords(t *testing.T) {
	store, _ := newTestStore(t)

	cases := map[string]func(*JanitorSettings){
		"no workspace":       func(s *JanitorSettings) { s.WorkspaceID = "" },
		"bad daily time":     func(s *JanitorSettings) { s.DailyScanLocalTime = "9am" },
		"unknown timezone":   func(s *JanitorSettings) { s.Timezone = "Mars/Olympus" },
		"traversing filing":  func(s *JanitorSettings) { s.FilingRootName = "../Filed" },
		"relative root":      func(s *JanitorSettings) { s.RootPath = "relative/downloads" },
		"unknown mode":       func(s *JanitorSettings) { s.ContentMode = "everything" },
		"nested filing root": func(s *JanitorSettings) { s.FilingRootName = "Filed/Docs" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := configuredSettings(t, "ws-1")
			mutate(&s)
			if err := store.SaveSettings(s); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("expected ErrInvalidSettings, got %v", err)
			}
		})
	}
}

func TestSaveSettings_FailureLeavesPreviousRecordIntact(t *testing.T) {
	store, _ := newTestStore(t)
	good := configuredSettings(t, "ws-1")
	if err := store.SaveSettings(good); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	bad := good
	bad.DailyScanLocalTime = "nope"
	if err := store.SaveSettings(bad); err == nil {
		t.Fatal("expected the invalid save to be rejected")
	}

	got, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.RootPath != good.RootPath || got.DailyScanLocalTime != DefaultDailyScanLocalTime {
		t.Fatalf("a rejected save must not disturb the stored record: %+v", got)
	}
}

func TestUpdateSettings_SerializesConcurrentWriters(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.SaveSettings(configuredSettings(t, "ws-1")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	const writers = 20
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			_, _ = store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
				s.Paused = !s.Paused
				return nil
			})
		})
	}
	wg.Wait()

	// The record must still be readable and well-formed — no interleaved or
	// truncated write survived.
	got, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings after concurrent updates: %v", err)
	}
	if !got.IsSetUp() {
		t.Fatalf("concurrent updates corrupted the record: %+v", got)
	}
}

func TestUpdateSettings_MutateErrorLeavesDiskUnchanged(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.SaveSettings(configuredSettings(t, "ws-1")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	sentinel := errors.New("nope")
	if _, err := store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.Paused = true
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("expected the mutate error, got %v", err)
	}

	got, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.Paused {
		t.Fatal("a failed mutate must not be persisted")
	}
}

func TestStore_StateLivesInsideTheWorkspaceFolder(t *testing.T) {
	store, resolver := newTestStore(t)
	if err := store.SaveSettings(configuredSettings(t, "ws-1")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	path, err := store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolver.root, "ws-1", StateDirName, settingsFileName)
	if path != want {
		t.Fatalf("state path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	// No stray temp files left behind by the atomic write.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != settingsFileName {
			t.Fatalf("unexpected leftover in the state directory: %q", entry.Name())
		}
	}
}

func TestStore_UnresolvableWorkspaceIsAnError(t *testing.T) {
	root := t.TempDir()
	store := NewStore(fakeResolver{root: root, missing: map[string]bool{"gone": true}})
	if _, err := store.LoadSettings("gone"); err == nil {
		t.Fatal("expected an error for a workspace with no folder")
	}
	if _, err := store.LoadSettings("  "); err == nil {
		t.Fatal("expected an error for a blank workspace id")
	}
}

func TestSettings_StoredRecordCarriesNoFileContent(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.SaveSettings(configuredSettings(t, "ws-1")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	path, err := store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("stored record is not valid JSON: %v", err)
	}
	allowed := map[string]bool{
		"schema_version": true, "workspace_id": true, "directory_reference_id": true,
		"root_path": true, "filing_root_name": true, "daily_scan_local_time": true,
		"timezone": true, "content_mode": true, "content_provider": true,
		"content_consent_provider": true, "content_consent_at": true, "paused": true,
		"setup_completed_at": true, "updated_at": true,
	}
	for key := range raw {
		if !allowed[key] {
			t.Fatalf("unexpected key %q in persisted settings; state must stay configuration-only", key)
		}
	}
}
