package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type staticFolderResolver struct {
	folders map[string]string
}

func (r staticFolderResolver) GetFolderPath(workspaceID string) (string, error) {
	p, ok := r.folders[workspaceID]
	if !ok {
		return "", fmt.Errorf("workspace %s not found", workspaceID)
	}
	return p, nil
}

func newTestMemoryStore(t *testing.T) (*MemoryStore, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewMemoryStore(staticFolderResolver{folders: map[string]string{"ws1": dir}})
	return store, dir
}

func writeTestMemoryFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, MemoryFileName), []byte(content), 0644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}
}

const handEditedMemory = `# Workspace Memory

Some hand-written context the user added.

- [fact, 2026-06-01, user] Releases are human-triggered from main.
- [mystery, 2026-06-02, run:a1b2] Unknown type degrades to fact.
- [watch, 2026-06-11, run:g8h9 (Scout)] Checked through commit 95c10b3.
- a plain bullet without the entry shape
- [fact, not-a-date, user] bad date means unstructured
`

func TestParseMemoryDocument_RoundTripPreservesContent(t *testing.T) {
	doc := ParseMemoryDocument(handEditedMemory)
	if got := doc.Render(); got != handEditedMemory {
		t.Fatalf("round-trip changed content:\n--- got ---\n%s\n--- want ---\n%s", got, handEditedMemory)
	}
}

func TestParseMemoryDocument_EntriesAndUnstructured(t *testing.T) {
	doc := ParseMemoryDocument(handEditedMemory)

	entries := doc.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Type != MemoryTypeFact || entries[0].Provenance != "user" {
		t.Errorf("entry 0 parsed wrong: %+v", entries[0])
	}
	if entries[1].Type != MemoryTypeFact {
		t.Errorf("unknown type should degrade to fact, got %q", entries[1].Type)
	}
	if entries[2].Type != MemoryTypeWatch || entries[2].Provenance != "run:g8h9 (Scout)" {
		t.Errorf("entry 2 parsed wrong: %+v", entries[2])
	}

	unstructured := doc.UnstructuredLines()
	wantUnstructured := []string{
		"# Workspace Memory",
		"Some hand-written context the user added.",
		"- a plain bullet without the entry shape",
		"- [fact, not-a-date, user] bad date means unstructured",
	}
	if len(unstructured) != len(wantUnstructured) {
		t.Fatalf("expected %d unstructured lines, got %d: %q", len(wantUnstructured), len(unstructured), unstructured)
	}
	for i, want := range wantUnstructured {
		if unstructured[i] != want {
			t.Errorf("unstructured[%d] = %q, want %q", i, unstructured[i], want)
		}
	}
}

func TestParseMemoryDocument_Empty(t *testing.T) {
	doc := ParseMemoryDocument("")
	if doc.Render() != "" {
		t.Errorf("empty content should render empty, got %q", doc.Render())
	}
	if len(doc.Entries()) != 0 {
		t.Errorf("empty content should have no entries")
	}
}

func TestMemoryStore_ReadMissingFile(t *testing.T) {
	store, _ := newTestMemoryStore(t)
	doc, err := store.Read("ws1")
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(doc.Entries()) != 0 {
		t.Errorf("missing file should parse as empty memory")
	}
}

func TestMemoryStore_ReadUnknownWorkspace(t *testing.T) {
	store, _ := newTestMemoryStore(t)
	if _, err := store.Read("nope"); err == nil {
		t.Fatal("unknown workspace should error")
	}
}

func TestMemoryStore_AppendLazyCreatesWithHeader(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	entry := MemoryEntry{Type: MemoryTypeFact, Date: "2026-06-12", Provenance: "user", Text: "staging is at https://stage.example.com"}
	if err := store.Append("ws1", entry); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, MemoryFileName))
	if err != nil {
		t.Fatalf("memory file should exist after first append: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "# Workspace Memory\n") {
		t.Errorf("lazy-created file should start with title header, got:\n%s", content)
	}
	if !strings.Contains(content, "- [fact, 2026-06-12, user] staging is at https://stage.example.com") {
		t.Errorf("entry line missing, got:\n%s", content)
	}

	doc, err := store.Read("ws1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(doc.Entries()) != 1 {
		t.Fatalf("expected 1 entry after append, got %d", len(doc.Entries()))
	}
}

func TestMemoryStore_AppendValidation(t *testing.T) {
	store, _ := newTestMemoryStore(t)
	if err := store.Append("ws1", MemoryEntry{Text: "   "}); err == nil {
		t.Error("empty text should be rejected")
	}
	if err := store.Append("ws1", MemoryEntry{Type: "bogus", Date: "2026-06-12", Provenance: "user", Text: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	doc, _ := store.Read("ws1")
	if doc.Entries()[0].Type != MemoryTypeFact {
		t.Errorf("bogus type should normalize to fact, got %q", doc.Entries()[0].Type)
	}
}

func TestMemoryStore_AppendPreservesHandEdits(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	writeTestMemoryFile(t, dir, handEditedMemory)

	if err := store.Append("ws1", MemoryEntry{Type: MemoryTypeThread, Date: "2026-06-12", Provenance: "run:z9", Text: "new thread"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, MemoryFileName))
	content := string(data)
	if !strings.HasPrefix(content, handEditedMemory) {
		t.Errorf("append must preserve existing content byte-identically:\n%s", content)
	}
	if !strings.HasSuffix(content, "- [thread, 2026-06-12, run:z9] new thread\n") {
		t.Errorf("appended entry should be the last line:\n%s", content)
	}
}

func TestMemoryStore_Forget(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	writeTestMemoryFile(t, dir, `# Workspace Memory

- [fact, 2026-06-01, user] alpha beta
- [fact, 2026-06-02, user] beta gamma
`)

	// Ambiguous substring across both entries.
	_, err := store.Forget("ws1", "beta")
	if !errors.Is(err, ErrMemoryAmbiguousMatch) {
		t.Fatalf("expected ambiguous-match error, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "alpha beta") {
		t.Errorf("ambiguity error should list candidates, got: %v", err)
	}

	// Exact match wins even when it is also a substring of nothing else.
	removed, err := store.Forget("ws1", "alpha beta")
	if err != nil {
		t.Fatalf("exact forget: %v", err)
	}
	if removed.Text != "alpha beta" {
		t.Errorf("removed wrong entry: %+v", removed)
	}

	// Unambiguous substring.
	if _, err := store.Forget("ws1", "gamma"); err != nil {
		t.Fatalf("substring forget: %v", err)
	}

	// Nothing left to match.
	_, err = store.Forget("ws1", "beta")
	if !errors.Is(err, ErrMemoryEntryNotFound) {
		t.Fatalf("expected not-found error, got %v", err)
	}

	// Header survives all mutations.
	data, _ := os.ReadFile(filepath.Join(dir, MemoryFileName))
	if !strings.HasPrefix(string(data), "# Workspace Memory\n") {
		t.Errorf("unstructured header must survive forgets:\n%s", string(data))
	}
}

func TestMemoryStore_EditAtAndDeleteAt(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	writeTestMemoryFile(t, dir, `- [fact, 2026-06-01, user] first
- [fact, 2026-06-02, user] second
`)

	if err := store.EditAt("ws1", 1, MemoryEntry{Type: MemoryTypeDecision, Date: "2026-06-12", Provenance: "user", Text: "second, revised"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	doc, _ := store.Read("ws1")
	if got := doc.Entries()[1]; got.Text != "second, revised" || got.Type != MemoryTypeDecision {
		t.Errorf("edit did not apply: %+v", got)
	}

	if err := store.DeleteAt("ws1", 0); err != nil {
		t.Fatalf("delete: %v", err)
	}
	doc, _ = store.Read("ws1")
	if len(doc.Entries()) != 1 || doc.Entries()[0].Text != "second, revised" {
		t.Errorf("delete removed wrong entry: %+v", doc.Entries())
	}

	for _, idx := range []int{-1, 5} {
		if err := store.EditAt("ws1", idx, MemoryEntry{Text: "x"}); !errors.Is(err, ErrMemoryIndexOutOfRange) {
			t.Errorf("EditAt(%d) expected out-of-range, got %v", idx, err)
		}
		if err := store.DeleteAt("ws1", idx); !errors.Is(err, ErrMemoryIndexOutOfRange) {
			t.Errorf("DeleteAt(%d) expected out-of-range, got %v", idx, err)
		}
	}
}

func TestWorkspaceMemoryToolsGate(t *testing.T) {
	hostileOverrides := map[string]SideEffect{
		MemoryWriteToolName:  SideEffectExternal,
		MemoryForgetToolName: SideEffectExternal,
	}
	for _, policy := range []AutonomyPolicy{AutonomyWatch, AutonomyPropose} {
		for _, tool := range []string{MemoryWriteToolName, MemoryForgetToolName} {
			dec := EvaluateMissionToolCall(policy, SideEffectExternal, hostileOverrides, tool)
			if !dec.Allowed {
				t.Errorf("%s must be allowed under %s, got blocked: %s", tool, policy, dec.Reason)
			}
			if dec.Classification != SideEffectWrite {
				t.Errorf("%s should classify as write, got %q", tool, dec.Classification)
			}

			// Handler-side wrapper must agree even when the caller passes a
			// hostile pre-resolved classification.
			if dec := EvaluateMissionToolCallDecision(policy, SideEffectExternal, tool); !dec.Allowed {
				t.Errorf("decision wrapper blocked %s under %s: %s", tool, policy, dec.Reason)
			}
		}
	}

	// Control: ordinary write tools are still denied under Watch.
	if dec := EvaluateMissionToolCall(AutonomyWatch, SideEffectWrite, nil, "save_note"); dec.Allowed {
		t.Error("save_note must stay blocked under Watch")
	}

	if !IsWorkspaceMemoryTool(MemoryWriteToolName) || !IsWorkspaceMemoryTool(MemoryForgetToolName) {
		t.Error("memory tool names should be recognized")
	}
	if IsWorkspaceMemoryTool("save_note") {
		t.Error("save_note is not a memory tool")
	}
}

func TestRenderMemoryPromptSection_EmptyAndGuidance(t *testing.T) {
	empty := ParseMemoryDocument("")

	// Empty + no guidance => nothing to inject (native-CLI path).
	if got := RenderMemoryPromptSection(empty, false); got != "" {
		t.Errorf("empty memory without guidance should render nothing, got: %q", got)
	}

	// Empty + guidance => header + guidance + empty marker (mission/chat path).
	withGuidance := RenderMemoryPromptSection(empty, true)
	if !strings.Contains(withGuidance, "## Workspace Memory") {
		t.Error("guidance render should include the section header")
	}
	if !strings.Contains(withGuidance, "memory_write") || !strings.Contains(withGuidance, "Vault") {
		t.Errorf("guidance should mention the tools and the secrets prohibition, got:\n%s", withGuidance)
	}
	if !strings.Contains(withGuidance, "empty") {
		t.Errorf("empty memory should be marked as such, got:\n%s", withGuidance)
	}
}

func TestRenderMemoryPromptSection_NoToolGuidanceForCLI(t *testing.T) {
	doc := ParseMemoryDocument("- [fact, 2026-06-01, user] alpha\n")
	section := RenderMemoryPromptSection(doc, false)
	if strings.Contains(section, "memory_write") {
		t.Errorf("native-CLI render must omit tool guidance, got:\n%s", section)
	}
	if !strings.Contains(section, "alpha") {
		t.Errorf("entry should still be rendered, got:\n%s", section)
	}
}

func TestRenderMemoryPromptSection_TruncationPrefersWatchAndThread(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Workspace Memory\n\n")
	// Many old facts that will overflow the budget.
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "- [fact, 2026-01-%02d, user] old padding fact number %03d with enough text to consume budget quickly\n", (i%28)+1, i)
	}
	// Critical cross-run state, oldest dates so pure-recency would drop them.
	b.WriteString("- [watch, 2020-01-01, run:x] CRITICAL checked through commit deadbeef\n")
	b.WriteString("- [thread, 2020-01-02, run:y] CRITICAL open investigation into flaky test\n")

	doc := ParseMemoryDocument(b.String())
	// Render without tool guidance so the assertion isolates the entry budget
	// from the fixed guidance/header overhead.
	section := RenderMemoryPromptSection(doc, false)

	if !strings.Contains(section, "CRITICAL checked through commit deadbeef") {
		t.Error("watch entry must survive truncation despite being oldest")
	}
	if !strings.Contains(section, "CRITICAL open investigation") {
		t.Error("thread entry must survive truncation despite being oldest")
	}
	if !strings.Contains(section, "memory truncated") {
		t.Error("over-budget memory should carry a truncation notice")
	}
	// Entry content is bounded by the budget; header + notice are small fixed overhead.
	if len(section) > memoryPromptCharBudget+256 {
		t.Errorf("rendered entries should respect the budget (got %d chars, budget %d)", len(section), memoryPromptCharBudget)
	}
}

func TestRenderMemoryPromptSection_KeepsFileOrder(t *testing.T) {
	doc := ParseMemoryDocument(strings.Join([]string{
		"- [fact, 2026-06-01, user] first",
		"- [watch, 2026-06-11, run:z] second is watch",
		"- [decision, 2026-06-05, user] third",
		"",
	}, "\n"))
	section := RenderMemoryPromptSection(doc, false)
	iFirst := strings.Index(section, "first")
	iSecond := strings.Index(section, "second is watch")
	iThird := strings.Index(section, "third")
	if iFirst >= iSecond || iSecond >= iThird {
		t.Errorf("kept entries should render in original file order, got offsets %d/%d/%d in:\n%s", iFirst, iSecond, iThird, section)
	}
}

func TestMemoryStore_ConcurrentAppends(t *testing.T) {
	store, _ := newTestMemoryStore(t)
	const n = 20

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := MemoryEntry{Type: MemoryTypeFact, Date: "2026-06-12", Provenance: "user", Text: fmt.Sprintf("entry %02d", i)}
			if err := store.Append("ws1", entry); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	doc, err := store.Read("ws1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(doc.Entries()) != n {
		t.Fatalf("expected %d entries after concurrent appends, got %d", n, len(doc.Entries()))
	}
}
