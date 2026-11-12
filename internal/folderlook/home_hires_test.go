package folderlook

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// A provider's own one-sentence description, longer than the 100 characters
// a job line may take.
const longRoleDescription = "Coordinates portfolio status, priorities, milestones, blockers, deliverables, and explicit handoffs without inheriting project access."

func TestJobLine_FirstSentenceRunesWordCutAndPathShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a long sentence is cut at a word, at 100 characters with its ellipsis", longRoleDescription,
			"Coordinates portfolio status, priorities, milestones, blockers, deliverables, and explicit handoffs…"},
		{"the first sentence only", "Plans the week. Then reviews it afterwards.", "Plans the week."},
		{"a question ends a sentence too", "Is it done? Yes it is.", "Is it done?"},
		{"an exclamation as well", "Ship it! Then rest.", "Ship it!"},
		{"a short line is kept as it is", "Keeps the shelf tidy", "Keeps the shelf tidy"},
		{"a dot inside a word is not a sentence end", "Reads v0.5 notes and files them", "Reads v0.5 notes and files them"},
		{"whitespace collapses", "  Keeps\tthe\n shelf   tidy ", "Keeps the shelf tidy"},
		{"nothing in, nothing out", "   ", ""},
		// The 100th character is the "d" of a word, so the cut steps back to the
		// space after the sixteenth "words" (index 95), never ending mid-word.
		{"a word cut mid-word steps back to the last whole word", strings.Repeat("words ", 30),
			strings.TrimSpace(strings.Repeat("words ", 16)) + "…"},
		{"trailing punctuation is trimmed before the ellipsis", strings.Repeat("word ", 19) + "x, " + strings.Repeat("y", 40),
			strings.TrimSpace(strings.Repeat("word ", 19)) + " x…"},
		{"a path shape blanks only that job: slash", "Files things in /Users/someone/Music", ""},
		{"a path shape blanks only that job: and/or", "Plans and/or reviews", ""},
		{"a path shape blanks only that job: backslash", `Reads C:\Music`, ""},
		{"a path shape blanks only that job: home", "~root things", ""},
		{"a path shape blanks only that job: NUL", "Keeps\x00things", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JobLine(tc.in)
			if got != tc.want {
				t.Fatalf("JobLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > MaxJobLineRunes {
				t.Fatalf("%d characters, at most %d", n, MaxJobLineRunes)
			}
		})
	}

	// The music role's sentence comes to exactly 100 characters.
	if n := utf8.RuneCountInString(JobLine(longRoleDescription)); n != 100 {
		t.Fatalf("the long description comes to %d characters, want exactly 100", n)
	}

	// Characters are counted, not bytes: a long run of three-byte characters
	// with no space is cut to 99 plus the ellipsis, and stays valid text.
	long := strings.Repeat("歌", 150)
	got := JobLine(long)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 100 || !strings.HasSuffix(got, "…") ||
		strings.Count(got, "歌") != 99 {
		t.Fatalf("a long run of wide characters = %q (%d characters)", got, utf8.RuneCountInString(got))
	}
	// A word with no space to step back to is cut where it falls.
	if got := JobLine(strings.Repeat("a", 150)); got != strings.Repeat("a", 99)+"…" {
		t.Fatalf("an unbroken word = %q", got)
	}
}

func TestCleanHomeFacts_ALongJobKeepsTheRoster(t *testing.T) {
	out := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: []HomeHire{
		{Name: "Portfolio Manager", Job: longRoleDescription},
		{Name: "Library Manager", Job: "Keeps the shelf tidy."},
	}})
	if !out.RolesKnown || !reflect.DeepEqual(out.MissingRoles, []string{"Portfolio Manager", "Library Manager"}) {
		t.Fatalf("a long job discarded the roster: %+v", out)
	}
	if got := out.Hires[0].Job; got != JobLine(longRoleDescription) || utf8.RuneCountInString(got) > MaxJobLineRunes {
		t.Fatalf("the long job = %q", got)
	}
	if out.Hires[1].Job != "Keeps the shelf tidy." {
		t.Fatalf("the short job = %q", out.Hires[1].Job)
	}
	// A path-shaped job blanks itself and nothing else.
	odd := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: []HomeHire{{Name: "A", Job: "Reads /etc/things"}, {Name: "B", Job: "Fine."}}})
	if !odd.RolesKnown || odd.Hires[0].Job != "" || odd.Hires[1].Job != "Fine." {
		t.Fatalf("an odd job spread: %+v", odd.Hires)
	}
}

func TestCleanHomeFacts_ABadNameOrNineHiresLeaveRolesUnknown(t *testing.T) {
	for _, bad := range []string{"Bad\nName", "/Users/someone/agent", "~home", strings.Repeat("n", maxHomeFactText+1), "  "} {
		out := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: []HomeHire{{Name: "Portfolio Manager", Job: "Fine."}, {Name: bad}}})
		if out.RolesKnown || out.MissingRoles != nil || out.Hires != nil {
			t.Fatalf("name %q left the roles known: %+v", bad, out)
		}
	}
	nine := make([]HomeHire, maxHomeRoles+1)
	for i := range nine {
		nine[i] = HomeHire{Name: "Role"}
	}
	if out := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: nine}); out.RolesKnown || out.Hires != nil || out.MissingRoles != nil {
		t.Fatalf("nine hires stayed known: %+v", out)
	}
	// The host saying it does not know stays unknown, whatever it listed.
	if out := cleanHomeFacts(HomeFacts{RolesKnown: false, Hires: []HomeHire{{Name: "A"}}}); out.RolesKnown || out.Hires != nil {
		t.Fatalf("an unknown roster was kept: %+v", out)
	}
	// Eight is the most a Home declares and is kept.
	eight := nine[:maxHomeRoles]
	if out := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: eight}); !out.RolesKnown || len(out.Hires) != maxHomeRoles {
		t.Fatalf("eight hires were refused: %+v", out)
	}
}

func TestCleanHomeFacts_LegacyMissingRolesStillRead(t *testing.T) {
	// A record from before the hires were described has names only.
	out := cleanHomeFacts(HomeFacts{RolesKnown: true, MissingRoles: []string{"Portfolio Manager", "Library Manager"}})
	if !out.RolesKnown || !reflect.DeepEqual(out.MissingRoles, []string{"Portfolio Manager", "Library Manager"}) {
		t.Fatalf("names-only roles were lost: %+v", out)
	}
	if len(out.Hires) != 2 || out.Hires[0] != (HomeHire{Name: "Portfolio Manager"}) || out.Hires[1] != (HomeHire{Name: "Library Manager"}) {
		t.Fatalf("hires from names = %+v", out.Hires)
	}
	// The two lists stay aligned, and a taken name keeps what it replaced.
	both := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: []HomeHire{
		{Name: "Portfolio Manager 2", Job: "Plans.", Taken: "Portfolio Manager"},
		{Name: "Library Manager"},
	}})
	if !reflect.DeepEqual(both.MissingRoles, []string{"Portfolio Manager 2", "Library Manager"}) ||
		both.Hires[0].Taken != "Portfolio Manager" || both.Hires[1].Taken != "" {
		t.Fatalf("hires = %+v roles = %v", both.Hires, both.MissingRoles)
	}
	// A taken name that reads like a path is dropped, not the hire.
	odd := cleanHomeFacts(HomeFacts{RolesKnown: true, Hires: []HomeHire{{Name: "A 2", Taken: "/x/A"}}})
	if !odd.RolesKnown || odd.Hires[0].Taken != "" || odd.Hires[0].Name != "A 2" {
		t.Fatalf("a bad taken name = %+v", odd)
	}
	// Roles known and none missing (a Home with every role filled) is known and empty.
	none := cleanHomeFacts(HomeFacts{RolesKnown: true, HomeExists: true})
	if !none.RolesKnown || len(none.Hires) != 0 || len(none.MissingRoles) != 0 {
		t.Fatalf("no missing roles = %+v", none)
	}
}
