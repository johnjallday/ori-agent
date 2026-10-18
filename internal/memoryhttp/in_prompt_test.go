package memoryhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// overBudgetMemoryLines is more ordinary memory than the prompt budget holds,
// plus a managed marker and old watch/thread entries the budget prefers.
func overBudgetMemoryLines() []string {
	lines := []string{
		"- [watch, 2025-01-03, run:w1] Checked upstream through commit 95c10b3.",
		"- [fact, 2026-05-01, ori-hq:item-1:rev-2] HQ-SENTINEL reviewed line.",
		"- [fact, 2026-04-19, global:gm-42] Prefers metric units in every report.",
	}
	types := []string{"fact", "decision", "feedback", "dead-end"}
	for i := 0; i < 36; i++ {
		lines = append(lines, fmt.Sprintf("- [%s, 2026-03-%02d, run:f%02d] Filler %02d %s",
			types[i%len(types)], (i%28)+1, i, i, strings.TrimSpace(strings.Repeat("lorem ipsum dolor ", 12))))
	}
	return append(lines,
		"- [fact, 2025-12-01, user] Short old fact A.",
		"- [decision, 2025-12-02, user] Short old decision B.",
		"- [thread, 2025-03-01, run:t2] Second open thread.")
}

func readMemoryFile(t *testing.T, fs *workspace.FileStore) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mustFolder(t, fs), workspace.MemoryFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func dtoLine(e entryDTO) string {
	return workspace.MemoryEntry{Type: workspace.MemoryEntryType(e.Type), Date: e.Date, Provenance: e.Provenance, Text: e.Text}.Render()
}

var truncatedCount = regexp.MustCompile(`memory truncated — (\d+) entr`)

func TestGetMemory_InPromptFalseIsExactlyWhatThePromptDrops(t *testing.T) {
	h, fs := newTestHandler(t)
	seedMemory(t, fs, "ws1", overBudgetMemoryLines()...)
	resp := decodeMemory(t, serve(h, http.MethodGet, "/api/workspaces/ws1/memory", ""))
	if !resp.OverBudget {
		t.Fatalf("fixture must be over budget: raw_size=%d", resp.RawSize)
	}

	section := workspace.RenderMemoryPromptSection(workspace.ParseMemoryDocument(readMemoryFile(t, fs)), false)
	rendered := map[string]bool{}
	renderedEntries := 0
	for _, line := range strings.Split(section, "\n") {
		rendered[line] = true
		if strings.HasPrefix(line, "- [") {
			renderedEntries++
		}
	}
	match := truncatedCount.FindStringSubmatch(section)
	if match == nil {
		t.Fatalf("prompt section has no truncation notice:\n%s", section)
	}
	dropped, _ := strconv.Atoi(match[1])

	inPrompt, leftOut := 0, 0
	var firstLeftOut *entryDTO
	for i := range resp.Entries {
		entry := resp.Entries[i]
		if want := rendered[dtoLine(entry)]; entry.InPrompt != want {
			t.Errorf("entry %d %q: in_prompt=%v but rendered=%v", entry.Index, entry.Text, entry.InPrompt, want)
		}
		switch {
		case entry.InPrompt:
			inPrompt++
		case !entry.Managed:
			leftOut++
			if firstLeftOut == nil {
				firstLeftOut = &entry
			}
		}
	}
	if inPrompt != renderedEntries || leftOut != dropped || dropped == 0 {
		t.Fatalf("in_prompt true=%d (prompt renders %d), false=%d (prompt drops %d)", inPrompt, renderedEntries, leftOut, dropped)
	}

	// The index is the one edit/delete address: deleting a left-out entry by
	// its index removes exactly that entry.
	rec := serve(h, http.MethodDelete, fmt.Sprintf("/api/workspaces/ws1/memory/entries/%d", firstLeftOut.Index), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE left-out entry: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(readMemoryFile(t, fs), dtoLine(*firstLeftOut)) {
		t.Fatalf("index %d did not address the left-out entry %q", firstLeftOut.Index, firstLeftOut.Text)
	}
}

func TestGetMemory_UnderBudgetEveryEntryIsInPrompt(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, text := range []string{"first", "second", "third"} {
		serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"`+text+`","type":"decision"}`)
	}
	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	resp := decodeMemory(t, rec)
	if len(resp.Entries) != 3 || resp.OverBudget {
		t.Fatalf("entries=%d over_budget=%v", len(resp.Entries), resp.OverBudget)
	}
	for _, entry := range resp.Entries {
		if !entry.InPrompt {
			t.Errorf("under budget, entry %d %q must be in_prompt", entry.Index, entry.Text)
		}
	}

	// in_prompt is always present, never omitted.
	var raw struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for i, entry := range raw.Entries {
		if string(entry["in_prompt"]) != "true" {
			t.Errorf("entry %d in_prompt = %s", i, entry["in_prompt"])
		}
	}
}

func TestGetMemory_ManagedLinesAndLearningsAreNeverInPrompt(t *testing.T) {
	h, fs := newTestHandler(t)
	// Managed markers big enough to exceed the budget on their own: they are
	// not in the prompt section, so they must not push ordinary entries out.
	lines := []string{"- [fact, 2026-10-01, user] Ordinary fact stays in."}
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("- [fact, 2026-10-%02d, ori-hq:item-%d:rev-1] Managed %02d %s",
			i+2, i, i, strings.Repeat("x", 450)))
	}
	lines = append(lines, "- [watch, 2026-10-02, run:w] Ordinary watch stays in.")
	seedMemory(t, fs, "ws1", lines...)

	// An approved assistant learning is shown separately in managed_learnings.
	if err := fs.Update("ws1", func(current *workspace.Workspace) error {
		current.SetAssistantProgramState(&workspace.AssistantProgramState{
			SchemaVersion: 1,
			Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "plugin", ProgramID: "program"},
			Declaration:   &workspace.AssistantProgramDeclaration{SchemaVersion: 1, ID: "program"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	learningStore := workspace.NewAssistantLearningStore(fs)
	document, err := learningStore.AddCandidates("ws1", 0, []workspace.AssistantLearningCandidate{{
		Fingerprint: "pattern", Type: "preference", Text: "Keep reviews concise.", Confidence: "high",
		Evidence: []workspace.AssistantEvidenceReference{
			{SourceID: "a", ProjectID: "a", Summary: "Pattern in A", ObservedAt: now},
			{SourceID: "b", ProjectID: "b", Summary: "Pattern in B", ObservedAt: now},
			{SourceID: "c", ProjectID: "c", Summary: "Pattern in C", ObservedAt: now},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := learningStore.ApproveCandidate("ws1", document.Candidates[0].ID, document.Version); err != nil {
		t.Fatal(err)
	}

	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	resp := decodeMemory(t, rec)
	if !resp.OverBudget || len(resp.Entries) != 22 {
		t.Fatalf("over_budget=%v entries=%d", resp.OverBudget, len(resp.Entries))
	}
	for _, entry := range resp.Entries {
		if entry.InPrompt == entry.Managed {
			t.Errorf("entry %d %q: managed=%v in_prompt=%v", entry.Index, entry.Text, entry.Managed, entry.InPrompt)
		}
	}
	if len(resp.ManagedLearnings) != 1 {
		t.Fatalf("managed learnings = %+v", resp.ManagedLearnings)
	}
	var raw struct {
		ManagedLearnings []map[string]json.RawMessage `json:"managed_learnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.ManagedLearnings[0]["in_prompt"]; ok {
		t.Errorf("managed learnings keep their shape; no in_prompt: %v", raw.ManagedLearnings[0])
	}
}
