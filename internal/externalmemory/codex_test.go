package externalmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readCodexFixture(t *testing.T, root string) []Entry {
	t.Helper()
	entries, problems, err := NewCodexSource(root).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("unexpected parse problems: %+v", problems)
	}
	return entries
}

func TestCodexSource_ReadsEveryDocumentedFile(t *testing.T) {
	root := codexFixtureRoot(t)
	entries := readCodexFixture(t, root)
	memories := filepath.Join(root, "memories")

	byFile := map[string]int{}
	for _, e := range entries {
		if e.Source != SourceCodex || e.State != StateActive || e.ID == "" || e.ContentHash == "" {
			t.Fatalf("entry missing identity fields: %+v", e)
		}
		file, _, _ := strings.Cut(e.Origin, "#")
		rel, err := filepath.Rel(memories, file)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("entry origin %q is outside the memories folder", e.Origin)
		}
		byFile[filepath.ToSlash(rel)]++
	}
	want := map[string]int{
		"memory_summary.md": 9, // 2 profile paragraphs, 3 preferences, 2 tips, 2 index bullets
		"MEMORY.md":         9, // group 1: 2 tasks + 5 lessons; group 2: 1 task + 1 lesson
		"raw_memories.md":   2,
		"rollout_summaries/2026-10-05T09-03-33-AbCd-ema_exit_fee_analysis.md": 1,
		"rollout_summaries/2026-08-02T10-00-00-IjKl-notes_release.md":         1,
		"extensions/ad_hoc/notes/20260716T235137Z-demo-and-pr-coherence.md":   1,
	}
	if len(byFile) != len(want) {
		t.Fatalf("files read = %v, want %v", byFile, want)
	}
	for file, n := range want {
		if byFile[file] != n {
			t.Errorf("%s: %d entries, want %d", file, byFile[file], n)
		}
	}
}

func TestCodexSource_SkipsUndocumentedFiles(t *testing.T) {
	for _, e := range readCodexFixture(t, codexFixtureRoot(t)) {
		for _, skipped := range []string{"/skills/", "instructions.md", ".sqlite"} {
			if strings.Contains(e.Origin, skipped) {
				t.Fatalf("read a file it must skip: %s", e.Origin)
			}
		}
	}
}

func TestCodexSource_SummaryFields(t *testing.T) {
	entries := entriesFrom(readCodexFixture(t, codexFixtureRoot(t)), "memory_summary.md")
	assertCategories(t, entries, map[string]int{"user-profile": 2, "user-preferences": 3, "general-tips": 2, "whats-in-memory": 2})
	for _, e := range entries {
		switch e.Category {
		case "user-profile", "user-preferences", "general-tips":
			if e.Folder != "" {
				t.Errorf("profile-grade entry %q has folder %q", e.Title, e.Folder)
			}
		case "whats-in-memory":
			if !strings.HasPrefix(e.Folder, "/Users/example/Projects/") {
				t.Errorf("index bullet %q has folder %q", e.Title, e.Folder)
			}
		}
	}
	ema := find(t, entries, "EMA exit study with fee sensitivity")
	if ema.Folder != "/Users/example/Projects/chart-lab" || ema.Date != "2026-10-14" || !strings.Contains(ema.Body, "learnings: Fees decide") {
		t.Fatalf("index bullet lost its folder, date, or nested lines: %+v", ema)
	}
	notes := find(t, entries, "Notes app release checklist")
	if notes.Folder != "/Users/example/Projects/notes-app" || notes.Date != "" {
		t.Fatalf("older-topic bullet: folder %q date %q", notes.Folder, notes.Date)
	}
	find(t, entries, "결과 보고서는 한국어로")
}

func TestCodexSource_TaskGroupFields(t *testing.T) {
	entries := entriesFrom(readCodexFixture(t, codexFixtureRoot(t)), "MEMORY.md")
	assertCategories(t, entries, map[string]int{"task": 3, "user-preferences": 2, "reusable-knowledge": 3, "failures-and-how-to-do-differently": 1})

	task := find(t, entries, "Task 1: EMA exit comparison with fee sensitivity")
	if task.Folder != "/Users/example/Projects/chart-lab" || task.Category != "task" || task.Date != "2026-10-05" {
		t.Fatalf("task entry: %+v", task)
	}
	if !strings.Contains(task.Body, "### keywords") || strings.Contains(task.Body, "Hourly breakout") {
		t.Fatalf("task body should hold its own subsections only:\n%s", task.Body)
	}
	lesson := find(t, entries, "Exits on an EMA20 close below EMA50")
	if lesson.Folder != "/Users/example/Projects/chart-lab" || lesson.Date != "2026-10-05" {
		t.Fatalf("group lesson should take the group's folder and latest date: %+v", lesson)
	}
	release := find(t, entries, "Notarization needs the hardened runtime")
	if release.Folder != "/Users/example/Projects/notes-app" || release.Date != "2026-08-02" {
		t.Fatalf("second group lesson: %+v", release)
	}
}

func TestCodexSource_ThreadRolloutAndNoteFields(t *testing.T) {
	all := readCodexFixture(t, codexFixtureRoot(t))

	thread := find(t, entriesFrom(all, "raw_memories.md"), "Compared EMA exits against fixed stops")
	if thread.Category != "thread" || thread.Folder != "/Users/example/Projects/chart-lab" || thread.Date != "2026-10-05" {
		t.Fatalf("thread entry: %+v", thread)
	}
	if !strings.Contains(thread.Body, "fixed stop paid fees") || strings.Contains(thread.Body, "notarized") {
		t.Fatalf("thread body should hold exactly its own block:\n%s", thread.Body)
	}

	rollout := find(t, entriesFrom(all, "ema_exit_fee_analysis.md"), "EMA exit and fee sensitivity analysis")
	if rollout.Category != "summary" || rollout.Folder != "/Users/example/Projects/chart-lab" || rollout.Date != "2026-10-05" {
		t.Fatalf("rollout entry: %+v", rollout)
	}
	if strings.Contains(rollout.Body, "rollout_path:") || !strings.Contains(rollout.Body, "ranked first after fees") {
		t.Fatalf("rollout body should start at its heading:\n%s", rollout.Body)
	}

	note := find(t, entriesFrom(all, "demo-and-pr-coherence.md"), "Memory update: demo and PR workflow")
	if note.Category != "note" || note.Folder != "" || note.Date != "2026-07-16" {
		t.Fatalf("note entry: %+v", note)
	}
}

func TestCodexSource_IDsAreStableAndUnique(t *testing.T) {
	root := codexFixtureRoot(t)
	first, second := readCodexFixture(t, root), readCodexFixture(t, root)
	if len(first) != len(second) {
		t.Fatalf("two parses returned %d and %d entries", len(first), len(second))
	}
	seen := map[string]string{}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].ContentHash != second[i].ContentHash {
			t.Fatalf("entry %d changed between parses: %s vs %s", i, first[i].ID, second[i].ID)
		}
		if prev, dup := seen[first[i].ID]; dup {
			t.Fatalf("duplicate id %s for %q and %q", first[i].ID, prev, first[i].Title)
		}
		seen[first[i].ID] = first[i].Title
	}
}

func TestCodexSource_InsertedBulletKeepsOtherIDs(t *testing.T) {
	root := copyFixture(t, codexFixtureRoot(t))
	before := idsByTitle(entriesFrom(readCodexFixture(t, root), "memory_summary.md"))

	path := filepath.Join(root, "memories", "memory_summary.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "## User preferences\n\n", "## User preferences\n\n- A brand new first preference.\n", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	after := idsByTitle(entriesFrom(readCodexFixture(t, root), "memory_summary.md"))
	for title, id := range before {
		if after[title] != id {
			t.Fatalf("inserting a bullet changed the id of %q", title)
		}
	}
	if len(after) != len(before)+1 {
		t.Fatalf("after insert: %d entries, want %d", len(after), len(before)+1)
	}
}

func TestCodexSource_AvailabilityAndMissingRoot(t *testing.T) {
	source := NewCodexSource(filepath.Join(t.TempDir(), "absent"))
	if source.Available() {
		t.Fatal("absent root reported available")
	}
	if !strings.HasSuffix(source.Location(), filepath.Join("absent", "memories")) {
		t.Fatalf("location = %q", source.Location())
	}
	if _, _, err := source.Read(context.Background()); err == nil {
		t.Fatal("reading an absent root should fail the source")
	}
	t.Setenv("CODEX_HOME", "/tmp/codex-home-override")
	if DefaultCodexRoot() != "/tmp/codex-home-override" {
		t.Fatal("CODEX_HOME not honored")
	}
}

func TestCodexSource_UnreadableFileIsReportedNotFatal(t *testing.T) {
	root := copyFixture(t, codexFixtureRoot(t))
	// A directory where a file is expected cannot be read as markdown.
	bad := filepath.Join(root, "memories", "rollout_summaries", "broken.md")
	if err := os.MkdirAll(bad, 0o750); err != nil {
		t.Fatal(err)
	}
	entries, problems, err := NewCodexSource(root).Read(context.Background())
	if err != nil {
		t.Fatalf("one bad file failed the source: %v", err)
	}
	if len(problems) != 1 || problems[0].Path != bad {
		t.Fatalf("problems = %+v, want the broken file", problems)
	}
	if len(entries) != 23 {
		t.Fatalf("entries = %d, want the 23 good ones", len(entries))
	}
}

// --- helpers shared by the source tests ---

func entriesFrom(entries []Entry, fileSuffix string) []Entry {
	var out []Entry
	for _, e := range entries {
		file, _, _ := strings.Cut(e.Origin, "#")
		if strings.HasSuffix(file, fileSuffix) {
			out = append(out, e)
		}
	}
	return out
}

func find(t *testing.T, entries []Entry, titlePrefix string) Entry {
	t.Helper()
	for _, e := range entries {
		if strings.HasPrefix(e.Title, titlePrefix) {
			return e
		}
	}
	var titles []string
	for _, e := range entries {
		titles = append(titles, e.Title)
	}
	t.Fatalf("no entry titled %q among %q", titlePrefix, titles)
	return Entry{}
}

func assertCategories(t *testing.T, entries []Entry, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, e := range entries {
		got[e.Category]++
	}
	if len(got) != len(want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	for category, n := range want {
		if got[category] != n {
			t.Fatalf("categories = %v, want %v", got, want)
		}
	}
}

func idsByTitle(entries []Entry) map[string]string {
	out := map[string]string{}
	for _, e := range entries {
		out[e.Title] = e.ID
	}
	return out
}

// copyFixture copies a fixture tree into a temp folder so a test can edit it.
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
