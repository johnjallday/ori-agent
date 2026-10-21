package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

var secondBrainNow = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func newSecondBrainStoreFixture(t *testing.T) (*SecondBrainStore, *knowledgeFixture) {
	t.Helper()
	kf := newKnowledgeFixture(t)
	return NewSecondBrainStore(NewKnowledgeStore(kf.resolver(), kf.folder)), kf
}

func secondBrainSource(path string) secondbrain.Source {
	return secondbrain.Source{
		ID: secondbrain.SourceID(path), Kind: secondbrain.KindObsidian, Name: filepath.Base(path),
		Path: path, ConnectedAt: secondBrainNow, RequestID: "connect-1",
	}
}

func TestSecondBrainStore_RoundTripsTheRecordUnderTheHQ(t *testing.T) {
	ctx := context.Background()
	store, kf := newSecondBrainStoreFixture(t)
	empty, err := store.Read(ctx, "local")
	if err != nil || empty.Present || empty.Connected() != nil || empty.Ask.State() != secondbrain.AskPending {
		t.Fatalf("empty read = %+v, %v", empty, err)
	}

	finished := secondBrainNow.Add(time.Minute)
	source := secondBrainSource("/Users/sam/Second Brain")
	written, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		connected, err := doc.Connect(source, false)
		if err != nil {
			return err
		}
		connected.LastLook = &secondbrain.Look{
			ID: "look-1", State: secondbrain.LookFinished, RequestID: "connect-1", StartedAt: secondBrainNow, FinishedAt: &finished,
			Headline: "1 open item across 1 note, 0 with dates.", HeadlineBy: secondbrain.HeadlineByRules,
			Counts: secondbrain.LookCounts{Notes: 1, OpenItems: 1, NotesWithOpen: 1},
			Items: []secondbrain.LookItem{{
				ID: secondbrain.ItemID(source.ID, "Inbox.md", "call the distributor"), Text: "call the distributor", Note: "Inbox.md", Line: 3, Suggested: true,
			}},
		}
		doc.MarkPrompted("prompt-1", secondBrainNow)
		doc.Remember(secondbrain.Receipt{RequestID: "connect-1", Action: "connect", At: secondBrainNow, LookID: "look-1"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if written.Version != 1 || !written.Present {
		t.Fatalf("written = version %d present %v", written.Version, written.Present)
	}

	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	connected := doc.Connected()
	if connected == nil || connected.Path != source.Path || connected.LastLook == nil || connected.LastLook.Items[0].Line != 3 ||
		doc.Ask.State() != secondbrain.AskShown || len(doc.Receipts) != 1 {
		t.Fatalf("read back %+v", doc)
	}

	file := filepath.Join(kf.folder.path, ".ori", secondbrain.RecordFileName)
	info, err := os.Lstat(file)
	if err != nil {
		t.Fatalf("record file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(file) // #nosec G304 -- the test's own temp HQ
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema_version": 1`, `"owner"`, `"sources"`, `"last_look"`, `"ask"`, `"receipts"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("record file lacks %s:\n%s", key, data)
		}
	}
	entries, err := os.ReadDir(filepath.Join(kf.folder.path, ".ori"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".second-brain-") {
			t.Fatalf("a temp file was left behind: %s", entry.Name())
		}
	}
}

func TestSecondBrainStore_ConflictsOnAStaleVersion(t *testing.T) {
	ctx := context.Background()
	store, _ := newSecondBrainStoreFixture(t)
	if _, err := store.Update(ctx, "local", 0, func(doc *SecondBrainDocument) error {
		doc.MarkPrompted("prompt-1", secondBrainNow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "local", 0, func(*SecondBrainDocument) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version err = %v, want ErrConflict", err)
	}
}

func TestSecondBrainStore_ConcurrentWritersNeverLoseAnUpdate(t *testing.T) {
	ctx := context.Background()
	store, _ := newSecondBrainStoreFixture(t)
	const writers = 6
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
				doc.Remember(secondbrain.Receipt{RequestID: fmt.Sprintf("look-%d", i), Action: "look", At: secondBrainNow})
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
	if err != nil || len(doc.Receipts) != writers || doc.Version != writers {
		t.Fatalf("after concurrent writes: %d receipts, version %d, err %v", len(doc.Receipts), doc.Version, err)
	}
}

func TestSecondBrainStore_RefusesAnInvalidRecordAndKeepsTheOldOne(t *testing.T) {
	ctx := context.Background()
	store, _ := newSecondBrainStoreFixture(t)
	if _, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		_, err := doc.Connect(secondBrainSource("/Users/sam/Brain"), false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		doc.Sources[0].Path = "relative/path"
		return nil
	}); !errors.Is(err, secondbrain.ErrInvalidRecord) {
		t.Fatalf("invalid write err = %v", err)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil || doc.Connected().Path != "/Users/sam/Brain" || doc.Version != 1 {
		t.Fatalf("after a refused write = %+v, %v", doc, err)
	}
}

func TestSecondBrainStore_CapsTheRecordSize(t *testing.T) {
	ctx := context.Background()
	store, _ := newSecondBrainStoreFixture(t)
	source := secondBrainSource("/Users/sam/Brain")
	finished := secondBrainNow
	_, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		connected, err := doc.Connect(source, false)
		if err != nil {
			return err
		}
		look := &secondbrain.Look{ID: "look-1", State: secondbrain.LookFinished, StartedAt: secondBrainNow, FinishedAt: &finished}
		for i := range secondbrain.MaxLookItems {
			// "<" is written as a six-byte JSON escape, so 500 of these
			// items pass the record's own bounds and still overflow the file.
			text := fmt.Sprintf("%03d %s", i, strings.Repeat("<", 190))
			look.Items = append(look.Items, secondbrain.LookItem{ID: secondbrain.ItemID(source.ID, "Inbox.md", text), Text: text, Note: "Inbox.md", Line: i + 1})
		}
		connected.LastLook = look
		return nil
	})
	if !errors.Is(err, ErrKnowledgeLimit) {
		t.Fatalf("oversized record err = %v, want ErrKnowledgeLimit", err)
	}
}

func TestDecodeSecondBrain_RefusesUnknownFieldsForeignOwnersAndOtherSchemas(t *testing.T) {
	owner := KnowledgeOwner{UserID: "local", HQWorkspaceID: "hq-local"}
	good := `{"schema_version":1,"version":1,"owner":{"user_id":"local","assistant_id":"","hq_workspace_id":"hq-local","hq_folder_slug":"","entry_agent_instance_id":""},"ask":{}}`
	if _, err := decodeSecondBrain([]byte(good), owner); err != nil {
		t.Fatalf("good document: %v", err)
	}
	for name, data := range map[string]string{
		"unknown field":  strings.Replace(good, `"ask":{}`, `"ask":{},"notes":"the user's notes"`, 1),
		"unknown nested": strings.Replace(good, `"ask":{}`, `"ask":{"path":"/Users/sam"}`, 1),
		"foreign owner":  strings.Replace(good, `"user_id":"local"`, `"user_id":"someone-else"`, 1),
		"schema":         strings.Replace(good, `"schema_version":1`, `"schema_version":2`, 1),
		"version zero":   strings.Replace(good, `"version":1`, `"version":0`, 1),
		"trailing data":  good + `{}`,
		"not json":       `{"schema_version":`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSecondBrain([]byte(data), owner); !errors.Is(err, ErrKnowledgeCorrupt) {
				t.Fatalf("err = %v, want ErrKnowledgeCorrupt", err)
			}
		})
	}
}

func TestSecondBrainStore_NeverFollowsALinkedSidecarFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows store checks the .ori path instead of opening it without following")
	}
	ctx := context.Background()
	store, kf := newSecondBrainStoreFixture(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(kf.folder.path, ".ori")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		doc.MarkPrompted("prompt-1", secondBrainNow)
		return nil
	}); err == nil {
		t.Fatal("the store wrote through a linked .ori folder")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("the link's target gained %d entries", len(entries))
	}
}

func TestSecondBrainStore_RefusesAnotherUsersHQ(t *testing.T) {
	store, _ := newSecondBrainStoreFixture(t)
	if _, err := store.Read(context.Background(), "someone-else"); err == nil {
		t.Fatal("another user's id resolved this HQ's record")
	}
}
