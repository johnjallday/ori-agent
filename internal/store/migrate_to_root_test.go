package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

// migrationFixture is a data dir with a legacy agents folder and a confirmed,
// existing workspace root, both under one temp dir.
type migrationFixture struct {
	dataDir string
	root    string
}

func newMigrationFixture(t *testing.T) migrationFixture {
	t.Helper()
	base := t.TempDir()
	f := migrationFixture{dataDir: filepath.Join(base, "data"), root: filepath.Join(base, "Ori Workspaces")}
	for _, dir := range []string{f.dataDir, f.root} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.dataDir, "agents.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	return f
}

func (f migrationFixture) legacyDir(name string) string {
	return filepath.Join(f.dataDir, "agents", name)
}

func (f migrationFixture) rootDir(name string) string {
	return filepath.Join(f.root, "Agents", name)
}

func (f migrationFixture) seed(t *testing.T, name, definition string, sidecars map[string]string) {
	t.Helper()
	dir := f.legacyDir(name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte(definition), 0o600); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	for file, body := range sidecars {
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("seed %s/%s: %v", name, file, err)
		}
	}
}

// seedStandard writes the built-in assistant plus two user agents: one paused
// under the legacy status, one with sidecar files and statistics.
func (f migrationFixture) seedStandard(t *testing.T) {
	t.Helper()
	f.seed(t, systemassistant.CanonicalName,
		`{"role":"orchestrator","Settings":{"model":"gpt-4o-mini"},"metadata":{"tags":["`+systemassistant.ProtectedMarker+`"]}}`, nil)
	f.seed(t, "Scout",
		`{"role":"researcher","Settings":{"model":"gpt-4o-mini","system_prompt":"scout"},"status":"active","statistics":{"message_count":7}}`,
		map[string]string{
			"mcp_servers.json":         `{"enabled_servers":["git"]}`,
			"skills_state.json":        `{"skills":{"*":{"enabled":false,"trusted":false}}}`,
			"skills/notes/SKILL.md":    "a per-agent skill",
			"skills/notes/extra/a.txt": "nested",
		})
	f.seed(t, "Sleeper", `{"Settings":{"model":"gpt-4o-mini"},"status":"disabled"}`, nil)
}

func (f migrationFixture) run(t *testing.T) *RootMigrationReport {
	t.Helper()
	return RootMigration{DataDir: f.dataDir, Root: f.root}.Run()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestMigrationMovesUserAgentsIntoTheRoot(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)

	report := f.run(t)
	if report.Status != MigrationDone {
		t.Fatalf("status = %q (%s), want done", report.Status, report.Error)
	}
	if strings.Join(report.Migrated, ",") != "Scout,Sleeper" || len(report.Skipped) != 0 {
		t.Errorf("migrated %v skipped %v", report.Migrated, report.Skipped)
	}

	for _, rel := range []string{"agent_settings.json", "mcp_servers.json", "skills_state.json", "skills/notes/SKILL.md", "skills/notes/extra/a.txt"} {
		if !exists(filepath.Join(f.rootDir("Scout"), rel)) {
			t.Errorf("Scout/%s did not reach the root", rel)
		}
	}
	for _, name := range []string{"Scout", "Sleeper"} {
		if exists(f.legacyDir(name)) {
			t.Errorf("%s is still in the data dir", name)
		}
	}
	if !exists(f.legacyDir(systemassistant.CanonicalName)) || exists(f.rootDir(systemassistant.CanonicalName)) {
		t.Error("the built-in assistant must stay in the data dir")
	}

	sleeper := definitionKeys(t, filepath.Join(f.rootDir("Sleeper"), "agent_settings.json"))
	if string(sleeper["paused"]) != "true" {
		t.Errorf(`legacy "status": "disabled" must become "paused": true, got %v`, sleeper)
	}
	if _, has := definitionKeys(t, filepath.Join(f.rootDir("Scout"), "agent_settings.json"))["statistics"]; has {
		t.Error("the migrated definition still carries statistics")
	}
	state, ok, err := NewRuntimeStateStore(filepath.Join(f.dataDir, "agent_state"), f.root).Load("Scout")
	if err != nil || !ok || state.Statistics == nil || state.Statistics.MessageCount != 7 {
		t.Errorf("Scout's statistics did not reach the root's state: ok %v err %v %+v", ok, err, state)
	}

	if report.Backup == "" || !exists(filepath.Join(report.Backup, "agents", "Scout", "mcp_servers.json")) ||
		!exists(filepath.Join(report.Backup, "agents.json")) {
		t.Errorf("no complete backup at %q", report.Backup)
	}
	var marker RootMigrationReport
	data, err := os.ReadFile(filepath.Join(f.dataDir, RootMigrationMarker))
	if err != nil || json.Unmarshal(data, &marker) != nil {
		t.Fatalf("marker missing or unreadable: %v", err)
	}
	if marker.Root != f.root || len(marker.Migrated) != 2 || marker.MigratedAt.IsZero() {
		t.Errorf("marker = %+v", marker)
	}
}

func TestMigrationStopsWithoutChangesWhenTheBackupFails(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	// A file where the recovery folder should go makes the backup fail.
	if err := os.WriteFile(filepath.Join(f.dataDir, "recovery"), []byte("in the way"), 0o600); err != nil {
		t.Fatalf("block recovery: %v", err)
	}

	report := f.run(t)
	if report.Status != MigrationBackupFailed || !report.KeepsLegacyLocation() {
		t.Fatalf("status = %q, want backup_failed", report.Status)
	}
	if !exists(f.legacyDir("Scout")) || exists(filepath.Join(f.root, "Agents")) {
		t.Error("a failed backup must change nothing")
	}
	if exists(filepath.Join(f.dataDir, RootMigrationMarker)) {
		t.Error("a failed migration wrote the marker")
	}
}

func TestMigrationSkipsANameTheRootAlreadyHas(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	theirs := filepath.Join(f.rootDir("Scout"), "agent_settings.json")
	if err := os.MkdirAll(filepath.Dir(theirs), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := []byte(`{"Settings":{"model":"synced-from-another-machine"}}`)
	if err := os.WriteFile(theirs, body, 0o600); err != nil {
		t.Fatalf("seed root agent: %v", err)
	}

	report := f.run(t)
	if report.Status != MigrationDone {
		t.Fatalf("status = %q (%s)", report.Status, report.Error)
	}
	if strings.Join(report.Skipped, ",") != "Scout" || strings.Join(report.Migrated, ",") != "Sleeper" {
		t.Errorf("migrated %v skipped %v", report.Migrated, report.Skipped)
	}
	if got, _ := os.ReadFile(theirs); string(got) != string(body) {
		t.Error("the migration overwrote an agent already in the root")
	}
	if !exists(f.legacyDir("Scout")) {
		t.Error("a skipped agent's legacy folder must stay")
	}
}

func TestMigrationIsBlockedByAWorkspaceNamedAgents(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	if err := os.MkdirAll(filepath.Join(f.root, "Agents"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "Agents", "workspace.json"), []byte(`{"id":"ws"}`), 0o600); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	report := f.run(t)
	if report.Status != MigrationBlockedByWorkspace || !report.KeepsLegacyLocation() {
		t.Fatalf("status = %q, want blocked_by_workspace", report.Status)
	}
	if !exists(f.legacyDir("Scout")) || exists(f.rootDir("Scout")) {
		t.Error("a blocked migration must move nothing")
	}
}

func TestMigrationStopsWhenTheRootIsNotWritable(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	if err := os.Chmod(f.root, 0o555); err != nil {
		t.Skipf("cannot make the root read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.root, 0o750) })
	if probeWritable(f.root) == nil {
		t.Skip("the file system allowed a write despite read-only permissions")
	}

	report := f.run(t)
	if report.Status != MigrationNotWritable || !report.KeepsLegacyLocation() {
		t.Fatalf("status = %q, want not_writable", report.Status)
	}
	if !exists(f.legacyDir("Scout")) || exists(filepath.Join(f.dataDir, RootMigrationMarker)) {
		t.Error("an unwritable root must leave the agents in place and retry next start")
	}
}

func TestMigrationSecondRunIsANoOp(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	if first := f.run(t); first.Status != MigrationDone {
		t.Fatalf("first run: %q (%s)", first.Status, first.Error)
	}
	definition := filepath.Join(f.rootDir("Scout"), "agent_settings.json")
	backdate(t, definition)

	second := f.run(t)
	if second.Status != MigrationDone || strings.Join(second.Migrated, ",") != "Scout,Sleeper" {
		t.Errorf("second run = %+v, want the recorded result", second)
	}
	assertUntouched(t, definition)
	backups, _ := os.ReadDir(filepath.Join(f.dataDir, "recovery"))
	if len(backups) != 1 {
		t.Errorf("a second run made another backup: %d", len(backups))
	}
}

func TestInterruptedMigrationFinishesOnTheNextRun(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)

	interrupted := RootMigration{DataDir: f.dataDir, Root: f.root, afterAgent: func(string) error {
		return errors.New("power cut")
	}}.Run()
	if interrupted.Status != MigrationIncomplete || len(interrupted.Migrated) != 1 {
		t.Fatalf("interrupted run = %+v", interrupted)
	}
	if exists(filepath.Join(f.dataDir, RootMigrationMarker)) {
		t.Fatal("an interrupted run wrote the marker")
	}

	finished := f.run(t)
	if finished.Status != MigrationDone {
		t.Fatalf("second run: %q (%s)", finished.Status, finished.Error)
	}
	for _, name := range []string{"Scout", "Sleeper"} {
		if !exists(filepath.Join(f.rootDir(name), "agent_settings.json")) || exists(f.legacyDir(name)) {
			t.Errorf("%s is not exactly once, in the root", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(f.root, "Agents"))
	if len(entries) != 2 {
		t.Errorf("the root holds %d entries, want exactly the 2 agents", len(entries))
	}
}

func TestInterruptionAfterTheRenameIsRecognizedAsItsOwnWork(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	first := f.run(t)
	if first.Status != MigrationDone {
		t.Fatalf("first run: %q", first.Status)
	}
	// Simulate a crash after Scout was renamed into the root but before its
	// legacy folder was removed: put the legacy folder back, forget the marker.
	if err := copyTree(filepath.Join(first.Backup, "agents", "Scout"), f.legacyDir("Scout"), nil); err != nil {
		t.Fatalf("restore legacy Scout: %v", err)
	}
	if err := os.Remove(filepath.Join(f.dataDir, RootMigrationMarker)); err != nil {
		t.Fatalf("remove marker: %v", err)
	}

	again := RootMigration{DataDir: f.dataDir, Root: f.root, Now: func() time.Time { return time.Now().Add(time.Hour) }}.Run()
	if again.Status != MigrationDone || strings.Join(again.Migrated, ",") != "Scout" || len(again.Skipped) != 0 {
		t.Fatalf("resumed run = %+v, want Scout finished rather than skipped", again)
	}
	if exists(f.legacyDir("Scout")) {
		t.Error("the leftover legacy copy was not removed")
	}
}

func TestMigrationLeavesAMissingRootAlone(t *testing.T) {
	f := newMigrationFixture(t)
	f.seedStandard(t)
	missing := filepath.Join(f.root, "not-mounted")

	report := RootMigration{DataDir: f.dataDir, Root: missing}.Run()
	if report.Status != MigrationRootMissing {
		t.Fatalf("status = %q, want root_missing", report.Status)
	}
	if exists(missing) || !exists(f.legacyDir("Scout")) {
		t.Error("a missing root must not be created, and nothing may move")
	}
}

func TestMigrationWithOnlyTheAssistantIsNotNeeded(t *testing.T) {
	f := newMigrationFixture(t)
	f.seed(t, systemassistant.CanonicalName,
		`{"Settings":{"model":"gpt-4o-mini"},"metadata":{"tags":["`+systemassistant.ProtectedMarker+`"]}}`, nil)
	if report := f.run(t); report.Status != MigrationNotNeeded {
		t.Fatalf("status = %q, want not_needed", report.Status)
	}
	if exists(filepath.Join(f.root, "Agents")) || exists(filepath.Join(f.dataDir, "recovery")) {
		t.Error("a migration with nothing to move must not touch the root or make a backup")
	}
}

func TestMigrationTreatsAnUnmarkedRetiredAssistantNameAsTheAssistant(t *testing.T) {
	f := newMigrationFixture(t)
	f.seed(t, "Workspace Manager", `{"Settings":{"model":"gpt-4o-mini"}}`, nil)
	f.seed(t, "Scout", `{"Settings":{"model":"gpt-4o-mini"}}`, nil)
	report := f.run(t)
	if strings.Join(report.Migrated, ",") != "Scout" || !exists(f.legacyDir("Workspace Manager")) {
		t.Errorf("an unmarked retired assistant must stay in the data dir: %+v", report)
	}
}
