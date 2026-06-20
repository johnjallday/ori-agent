package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeParseGitSubdirRoundTrip(t *testing.T) {
	cases := []struct {
		name                       string
		url, subdir, ref, sha      string
		wantOK                     bool
		wantSubdir, wantRef, wantS string
	}{
		{name: "subdir+sha", url: "https://github.com/a/b.git", subdir: "plugins/x", sha: "abc123", wantOK: true, wantSubdir: "plugins/x", wantS: "abc123"},
		{name: "subdir+ref", url: "https://github.com/a/b.git", subdir: "plugins/x", ref: "v1.2.3", wantOK: true, wantSubdir: "plugins/x", wantRef: "v1.2.3"},
		{name: "whole-repo pinned", url: "https://github.com/a/b.git", ref: "v1", wantOK: true, wantRef: "v1"},
		{name: "plain url has no fragment", url: "https://github.com/a/b.git", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := encodeGitSubdir(tc.url, tc.subdir, tc.ref, tc.sha)
			g, ok := parseGitSubdir(enc)
			if ok != tc.wantOK {
				t.Fatalf("parse ok = %v, want %v (enc=%q)", ok, tc.wantOK, enc)
			}
			if !tc.wantOK {
				if enc != tc.url {
					t.Errorf("plain encode = %q, want %q", enc, tc.url)
				}
				return
			}
			if g.URL != tc.url {
				t.Errorf("url = %q, want %q", g.URL, tc.url)
			}
			if g.Subdir != tc.wantSubdir {
				t.Errorf("subdir = %q, want %q", g.Subdir, tc.wantSubdir)
			}
			if g.Ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", g.Ref, tc.wantRef)
			}
			if g.Sha != tc.wantS {
				t.Errorf("sha = %q, want %q", g.Sha, tc.wantS)
			}
		})
	}
}

func TestParseGitSubdirIgnoresPlainSource(t *testing.T) {
	for _, s := range []string{"./plugins/x", "/abs/path", "https://github.com/a/b.git"} {
		if _, ok := parseGitSubdir(s); ok {
			t.Errorf("parseGitSubdir(%q) = ok, want not ok", s)
		}
	}
}

func TestResolveEntrySource(t *testing.T) {
	cat := t.TempDir()
	// relative path joins the catalog dir
	if got := resolveEntrySource(cat, "./plugins/x"); got != filepath.Join(cat, "plugins", "x") {
		t.Errorf("relative = %q, want %q", got, filepath.Join(cat, "plugins", "x"))
	}
	// git URL is returned as-is (not joined)
	if got := resolveEntrySource(cat, "https://github.com/a/b.git"); got != "https://github.com/a/b.git" {
		t.Errorf("git url = %q", got)
	}
	// composite git-subdir (https) is returned as-is
	comp := encodeGitSubdir("https://github.com/a/b.git", "plugins/x", "v1", "")
	if got := resolveEntrySource(cat, comp); got != comp {
		t.Errorf("composite = %q, want %q", got, comp)
	}
}

// gitInitPluginRepo builds a real git repo containing a Claude plugin under
// plugins/demo, commits it, and returns the repo dir and the HEAD sha. The repo
// is configured to serve arbitrary shas so a pinned fetch works over the local
// transport.
func gitInitPluginRepo(t *testing.T) (repoDir, sha string) {
	t.Helper()
	repoDir = t.TempDir()
	writeFile(t, filepath.Join(repoDir, "plugins", "demo", ".claude-plugin", "plugin.json"), `{"name":"demo","version":"0.1.0"}`)
	writeFile(t, filepath.Join(repoDir, "plugins", "demo", ".mcp.json"), `{"demo-mcp":{"command":"/usr/bin/true"}}`)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "uploadpack.allowAnySHA1InWant", "true")
	run("config", "uploadpack.allowReachableSHA1InWant", "true")
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return repoDir, strings.TrimSpace(string(out))
}

func TestResolveSourceGitSubdirPinned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, sha := gitInitPluginRepo(t)

	src := encodeGitSubdir(repo, "plugins/demo", "", sha)
	root, err := ResolveSource(src, t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(root) != "demo" {
		t.Errorf("root = %q, want .../demo", root)
	}
	if !fileExists(filepath.Join(root, ".claude-plugin", "plugin.json")) {
		t.Errorf("manifest missing under %q", root)
	}
}

func TestResolveSourceGitWholeRepoPinned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, sha := gitInitPluginRepo(t)

	// No subdir: a pinned whole-repo source resolves to the repo root.
	src := encodeGitSubdir(repo, "", "", sha)
	root, err := ResolveSource(src, t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !fileExists(filepath.Join(root, "plugins", "demo", ".claude-plugin", "plugin.json")) {
		t.Errorf("repo root not resolved: %q", root)
	}
}

func TestResolveSourceGitSubdirMissingSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, sha := gitInitPluginRepo(t)

	src := encodeGitSubdir(repo, "plugins/does-not-exist", "", sha)
	if _, err := ResolveSource(src, t.TempDir()); err == nil {
		t.Error("expected error for missing subdirectory")
	}
}
