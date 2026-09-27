package projectlibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover_RecognizesOneFolderPerProjectWithoutReadingContents(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "discovery-root")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree.single, filepath.Join(tree.root, "Duplicate link")); err != nil {
		t.Fatal(err)
	}
	result, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || result.PartialReason != "" || result.SkippedLinks != 1 || len(result.Candidates) != 4 {
		t.Fatalf("discovery: %+v %v", result, err)
	}
	byFolder := make(map[string]Candidate)
	for _, candidate := range result.Candidates {
		byFolder[candidate.RelativeFolder] = candidate
		if candidate.FileIdentity == "" {
			t.Fatalf("candidate lacks pinned file identity: %+v", candidate)
		}
	}
	if _, present := byFolder["Empty"]; present {
		t.Fatal("empty folder became a project")
	}
	if byFolder["Single"].Format != "reaper" || byFolder["Single"].Ambiguous ||
		len(byFolder["Single"].Alternates) != 1 || byFolder["Single"].Alternates[0] != "Song.rpp" {
		t.Fatalf("single song: %+v", byFolder["Single"])
	}
	if byFolder["Alternates"].Format != "reaper" || !byFolder["Alternates"].Ambiguous ||
		len(byFolder["Alternates"].Alternates) != 2 {
		t.Fatalf("alternates or backup was misidentified: %+v", byFolder["Alternates"])
	}
	if byFolder["Ableton"].Format != "ableton" || byFolder["Logic"].Format != "logic" {
		t.Fatalf("unsupported DAWs lost: %+v", byFolder)
	}
	if after := fileDigest(t, filepath.Join(tree.single, "Song.rpp")); after != before {
		t.Fatal("discovery changed source bytes")
	}
	revoke, err := r.ReviewRevoke(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitRevoke(scope, root.ID, revoke.Token, "stop-discovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Discover(context.Background(), scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("discovery after revoke: %v", err)
	}
}

func TestDiscover_BundleAndBackupsAreNotAdditionalProjects(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.logic)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "logic-bundle")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || len(result.Candidates) != 1 || result.Candidates[0].RelativeFolder != "" || result.Candidates[0].Format != "logic" {
		t.Fatalf("bundle should be one root candidate: %+v %v", result, err)
	}
	for _, row := range []DirectoryRow{
		{Name: "Song.rpp-bak"}, {Name: "FakeDirectory.rpp", IsDir: true},
		{Name: "FakeDirectory.als", IsDir: true}, {Name: "Hidden.rpp", Unreadable: true},
		{Name: ".Secret.rpp"}, {Name: "Cloud.rpp.icloud"},
	} {
		candidate, ok := recognizedProject("Backup", "identity", []DirectoryRow{row})
		if ok {
			t.Fatalf("backup/unreadable/hidden/cloud placeholder became a song: %+v", candidate)
		}
	}
}

func TestDiscover_CancelledBeforeScanReturnsNoResults(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	picker.path, _ = filepath.EvalSymlinks(newMusicTree(t).root)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "cancel-root")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := r.Discover(ctx, scope, root.ID); !errors.Is(err, context.Canceled) || len(result.Candidates) != 0 {
		t.Fatalf("cancelled scan: %+v %v", result, err)
	}
}
