package workspace

import (
	"errors"
	"strings"
	"testing"
)

func managed(block string) string {
	return MemoryBlockStart + "\n" + block + "\n" + MemoryBlockEnd + "\n"
}

func TestApplyManagedMemoryBlock(t *testing.T) {
	const user = "# My agent notes\n\nUse pnpm, never npm.\n"
	const userBelow = "\n## Later\nkeep this too\n"
	cases := []struct {
		name     string
		existing string
		exists   bool
		block    string
		want     string
		keep     bool
	}{
		{"missing file gets only the block", "", false, "- fact one", managed("- fact one"), true},
		{"missing file and nothing to add stays missing", "", false, "", "", false},
		{"file without markers gets the block appended", user, true, "- fact one", user + "\n" + managed("- fact one"), true},
		{"file without a trailing newline", "no newline", true, "- f", "no newline\n\n" + managed("- f"), true},
		{"whitespace-only file becomes just the block", " \n\n", true, "- f", managed("- f"), true},
		{"markers: only the block is replaced, text above and below untouched",
			user + "\n" + managed("- old") + userBelow, true, "- new\n- newer",
			user + "\n" + managed("- new\n- newer") + userBelow, true},
		{"switch off restores the user's file", user + "\n" + managed("- fact"), true, "", user, true},
		{"switch off keeps text below the block", user + "\n" + managed("- fact") + userBelow, true, "", user + userBelow, true},
		{"switch off on a file holding only the block deletes it", managed("- fact"), true, "", "", false},
		{"switch off on a file without markers leaves it alone", user, true, "", user, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, keep, err := ApplyManagedMemoryBlock(tc.existing, tc.exists, tc.block)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || keep != tc.keep {
				t.Fatalf("got keep=%v\n%q\nwant keep=%v\n%q", keep, got, tc.keep, tc.want)
			}
		})
	}
}

func TestApplyManagedMemoryBlock_RoundTripIsExact(t *testing.T) {
	original := "# Repo guide\n\nRun `make test` before every commit.\n"
	on, _, err := ApplyManagedMemoryBlock(original, true, "- fact")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := ApplyManagedMemoryBlock(on, true, "- fact")
	if err != nil || again != on {
		t.Fatalf("re-applying the same block changed the file:\n%q\n%q", on, again)
	}
	off, keep, err := ApplyManagedMemoryBlock(on, true, "")
	if err != nil || !keep || off != original {
		t.Fatalf("on then off did not restore the file exactly:\n%q", off)
	}
}

func TestApplyManagedMemoryBlock_RefusesMalformedMarkers(t *testing.T) {
	for name, existing := range map[string]string{
		"end before start": MemoryBlockEnd + "\n" + MemoryBlockStart + "\n",
		"unclosed":         "text\n" + MemoryBlockStart + "\n- x\n",
		"stray end":        "text\n" + MemoryBlockEnd + "\n",
		"two blocks":       managed("- a") + managed("- b"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ApplyManagedMemoryBlock(existing, true, "- new"); !errors.Is(err, ErrManagedBlockMalformed) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if strings.Contains(MemoryBlockStart, "\n") || !strings.HasPrefix(MemoryBlockStart, "<!--") {
		t.Fatal("markers must be single-line HTML comments, invisible when rendered")
	}
}
