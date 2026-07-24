package workspace

import (
	"strings"
	"testing"
)

func TestRenderWorkspaceBacklogMarkdown_SchemaAndGuidance(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	rendered := RenderWorkspaceBacklogMarkdown(ws)

	for _, want := range []string{
		"type: ori_workspace_backlog",
		"schema_version: 1",
		"workspace_id: " + ws.ID,
		"content_hash: sha256:",
		"# Backlog",
		"## Backlog",
		"## Promote to Ready",
		"Synchronized by Ori",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered document missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderWorkspaceBacklogMarkdown_EmptyState(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	rendered := RenderWorkspaceBacklogMarkdown(ws)
	if !strings.Contains(rendered, "Nothing saved for later") {
		t.Fatalf("expected empty-state copy, got:\n%s", rendered)
	}
}

func TestBacklogMarkdownRoundTrip_StableIDsAndOrder(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setup error: %v", err)
		}
	}
	must(ws.AddTask(Task{ID: "b", Status: TaskStatusBacklog, Description: "second", BacklogRank: 2, Priority: 1}))
	must(ws.AddTask(Task{ID: "a", Status: TaskStatusBacklog, Description: "first", BacklogRank: 1, Priority: 5, Tags: []string{"x", "y"}}))

	rendered := RenderWorkspaceBacklogMarkdown(ws)

	// Rendered order follows rank, not insertion order.
	firstIdx := strings.Index(rendered, "first")
	secondIdx := strings.Index(rendered, "second")
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Fatalf("expected 'first' (rank 1) before 'second' (rank 2):\n%s", rendered)
	}

	known := map[string]struct{}{"a": {}, "b": {}}
	doc, err := ParseWorkspaceBacklogMarkdown(rendered, ws.ID, known)
	if err != nil {
		t.Fatalf("ParseWorkspaceBacklogMarkdown() error = %v", err)
	}
	if len(doc.BacklogRows) != 2 {
		t.Fatalf("expected 2 parsed rows, got %d: %+v", len(doc.BacklogRows), doc.BacklogRows)
	}
	if doc.BacklogRows[0].ID != "a" || doc.BacklogRows[0].Title != "first" || doc.BacklogRows[0].Priority != "low" {
		t.Fatalf("row[0] mismatch: %+v", doc.BacklogRows[0])
	}
	if doc.BacklogRows[1].ID != "b" || doc.BacklogRows[1].Title != "second" || doc.BacklogRows[1].Priority != "high" {
		t.Fatalf("row[1] mismatch: %+v", doc.BacklogRows[1])
	}
	if !stringSlicesEqualUnordered(doc.BacklogRows[0].Tags, []string{"x", "y"}) {
		t.Fatalf("tags not round-tripped: %+v", doc.BacklogRows[0].Tags)
	}
}

func TestParseWorkspaceBacklogMarkdown_ValidationFailures(t *testing.T) {
	validFrontmatter := "---\ntype: ori_workspace_backlog\nschema_version: 1\nworkspace_id: ws-1\n---\n\n"

	t.Run("missing frontmatter", func(t *testing.T) {
		_, err := ParseWorkspaceBacklogMarkdown("# Backlog\n\n## Backlog\n\n- idea\n", "ws-1", nil)
		if err == nil {
			t.Fatalf("expected error for missing frontmatter")
		}
	})

	t.Run("wrong doc type", func(t *testing.T) {
		content := "---\ntype: something_else\nworkspace_id: ws-1\n---\n\nbody\n"
		_, err := ParseWorkspaceBacklogMarkdown(content, "ws-1", nil)
		if err == nil {
			t.Fatalf("expected error for wrong type")
		}
	})

	t.Run("mismatched workspace_id", func(t *testing.T) {
		_, err := ParseWorkspaceBacklogMarkdown(validFrontmatter+"## Backlog\n\n- idea\n", "ws-2", nil)
		if err == nil {
			t.Fatalf("expected error for mismatched workspace_id")
		}
	})

	t.Run("duplicate ids", func(t *testing.T) {
		content := validFrontmatter + "## Backlog\n\n- one <!-- ori:id=x -->\n- two <!-- ori:id=x -->\n"
		known := map[string]struct{}{"x": {}}
		_, err := ParseWorkspaceBacklogMarkdown(content, "ws-1", known)
		if err == nil {
			t.Fatalf("expected error for duplicate id")
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		content := validFrontmatter + "## Backlog\n\n- one <!-- ori:id=nonexistent -->\n"
		known := map[string]struct{}{"x": {}}
		_, err := ParseWorkspaceBacklogMarkdown(content, "ws-1", known)
		if err == nil {
			t.Fatalf("expected error for unknown id")
		}
	})

	t.Run("oversized input", func(t *testing.T) {
		huge := validFrontmatter + strings.Repeat("- filler\n", maxBacklogMarkdownBytes)
		_, err := ParseWorkspaceBacklogMarkdown(huge, "ws-1", nil)
		if err == nil {
			t.Fatalf("expected error for oversized input")
		}
	})

	t.Run("valid document with no rows is accepted", func(t *testing.T) {
		doc, err := ParseWorkspaceBacklogMarkdown(validFrontmatter+"## Backlog\n\n_Nothing saved for later. Add an idea without committing it to an agent._\n", "ws-1", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(doc.BacklogRows) != 0 {
			t.Fatalf("placeholder italic text should not parse as a row: %+v", doc.BacklogRows)
		}
	})
}

func TestParseWorkspaceBacklogMarkdown_PromoteSection(t *testing.T) {
	content := "---\ntype: ori_workspace_backlog\nworkspace_id: ws-1\n---\n\n" +
		"## Backlog\n\n- staying <!-- ori:id=a -->\n\n" +
		"## Promote to Ready\n\n- moved <!-- ori:id=b -->\n- brand new direct ready\n"
	known := map[string]struct{}{"a": {}, "b": {}}
	doc, err := ParseWorkspaceBacklogMarkdown(content, "ws-1", known)
	if err != nil {
		t.Fatalf("ParseWorkspaceBacklogMarkdown() error = %v", err)
	}
	if len(doc.BacklogRows) != 1 || doc.BacklogRows[0].ID != "a" {
		t.Fatalf("unexpected backlog rows: %+v", doc.BacklogRows)
	}
	if len(doc.PromoteRows) != 2 {
		t.Fatalf("expected 2 promote rows, got %+v", doc.PromoteRows)
	}
	if doc.PromoteRows[0].ID != "b" || doc.PromoteRows[0].Title != "moved" {
		t.Fatalf("promote row[0] mismatch: %+v", doc.PromoteRows[0])
	}
	if doc.PromoteRows[1].ID != "" || doc.PromoteRows[1].Title != "brand new direct ready" {
		t.Fatalf("promote row[1] mismatch: %+v", doc.PromoteRows[1])
	}
}

func TestBacklogMarkdownContentRoot(t *testing.T) {
	if got := backlogMarkdownContentRoot("/ws/normal", false); got != "/ws/normal" {
		t.Fatalf("normal workspace root = %q, want /ws/normal", got)
	}
	if got := backlogMarkdownContentRoot("/ws/group", true); got != "/ws/group/"+FilesDir {
		t.Fatalf("group workspace root = %q, want /ws/group/%s", got, FilesDir)
	}
}

func TestPriorityWordIntRoundTrip(t *testing.T) {
	cases := []struct {
		word string
		want int
	}{{"high", 1}, {"HIGH", 1}, {"medium", 3}, {"", 3}, {"unknown", 3}, {"low", 5}}
	for _, tc := range cases {
		if got := priorityWordToInt(tc.word); got != tc.want {
			t.Errorf("priorityWordToInt(%q) = %d, want %d", tc.word, got, tc.want)
		}
	}
	intCases := []struct {
		p    int
		want string
	}{{0, "medium"}, {1, "high"}, {2, "high"}, {3, "medium"}, {4, "low"}, {5, "low"}}
	for _, tc := range intCases {
		if got := priorityIntToWord(tc.p); got != tc.want {
			t.Errorf("priorityIntToWord(%d) = %q, want %q", tc.p, got, tc.want)
		}
	}
}
