package secondbrain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fillInNow = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func heldFact(text string) HeldFact {
	return HeldFact{Text: text, How: "You told Ori this", Date: "2 Nov 2026"}
}

func fillInHoldings() Holdings {
	return Holdings{
		Facts:            []HeldFact{heldFact("You mix on Fridays."), heldFact("You keep invoices in Drive.")},
		WorkingAgreement: "Keep my week honest.",
		People:           []HeldFact{heldFact("Dana Lee is my distributor."), heldFact("Sam Ortiz runs the studio.")},
	}
}

func reconcileTree(t *testing.T, vault string, ledger []Mirror, holdings Holdings) SyncOutcome {
	t.Helper()
	return Reconcile(newWriterFor(t, vault), ledger, PlanNotes(holdings), fillInNow)
}

func mirrorFor(t *testing.T, ledger []Mirror, rel string) Mirror {
	t.Helper()
	for _, mirror := range ledger {
		if mirror.Rel == rel {
			return mirror
		}
	}
	t.Fatalf("no ledger entry for %s in %+v", rel, ledger)
	return Mirror{}
}

func noteContent(t *testing.T, vault, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(rel))) // #nosec G304 -- the test's own temp tree
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReconcile_WritesTheNotesOnceAndASecondRunWritesNothing(t *testing.T) {
	tree := newWriterTree(t)
	first := reconcileTree(t, tree.vault, nil, fillInHoldings())
	if first.Err != nil || first.Written[NoteKindFacts] != 1 || first.Written[NoteKindPerson] != 2 || first.MorePending {
		t.Fatalf("first run = %+v", first)
	}
	facts := noteContent(t, tree.vault, FactsNoteRel)
	mark := ReadMark([]byte(facts))
	if !mark.OwnedByOri || mark.Kind != NoteKindFacts {
		t.Fatalf("facts note mark = %+v", mark)
	}
	for _, want := range []string{"# What Ori knows about you\n", "- You mix on Fridays.\n", "- You keep invoices in Drive.\n", "How you work with Ori: Keep my week honest.\n", "## How Ori knows this", "2 Nov 2026"} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts note lacks %q:\n%s", want, facts)
		}
	}
	person := noteContent(t, tree.vault, "People/Dana Lee.md")
	if mark := ReadMark([]byte(person)); !mark.OwnedByOri || mark.Kind != NoteKindPerson {
		t.Fatalf("person note mark = %+v", mark)
	}
	for _, want := range []string{"# Dana Lee\n", "Dana Lee is my distributor.\n", "## How Ori knows this\n\n- You told Ori this, 2 Nov 2026.\n"} {
		if !strings.Contains(person, want) {
			t.Errorf("person note lacks %q:\n%s", want, person)
		}
	}

	before := snapshot(t, tree.base)
	second := reconcileTree(t, tree.vault, first.Mirrors, fillInHoldings())
	if second.WrittenTotal() != 0 || second.Err != nil || second.MorePending {
		t.Fatalf("second run = %+v", second)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("a second run changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if fmt.Sprint(second.Mirrors) != fmt.Sprint(first.Mirrors) {
		t.Fatalf("a second run changed the ledger:\n%+v\n%+v", first.Mirrors, second.Mirrors)
	}
}

func TestReconcile_StopsAtTheWriteBoundAndTheNextRunContinues(t *testing.T) {
	tree := newWriterTree(t)
	holdings := Holdings{Facts: []HeldFact{heldFact("You mix on Fridays.")}}
	for i := range MaxSyncWrites + 5 {
		holdings.People = append(holdings.People, heldFact(fmt.Sprintf("Person%c%c Lee is a friend.", 'A'+i/26, 'a'+i%26)))
	}
	first := reconcileTree(t, tree.vault, nil, holdings)
	if first.WrittenTotal() != MaxSyncWrites || !first.MorePending {
		t.Fatalf("first run wrote %d, pending %v", first.WrittenTotal(), first.MorePending)
	}
	second := reconcileTree(t, tree.vault, first.Mirrors, holdings)
	if second.WrittenTotal() != 6 || second.MorePending {
		t.Fatalf("second run wrote %d, pending %v", second.WrittenTotal(), second.MorePending)
	}
	third := reconcileTree(t, tree.vault, second.Mirrors, holdings)
	if third.WrittenTotal() != 0 || third.MorePending {
		t.Fatalf("third run wrote %d, pending %v", third.WrittenTotal(), third.MorePending)
	}
}

// A seed the setup wrote holds the same person: it is adopted, not rewritten and
// not duplicated, and it updates like any other note once what Ori knows changes.
func TestReconcile_AdoptsASeedRatherThanDuplicatingIt(t *testing.T) {
	tree := newWriterTree(t)
	seed := string(RenderNote(NoteKindSeed, []byte("# Dana Lee\n\nDana Lee is my distributor.\n")))
	writeNote(t, tree.vault, "People/Dana Lee.md", seed)
	holdings := Holdings{People: []HeldFact{heldFact("Dana Lee is my distributor.")}}

	before := snapshot(t, tree.base)
	first := reconcileTree(t, tree.vault, nil, holdings)
	if first.WrittenTotal() != 0 || first.Err != nil {
		t.Fatalf("adopting wrote: %+v", first)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("adopting changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if mirror := mirrorFor(t, first.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorCurrent {
		t.Fatalf("adopted entry = %+v", mirror)
	}

	holdings.People = append(holdings.People, heldFact("Dana Lee prefers email."))
	second := reconcileTree(t, tree.vault, first.Mirrors, holdings)
	if second.Written[NoteKindPerson] != 1 {
		t.Fatalf("a changed person = %+v", second)
	}
	content := noteContent(t, tree.vault, "People/Dana Lee.md")
	if mark := ReadMark([]byte(content)); !mark.OwnedByOri || mark.Kind != NoteKindPerson {
		t.Fatalf("mark = %+v", mark)
	}
	if !strings.Contains(content, "Dana Lee is my distributor.\n") || !strings.Contains(content, "Dana Lee prefers email.\n") {
		t.Fatalf("the note = %q", content)
	}
	entries, err := os.ReadDir(filepath.Join(tree.vault, "People"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("People holds %v, %v", entries, err)
	}
}

// A crash between the write and the ledger's save leaves a note with no entry.
// The next run recognises its own note and writes nothing.
func TestReconcile_RecognisesItsOwnNoteAfterALostLedger(t *testing.T) {
	tree := newWriterTree(t)
	if first := reconcileTree(t, tree.vault, nil, fillInHoldings()); first.WrittenTotal() != 3 {
		t.Fatalf("first run = %+v", first)
	}
	before := snapshot(t, tree.base)
	again := reconcileTree(t, tree.vault, nil, fillInHoldings())
	if again.WrittenTotal() != 0 || len(again.Mirrors) != 3 {
		t.Fatalf("a lost ledger = %+v", again)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestReconcile_ANoteTheUserHasIsTheirsAndNeverWritten(t *testing.T) {
	tree := newWriterTree(t)
	writeNote(t, tree.vault, "People/Dana Lee.md", "# Dana\n\nMy own words.\n")
	before := snapshot(t, tree.base)
	outcome := reconcileTree(t, tree.vault, nil, Holdings{People: []HeldFact{heldFact("Dana Lee is my distributor.")}})
	if outcome.WrittenTotal() != 0 || outcome.Err != nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	if mirror := mirrorFor(t, outcome.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorUserOwned {
		t.Fatalf("entry = %+v", mirror)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestReconcile_AnEditEndsOriWritingThatNoteForGood(t *testing.T) {
	tree := newWriterTree(t)
	holdings := fillInHoldings()
	first := reconcileTree(t, tree.vault, nil, holdings)
	full := filepath.Join(tree.vault, "People", "Dana Lee.md")
	edited := noteContent(t, tree.vault, "People/Dana Lee.md") + "A line the user added.\n"
	if err := os.WriteFile(full, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	holdings.People = append(holdings.People, heldFact("Dana Lee prefers email."))
	second := reconcileTree(t, tree.vault, first.Mirrors, holdings)
	if second.Written[NoteKindPerson] != 0 || second.Err != nil {
		t.Fatalf("second run = %+v", second)
	}
	if mirror := mirrorFor(t, second.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorUserOwned {
		t.Fatalf("entry = %+v", mirror)
	}
	if got := noteContent(t, tree.vault, "People/Dana Lee.md"); got != edited {
		t.Fatalf("the user's edit was lost: %q", got)
	}
	// Putting the note back exactly as Ori wrote it does not hand it back either.
	if err := os.WriteFile(full, []byte(RenderNote(NoteKindPerson, []byte("# Dana Lee\n"))), 0o600); err != nil {
		t.Fatal(err)
	}
	restored := noteContent(t, tree.vault, "People/Dana Lee.md")
	third := reconcileTree(t, tree.vault, second.Mirrors, holdings)
	if third.WrittenTotal() != 0 || noteContent(t, tree.vault, "People/Dana Lee.md") != restored {
		t.Fatalf("third run = %+v", third)
	}
}

func TestReconcile_ANoteTheUserDeletedOrMovedStaysGone(t *testing.T) {
	tree := newWriterTree(t)
	holdings := fillInHoldings()
	first := reconcileTree(t, tree.vault, nil, holdings)
	if err := os.Remove(filepath.Join(tree.vault, "People", "Dana Lee.md")); err != nil {
		t.Fatal(err)
	}
	holdings.People = append(holdings.People, heldFact("Dana Lee prefers email."))
	second := reconcileTree(t, tree.vault, first.Mirrors, holdings)
	if second.WrittenTotal() != 0 {
		t.Fatalf("second run = %+v", second)
	}
	if mirror := mirrorFor(t, second.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorUserOwned {
		t.Fatalf("entry = %+v", mirror)
	}
	if _, err := os.Lstat(filepath.Join(tree.vault, "People", "Dana Lee.md")); !os.IsNotExist(err) {
		t.Fatalf("a deleted note came back: %v", err)
	}
}

// Forgetting deletes nothing: the person's note stays and goes stale, and the
// facts note drops the line.
func TestReconcile_AForgottenFactLeavesThePersonAndDropsTheLine(t *testing.T) {
	tree := newWriterTree(t)
	holdings := fillInHoldings()
	first := reconcileTree(t, tree.vault, nil, holdings)
	dana := noteContent(t, tree.vault, "People/Dana Lee.md")

	holdings.Facts = holdings.Facts[1:]
	holdings.People = holdings.People[1:]
	second := reconcileTree(t, tree.vault, first.Mirrors, holdings)
	if second.Written[NoteKindFacts] != 1 || second.Written[NoteKindPerson] != 0 {
		t.Fatalf("second run = %+v", second)
	}
	if facts := noteContent(t, tree.vault, FactsNoteRel); strings.Contains(facts, "You mix on Fridays.") || !strings.Contains(facts, "You keep invoices in Drive.") {
		t.Fatalf("facts note = %q", facts)
	}
	if got := noteContent(t, tree.vault, "People/Dana Lee.md"); got != dana {
		t.Fatalf("the person's note changed: %q", got)
	}
	if mirror := mirrorFor(t, second.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorStale {
		t.Fatalf("entry = %+v", mirror)
	}

	// Approving them again brings the entry back, with nothing written twice.
	holdings = fillInHoldings()
	third := reconcileTree(t, tree.vault, second.Mirrors, holdings)
	if mirror := mirrorFor(t, third.Mirrors, "People/Dana Lee.md"); mirror.State != MirrorCurrent || third.Written[NoteKindPerson] != 0 {
		t.Fatalf("entry = %+v, written %v", mirror, third.Written)
	}
}

func TestReconcile_TheFactsNoteIsNotCreatedEmptyButIsEmptiedWhenTheLastFactGoes(t *testing.T) {
	tree := newWriterTree(t)
	empty := reconcileTree(t, tree.vault, nil, Holdings{})
	if empty.WrittenTotal() != 0 || len(empty.Mirrors) != 0 {
		t.Fatalf("nothing held = %+v", empty)
	}
	if _, err := os.Lstat(filepath.Join(tree.vault, FactsNoteRel)); !os.IsNotExist(err) {
		t.Fatalf("an empty facts note was created: %v", err)
	}
	first := reconcileTree(t, tree.vault, nil, Holdings{Facts: []HeldFact{heldFact("You mix on Fridays.")}})
	second := reconcileTree(t, tree.vault, first.Mirrors, Holdings{})
	if second.Written[NoteKindFacts] != 1 {
		t.Fatalf("second run = %+v", second)
	}
	if facts := noteContent(t, tree.vault, FactsNoteRel); strings.Contains(facts, "You mix on Fridays.") {
		t.Fatalf("facts note = %q", facts)
	}
}

func TestPlanNotes_SecretsNeverReachANoteAndSentencesAboutOnePersonShareIt(t *testing.T) {
	secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
	notes := PlanNotes(Holdings{
		Facts: []HeldFact{heldFact("My key is " + secret), heldFact("You mix on Fridays.")},
		People: []HeldFact{
			heldFact("Dana Lee is my distributor."), heldFact("dana lee prefers email."), heldFact("Dana Lee has the token " + secret),
			heldFact("nobody in particular"),
		},
		WorkingAgreement: "Use " + secret,
	})
	if len(notes) != 2 {
		t.Fatalf("notes = %d: %+v", len(notes), notes)
	}
	for _, note := range notes {
		if strings.Contains(string(note.Body), secret) {
			t.Errorf("%s carries the secret:\n%s", note.Rel, note.Body)
		}
	}
	if !strings.Contains(string(notes[0].Body), "You mix on Fridays.") || strings.Contains(string(notes[0].Body), "How you work with Ori") {
		t.Errorf("facts note:\n%s", notes[0].Body)
	}
}

func TestPersonNoteRel_ALongNameIsCutInTheFileNameOnly(t *testing.T) {
	long := strings.Repeat("語", 80)
	rel := personNoteRel(long)
	if !mirrorRelFits(rel) {
		t.Fatalf("rel does not fit: %s", rel)
	}
	if _, _, err := splitOwnedPath(rel); err != nil {
		t.Fatalf("rel %q: %v", rel, err)
	}
	if got := personNoteRel("Readme"); got != "People/Readme (person).md" {
		t.Fatalf("a person called Readme = %s", got)
	}
	if _, _, err := splitOwnedPath(personNoteRel("Readme")); err != nil {
		t.Fatal(err)
	}
}

// A follow-up Ori copied to Inbox is the user's follow-up already: the look marks
// it as it marks a seed, and never suggests it back (FR3).
func TestSeededNotes_TreatsAFollowUpNoteLikeASeedInInboxOnly(t *testing.T) {
	note := func(rel, kind string) Note {
		return Note{RelPath: rel, Content: RenderNote(kind, []byte("- [ ] Call Dana\n"))}
	}
	reading := Reading{Notes: []Note{
		note("Inbox/seed.md", NoteKindSeed), note("Inbox/follow-up.md", NoteKindFollowUp), note("Inbox/capture.md", NoteKindCapture),
		note("People/seed.md", NoteKindSeed), {RelPath: "Inbox/mine.md", Content: []byte("- [ ] Call Dana\n")},
	}}
	seeded := seededNotes(createdSource("/brain"), reading)
	if len(seeded) != 2 || !seeded["Inbox/seed.md"] || !seeded["Inbox/follow-up.md"] {
		t.Fatalf("seeded = %v", seeded)
	}
	if connected := seededNotes(testSource("/brain"), reading); connected != nil {
		t.Fatalf("a connected folder = %v", connected)
	}
}

// TestSync_ChangesOnlyOriOwnedTargets is the fill-in's never-touch proof (FR12):
// a full run against a tree of user notes, links and Ori notes the user edited,
// comparing a byte, mode and mtime snapshot before and after.
func TestSync_ChangesOnlyOriOwnedTargets(t *testing.T) {
	tree := newWriterTree(t)
	addFillInTargets(t, tree)
	// Each target of the plan below is taken some other way.
	writeNote(t, tree.vault, "People/Mine.md", "# Mine\n\nThe user's note about Mine.\n")
	writeNote(t, tree.vault, "People/Edited Lee.md", string(RenderNote(NoteKindSeed, []byte("# Edited Lee\n")))+"A line the user added.\n")
	if err := os.Symlink(filepath.Join(tree.outside, "secret.md"), filepath.Join(tree.vault, "People", "Linked Lee.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree.vault, "README.md"), filepath.Join(tree.vault, FactsNoteRel)); err != nil {
		t.Fatal(err)
	}
	holdings := Holdings{
		Facts: []HeldFact{heldFact("You mix on Fridays.")},
		People: []HeldFact{
			heldFact("Mine Park is a friend."), heldFact("Edited Lee is a friend."), heldFact("Linked Lee is a friend."),
			heldFact("Fresh Lee is a friend."),
		},
	}

	before := snapshot(t, tree.base)
	outcome := reconcileTree(t, tree.vault, nil, holdings)
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if outcome.WrittenTotal() != 2 {
		t.Fatalf("written = %v", outcome.Written)
	}
	if mirror := mirrorFor(t, outcome.Mirrors, FactsNoteRel); mirror.State != MirrorUserOwned {
		t.Fatalf("a link at the facts note = %+v", mirror)
	}
	for _, rel := range []string{"People/Edited Lee.md", "People/Linked Lee.md"} {
		if mirror := mirrorFor(t, outcome.Mirrors, rel); mirror.State != MirrorUserOwned {
			t.Fatalf("%s = %+v", rel, mirror)
		}
	}
	base := filepath.Base(tree.vault)
	skip := []string{
		filepath.Join(base, "People"),
		filepath.Join(base, "People", "Mine Park.md"),
		filepath.Join(base, "People", "Fresh Lee.md"),
	}
	if got, want := withoutPath(snapshot(t, tree.base), skip...), withoutPath(before, skip...); got != want {
		t.Fatalf("a sync changed what is not Ori's.\nbefore:\n%s\nafter:\n%s", want, got)
	}
	assertNoTempFiles(t, tree.vault)

	settled := snapshot(t, tree.base)
	again := Reconcile(newWriterFor(t, tree.vault), outcome.Mirrors, PlanNotes(holdings), fillInNow.Add(time.Hour))
	if again.WrittenTotal() != 0 || snapshot(t, tree.base) != settled {
		t.Fatalf("a second run wrote: %+v", again)
	}
}
