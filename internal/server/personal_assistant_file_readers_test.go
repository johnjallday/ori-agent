package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	linkedFileBody     = "LINKED_FILE_BODY the bridge is in D minor"
	attachmentFileBody = "ATTACHMENT_FILE_BODY artwork is due on the 12th"
	// stageReviewFolder writes this into the folder that is only attached.
	unlinkedFolderBody = "source must stay unchanged"
)

type fileReaderFixture struct {
	*workspaceReaderFixture
	folder       string
	folderID     string
	attachmentID string
}

// newFileReaderFixture gives the project one linked folder and one stored
// attachment, through the production folder store and canonical workspace
// store. Everything lives under the test's sandboxed HOME.
func newFileReaderFixture(t *testing.T) *fileReaderFixture {
	t.Helper()
	f := &fileReaderFixture{workspaceReaderFixture: newWorkspaceReaderFixture(t), folderID: uuid.NewString(), attachmentID: uuid.NewString()}
	f.folder = filepath.Join(os.Getenv("HOME"), "Music", "Album-1 assets")
	if err := os.MkdirAll(filepath.Join(f.folder, "lyrics"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.folder, "lyrics", "bridge.txt"), []byte(linkedFileBody), 0o600); err != nil {
		t.Fatal(err)
	}
	files := f.builder.workspaceFileStore.GetFilesPath(f.project.ID)
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "artwork.md"), []byte(attachmentFileBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		ws.Attachments = append(ws.Attachments, workspace.Attachment{ID: f.attachmentID, WorkspaceID: ws.ID, Title: "Artwork", File: &workspace.AttachmentFileMeta{Name: "artwork.md", RelativePath: "artwork.md"}})
		return ws.AddDirectoryReference(workspace.DirectoryReference{ID: f.folderID, Name: "Album-1 assets", Path: f.folder})
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// Through the real routes, folder store and filesystem: a linked file and an
// attachment are read and attributed, and the references are saved without the
// bodies. No absolute path reaches the model, the browser or the saved turn.
func TestAssistantFileReaders_RealHostReadsLinkedFileAndAttachmentWithAttribution(t *testing.T) {
	f := newFileReaderFixture(t)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_files", nil),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "lyrics/bridge.txt"}),
		readerCall("assistant_workspace_file", map[string]any{"attachment_id": f.attachmentID}),
		{Content: "The bridge is in D minor [S1] and artwork is due on the 12th [S2].", Model: "sonnet", Provider: "claude_code"},
	}
	reply := f.ask(t, "", "What key is the bridge in, and when is the artwork due?")
	if reply["conversation"].(map[string]any)["stored"] != true || len(f.provider.requests) != 4 {
		t.Fatalf("reply: %v", reply)
	}
	listing := f.provider.requests[1].Messages[len(f.provider.requests[1].Messages)-1].Content
	if !strings.Contains(listing, f.folderID) || !strings.Contains(listing, "artwork.md") || strings.Contains(listing, linkedFileBody) || strings.Contains(listing, attachmentFileBody) {
		t.Fatalf("the listing should name sources without reading them: %s", listing)
	}
	if !strings.Contains(f.provider.requests[2].Messages[len(f.provider.requests[2].Messages)-1].Content, linkedFileBody) || !strings.Contains(f.lastToolResult(t), attachmentFileBody) {
		t.Fatal("the files' content did not reach the model")
	}
	sources := replySources(t, reply["workspace_context"])
	if len(sources) != 2 || sources[0]["kind"] != "file" || sources[0]["label"] != "bridge.txt" || sources[0]["detail"] != "Linked folder “Album-1 assets” · lyrics/bridge.txt" || sources[0]["coverage"] != "full" || sources[0]["cited"] != true ||
		sources[1]["kind"] != "attachment" || sources[1]["label"] != "artwork.md" || sources[1]["detail"] != "Workspace attachment" || sources[1]["workspace"] != f.project.Name {
		t.Fatalf("sources: %v", sources)
	}
	id := reply["conversation"].(map[string]any)["id"].(string)
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	encoded := mustJSON(t, saved) + mustJSON(t, reply)
	for _, request := range f.provider.requests {
		encoded += mustJSON(t, request.Messages)
	}
	// Not the sandbox home, the linked folder's location or the files folder.
	for _, location := range []string{os.Getenv("HOME"), f.folder, f.builder.workspaceFileStore.GetFilesPath(f.project.ID)} {
		if strings.Contains(encoded, location) {
			t.Fatalf("an absolute path was exposed: %s", location)
		}
	}
	if stored := mustJSON(t, saved["messages"]); strings.Contains(stored, linkedFileBody) || strings.Contains(stored, attachmentFileBody) || !strings.Contains(stored, "lyrics/bridge.txt") {
		t.Fatal("a file body was saved with the turn, or its reference was not")
	}
	contents, err := os.ReadFile(filepath.Join(f.folder, "lyrics", "bridge.txt"))
	if err != nil || string(contents) != linkedFileBody {
		t.Fatal("reading changed the file", err)
	}
}

// One diagnostics line per workspace turn says what was resolved and read in
// IDs, status words, counts and durations. Nothing logged while the turn runs
// carries a source body, the prompt, or where a linked folder lives.
func TestAssistantFileReaders_DiagnosticsCountReadsWithoutBodiesOrPaths(t *testing.T) {
	f := newFileReaderFixture(t)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "lyrics/bridge.txt"}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "../../outside.txt"}),
		readerCall("assistant_workspace_note", map[string]any{"note_id": f.noteID}),
		{Content: "The bridge is in D minor [S1].", Model: "sonnet", Provider: "claude_code"},
	}
	var captured strings.Builder
	previous := log.Writer()
	log.SetOutput(&captured)
	const prompt = "PROMPT_TEXT_MUST_NOT_BE_LOGGED what key is the bridge in?"
	f.ask(t, "", prompt)
	log.SetOutput(previous)

	var line string
	for _, candidate := range strings.Split(captured.String(), "\n") {
		if strings.Contains(candidate, "Home assistant workspace context") {
			line = candidate
		}
	}
	for _, expected := range []string{
		"scope=home_assistant.workspace_context", "context_status=available", "subject_id=" + f.project.ID,
		"readers_offered=true", "file_readers_offered=true", "reader_calls=3", "sources_read=2", "sources_partial=0",
		"available:2", "denied:1", "file:1", "note:1", "evidence_limit=64000", "context_prepare_us=", "read_us=", "turn_failed=false",
	} {
		if !strings.Contains(line, expected) {
			t.Fatalf("diagnostics line is missing %q: %s", expected, line)
		}
	}
	for _, private := range []string{linkedFileBody, readerNoteTail, "PROMPT_TEXT_MUST_NOT_BE_LOGGED", f.folder, "bridge.txt", "outside.txt", f.project.Name} {
		if strings.Contains(captured.String(), private) {
			t.Fatalf("the log carries %q", private)
		}
	}
}

// A Home's own folder holds its projects' folders. With that folder linked to
// the Home, its readers still do not reach a project inside it: being the
// parent grants nothing over a child's notes or files.
func TestAssistantFileReaders_ParentFolderDoesNotReachAChildWorkspace(t *testing.T) {
	f := newFileReaderFixture(t)
	home := placementWorkspace(t, f.draftServerFixture, "Reader Home fixture", "group", "")
	child := placementWorkspace(t, f.draftServerFixture, "Reader child fixture", "", home.ID)
	homeFolder, err := f.builder.workspaceFileStore.GetFolderPath(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	childFolder, err := f.builder.workspaceFileStore.GetFolderPath(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := filepath.Rel(homeFolder, childFolder)
	if err != nil || strings.HasPrefix(inside, "..") {
		t.Fatalf("fixture: the child's folder is expected inside its parent's, got %q", inside)
	}
	const childBody = "CHILD_WORKSPACE_BODY_MUST_NOT_BE_READ_THROUGH_THE_PARENT"
	if err := os.MkdirAll(filepath.Join(childFolder, "notes"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childFolder, "notes", "plan.md"), []byte(childBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeFolder, "home-readme.txt"), []byte("HOME_OWN_FILE a file of the Home itself"), 0o600); err != nil {
		t.Fatal(err)
	}
	homeFolderID := uuid.NewString()
	if err := f.builder.workspaceStore.Update(home.ID, func(ws *workspace.Workspace) error {
		return ws.AddDirectoryReference(workspace.DirectoryReference{ID: homeFolderID, Name: "Home folder", Path: homeFolder})
	}); err != nil {
		t.Fatal(err)
	}
	home, err = f.builder.workspaceStore.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.project = home
	childPath := filepath.ToSlash(filepath.Join(inside, "notes", "plan.md"))
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_folder", map[string]any{"directory_id": homeFolderID}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": homeFolderID, "path": childPath}),
		readerCall("assistant_workspace_folder", map[string]any{"directory_id": homeFolderID, "path": filepath.ToSlash(inside)}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": homeFolderID, "path": "home-readme.txt"}),
		{Content: "The Home has its own readme [S1].", Model: "sonnet", Provider: "claude_code"},
	}
	reply := f.ask(t, "", "What is in this Home's folder?")
	results := []string{}
	for _, message := range f.provider.requests[len(f.provider.requests)-1].Messages {
		if message.Role == llm.RoleTool {
			results = append(results, message.Content)
		}
	}
	if len(results) != 4 {
		t.Fatalf("expected four reader results, got %d", len(results))
	}
	if strings.Contains(results[0], filepath.Base(childFolder)) || strings.Contains(results[0], "sub-workspaces") || !strings.Contains(results[0], "home-readme.txt") {
		t.Fatalf("the Home's listing named a child workspace's folder: %s", results[0])
	}
	for index, result := range results[1:3] {
		if !strings.Contains(result, "not_readable_here") || strings.Contains(result, "plan.md") {
			t.Fatalf("a child workspace was reached through its parent (call %d): %s", index+2, result)
		}
	}
	if !strings.Contains(results[3], "HOME_OWN_FILE") {
		t.Fatalf("the Home's own file was not read: %s", results[3])
	}
	sources := replySources(t, reply["workspace_context"])
	if strings.Contains(mustJSON(t, f.provider.requests)+mustJSON(t, reply), childBody) || len(sources) != 1 || sources[0]["label"] != "home-readme.txt" {
		t.Fatalf("the child's content reached the model or the reply: %v", sources)
	}
}

// A folder attached to the conversation stays metadata. Asking about file
// contents reads the workspace's own files and says so; the attached folder's
// file never reaches the model, and attaching it grants nothing.
func TestAssistantFileReaders_AttachedFolderStaysMetadataWhileWorkspaceFilesAreRead(t *testing.T) {
	f := newFileReaderFixture(t)
	staged := stageReviewFolder(t, f.draftServerFixture)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "lyrics/bridge.txt"}),
		// The model tries the attached folder by name, by path and by its file.
		readerCall("assistant_workspace_folder", map[string]any{"directory_id": staged["selection_id"]}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "../../Documents/Chosen/notes.txt"}),
		readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": filepath.Join(os.Getenv("HOME"), "Documents", "Chosen", "notes.txt")}),
		{Content: "From the workspace's linked folder: the bridge is in D minor [S1]. I only have metadata for the attached folder.", Model: "sonnet", Provider: "claude_code"},
	}
	before, _ := f.builder.workspaceStore.Get(f.project.ID)
	references := len(before.DirectoryReferences)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Summarize these documents", "intent": "assistant_conversation", "context": placementContext(f.project), "conversation": map[string]string{},
		"folder_context": map[string]any{"selection_id": staged["selection_id"], "revision": "", "draft_id": staged["draft_id"]},
	})
	if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true || len(f.provider.requests) != 5 {
		t.Fatalf("turn: %d %v", status, reply)
	}
	for i, request := range f.provider.requests {
		assertNoPanelExecutionAuthority(t, request)
		if strings.Contains(mustJSON(t, request.Messages), unlinkedFolderBody) {
			t.Fatalf("round %d: the attached folder's file reached the model", i)
		}
	}
	user := f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content
	if !strings.Contains(user, "The attached folder's files have NOT been read and cannot be read") || !strings.Contains(user, "File contents have not been read") {
		t.Fatal("the model was not told which files it may read")
	}
	for round, reason := range map[int]string{2: "folder_not_linked_to_this_workspace", 3: "path_outside_the_approved_folder", 4: "path_outside_the_approved_folder"} {
		if result := f.provider.requests[round].Messages[len(f.provider.requests[round].Messages)-1].Content; !strings.Contains(result, reason) || !strings.Contains(result, `"content_read":false`) {
			t.Fatalf("round %d was not refused as %s: %s", round, reason, result)
		}
	}
	sources := replySources(t, reply["workspace_context"])
	if len(sources) != 1 || sources[0]["detail"] != "Linked folder “Album-1 assets” · lyrics/bridge.txt" || sources[0]["workspace"] != f.project.Name {
		t.Fatalf("the answer was not attributed to the workspace's own source: %v", sources)
	}
	after, _ := f.builder.workspaceStore.Get(f.project.ID)
	if len(after.DirectoryReferences) != references || reply["folder_context"].(map[string]any)["offer_id"] != nil && reply["folder_context"].(map[string]any)["offer_id"] != "" {
		t.Fatal("asking about contents linked the attached folder or prepared a setup")
	}
}

// Without a file of its own to read, a request for the attached folder's
// contents is refused before any model call, and it says how to get there.
func TestAssistantFileReaders_NoReadableWorkspaceFileMeansAnHonestRefusal(t *testing.T) {
	f := newWorkspaceReaderFixture(t) // a project with a note and a task, no files
	staged := stageReviewFolder(t, f.draftServerFixture)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Summarize these documents", "intent": "assistant_conversation", "context": placementContext(f.project), "conversation": map[string]string{},
		"folder_context": map[string]any{"selection_id": staged["selection_id"], "revision": "", "draft_id": staged["draft_id"]},
	})
	answer, _ := reply["response"].(string)
	if status != http.StatusOK || len(f.provider.requests) != 0 || !strings.Contains(answer, "File contents have not been read") || !strings.Contains(answer, "Picking it is not permission to read its files") || !strings.Contains(answer, "reviewed setup") {
		t.Fatalf("refusal: %d %v", status, reply)
	}
	if len(replySources(t, reply["workspace_context"])) != 0 {
		t.Fatal("a refused request claimed a source")
	}
}

// Unlinking the folder takes effect on the next read. The earlier reply keeps
// what it read then; it is not used to read the folder again.
func TestAssistantFileReaders_UnlinkedFolderStopsLaterReadsAndKeepsEarlierHistory(t *testing.T) {
	f := newFileReaderFixture(t)
	read := readerCall("assistant_workspace_file", map[string]any{"directory_id": f.folderID, "path": "lyrics/bridge.txt"})
	f.provider.script = []llm.ChatResponse{read, {Content: "D minor [S1].", Model: "sonnet", Provider: "claude_code"}}
	first := f.ask(t, "", "What key is the bridge in?")
	id := first["conversation"].(map[string]any)["id"].(string)
	if len(replySources(t, first["workspace_context"])) != 1 {
		t.Fatalf("first read: %v", first)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		ws.DirectoryReferences = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.provider.script = append(make([]llm.ChatResponse, len(f.provider.requests)), read, llm.ChatResponse{Content: "I can no longer read that folder.", Model: "sonnet", Provider: "claude_code"})
	again := f.ask(t, id, "And the verse?")
	result := f.lastToolResult(t)
	if !strings.Contains(result, "folder_not_linked_to_this_workspace") || strings.Contains(result, linkedFileBody) || len(replySources(t, again["workspace_context"])) != 0 {
		t.Fatalf("an unlinked folder was still read: %s", result)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	var answers []map[string]any
	for _, row := range saved["messages"].([]any) {
		if message := row.(map[string]any); message["role"] == "assistant" {
			answers = append(answers, message)
		}
	}
	if len(answers) != 2 || len(replySources(t, answers[0]["workspace_context"])) != 1 || len(replySources(t, answers[1]["workspace_context"])) != 0 {
		t.Fatal("the earlier reply lost its history, or the later one gained a source")
	}
	if _, err := os.Stat(filepath.Join(f.folder, "lyrics", "bridge.txt")); err != nil {
		t.Fatal("unlinking removed the user's file", err)
	}
}
