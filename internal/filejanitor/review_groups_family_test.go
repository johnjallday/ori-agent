package filejanitor

import (
	"regexp"
	"strings"
	"testing"
)

func TestVersionEnding_NamesTheKindOfEnding(t *testing.T) {
	for name, want := range map[string]string{
		"Budget.pdf":                      EndingNone,
		"Budget (2).pdf":                  EndingNumber,
		"Budget(3).pdf":                   EndingNumber,
		"Budget copy.pdf":                 EndingCopy,
		"Budget copy 2.pdf":               EndingCopy,
		"Budget - Copy.pdf":               EndingCopy,
		"Budget_copy_12.pdf":              EndingCopy,
		"Budget-v2.pdf":                   EndingV,
		"Budget v12.pdf":                  EndingV,
		"Budget version 2.pdf":            EndingVersion,
		"Budget_version_3.pdf":            EndingVersion,
		"Budget version.pdf":              EndingNone, // no number, so not a version ending
		"Résumé (2).pdf":                  EndingNumber,
		"‮report (2).pdf":                 EndingNumber,
		"notes.txt":                       EndingNone,
		strings.Repeat("a", 1100) + "(2)": EndingNone, // over the name bound: never classified
	} {
		if got := versionEnding(name); got != want {
			t.Errorf("%q: ending %q, want %q", name, got, want)
		}
	}
}

func familyOf(t *testing.T, groups ReviewGroups, name string) ReviewGroupMember {
	t.Helper()
	for _, group := range groups.Groups {
		members, _ := groups.Members(group.ID)
		for _, member := range members {
			if member.Candidate.Name == name {
				return member
			}
		}
	}
	t.Fatalf("no member named %q", name)
	return ReviewGroupMember{}
}

var (
	familyPattern = regexp.MustCompile(`^fam-[0-9a-f]{24}$`)
	bundlePattern = regexp.MustCompile(`^bun-[0-9a-f]{24}$`)
)

// The files of one name apart from an ending share one family, with their ending
// kinds; a different extension is a different family; a file with no sibling has no
// family at all.
func TestAnnotateMembers_FamiliesAndEndings(t *testing.T) {
	batch, candidates := groupFixture(
		"Budget.pdf", "Budget (2).pdf", "Budget-v2.pdf", "Budget copy 3.pdf", "Budget (2).docx", "Budget.docx", "alone.pdf",
	)
	groups := mustGroup(t, batch, candidates)
	original := familyOf(t, groups, "Budget.pdf")
	if !familyPattern.MatchString(original.Family) || original.Ending != EndingNone {
		t.Fatalf("the original: %+v", original)
	}
	for name, ending := range map[string]string{"Budget (2).pdf": EndingNumber, "Budget-v2.pdf": EndingV, "Budget copy 3.pdf": EndingCopy} {
		member := familyOf(t, groups, name)
		if member.Family != original.Family || member.Ending != ending {
			t.Errorf("%s: family %q ending %q, want family %q ending %q", name, member.Family, member.Ending, original.Family, ending)
		}
	}
	docx := familyOf(t, groups, "Budget.docx")
	if docx.Family == "" || docx.Family == original.Family || familyOf(t, groups, "Budget (2).docx").Family != docx.Family {
		t.Fatalf("a different extension is its own family: %q vs %q", docx.Family, original.Family)
	}
	if alone := familyOf(t, groups, "alone.pdf"); alone.Family != "" || alone.Ending != "" {
		t.Fatalf("a file that is nobody's copy has no family: %+v", alone)
	}
}

// Files that share a name apart from the extension are one bundle; a song with no
// partner is not in one.
func TestAnnotateMembers_Bundles(t *testing.T) {
	batch, candidates := groupFixture("Song.wav", "Song.als", "Song.mp3", "Other.wav", "Other.als", "solo.mp3")
	groups := mustGroup(t, batch, candidates)
	song := familyOf(t, groups, "Song.wav")
	if !bundlePattern.MatchString(song.Bundle) {
		t.Fatalf("Song.wav: %+v", song)
	}
	for _, name := range []string{"Song.als", "Song.mp3"} {
		if got := familyOf(t, groups, name).Bundle; got != song.Bundle {
			t.Errorf("%s should share Song.wav's bundle: %q vs %q", name, got, song.Bundle)
		}
	}
	other := familyOf(t, groups, "Other.wav")
	if other.Bundle == "" || other.Bundle == song.Bundle || familyOf(t, groups, "Other.als").Bundle != other.Bundle {
		t.Fatalf("a second pair of files is a second bundle: %q vs %q", other.Bundle, song.Bundle)
	}
	if solo := familyOf(t, groups, "solo.mp3"); solo.Bundle != "" {
		t.Fatalf("a song with no partner is not a bundle: %+v", solo)
	}
}

// The identifiers say nothing about a file: they are a keyed hash, they never repeat
// a name, they are stable for one scan and they do not carry across scans or scopes.
func TestAnnotateMembers_IdentifiersAreOpaqueAndStable(t *testing.T) {
	names := []string{"Budget.pdf", "Budget (2).pdf", "Song.wav", "Song.als", "‮report.pdf", "‮report (2).pdf", "café menu.pdf", "café menu (2).pdf"}
	batch, candidates := groupFixture(names...)
	first := mustGroup(t, batch, candidates)
	again := mustGroup(t, batch, candidates)
	for _, name := range names {
		a, b := familyOf(t, first, name), familyOf(t, again, name)
		if a.Family != b.Family || a.Bundle != b.Bundle {
			t.Errorf("%s: identifiers changed between two reads of one scan", name)
		}
		for _, id := range []string{a.Family, a.Bundle} {
			lower := strings.ToLower(id)
			for _, word := range []string{"budget", "song", "report", "menu", "pdf", "wav"} {
				if strings.Contains(lower, word) {
					t.Errorf("identifier %q carries part of a name (%q)", id, word)
				}
			}
		}
	}
	other, err := GroupReview(batch, candidates, "another-scope")
	if err != nil {
		t.Fatal(err)
	}
	if familyOf(t, other, "Budget.pdf").Family == familyOf(t, first, "Budget.pdf").Family {
		t.Fatal("an identifier must not carry across scopes")
	}
	next := batch
	next.ID = "batch-2"
	for i := range candidates {
		candidates[i].BatchID = next.ID
	}
	moved, err := GroupReview(next, candidates, "validated-scope")
	if err != nil {
		t.Fatal(err)
	}
	if familyOf(t, moved, "Budget.pdf").Family == familyOf(t, first, "Budget.pdf").Family {
		t.Fatal("an identifier must not carry across scans")
	}
}

// A hostile or very long name never breaks grouping, and never gets an identifier it
// should not have.
func TestAnnotateMembers_HostileAndLongNames(t *testing.T) {
	long := strings.Repeat("x", 1100)
	batch, candidates := groupFixture(long+".pdf", long+" (2).pdf", "a\nb (2).pdf", "a\nb.pdf", "\U0001F3B5 song.wav", "\U0001F3B5 song.als")
	groups := mustGroup(t, batch, candidates)
	for _, name := range []string{long + ".pdf", long + " (2).pdf"} {
		if member := familyOf(t, groups, name); member.Family != "" || member.Bundle != "" {
			t.Errorf("a name over the bound must stay out of every family and bundle: %+v", member)
		}
	}
	if familyOf(t, groups, "a\nb.pdf").Family != familyOf(t, groups, "a\nb (2).pdf").Family {
		t.Error("a name with a newline still groups with its copy")
	}
	if got := familyOf(t, groups, "\U0001F3B5 song.wav").Bundle; !bundlePattern.MatchString(got) || got != familyOf(t, groups, "\U0001F3B5 song.als").Bundle {
		t.Errorf("an emoji name bundles with its sibling: %q", got)
	}
}
