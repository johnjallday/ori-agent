package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func addTestKnowledgeCandidate(doc *KnowledgeDocument) error {
	doc.Items = append(doc.Items, KnowledgeItem{
		ID: "item-1", Version: 1, State: KnowledgeCandidate,
		SemanticKey: "saved-app/v1/example", Category: "how_you_work",
		Revisions: []KnowledgeRevision{{ID: "revision-1", Text: "Review a short list before changing files."}},
	})
	return nil
}

func TestKnowledgeStoreDistinguishesMissingCorruptAndForeignMetadata(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	store := NewKnowledgeStore(f.resolver(), f.folder)
	missing, err := store.Read(ctx, "local")
	if err != nil || missing.Present || missing.Version != 0 {
		t.Fatalf("missing sidecar=%+v err=%v", missing, err)
	}
	if _, err := store.Update(ctx, "local", 0, addTestKnowledgeCandidate); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.folder.path, ".ori", knowledgeFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("sidecar mode=%v", info.Mode())
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("directory mode=%v", info.Mode())
	}

	original, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"invalid json", []byte("{broken")},
		{"unknown schema", []byte(`{"schema_version":999,"version":1}`)},
		{"foreign owner", []byte(`{"schema_version":1,"version":1,"owner":{"user_id":"foreign","assistant_id":"assistant-a","hq_workspace_id":"hq-local","hq_folder_slug":"personal-hq","entry_agent_instance_id":"instance-local"}}`)},
		{"trailing payload", append(append([]byte(nil), original...), []byte(" {}")...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(ctx, "local"); !errors.Is(err, ErrKnowledgeCorrupt) {
				t.Fatalf("malformed metadata error=%v", err)
			}
			if _, err := store.Update(ctx, "local", 1, func(*KnowledgeDocument) error { return nil }); !errors.Is(err, ErrKnowledgeCorrupt) {
				t.Fatalf("corrupt metadata was overwritten: %v", err)
			}
			data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
			if err != nil || string(data) != string(test.data) {
				t.Fatalf("corruption was discarded: %q %v", data, err)
			}
		})
	}
}

func TestKnowledgeStoreRejectsSymlinkedDirectoryAndFile(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.folder.path, ".ori")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store := NewKnowledgeStore(f.resolver(), f.folder)
	if _, err := store.Update(ctx, "local", 0, addTestKnowledgeCandidate); err == nil {
		t.Fatal("wrote knowledge through symlinked .ori directory")
	}
	if _, err := os.Stat(filepath.Join(outside, knowledgeFileName)); !os.IsNotExist(err) {
		t.Fatalf("foreign directory was written: %v", err)
	}
	if err := os.Remove(filepath.Join(f.folder.path, ".ori")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.folder.path, ".ori"), 0o750); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(outside, "do-not-edit.json")
	if err := os.WriteFile(foreign, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(f.folder.path, ".ori", knowledgeFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "local", 0, addTestKnowledgeCandidate); err == nil {
		t.Fatal("replaced symlinked knowledge file")
	}
	data, err := os.ReadFile(foreign) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != "untouched" {
		t.Fatalf("foreign file changed: %q %v", data, err)
	}
}

func TestKnowledgeStoreCrossInstanceCASAndBounds(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	first := NewKnowledgeStore(f.resolver(), f.folder)
	second := NewKnowledgeStore(f.resolver(), f.folder)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, store := range []*KnowledgeStore{first, second} {
		group.Add(1)
		go func(store *KnowledgeStore) {
			defer group.Done()
			<-start
			_, err := store.Update(ctx, "local", 0, addTestKnowledgeCandidate)
			results <- err
		}(store)
	}
	close(start)
	group.Wait()
	close(results)
	var winners, conflicts int
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	if _, err := second.Update(ctx, "local", 1, func(doc *KnowledgeDocument) error {
		doc.Items = make([]KnowledgeItem, knowledgeMaxItems+1)
		return nil
	}); !errors.Is(err, ErrKnowledgeLimit) {
		t.Fatalf("overflow error=%v", err)
	}
	doc, err := first.Read(ctx, "local")
	if err != nil || doc.Version != 1 || len(doc.Items) != 1 {
		t.Fatalf("overflow changed durable data: %+v, %v", doc, err)
	}
}
