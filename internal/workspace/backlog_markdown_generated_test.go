package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BACKLOG.md stops being a source of truth and becomes a generated index
// (tasks/prd-workspace-ticket-management.md FR-119 through FR-122).
//
// The safety properties are the whole point: the final import adopts file-side
// work rather than discarding it, manual edits afterwards cannot mutate a
// Ticket, and a hand-edited file is never silently overwritten.

func newMarkdownTestSetup(t *testing.T) (*FileBacklogSynchronizer, Store, *Workspace, string) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Markdown"})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	sync := NewFileBacklogSynchronizer(store)
	path, _, ok, err := sync.resolve(ws.ID)
	if err != nil || !ok {
		t.Fatalf("resolve backlog path: ok=%v err=%v", ok, err)
	}
	return sync, store, ws, path
}

func readBacklogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read BACKLOG.md: %v", err)
	}
	return string(data)
}

// FR-119/FR-120: the final import runs the existing two-way importer FIRST, so
// work someone did in the file before upgrading is adopted rather than lost.
func TestFinalizeBacklogMarkdownImport_AdoptsFileSideWorkThenSwitches(t *testing.T) {
	sync, store, ws, path := newMarkdownTestSetup(t)

	// Start from a real Ori-managed file — the importer refuses a document
	// without Ori frontmatter, which is what stops it adopting a foreign file
	// that happens to sit at the managed path.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("seed render: %v", err)
	}

	// A row the user typed into the file while it was still authoritative.
	fileSide := readBacklogFile(t, path) + "- [ ] Idea written straight into the file\n"
	if err := os.WriteFile(path, []byte(fileSide), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("FinalizeBacklogMarkdownImport: %v", err)
	}

	// The file-side idea became a real ticket rather than being discarded.
	//
	// Which STATE it lands in depends on the section it was typed under — the
	// row above was appended past "## Promote to Ready", so it arrives Ready.
	// The guarantee under test is that the work survived the switch at all,
	// not which bucket the existing parser chose for it.
	svc := NewTicketService(store)
	page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var adopted bool
	for _, ticket := range page.Tickets {
		if strings.Contains(ticket.Title, "Idea written straight into the file") {
			adopted = true
		}
	}
	if !adopted {
		t.Fatalf("the final import discarded file-side work; tickets = %+v", page.Tickets)
	}

	// And the workspace has switched to generated-index behavior.
	reloaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !backlogMarkdownIsGenerated(reloaded) {
		t.Fatalf("the workspace did not switch to generated-index mode")
	}
}

// FR-120/FR-121: after the switch, editing the file changes nothing.
func TestGeneratedBacklogMarkdown_ManualEditsNeverMutateTickets(t *testing.T) {
	sync, store, ws, path := newMarkdownTestSetup(t)
	svc := NewTicketService(store)
	svc.SetEventBus(nil)

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := svc.Create(TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Real ticket",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("render: %v", err)
	}

	before, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Someone edits the generated file by hand: renames a row and adds one.
	edited := readBacklogFile(t, path) +
		"\n- [ ] An idea typed into the generated file\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("edit file: %v", err)
	}

	// A read that would previously have imported the file.
	if err := sync.ImportBeforeRead(ws.ID); err != nil {
		t.Fatalf("ImportBeforeRead: %v", err)
	}

	after, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if after.Total != before.Total {
		t.Fatalf("a manual edit to the generated file changed the ticket count %d → %d",
			before.Total, after.Total)
	}
	for i := range after.Tickets {
		if after.Tickets[i].Title != before.Tickets[i].Title {
			t.Fatalf("a manual edit rewrote a ticket title: %q → %q",
				before.Tickets[i].Title, after.Tickets[i].Title)
		}
	}
}

// FR-122: a hand-edited generated file is never silently overwritten.
func TestGeneratedBacklogMarkdown_EditedFileIsNotOverwritten(t *testing.T) {
	sync, store, ws, path := newMarkdownTestSetup(t)
	svc := NewTicketService(store)

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := svc.Create(TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "First",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("render: %v", err)
	}

	handEdited := readBacklogFile(t, path) + "\n<!-- a human wrote this -->\n"
	if err := os.WriteFile(path, []byte(handEdited), 0o600); err != nil {
		t.Fatalf("edit file: %v", err)
	}

	// A later ticket change would normally regenerate the file.
	if _, err := svc.Create(TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Second",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("render after edit: %v", err)
	}

	// The human's content is still there — the render refused to clobber it.
	current := readBacklogFile(t, path)
	if !strings.Contains(current, "a human wrote this") {
		t.Fatalf("a hand-edited generated file was silently overwritten")
	}
	// And the collision is reported rather than swallowed.
	status := sync.Status(ws.ID)
	if status.Warning == "" {
		t.Fatalf("an unwritable-because-edited file must surface a warning")
	}

	// Explicitly choosing replace is the way out, and it works.
	if err := sync.ReplaceCollisionForce(ws.ID); err != nil {
		t.Fatalf("ReplaceCollisionForce: %v", err)
	}
	replaced := readBacklogFile(t, path)
	if strings.Contains(replaced, "a human wrote this") {
		t.Fatalf("an explicit replace should have discarded the hand-edited content")
	}
	if !strings.Contains(replaced, "Second") {
		t.Fatalf("the replaced index should list current tickets")
	}
}

// FR-121: the generated file says plainly that it is generated.
func TestGeneratedBacklogMarkdown_HeaderStatesItIsGenerated(t *testing.T) {
	sync, store, ws, path := newMarkdownTestSetup(t)

	// Before the switch it still advertises itself as editable.
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(readBacklogFile(t, path), "do not edit") {
		t.Fatalf("a still-authoritative file must not claim to be generated")
	}

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	generated := readBacklogFile(t, path)
	if !strings.Contains(generated, "Generated by Ori") || !strings.Contains(generated, "do not edit") {
		t.Fatalf("the generated file does not say it is generated:\n%s", generated)
	}
	if !strings.Contains(generated, "NOT imported") {
		t.Fatalf("the generated file must warn that edits are not imported")
	}

	_ = store
}

// FR-119: finalizing twice is safe.
func TestFinalizeBacklogMarkdownImport_IsIdempotent(t *testing.T) {
	sync, store, ws, _ := newMarkdownTestSetup(t)

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	first, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stamp := getBacklogMarkdownSyncState(first).FinalImportAt

	if _, err := sync.FinalizeBacklogMarkdownImport(ws.ID); err != nil {
		t.Fatalf("second finalize: %v", err)
	}
	second, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !getBacklogMarkdownSyncState(second).FinalImportAt.Equal(stamp) {
		t.Fatalf("a repeat finalize moved the final-import stamp")
	}
}
