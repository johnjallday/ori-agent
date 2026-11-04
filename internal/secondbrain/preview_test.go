package secondbrain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreview_ShowsOnlyOrisOwnNotesFollowsNoLinkAndChangesNothing is the
// preview's twin of the write-boundary test (Issue 776 FR16): it refuses the
// user's notes and edited ones, serves at most 4 KB of a note Ori wrote, follows
// no link at any step, and leaves a byte, mode and mtime snapshot unchanged.
func TestPreview_ShowsOnlyOrisOwnNotesFollowsNoLinkAndChangesNothing(t *testing.T) {
	tree := newWriterTree(t)
	addFillInTargets(t, tree)
	long := strings.Repeat("A line Ori wrote about the week. ", 400) // ~13 KB
	writeNote(t, tree.vault, "Inbox/long.md", string(RenderNote(NoteKindCapture, []byte("# Long\n\n"+long+"\n"))))
	writeNote(t, tree.vault, "Inbox/old-marker.md", "---\nori: seed\nori_hash: "+bodyHash([]byte("# Old\n\n- [ ] call\n"))+"\n---\n# Old\n\n- [ ] call\n")
	writeNote(t, tree.vault, "Daily/2026-11-04.md", string(RenderNote(NoteKindJournal, []byte("# 4 November\n\nA good day.\n"))))
	writeNote(t, tree.vault, "Inbox/a-person.md", string(RenderNote(NoteKindPerson, []byte("# Dana\n"))))
	if err := os.Symlink(filepath.Join(tree.vault, "Projects", "Seeded"), filepath.Join(tree.vault, "Projects", "Seeded Link")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)

	refusals := map[string]struct {
		kind, name string
		want       error
	}{
		"a user's note":               {PreviewInbox, "mine.md", ErrPreviewNotOri},
		"an Ori note the user edited": {PreviewDaily, "2026-10-30.md", ErrPreviewNotOri},
		"an edited project note":      {PreviewProject, "Edited", ErrPreviewNotOri},
		"the user's project":          {PreviewProject, "Album", ErrPreviewNotOri},
		"a person filed in Inbox":     {PreviewInbox, "a-person.md", ErrPreviewNotOri},
		"a link to outside":           {PreviewInbox, "linked-note.md", ErrPreviewNotFound},
		"a link inside":               {PreviewInbox, "linked-inside.md", ErrPreviewNotFound},
		"a project linked out":        {PreviewProject, "Linked Out", ErrPreviewNotFound},
		"a project linked across":     {PreviewProject, "Seeded Link", ErrPreviewNotFound},
		"not there":                   {PreviewInbox, "gone.md", ErrPreviewNotFound},
		"a slash":                     {PreviewInbox, "../README.md", ErrPreviewInvalid},
		"a nested name":               {PreviewInbox, "sub/note.md", ErrPreviewInvalid},
		"a dot prefix":                {PreviewInbox, ".hidden.md", ErrPreviewInvalid},
		"dot dot":                     {PreviewProject, "..", ErrPreviewInvalid},
		"a backslash":                 {PreviewDaily, `a\b.md`, ErrPreviewInvalid},
		"not Markdown":                {PreviewInbox, "script.sh", ErrPreviewInvalid},
		"a folder's README":           {PreviewInbox, "README.md", ErrPreviewInvalid},
		"another kind":                {"people", "Dana Lee.md", ErrPreviewInvalid},
		"the facts note":              {"facts", FactsNoteRel, ErrPreviewInvalid},
		"empty":                       {PreviewInbox, "", ErrPreviewInvalid},
	}
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadPreview(tree.vault, tc.kind, tc.name); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	short, err := ReadPreview(tree.vault, PreviewInbox, "ori-note.md")
	if err != nil || short.Body != "a capture\n" || short.Truncated || short.Rel != "Inbox/ori-note.md" || short.Kind != PreviewInbox {
		t.Fatalf("short preview = %+v, %v", short, err)
	}
	clipped, err := ReadPreview(tree.vault, PreviewInbox, "long.md")
	if err != nil || !clipped.Truncated || len(clipped.Body) > PreviewBodyBytes || !strings.HasPrefix(clipped.Body, "# Long\n") || clipped.Title != "Long" {
		t.Fatalf("long preview: truncated %t, %d bytes, title %q, %v", clipped.Truncated, len(clipped.Body), clipped.Title, err)
	}
	older, err := ReadPreview(tree.vault, PreviewInbox, "old-marker.md")
	if err != nil || older.Title != "Old" {
		t.Fatalf("a note with the 64-hex marker = %+v, %v", older, err)
	}
	daily, err := ReadPreview(tree.vault, PreviewDaily, "2026-11-04.md")
	if err != nil || daily.Body != "# 4 November\n\nA good day.\n" {
		t.Fatalf("daily = %+v, %v", daily, err)
	}
	project, err := ReadPreview(tree.vault, PreviewProject, "Seeded")
	if err != nil || project.Rel != "Projects/Seeded/README.md" || !strings.Contains(project.Body, "What I said at setup") {
		t.Fatalf("project = %+v, %v", project, err)
	}
	// The body matches the file after the marker, byte for byte.
	file, _ := os.ReadFile(filepath.Join(tree.vault, "Projects", "Seeded", "README.md")) // #nosec G304 -- the test's own temp tree
	if _, after, ok := strings.Cut(string(file), "\n---\n"); !ok || after != project.Body {
		t.Fatalf("preview body differs from the file after the marker")
	}

	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("a preview changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestPreviewRel_IsDecidedWithoutTheFilesystem(t *testing.T) {
	for kind, want := range map[string]string{PreviewInbox: "Inbox/note.md", PreviewDaily: "Daily/note.md"} {
		if rel, err := PreviewRel(kind, "note.md"); err != nil || rel != want {
			t.Errorf("%s: %q, %v", kind, rel, err)
		}
	}
	if rel, err := PreviewRel(PreviewProject, "Launch"); err != nil || rel != "Projects/Launch/README.md" {
		t.Errorf("project: %q, %v", rel, err)
	}
}

func TestListPeople_NamesFromFileNamesKindsFromTheMarkerBounded(t *testing.T) {
	tree := newWriterTree(t)
	addFillInTargets(t, tree)
	writeNote(t, tree.vault, "People/Dana Lee.md", string(RenderNote(NoteKindPerson, []byte("# Dana Lee\n"))))
	writeNote(t, tree.vault, "People/Sam Ortiz.md", string(RenderNote(NoteKindSeed, []byte("# Sam\n"))))
	writeNote(t, tree.vault, "People/.hidden.md", string(RenderNote(NoteKindPerson, []byte("x\n"))))
	writeNote(t, tree.vault, "People/notes.txt", "not a note")
	writeNote(t, tree.vault, "People/Sub/Inner.md", "nested")
	before := snapshot(t, tree.base)

	people, err := ListPeople(tree.vault)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, person := range people.People {
		got = append(got, person.Name+":"+person.Kind)
	}
	want := "Dana Lee:person,Mine:user,Sam Ortiz:seed"
	if strings.Join(got, ",") != want || people.Partial {
		t.Fatalf("people = %v (partial %t), want %s", got, people.Partial, want)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("listing People changed the tree")
	}

	many := canonicalTemp(t)
	for index := range MaxPeopleListed + 5 {
		writeNote(t, many, "People/Person "+strings.Repeat("x", index%3)+string(rune('A'+index%26))+string(rune('a'+index/26))+".md", "# p\n")
	}
	bounded, err := ListPeople(many)
	if err != nil || len(bounded.People) != MaxPeopleListed || !bounded.Partial {
		t.Fatalf("bounded = %d people, partial %t, %v", len(bounded.People), bounded.Partial, err)
	}
	if empty, err := ListPeople(canonicalTemp(t)); err != nil || len(empty.People) != 0 {
		t.Fatalf("no People folder = %+v, %v", empty, err)
	}
}

func TestCreate_HidesPropertiesInNewSetupsOnly(t *testing.T) {
	creation, err := Creator{}.Create(canonicalTemp(t), "Notes", Seeds{})
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join(creation.Path, ".obsidian", "app.json")) // #nosec G304 -- the test's own temp tree
	if err != nil || !strings.Contains(string(app), `"propertiesInDocument": "hidden"`) {
		t.Fatalf("app.json = %s, %v", app, err)
	}
}
