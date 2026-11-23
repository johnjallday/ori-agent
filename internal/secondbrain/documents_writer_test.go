//go:build darwin || linux

package secondbrain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDocumentsProjectExclusivePublicationAndMirrorCollision(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Projects"), 0750); err != nil {
		t.Fatal(err)
	}
	writer := newWriterFor(t, root)
	id, vacant, err := writer.ReviewProject("Thesis")
	if err != nil || !vacant {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	body := []byte("# Thesis\n")
	for _, op := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		wg.Go(func() { out <- writer.PublishDocumentsProject("Thesis", id, op, body) })
	}
	wg.Wait()
	close(out)
	won := 0
	for e := range out {
		if e == nil {
			won++
		} else if !errors.Is(e, ErrNoteExists) {
			t.Fatal(e)
		}
	}
	if won != 1 {
		t.Fatalf("published %d", won)
	}
	wanted := PlannedNote{Ref: MirrorRef{Kind: NoteKindProject, ID: "workspace"}, Kind: NoteKindProject, Rel: "Projects/Thesis/README.md", Body: []byte("workspace refresh")}
	result := Reconcile(writer, nil, []PlannedNote{wanted}, time.Now())
	if result.Err != nil || len(result.Mirrors) != 1 || result.Mirrors[0].State != MirrorUserOwned || result.Written[NoteKindProject] != 0 {
		t.Fatal(result)
	}
	preview, err := ReadPreview(root, PreviewProject, "Thesis")
	if err != nil || preview.Body != string(body) {
		t.Fatal(preview, err)
	}
}
