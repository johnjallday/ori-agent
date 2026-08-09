package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// These tests drive the real persistence seam rather than the pure mapping
// function (which internal/types covers): they write a legacy record to disk,
// load it, and assert what comes back and what gets written next.

func writeLegacyAgent(t *testing.T, dir, name, settingsJSON string) string {
	t.Helper()
	agentDir := filepath.Join(dir, "agents", name)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	path := filepath.Join(agentDir, "agent_settings.json")
	if err := os.WriteFile(path, []byte(settingsJSON), 0o644); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}
	return path
}

func loadStore(t *testing.T, dir string) *fileStore {
	t.Helper()
	fs := &fileStore{
		path:   filepath.Join(dir, "agents_index.json"),
		agents: make(map[string]*agent.Agent),
	}
	if err := fs.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	return fs
}

func TestLoadMigratesLegacyGlobalRecords(t *testing.T) {
	dir := t.TempDir()
	entry := charactercatalog.MustLoad().Working()[0]

	writeLegacyAgent(t, dir, "uploader", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{
			"description":"kept",
			"avatar_color":"#3366FF",
			"avatar_image":"atlas.webp",
			"character":{"display_mode":"uploaded","catalog_id":"`+string(entry.ID)+`","catalog_version":1,"voice_enabled":true}
		}
	}`)

	// The upload has to exist for Upload to survive as the active mode: a
	// pointer at a file that is gone must not become the canonical active source
	// (FR-72). The store resolves uploads relative to the process working
	// directory, so seed one there.
	seedUpload(t, "atlas.webp")

	fs := loadStore(t, dir)
	ag, ok := fs.agents["uploader"]
	if !ok || ag == nil {
		t.Fatal("agent not loaded")
	}
	if ag.Appearance == nil {
		t.Fatal("a loaded record must have a canonical appearance")
	}
	if ag.Appearance.Mode != types.AppearanceModeUploaded {
		t.Errorf("mode = %q, want uploaded", ag.Appearance.Mode)
	}
	if ag.Appearance.GeneratedColor() != "#3366ff" {
		t.Errorf("colour = %q, want the normalized legacy colour", ag.Appearance.GeneratedColor())
	}
	if ag.Appearance.UploadedImage() != "atlas.webp" {
		t.Errorf("image = %q, want atlas.webp", ag.Appearance.UploadedImage())
	}
	// The character was inactive but chosen; losing it would make the first
	// switch after upgrading a rebuild rather than a switch (FR-70).
	if ag.Appearance.CharacterCatalogID() != string(entry.ID) {
		t.Errorf("catalog id = %q, want the retained selection", ag.Appearance.CharacterCatalogID())
	}
	if ag.Metadata == nil || ag.Metadata.Description != "kept" {
		t.Error("unrelated metadata must survive migration")
	}
}

func TestSaveWritesOnlyTheCanonicalSchema(t *testing.T) {
	dir := t.TempDir()
	path := writeLegacyAgent(t, dir, "legacy", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"avatar_color":"#3366ff","avatar_image":"atlas.webp","character":{"display_mode":"fallback","voice_enabled":true}}
	}`)

	fs := loadStore(t, dir)
	if err := fs.saveUnlocked(); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	written := string(raw)
	// After a successful migration there is no dual-write path: the retired
	// vocabulary must be gone from disk entirely (FR-77).
	for _, retired := range []string{"avatar_color", "avatar_image", "display_mode", "voice_enabled"} {
		if strings.Contains(written, retired) {
			t.Errorf("persisted record still contains %q:\n%s", retired, written)
		}
	}
	if !strings.Contains(written, `"appearance"`) {
		t.Errorf("persisted record is missing the canonical appearance:\n%s", written)
	}

	var record struct {
		Appearance *types.AgentAppearance `json:"appearance"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode written record: %v", err)
	}
	if record.Appearance == nil || record.Appearance.Mode != types.AppearanceModeGenerated {
		t.Fatalf("expected a generated appearance on disk, got %+v", record.Appearance)
	}
	// "fallback" became Generated, but the colour and the upload reference are
	// still the user's data and must survive.
	if record.Appearance.GeneratedColor() != "#3366ff" || record.Appearance.UploadedImage() != "atlas.webp" {
		t.Errorf("migration lost retained source data: %+v", record.Appearance)
	}
}

func TestMigrationIsStableAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := writeLegacyAgent(t, dir, "restarter", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"avatar_color":"#ABC","character":{"display_mode":"fallback"}}
	}`)

	first := loadStore(t, dir)
	if err := first.saveUnlocked(); err != nil {
		t.Fatalf("first save: %v", err)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}

	// A second startup over already-migrated data must produce byte-equivalent
	// output — that is what makes the migration safe to run on every boot
	// (FR-76/FR-110).
	second := loadStore(t, dir)
	if err := second.saveUnlocked(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second: %v", err)
	}

	if string(afterFirst) != string(afterSecond) {
		t.Fatalf("a second startup rewrote the record:\nfirst:\n%s\nsecond:\n%s", afterFirst, afterSecond)
	}
}

func TestMigrationDoesNotTouchUploadedFiles(t *testing.T) {
	dir := t.TempDir()
	writeLegacyAgent(t, dir, "keeper", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"avatar_image":"atlas.webp"}
	}`)
	uploadPath := seedUpload(t, "atlas.webp")

	before, err := os.ReadFile(uploadPath)
	if err != nil {
		t.Fatalf("read seeded upload: %v", err)
	}

	fs := loadStore(t, dir)
	if err := fs.saveUnlocked(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Adopting a new schema is never a reason to rename, recompress, or delete a
	// user's image (FR-74).
	after, err := os.ReadFile(uploadPath)
	if err != nil {
		t.Fatalf("upload disappeared during migration: %v", err)
	}
	if string(before) != string(after) {
		t.Error("migration modified the uploaded file")
	}
}

func TestMigrationRecordsANoteWithoutLeakingPaths(t *testing.T) {
	agent.ResetAppearanceMigrationNotes()
	t.Cleanup(agent.ResetAppearanceMigrationNotes)

	dir := t.TempDir()
	writeLegacyAgent(t, dir, "broken", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"avatar_image":"vanished.png","character":{"display_mode":"uploaded","voice_enabled":true}}
	}`)

	fs := loadStore(t, dir)
	if got := fs.agents["broken"].Appearance.Mode; got != types.AppearanceModeGenerated {
		t.Fatalf("a missing upload must not stay active, got %q", got)
	}

	notes := agent.AppearanceMigrationNotes()
	if len(notes) != 1 {
		t.Fatalf("expected exactly one note, got %d: %+v", len(notes), notes)
	}
	note := notes[0]
	if note.Agent != "broken" || note.Scope != "global" {
		t.Errorf("note does not identify the record: %+v", note)
	}
	wantReasons := map[string]bool{
		types.AppearanceReasonUploadMissing:  true,
		types.AppearanceReasonVoiceDiscarded: true,
	}
	for _, reason := range note.Reasons {
		if !wantReasons[reason] {
			t.Errorf("unexpected reason %q", reason)
		}
		// Reason codes are surfaced in logs and health output, so they must
		// never carry a filesystem path (FR-73).
		if strings.ContainsAny(reason, "/\\") {
			t.Errorf("reason %q looks like a path", reason)
		}
	}
	if len(note.Reasons) != 2 {
		t.Errorf("expected both reasons, got %v", note.Reasons)
	}
}

// seedUpload writes a placeholder image into the avatar directory the store
// resolves against, and removes it afterwards. The directory is relative to the
// process working directory, which is the package directory under `go test`.
func seedUpload(t *testing.T, name string) string {
	t.Helper()
	if err := os.MkdirAll(agent.AppearanceUploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	path := filepath.Join(agent.AppearanceUploadDir, name)
	if err := os.WriteFile(path, []byte("not-really-an-image"), 0o644); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(agent.AppearanceUploadDir)
	})
	return path
}
