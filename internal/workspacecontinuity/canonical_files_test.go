package workspacecontinuity

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalFilePublicationIsConfinedAndConditional(t *testing.T) {
	root := t.TempDir()
	ctx := t.Context()
	path := "agents/guide/config.json"
	first := []byte(`{"Settings":{"model":"offline"}}`)
	if err := ReplaceCanonicalFile(ctx, root, path, "", first); err != nil {
		t.Fatal(err)
	}
	read, err := ReadCanonicalFile(ctx, root, path, MaxRecordBytes)
	if err != nil || !bytes.Equal(read, first) {
		t.Fatalf("initial publication: %v", err)
	}
	for _, expected := range []string{"", Digest([]byte("stale"))} {
		if err := ReplaceCanonicalFile(ctx, root, path, expected, []byte("bad")); !errors.Is(err, ErrChanged) {
			t.Fatalf("unexpected overwrite: %v", err)
		}
	}
	second := []byte(`{"Settings":{"model":"another-offline"}}`)
	if err := ReplaceCanonicalFile(ctx, root, path, Digest(first), second); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, path))
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("projection file is not owner-private")
	}
	info, err = os.Stat(filepath.Join(root, "agents", "guide"))
	if err != nil || info.Mode().Perm()&0027 != 0 {
		t.Fatal("new projection directory has excessive permissions")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ReplaceCanonicalFile(canceled, root, path, Digest(second), first); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication succeeded")
	}
	for _, path := range []string{"../outside", "/absolute", ".ori/continuity/current.json", "sub_workspaces/child/workspace.json"} {
		if err := ReplaceCanonicalFile(ctx, root, path, "", first); err == nil {
			t.Fatal("accepted unsafe canonical path")
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "files")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ReplaceCanonicalFile(ctx, root, "files/foreign/config.json", "", first); err == nil {
		t.Fatal("followed intermediate symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside workspace")
	}
	if err := os.Symlink(filepath.Join(root, path), filepath.Join(root, "workspace.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalFile(ctx, root, "workspace.json", MaxChunkBytes); err == nil {
		t.Fatal("read a symlink")
	}
	if err := ReplaceCanonicalFile(ctx, root, "workspace.json", Digest(second), first); err == nil {
		t.Fatal("replaced a symlink")
	}
	entries, err = os.ReadDir(filepath.Join(root, "agents", "guide"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ori-config-") {
			t.Fatal("retained staging file after publication")
		}
	}
}

func TestReplaceCanonicalFileInRootCannotBeRedirectedByStagePathSwap(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "private-stage")
	detached := filepath.Join(parent, "detached-stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.Rename(stage, detached); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("private staged data")
	if err := ReplaceCanonicalFileInRoot(t.Context(), root, "notes/story.md", "", payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCanonicalFile(t.Context(), detached, "notes/story.md", MaxRecordBytes)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("pinned stage lost bytes", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "notes")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replacement stage received a redirected write", err)
	}
	if err := ReplaceCanonicalFileInRoot(t.Context(), root, "notes/story.md", "", []byte("overwrite")); !errors.Is(err, ErrChanged) {
		t.Fatal("occupied stage was overwritten", err)
	}
	if err := ReplaceCanonicalFileInRoot(t.Context(), root, "../escape", "", payload); !errors.Is(err, ErrInvalid) {
		t.Fatal("unconfined path was accepted", err)
	}
}

func TestCanonicalFileAndDocumentBounds(t *testing.T) {
	root := t.TempDir()
	if err := ReplaceCanonicalFile(t.Context(), root, "workspace.json", "", []byte(`{"id":"stable"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalFile(t.Context(), root, "workspace.json", 2); !errors.Is(err, ErrLimit) {
		t.Fatalf("unbounded read: %v", err)
	}
	type document struct {
		ID      string `json:"id"`
		Allowed *bool  `json:"allowed"`
	}
	for _, data := range []string{`{"id":"a","id":"b"}`, `{"ID":"a"}`, `{"id":null}`, `{"id":"a","extra":true}`} {
		var got document
		if err := DecodeDocument([]byte(data), &got, MaxRecordBytes); err == nil {
			t.Fatal("accepted ambiguous document")
		}
	}
	var got document
	if err := DecodeRequiredDocument([]byte(`{"id":"a"}`), &got, MaxRecordBytes); err == nil {
		t.Fatal("invented a missing nullable grant choice")
	}
	if err := DecodeRequiredDocument([]byte(`{"id":"a","allowed":null}`), &got, MaxRecordBytes); err != nil {
		t.Fatal(err)
	}
}
