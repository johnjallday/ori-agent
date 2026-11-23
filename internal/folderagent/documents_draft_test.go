//go:build darwin || linux

package folderagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func TestDocumentsDraftRecoversOnlyProvenPublication(t *testing.T) {
	root := build(t, "Work/Thesis/main.tex")
	marker := filepath.Join(root, "Work/Thesis/main.tex")
	if err := os.WriteFile(marker, []byte("private source text must never appear in the draft"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(marker, 0); err != nil {
		t.Fatal(err)
	} // unreadable body; stat and directory listing still work
	h, id := guidedLook(t, root)
	notes := t.TempDir()
	if err := os.Mkdir(filepath.Join(notes, "Projects"), 0750); err != nil {
		t.Fatal(err)
	}
	source := secondbrain.Source{Name: "My Notes", Path: notes, Origin: secondbrain.OriginCreatedByOri, FillIn: &secondbrain.FillIn{Consent: secondbrain.ConsentAllowed, ProjectAsk: secondbrain.ProjectDeferred}}
	var mu sync.Mutex
	generation := "generation1"
	h.svc.deps.DocumentsNotebook = func(_ context.Context, _, _ string, fn func(secondbrain.Source, string) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(source, generation)
	}
	h.svc.deps.DocumentsScope = func(context.Context, string) (DocumentsScope, error) {
		return DocumentsScope{Writable: true, Notebook: notes}, nil
	}
	ctx := t.Context()
	o, err := h.svc.DocumentsOffer(ctx, "u1", "hq", "attempt", id)
	if err != nil || o == nil {
		t.Fatal(err)
	}
	in := DocumentsDraftInput{HQ: "hq", Attempt: "attempt", Report: o.Report, Candidate: o.Projects[0].Ref, Name: "Thesis", Answer: "Send the methods chapter to my advisor"}
	review, err := h.svc.DocumentsDraft(ctx, "u1", id, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if review.Body != "# Thesis\n\nSend the methods chapter to my advisor\n\nSource: Documents/Work/Thesis\n" {
		t.Fatal("draft was not solely the reviewed metadata and answer")
	}
	in.Review, in.RequestID = review.Review, "save-thesis"
	path := filepath.Join(notes, review.Rel)
	dir := h.svc.deps.Store.sidecarDir
	h.svc.deps.Store.sidecarDir = func(id string) (string, error) {
		if _, err := os.Stat(path); err == nil {
			return "", errors.New("simulated lost receipt")
		}
		return dir(id)
	}
	if _, err := h.svc.DocumentsDraft(ctx, "u1", id, in, true); err == nil {
		t.Fatal("unrecorded completion claimed")
	}
	h.svc.deps.Store.sidecarDir = dir
	restarted := NewService(h.svc.deps)
	raw, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(raw), review.Body) {
		t.Fatal("publication missing", err)
	}
	if _, err := restarted.OpenDocumentsDraft(ctx, "u1", id, "hq", "attempt", in.RequestID); err == nil {
		t.Fatal("uncommitted result exposed")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			v, err := restarted.DocumentsDraft(ctx, "u1", id, in, true)
			if err != nil || !v.Saved || v.Rel != review.Rel {
				t.Errorf("replay %v %v", v, err)
			}
		})
	}
	wg.Wait()
	rec, err := restarted.deps.Store.Read(id)
	if err != nil || len(rec.DocumentsDecisions) != 1 || rec.DocumentsDecisions[0].State != "saved" {
		t.Fatal("result not reconciled", err)
	}
	changed := in
	changed.Answer = "different"
	if _, err := restarted.DocumentsDraft(ctx, "u1", id, changed, true); err == nil {
		t.Fatal("changed payload replayed")
	}
	writer, err := secondbrain.NewWriter(&source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(review.Rel, secondbrain.NoteKindProject, []byte("generated refresh")); !errors.Is(err, secondbrain.ErrUserModified) {
		t.Fatal("ordinary writer replaced protected note", err)
	}
	v, err := restarted.OpenDocumentsDraft(ctx, "u1", id, "hq", "attempt", in.RequestID)
	if err != nil || v.Body != review.Body {
		t.Fatal("incorrect preview", err)
	}
	generation = "generation2"
	if _, err := restarted.OpenDocumentsDraft(ctx, "u1", id, "hq", "attempt", in.RequestID); err == nil {
		t.Fatal("another connection previewed")
	}
	generation = "generation1"
	if err := os.WriteFile(path, append(raw, []byte("user edit")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.DocumentsDraft(ctx, "u1", id, in, true); err == nil {
		t.Fatal("edited note acknowledged")
	}
	if _, err := restarted.OpenDocumentsDraft(ctx, "u1", id, "hq", "attempt", in.RequestID); err == nil {
		t.Fatal("edited preview")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.DocumentsDraft(ctx, "u1", id, in, true); err == nil {
		t.Fatal("deleted note recreated")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
