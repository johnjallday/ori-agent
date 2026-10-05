package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// Inert gitfile/commondir fixtures exercise the extraction identity contract
// without creating, registering or removing any actual Git worktrees.
func TestExtractionLinkedMetadataKeepsTargetAndRuntimeSeparate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	common := filepath.Join(root, "ori source", ".git")
	runtimeRoot := filepath.Join(root, "stable runtime")
	var resolved []Paths
	for _, name := range []string{"ori dev", "ori feature"} {
		target := filepath.Join(root, name)
		metadata := filepath.Join(common, "worktrees", name)
		for _, dir := range []string{target, metadata} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]string{
			filepath.Join(target, ".git"):        "gitdir: " + metadata + "\n",
			filepath.Join(metadata, "commondir"): "../..\n",
		}
		for path, text := range files {
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		paths, err := Resolve(target, func(key string) (string, bool) {
			return runtimeRoot, key == HomeOverrideEnv
		})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != filepath.Join(paths.RepoRoot, ".herdr", "devflow.toml") {
			t.Fatalf("config escaped target: %#v", paths)
		}
		resolved = append(resolved, paths)
	}
	first, second := resolved[0], resolved[1]
	if first.RepoRoot == second.RepoRoot || first.ConfigPath == second.ConfigPath {
		t.Fatal("linked targets must retain separate roots and config paths")
	}
	if first.RepositoryID != second.RepositoryID || first.GitCommonDir != second.GitCommonDir {
		t.Fatal("linked targets must share the same canonical Git identity")
	}
	if first.RuntimeRoot != second.RuntimeRoot || first.HelperPath != second.HelperPath {
		t.Fatal("linked targets must share the explicitly selected stable runtime")
	}
	if _, err := os.Stat(runtimeRoot); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created runtime state: %v", err)
	}
}
