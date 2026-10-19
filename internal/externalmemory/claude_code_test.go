package externalmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "claude_code"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClaudeCodeSource_ReadsOneEntryPerMemoryFile(t *testing.T) {
	entries, problems, err := NewClaudeCodeSource(claudeFixtureRoot(t)).Read(context.Background())
	if err != nil || len(problems) != 0 {
		t.Fatalf("Read: err=%v problems=%+v", err, problems)
	}
	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6 (MEMORY.md and the temp-root session skipped)", len(entries))
	}
	assertCategories(t, entries, map[string]int{"user": 1, "feedback": 1, "project": 3, "reference": 1})
	for _, e := range entries {
		if strings.Contains(e.Origin, "MEMORY.md") || strings.Contains(e.Origin, "scratch") {
			t.Fatalf("read a file it must skip: %s", e.Origin)
		}
		if e.Source != SourceClaudeCode || e.Date == "" {
			t.Fatalf("entry missing source or date: %+v", e)
		}
	}

	user := find(t, entries, "user-role")
	if user.Category != "user" || user.Date != "2026-10-12" || !strings.HasPrefix(user.Body, "The user builds trading research tools") {
		t.Fatalf("user entry: %+v", user)
	}
	if !strings.Contains(user.Body, "Solo developer") {
		t.Fatal("body after the front matter is missing")
	}
	feedback := find(t, entries, "feedback-net-results")
	if feedback.Category != "feedback" || feedback.Date != "2026-10-14" {
		t.Fatalf("flat front matter not read: %+v", feedback)
	}
	if untyped := find(t, entries, "untyped-gap-handling"); untyped.Category != "project" {
		t.Fatalf("missing type should mean project, got %q", untyped.Category)
	}
	layout := find(t, entries, "project-backtest-layout")
	if !strings.Contains(layout.Body, "결과 파일은") {
		t.Fatal("Korean body text lost")
	}
}

func TestClaudeCodeSource_UndecodableProjectKeyKeepsTheRawKey(t *testing.T) {
	entries, _, _ := NewClaudeCodeSource(claudeFixtureRoot(t)).Read(context.Background())
	for _, e := range entries {
		// /Users/example/... does not exist on a test machine.
		if !e.FolderUnverified || !strings.HasPrefix(e.Folder, "-Users-example-Projects-") {
			t.Fatalf("entry %q: folder %q unverified=%v", e.Title, e.Folder, e.FolderUnverified)
		}
	}
}

func TestClaudeCodeSource_DecodesAProjectKeyThatExistsOnDisk(t *testing.T) {
	work := filepath.Join(t.TempDir(), "Work", "ori-agent")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	work, _ = filepath.EvalSymlinks(work)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "projects", encodeProjectKey(work), "memory", "fact.md"),
		"---\nname: a-fact\nmetadata:\n  type: user\n---\nBody.\n")

	entries, problems, err := NewClaudeCodeSource(root).WithTempRoots().Read(context.Background())
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("entries=%d problems=%+v err=%v", len(entries), problems, err)
	}
	if entries[0].Folder != work || entries[0].FolderUnverified {
		t.Fatalf("folder = %q (unverified %v), want %q", entries[0].Folder, entries[0].FolderUnverified, work)
	}
	// With the default temp roots the same key is a scratch session.
	if entries, _, _ := NewClaudeCodeSource(root).Read(context.Background()); len(entries) != 0 {
		t.Fatalf("a project under the temp folder should be skipped, got %d entries", len(entries))
	}
}

func TestDecodeProjectKey(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		exists []string
		want   string
		ok     bool
	}{
		{
			name:   "dash inside a folder name",
			key:    "-Users-me-Projects-ori-agent",
			exists: []string{"/Users", "/Users/me", "/Users/me/Projects", "/Users/me/Projects/ori-agent"},
			want:   "/Users/me/Projects/ori-agent", ok: true,
		},
		{
			name:   "dash as a separator",
			key:    "-Users-me-Projects-ori-agent",
			exists: []string{"/Users", "/Users/me", "/Users/me/Projects", "/Users/me/Projects/ori", "/Users/me/Projects/ori/agent"},
			want:   "/Users/me/Projects/ori/agent", ok: true,
		},
		{
			name: "longest name wins when both exist",
			key:  "-Users-me-Projects-ori-agent",
			exists: []string{"/Users", "/Users/me", "/Users/me/Projects", "/Users/me/Projects/ori-agent",
				"/Users/me/Projects/ori", "/Users/me/Projects/ori/agent"},
			want: "/Users/me/Projects/ori-agent", ok: true,
		},
		{
			name:   "a leading dot shows as an empty piece",
			key:    "-Users-me--claude-projects",
			exists: []string{"/Users", "/Users/me", "/Users/me/.claude", "/Users/me/.claude/projects"},
			want:   "/Users/me/.claude/projects", ok: true,
		},
		{
			name:   "underscore and space names",
			key:    "-Users-me-my-app-My-Docs",
			exists: []string{"/Users", "/Users/me", "/Users/me/my_app", "/Users/me/my_app/My Docs"},
			want:   "/Users/me/my_app/My Docs", ok: true,
		},
		{
			name:   "korean folder names",
			key:    "-Users-me-연구-비트코인",
			exists: []string{"/Users", "/Users/me", "/Users/me/연구", "/Users/me/연구/비트코인"},
			want:   "/Users/me/연구/비트코인", ok: true,
		},
		{
			// A temp folder name mixes a dash and a dot inside one name.
			name:   "mixed characters inside one name",
			key:    "-private-var-T-ori-test-run-gIURPp-go-tmp-TestX-1-001",
			exists: []string{"/private", "/private/var", "/private/var/T", "/private/var/T/ori-test-run.gIURPp", "/private/var/T/ori-test-run.gIURPp/go-tmp", "/private/var/T/ori-test-run.gIURPp/go-tmp/TestX_1", "/private/var/T/ori-test-run.gIURPp/go-tmp/TestX_1/001"},
			want:   "/private/var/T/ori-test-run.gIURPp/go-tmp/TestX_1/001", ok: true,
		},
		{
			name:   "backtracks when the longest name leads nowhere",
			key:    "-Users-me-a-b-c",
			exists: []string{"/Users", "/Users/me", "/Users/me/a-b", "/Users/me/a", "/Users/me/a/b-c"},
			want:   "/Users/me/a/b-c", ok: true,
		},
		{name: "nothing exists", key: "-Users-me-gone", exists: []string{"/Users"}},
		{name: "not a key", key: "Users-me", exists: []string{"/Users"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := func(dir string) []string {
				var names []string
				for _, p := range tc.exists {
					if filepath.Dir(p) == dir {
						names = append(names, filepath.Base(p))
					}
				}
				return names
			}
			got, ok := decodeProjectKey(tc.key, list)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("decodeProjectKey(%q) = %q, %v; want %q, %v", tc.key, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestIsTempProjectKey(t *testing.T) {
	roots := []string{"/private/tmp", "/private/var/folders", "/var/folders/0t/abc/T"}
	cases := map[string]bool{
		"-private-tmp-claude-502-scratch":     true,
		"-private-tmp":                        true,
		"-private-var-folders-0t-x-T-sandbox": true,
		"-var-folders-0t-abc-T-ori-demo":      true,
		"-Users-me-Projects-crypto":           false,
		"-private-tmpfoo":                     false,
	}
	for key, want := range cases {
		if got := isTempProjectKey(key, roots); got != want {
			t.Errorf("isTempProjectKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestClaudeCodeSource_BadFrontMatterSkipsOnlyThatFile(t *testing.T) {
	root := copyFixture(t, claudeFixtureRoot(t))
	bad := filepath.Join(root, "projects", "-Users-example-Projects-chart-lab", "memory", "broken.md")
	mustWrite(t, bad, "---\nname: [unclosed\n---\nbody\n")
	entries, problems, err := NewClaudeCodeSource(root).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Path != bad || problems[0].Reason != "invalid front matter" {
		t.Fatalf("problems = %+v", problems)
	}
	if len(entries) != 6 {
		t.Fatalf("entries = %d, want the 6 good ones", len(entries))
	}
}

func TestSplitFrontMatter(t *testing.T) {
	cases := []struct{ in, front, body string }{
		{"---\na: 1\n---\nbody\n", "a: 1", "body"},
		{"no front matter\n---\nstill body", "", "no front matter\n---\nstill body"},
		{"---\nunclosed: true\nbody", "", "---\nunclosed: true\nbody"},
	}
	for _, tc := range cases {
		front, body, err := splitFrontMatter(strings.NewReader(tc.in))
		if err != nil || front != tc.front || strings.TrimSpace(body) != tc.body {
			t.Errorf("splitFrontMatter(%q) = %q, %q, %v", tc.in, front, body, err)
		}
	}
}

func TestClaudeCodeSource_RootFromEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-config-override")
	if DefaultClaudeCodeRoot() != "/tmp/claude-config-override" {
		t.Fatal("CLAUDE_CONFIG_DIR not honored")
	}
	source := NewClaudeCodeSource(filepath.Join(t.TempDir(), "absent"))
	if source.Available() || !strings.HasSuffix(source.Location(), "projects") {
		t.Fatalf("absent root: available=%v location=%q", source.Available(), source.Location())
	}
}
