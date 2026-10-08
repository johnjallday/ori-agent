package server

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The provisional target is 250 ms p95 of added local preparation for a warm
// workspace overview, excluding the model and optional document parsing.
const overviewLatencyTarget = 250 * time.Millisecond

func latencySummary(samples []time.Duration) (p50, p95, longest time.Duration) {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2], sorted[len(sorted)*95/100], sorted[len(sorted)-1]
}

// TestAssistantWorkspaceLatency_LargeWorkspace measures, through the real
// routes, stores and filesystem, how long Ori takes to prepare a workspace
// overview and to run each reader on a deliberately large workspace, and what a
// turn that reads until its budget is spent delivers. It is a measurement on
// the machine it runs on, so it runs only when asked:
//
//	ORI_ASSISTANT_LATENCY=1 ./scripts/run-test-command.sh go test ./internal/server -run TestAssistantWorkspaceLatency -count=1 -v
//
// Model time is excluded: the provider is an in-process stand-in that returns
// at once. Reader time is the gap between the stand-in asking for a reader and
// Ori's next request, so it includes the access re-check made before every read.
func TestAssistantWorkspaceLatency_LargeWorkspace(t *testing.T) {
	if os.Getenv("ORI_ASSISTANT_LATENCY") != "1" {
		t.Skip("set ORI_ASSISTANT_LATENCY=1 to measure")
	}
	f := newWorkspaceReaderFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// A Home with 40 projects; one of them holds the large fixture.
	home := placementWorkspace(t, f.draftServerFixture, "Large Home fixture", "group", "")
	for index := range 40 {
		placementWorkspace(t, f.draftServerFixture, fmt.Sprintf("Sibling project %02d", index), "", home.ID)
	}
	f.project = placementWorkspace(t, f.draftServerFixture, "Large project fixture", "", home.ID)

	const taskCount, noteCount, attachmentCount, folderFiles = 1000, 300, 200, 2000
	paragraph := strings.Repeat("A paragraph of ordinary planning prose for the fixture. ", 70) // ~4,000 characters
	longNoteID := uuid.NewString()
	for index := range noteCount {
		id, content := uuid.NewString(), paragraph
		if index == 0 {
			id, content = longNoteID, strings.Repeat(paragraph, 38) // ~150,000 characters
		}
		if err := f.builder.sessionStore.CreateNote(ctx, &session.WorkspaceNote{ID: id, WorkspaceID: f.project.ID, Name: fmt.Sprintf("Fixture note %03d", index), Content: content, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	assets := filepath.Join(os.Getenv("HOME"), "Music", "Large assets")
	for index := range folderFiles {
		dir := filepath.Join(assets, fmt.Sprintf("set-%02d", index%40), fmt.Sprintf("take-%d", index%5))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d.txt", index)), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Just under the parser's 10 MB limit, so every part re-reads a large file.
	if err := os.WriteFile(filepath.Join(assets, "session-log.txt"), bytes.Repeat([]byte("Session log line with ordinary words in it.\n"), 9*1024*1024/44), 0o600); err != nil {
		t.Fatal(err)
	}
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		strings.Repeat(`<w:p><w:r><w:t>A paragraph in a parsed document for the fixture.</w:t></w:r></w:p>`, 2000) + `</w:body></w:document>`))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	files := f.builder.workspaceFileStore.GetFilesPath(f.project.ID)
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "plan.docx"), document.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	folderID, documentID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		for index := range taskCount {
			id, status := uuid.NewString(), workspace.TaskStatusPending
			if index == 0 {
				id = taskID
			}
			if index%3 == 0 {
				status = workspace.TaskStatusCompleted
			}
			ws.Tasks = append(ws.Tasks, workspace.Task{ID: id, WorkspaceID: ws.ID, Description: fmt.Sprintf("Fixture task %04d", index), Details: paragraph[:600], Status: status, CreatedAt: now, UpdatedAt: now})
		}
		ws.Attachments = append(ws.Attachments, workspace.Attachment{ID: documentID, WorkspaceID: ws.ID, Title: "Plan", File: &workspace.AttachmentFileMeta{Name: "plan.docx", RelativePath: "plan.docx"}})
		for index := range attachmentCount - 1 {
			ws.Attachments = append(ws.Attachments, workspace.Attachment{ID: uuid.NewString(), WorkspaceID: ws.ID, Title: fmt.Sprintf("Attachment %03d", index), File: &workspace.AttachmentFileMeta{Name: fmt.Sprintf("attachment-%03d.md", index), RelativePath: fmt.Sprintf("attachment-%03d.md", index)}})
		}
		return ws.AddDirectoryReference(workspace.DirectoryReference{ID: folderID, Name: "Large assets", Path: assets})
	}); err != nil {
		t.Fatal(err)
	}
	subject, err := f.builder.workspaceStore.Get(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.project = subject
	t.Logf("fixture: Home with 41 projects; subject has %d tasks, %d notes (one of ~150,000 characters), %d attachments, a linked folder of %d files in 200 folders, a 9 MB text file and a 2,000-paragraph .docx",
		taskCount, noteCount, attachmentCount, folderFiles)

	// 1. Warm overview through the route the drawer calls on open and navigation.
	overview := func(location *workspace.Workspace, label string) time.Duration {
		samples := make([]time.Duration, 0, 200)
		for index := range 201 {
			started := time.Now()
			status, reply := f.call(t, http.MethodPost, "/api/home-assistant/context", map[string]any{"context": placementContext(location)})
			elapsed := time.Since(started)
			if status != http.StatusOK || reply["status"] != "available" {
				t.Fatalf("%s overview: %d %v", label, status, reply)
			}
			if size := len(mustJSON(t, reply)); size > 16000 {
				t.Fatalf("%s overview is %d bytes, not a bounded overview", label, size)
			}
			if index > 0 { // the first call warms the caches
				samples = append(samples, elapsed)
			}
		}
		p50, p95, longest := latencySummary(samples)
		t.Logf("warm overview, %s (200 samples, HTTP route included): p50=%s p95=%s max=%s", label, p50, p95, longest)
		return p95
	}
	for label, location := range map[string]*workspace.Workspace{"large project": subject, "Home of 41 projects": home} {
		if p95 := overview(location, label); p95 > overviewLatencyTarget {
			t.Errorf("%s: warm overview p95 %s is over the %s target", label, p95, overviewLatencyTarget)
		}
	}

	// 2. Each reader, one call per turn, timed from the stand-in's request for it
	// to Ori's next request.
	reader := func(label string, call llm.ChatResponse, expect string) {
		samples := make([]time.Duration, 0, 30)
		for range 30 {
			var asked time.Time
			f.provider.requests, f.provider.script = nil, []llm.ChatResponse{call, {Content: "Done.", Model: "sonnet", Provider: "claude_code"}}
			f.provider.before = map[int]func(){1: func() { asked = time.Now() }, 2: func() { samples = append(samples, time.Since(asked)) }}
			f.ask(t, "", "Measure one reader")
			if result := f.lastToolResult(t); !strings.Contains(result, expect) {
				t.Fatalf("%s did not return %q: %.300s", label, expect, result)
			}
		}
		p50, p95, longest := latencySummary(samples)
		t.Logf("reader %-44s (30 samples): p50=%s p95=%s max=%s", label, p50, p95, longest)
	}
	reader("task list (1,000 tasks)", readerCall("assistant_workspace_tasks", nil), `"status":"available"`)
	reader("task detail", readerCall("assistant_workspace_task", map[string]any{"task_id": taskID}), `"content_read":true`)
	reader("note list (300 notes)", readerCall("assistant_workspace_notes", nil), `"status":"available"`)
	reader("note part (40,000 of ~150,000 characters)", readerCall("assistant_workspace_note", map[string]any{"note_id": longNoteID}), `"coverage":"partial"`)
	reader("file sources (200 attachments, 2 folders)", readerCall("assistant_workspace_files", nil), `"status":"available"`)
	reader("folder listing (2,000 files, shortened to fit)", readerCall("assistant_workspace_folder", map[string]any{"directory_id": folderID}), `"listing_shortened_to_fit_reading_budget"`)
	reader("text file part (40,000 characters of 9 MB)", readerCall("assistant_workspace_file", map[string]any{"directory_id": folderID, "path": "session-log.txt"}), `"coverage":"partial"`)
	reader("parsed .docx (2,000 paragraphs)", readerCall("assistant_workspace_file", map[string]any{"attachment_id": documentID}), `"content_read":true`)

	// 3. A turn that keeps reading until the budget is spent.
	f.provider.before = map[int]func(){}
	f.provider.requests, f.provider.script = nil, []llm.ChatResponse{
		readerCall("assistant_workspace_note", map[string]any{"note_id": longNoteID}),
		readerCall("assistant_workspace_note", map[string]any{"note_id": longNoteID, "offset": 40000}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": folderID, "path": "session-log.txt"}),
		readerCall("assistant_workspace_tasks", nil),
		{Content: "I read part of the note [S1] and could not read the rest.", Model: "sonnet", Provider: "claude_code"},
	}
	started := time.Now()
	reply := f.ask(t, "", "Read everything in this workspace")
	last := f.provider.requests[len(f.provider.requests)-1]
	delivered, refused := 0, 0
	for _, message := range last.Messages {
		if message.Role == llm.RoleTool {
			delivered += len([]rune(message.Content))
			if strings.Contains(message.Content, "evidence_budget_exhausted") {
				refused++
			}
		}
	}
	sources := replySources(t, reply["workspace_context"])
	if delivered > 64000 || refused == 0 || len(sources) != 1 || sources[0]["coverage"] != "partial" {
		t.Fatalf("budget exhaustion: %d characters delivered by readers, %d refusals, sources %v", delivered, refused, sources)
	}
	t.Logf("budget exhaustion: readers delivered %d characters in total (limit 64,000 including the overview), %d later reads refused as over budget, the one source is reported partial; whole turn took %s without a model",
		delivered, refused, time.Since(started))
}
