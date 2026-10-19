package server

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// coveragePrivacyReader stands in for the owning service at the server seam.
// It answers from the folders it receives, so those paths are in hand here.
type coveragePrivacyReader struct {
	received []backuprecovery.AdoptedFolder
	at       time.Time
}

func (r *coveragePrivacyReader) ReadBackupSummaries(context.Context, string, []string) (backuprecovery.SummaryRead, error) {
	return backuprecovery.SummaryRead{Complete: true}, nil
}
func (r *coveragePrivacyReader) ReadFolderCoverage(_ context.Context, _ string, _ []string, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	r.received = append([]backuprecovery.AdoptedFolder(nil), folders...)
	read := backuprecovery.CoverageRead{Complete: true, Consoles: []backuprecovery.CoverageConsole{{WorkspaceID: "backup", Configured: true}}}
	for i, folder := range folders {
		row := backuprecovery.FolderCoverage{WorkspaceID: folder.WorkspaceID, State: backuprecovery.CoverageNotCovered, Reason: "not_in_any_source"}
		if i == 0 {
			row = backuprecovery.FolderCoverage{WorkspaceID: folder.WorkspaceID, State: backuprecovery.CoverageCovered, Reason: "completed_backup_observed", Provider: "restic", CoveringWorkspaceID: "backup", LastCompletedAt: &r.at}
		}
		read.Folders = append(read.Folders, row)
	}
	return read, nil
}

type privacyRelationship struct{ projection *personalassistant.Projection }

func (p privacyRelationship) Get(context.Context, string) (*personalassistant.Projection, error) {
	return p.projection, nil
}

type privacyBriefs struct{}

func (privacyBriefs) GetCurrent(context.Context, string) (*dailybrief.Revision, error) {
	return nil, nil
}

type privacyFollowUps struct{}

func (privacyFollowUps) List(context.Context, followup.Filter) ([]*followup.FollowUp, error) {
	return nil, nil
}

type privacyDeclines struct{}

func (privacyDeclines) DomainDecline(context.Context, string, string) (*personalassistant.FolderDomainDecline, error) {
	return nil, nil
}
func (privacyDeclines) DeclineDomain(context.Context, string, string, string) error { return nil }

var (
	rootedPath      = regexp.MustCompile(`(^|[\s"'(])/(Users|Volumes|Invented|private|var|tmp)\b`)
	storageIdentity = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{64}`)
)

// M3: stored folder paths reach the owning reader, and nothing the reader's
// result becomes — the coverage projection, Today's whole payload, each
// workspace fact and its agent prompt line — carries a rooted path, a volume
// identifier or a repository identifier.
func TestBackupCoverageProjectionsCarryNoPathOrStorageIdentity(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	save := func(ws *workspace.Workspace) {
		t.Helper()
		ws.OwnerUserID = "local"
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.FolderSlug = "hq", "personal-hq"
	save(hq)
	console := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "My Backups"})
	console.ID, console.FolderSlug = "backup", "my-backups"
	save(console)
	paths := []string{"/Users/invented/Music/Songs/Night Drive", "/Volumes/Invented Drive/Manuscript"}
	for i, path := range paths {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: []string{"Night Drive", "Manuscript"}[i]})
		ws.ID, ws.FolderSlug = []string{"song", "book"}[i], []string{"night-drive", "manuscript"}[i]
		ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "ref-" + ws.ID, WorkspaceID: ws.ID, Name: ws.Name, Path: path}}
		ws.SharedData = map[string]any{"primary_directory_id": "ref-" + ws.ID}
		save(ws)
	}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.ID, home.FolderSlug = "home", "music-home"
	library, _ := json.Marshal(map[string]any{"roots": []map[string]any{{"id": "11111111-2222-4333-8444-555555555555", "path": "/Users/invented/Music/Songs", "file_identity": strings.Repeat("a", 64)}}})
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: 1, ProjectLibrary: library})
	save(home)

	reader := &coveragePrivacyReader{at: now.Add(-14 * time.Hour)}
	source := dailyBriefBackupSource{reader: reader}
	relationship := &personalassistant.Projection{State: personalassistant.APIStateActive, HQWorkspaceID: "hq",
		DailyBrief: &personalassistant.BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}}
	today := personalassistant.NewTodayService(privacyRelationship{relationship}, privacyBriefs{}, store, privacyFollowUps{})
	today.SetBackupReader(source)
	today.SetBackupCoverage(source, privacyDeclines{})

	projection, err := today.Get(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.received) != 3 {
		t.Fatal("the owning reader did not receive the stored folders", reader.received)
	}
	scope := dailybrief.ResolveWorkspaceScope(store, dailybrief.Config{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}, "local")
	coverage := dailybrief.ReadCoverage(t.Context(), source, "local", scope)
	found := false
	for _, item := range projection.Backups.Items {
		found = found || item.Kind == "backup_coverage"
	}
	if !found || coverage.NotCovered != 2 {
		t.Fatal("fixture did not produce the line", projection.Backups.Items, coverage)
	}
	payloads := map[string]any{"today": projection, "coverage": coverage}
	for _, id := range []string{"song", "book", "home"} {
		fact, err := today.WorkspaceProtection(t.Context(), "local", id)
		if err != nil || fact == nil {
			t.Fatal(id, fact, err)
		}
		payloads["fact "+id] = fact
		payloads["prompt "+id] = dailybrief.ProtectionPromptLine(*fact)
	}
	for name, payload := range payloads {
		encoded, _ := json.Marshal(payload)
		text := string(encoded)
		for _, path := range append(paths, "/Users/invented/Music/Songs") {
			if strings.Contains(text, path) {
				t.Fatal(name, "carries a stored path", path)
			}
		}
		if match := rootedPath.FindString(text); match != "" {
			t.Fatal(name, "carries a rooted path", match)
		}
		if match := storageIdentity.FindString(text); match != "" {
			t.Fatal(name, "carries a storage identifier", match)
		}
	}
}
