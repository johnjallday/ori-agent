package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func newWorkspaceBuildStoreFixture(t *testing.T) (*WorkspaceBuildStore, *knowledgeFixture, *time.Time) {
	t.Helper()
	kf := newKnowledgeFixture(t)
	store := NewWorkspaceBuildStore(NewKnowledgeStore(kf.resolver(), kf.folder))
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	return store, kf, &now
}

func openBuild(id string, now time.Time) WorkspaceBuildSession {
	return WorkspaceBuildSession{
		ID: id, Status: WorkspaceBuildOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
		EntryPoint: "home_cockpit_create", Assistant: BuildAssistant{DisplayName: "Ada"},
		Transcript: []BuildTranscriptEntry{}, FurthestStep: 1,
	}
}

func TestWorkspaceBuildStore_RoundTripsASession(t *testing.T) {
	ctx := context.Background()
	store, kf, now := newWorkspaceBuildStoreFixture(t)
	empty, err := store.Read(ctx, "local")
	if err != nil || empty.Present || empty.Open() != nil {
		t.Fatalf("empty read = %+v, %v", empty, err)
	}
	session := openBuild("build-1", *now)
	session.Draft = BuildDraft{TemplateID: "content-production", Name: "Newsletter Desk", Description: "Drafts it"}
	session.Append(BuildTranscriptEntry{Role: BuildRoleUser, Text: "a newsletter", At: *now})
	session.Append(BuildTranscriptEntry{
		Role: BuildRoleAssistant, Text: "Content Production fits.", At: *now,
		Choices: []BuildChoice{{ID: "c1-1", Label: "No folder"}},
	})
	if _, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		doc.Add(session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	open := doc.Open()
	if open == nil || open.ID != "build-1" || open.Draft.Name != "Newsletter Desk" || len(open.Transcript) != 2 ||
		open.Transcript[1].Choices[0].ID != "c1-1" {
		t.Fatalf("read back %+v", open)
	}
	// The file sits beside the knowledge sidecar, under the HQ's .ori.
	if _, err := os.Stat(filepath.Join(kf.folder.path, ".ori", workspaceBuildFileName)); err != nil {
		t.Fatalf("sidecar file: %v", err)
	}
}

func TestWorkspaceBuildStore_RefusesASecondOpenBuild(t *testing.T) {
	ctx := context.Background()
	store, _, now := newWorkspaceBuildStoreFixture(t)
	add := func(id string) error {
		_, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
			doc.Add(openBuild(id, *now))
			return nil
		})
		return err
	}
	if err := add("build-1"); err != nil {
		t.Fatal(err)
	}
	if err := add("build-2"); !errors.Is(err, ErrWorkspaceBuildOpen) {
		t.Fatalf("second open build err = %v, want ErrWorkspaceBuildOpen", err)
	}
}

func TestWorkspaceBuildStore_AbandonsAWeekOldBuildOnRead(t *testing.T) {
	ctx := context.Background()
	store, _, now := newWorkspaceBuildStoreFixture(t)
	if _, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		build := openBuild("build-1", *now)
		build.Transcript = []BuildTranscriptEntry{{Role: BuildRoleUser, Text: "notes for my course", At: *now}}
		doc.Add(build)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(WorkspaceBuildExpiry - time.Minute)
	if doc, _ := store.Read(ctx, "local"); doc.Open() == nil {
		t.Fatal("a build inside the week must stay open")
	}
	*now = now.Add(2 * time.Minute)
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Open() != nil || doc.Session("build-1").Status != WorkspaceBuildAbandoned {
		t.Fatalf("expired build = %+v", doc.Session("build-1"))
	}
	if expired := doc.Session("build-1"); len(expired.Transcript) != 0 || expired.TeamState != nil {
		t.Fatalf("an expired build keeps only what a settled one needs: %+v", expired)
	}
	// The next write records it, and a new build can open.
	written, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		doc.Add(openBuild("build-2", *now))
		return nil
	})
	if err != nil || written.Session("build-1").Status != WorkspaceBuildAbandoned || written.Open().ID != "build-2" {
		t.Fatalf("after expiry write = %+v, %v", written, err)
	}
}

func TestWorkspaceBuildStore_ConcurrentWritersNeverLoseAnUpdate(t *testing.T) {
	ctx := context.Background()
	store, _, now := newWorkspaceBuildStoreFixture(t)
	if _, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		doc.Add(openBuild("build-1", *now))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const writers = 6
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
				open := doc.Open()
				open.Append(BuildTranscriptEntry{Role: BuildRoleUser, Text: fmt.Sprintf("turn %d", i), At: *now})
				return nil
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	doc, err := store.Read(ctx, "local")
	if err != nil || len(doc.Open().Transcript) != writers {
		t.Fatalf("transcript after concurrent writes = %d entries, err %v", len(doc.Open().Transcript), err)
	}
}

func TestWorkspaceBuildStore_KeepsTheTranscriptBounded(t *testing.T) {
	session := openBuild("build-1", time.Now())
	for i := range WorkspaceBuildMaxEntries + 5 {
		session.Append(BuildTranscriptEntry{Role: BuildRoleUser, Text: fmt.Sprintf("turn %d", i)})
	}
	if len(session.Transcript) != WorkspaceBuildMaxEntries || session.Transcript[0].Text != "turn 5" {
		t.Fatalf("entry cap: %d entries starting %q", len(session.Transcript), session.Transcript[0].Text)
	}
	long := strings.Repeat("x", WorkspaceBuildMaxText)
	for range 20 {
		session.Append(BuildTranscriptEntry{Role: BuildRoleUser, Text: long})
	}
	if transcriptBytes(session.Transcript) > WorkspaceBuildMaxTranscriptBytes {
		t.Fatalf("byte cap exceeded: %d", transcriptBytes(session.Transcript))
	}
}

// An attached folder is stored as the composer's path-free observation and
// held to its bounds; a settled build drops it with the rest.
func TestWorkspaceBuildStore_KeepsAnAttachedFolderWithinItsBounds(t *testing.T) {
	ctx := context.Background()
	store, _, now := newWorkspaceBuildStoreFixture(t)
	session := openBuild("build-1", *now)
	session.Folder = &BuildFolder{Observation: foldercontext.Observation{
		Version: foldercontext.Version, ID: "obs-1", Folder: "Elite School", ScannedAt: *now, Entries: 3, Files: 2,
		Kinds:    []foldercontext.Kind{{Name: "md", Count: 2}},
		Projects: []foldercontext.Project{{ID: "candidate-0", Name: "Elite School", Files: 2, Root: true}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3},
	}}
	if _, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		doc.Add(session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	open := doc.Open()
	if open == nil || open.Folder == nil || open.Folder.Observation.Folder != "Elite School" || open.Folder.Observation.Projects[0].Name != "Elite School" {
		t.Fatalf("read back %+v", open)
	}
	bad := session
	bad.Folder = &BuildFolder{Observation: foldercontext.Observation{Version: 2}}
	if err := validateBuildSession(bad); !errors.Is(err, ErrWorkspaceBuildInvalid) {
		t.Fatalf("an invalid observation is refused: %v", err)
	}
	open.Settle()
	if open.Folder != nil {
		t.Fatal("a settled build keeps no folder")
	}
}

func TestWorkspaceBuildStore_RejectsUnknownFields(t *testing.T) {
	owner := KnowledgeOwner{UserID: "local"}
	good := fmt.Sprintf(`{"schema_version":1,"version":1,"owner":{"user_id":%q}}`, owner.UserID)
	if _, err := decodeWorkspaceBuild([]byte(good), owner); err != nil {
		t.Fatalf("good document: %v", err)
	}
	bad := fmt.Sprintf(`{"schema_version":1,"version":1,"owner":{"user_id":%q},"path":"/Users/me"}`, owner.UserID)
	if _, err := decodeWorkspaceBuild([]byte(bad), owner); !errors.Is(err, ErrKnowledgeCorrupt) {
		t.Fatalf("unknown field err = %v", err)
	}
}
