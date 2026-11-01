package secondbrain

import (
	"strings"
	"testing"
)

func TestReadMark_RendersAndReadsBackEachKind(t *testing.T) {
	for _, kind := range []string{
		NoteKindJournal, NoteKindCapture, NoteKindSeed, NoteKindFacts, NoteKindPerson, NoteKindProject, NoteKindFollowUp,
	} {
		if !validNoteKind(kind) {
			t.Fatalf("%s is not a valid kind", kind)
		}
		mark := ReadMark(RenderNote(kind, []byte("# A note\n\n- [ ] a line\n")))
		if mark.Kind != kind || !mark.OwnedByOri || mark.UserModified || !mark.Marked() {
			t.Fatalf("%s: %+v", kind, mark)
		}
		// A kind the marker cannot read back would make Ori's own note the user's.
		edited := ReadMark(append(RenderNote(kind, []byte("x\n")), "edited\n"...))
		if edited.Kind != kind || edited.OwnedByOri {
			t.Fatalf("%s edited: %+v", kind, edited)
		}
	}
	if mark := ReadMark(RenderNote(NoteKindCapture, nil)); !mark.OwnedByOri {
		t.Fatalf("an empty body: %+v", mark)
	}
}

func TestReadMark_AnythingOriCannotProveItWroteIsTheUsers(t *testing.T) {
	intact := string(RenderNote(NoteKindJournal, []byte("the entry\n")))
	frontEnd := strings.LastIndex(intact, "---\n")
	withFront := func(extra string) string { return intact[:frontEnd] + extra + intact[frontEnd:] }

	cases := map[string]struct {
		content    string
		wantMarked bool
	}{
		"no frontmatter":         {"# My own note\n", false},
		"empty file":             {"", false},
		"user's frontmatter":     {"---\ntitle: Mine\n---\nbody\n", false},
		"unclosed fence":         {"---\nori: journal\n", false},
		"body edited":            {intact + "one more line\n", true},
		"body emptied":           {intact[:frontEnd+4], true},
		"a tag added":            {withFront("tags: [work]\n"), true},
		"an unknown kind":        {strings.Replace(intact, "ori: journal", "ori: other", 1), false},
		"a malformed hash":       {strings.Replace(intact, "ori_hash: ", "ori_hash: zz", 1), true},
		"a hash line removed":    {"---\nori: journal\n---\nthe entry\n", true},
		"line endings rewritten": {strings.ReplaceAll(intact, "\n", "\r\n"), false},
		"marker inside the body": {"# Pasted\n" + intact, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mark := ReadMark([]byte(tc.content))
			if mark.OwnedByOri || !mark.UserModified {
				t.Fatalf("mark = %+v, want user modified", mark)
			}
			if mark.Marked() != tc.wantMarked {
				t.Fatalf("marked = %v, want %v (%+v)", mark.Marked(), tc.wantMarked, mark)
			}
		})
	}
}
