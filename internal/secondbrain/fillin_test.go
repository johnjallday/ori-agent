package secondbrain

import (
	"errors"
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

// The facts note a setup writes is the note the first sync would write, so the
// sync adopts it and says nothing: no write, and nothing for the Today row.
func TestReconcile_AdoptsTheFactsNoteTheSetupWrote(t *testing.T) {
	parent := canonicalTemp(t)
	holdings := fillInHoldings()
	made, err := Creator{}.Create(parent, "Notes", Seeds{Facts: holdings.Facts, WorkingAgreement: holdings.WorkingAgreement})
	if err != nil || !made.Facts {
		t.Fatalf("Create = %+v, %v", made, err)
	}
	holdings.People = nil
	before := snapshot(t, parent)
	outcome := Reconcile(newWriterFor(t, made.Path), nil, PlanNotes(holdings), fillInNow)
	if outcome.WrittenTotal() != 0 || len(outcome.Added) != 0 || outcome.Err != nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	if entry := mirrorFor(t, outcome.Mirrors, FactsNoteRel); entry.State != MirrorCurrent {
		t.Fatalf("entry = %+v", entry)
	}
	if after := snapshot(t, parent); after != before {
		t.Fatalf("adopting changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// A fact approved later is a refresh of the same note.
	holdings.Facts = append(holdings.Facts, heldFact("You keep the masters on a drive."))
	next := Reconcile(newWriterFor(t, made.Path), outcome.Mirrors, PlanNotes(holdings), fillInNow)
	if next.Written[NoteKindFacts] != 1 || !strings.Contains(noteContent(t, made.Path, FactsNoteRel), "You keep the masters on a drive.") {
		t.Fatalf("next = %+v", next)
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

func followUpHoldings(followUps ...HeldFollowUp) Holdings { return Holdings{FollowUps: followUps} }

func TestReconcile_AFollowUpBecomesAnInboxNoteAndAnotherOfTheSameTitleGetsANumber(t *testing.T) {
	tree := newWriterTree(t)
	writeNote(t, tree.vault, "Inbox/call-dana-2.md", "# The user's note at the next name\n")
	first := reconcileTree(t, tree.vault, nil, followUpHoldings(
		HeldFollowUp{ID: "f-1", Title: "Call Dana", Due: "2026-11-05", Source: "Added by you"},
		HeldFollowUp{ID: "f-2", Title: "Call Dana"},
	))
	if first.Err != nil || first.Written[NoteKindFollowUp] != 2 {
		t.Fatalf("first = %+v", first)
	}
	content := noteContent(t, tree.vault, "Inbox/call-dana.md")
	for _, want := range []string{"ori: follow_up", "# Call Dana\n", "- [ ] Call Dana 📅 2026-11-05\n", "From: Added by you\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("note lacks %q:\n%s", want, content)
		}
	}
	if _, err := os.Lstat(filepath.Join(tree.vault, "Inbox", "call-dana-3.md")); err != nil {
		t.Fatalf("the second follow-up skipped past the user's note: %v", err)
	}
	if got := noteContent(t, tree.vault, "Inbox/call-dana-2.md"); got != "# The user's note at the next name\n" {
		t.Fatalf("the user's note changed: %q", got)
	}
}

// The loop guard (FR4): a follow-up made from a line in the notes is never
// written back, so the next look cannot find it a second time.
func TestReconcile_AFollowUpFromTheNotesIsNeverWrittenBack(t *testing.T) {
	tree := newWriterTree(t)
	before := snapshot(t, tree.base)
	outcome := reconcileTree(t, tree.vault, nil, followUpHoldings(HeldFollowUp{ID: "f-1", Title: "Line from a note", FromNotes: true}))
	if outcome.WrittenTotal() != 0 || len(outcome.Mirrors) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestReconcile_AdoptsASeededFollowUpAndTicksItWhenDone(t *testing.T) {
	tree := newWriterTree(t)
	body, ok := followUpNote(SeedFollowUp{Title: "Call Dana", Source: "Added by you"})
	if !ok {
		t.Fatal("no seed")
	}
	writeNote(t, tree.vault, "Inbox/call-dana.md", string(RenderNote(NoteKindSeed, body)))
	open := HeldFollowUp{ID: "f-1", Title: "Call Dana", Source: "Added by you"}
	first := reconcileTree(t, tree.vault, nil, followUpHoldings(open))
	if first.WrittenTotal() != 0 || mirrorFor(t, first.Mirrors, "Inbox/call-dana.md").State != MirrorCurrent {
		t.Fatalf("adopting = %+v", first)
	}

	done := open
	done.Done = true
	second := reconcileTree(t, tree.vault, first.Mirrors, followUpHoldings(done))
	if second.Written[NoteKindFollowUp] != 1 || len(second.Added) != 0 {
		t.Fatalf("ticking = %+v", second)
	}
	content := noteContent(t, tree.vault, "Inbox/call-dana.md")
	mark := ReadMark([]byte(content))
	if !mark.OwnedByOri || mark.Kind != NoteKindSeed || !strings.Contains(content, "- [x] Call Dana\n") || strings.Contains(content, "- [ ]") {
		t.Fatalf("mark %+v, note:\n%s", mark, content)
	}
	if entry := mirrorFor(t, second.Mirrors, "Inbox/call-dana.md"); entry.State != MirrorStale {
		t.Fatalf("entry = %+v", entry)
	}
	before := snapshot(t, tree.base)
	third := reconcileTree(t, tree.vault, second.Mirrors, followUpHoldings(done))
	if third.WrittenTotal() != 0 || snapshot(t, tree.base) != before {
		t.Fatalf("a second tick wrote: %+v", third)
	}

	// Reopened in Ori, the note is opened again.
	fourth := reconcileTree(t, tree.vault, third.Mirrors, followUpHoldings(open))
	if fourth.Written[NoteKindFollowUp] != 1 || !strings.Contains(noteContent(t, tree.vault, "Inbox/call-dana.md"), "- [ ] Call Dana\n") {
		t.Fatalf("reopening = %+v", fourth)
	}
}

// An edited note gets no write, ever: the look's "Done in your notes" path stays
// the only one for it (FR5).
func TestReconcile_ACompletedFollowUpNeverTouchesAnEditedNote(t *testing.T) {
	tree := newWriterTree(t)
	open := HeldFollowUp{ID: "f-1", Title: "Call Dana"}
	first := reconcileTree(t, tree.vault, nil, followUpHoldings(open))
	edited := noteContent(t, tree.vault, "Inbox/call-dana.md") + "More from the user.\n"
	if err := os.WriteFile(filepath.Join(tree.vault, "Inbox", "call-dana.md"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	done := open
	done.Done = true
	second := reconcileTree(t, tree.vault, first.Mirrors, followUpHoldings(done))
	if second.WrittenTotal() != 0 || second.Err != nil {
		t.Fatalf("second = %+v", second)
	}
	if got := noteContent(t, tree.vault, "Inbox/call-dana.md"); got != edited {
		t.Fatalf("the edited note changed: %q", got)
	}
	if entry := mirrorFor(t, second.Mirrors, "Inbox/call-dana.md"); entry.State != MirrorUserOwned {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestReconcile_ACompletedFollowUpIsNeverWrittenForTheFirstTime(t *testing.T) {
	tree := newWriterTree(t)
	outcome := reconcileTree(t, tree.vault, nil, followUpHoldings(HeldFollowUp{ID: "f-1", Title: "Long done", Done: true}))
	if outcome.WrittenTotal() != 0 || len(outcome.Mirrors) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestWriterTick_OnlyInAnOriFollowUpOrSeedNote(t *testing.T) {
	tree := newWriterTree(t)
	writer := newWriterFor(t, tree.vault)
	writeNote(t, tree.vault, "Inbox/follow.md", string(RenderNote(NoteKindFollowUp, []byte("# A\n\n- [ ] A\n- [ ] B\n"))))
	writeNote(t, tree.vault, "Inbox/capture.md", string(RenderNote(NoteKindCapture, []byte("- [ ] mine\n"))))
	writeNote(t, tree.vault, "Inbox/edited.md", string(RenderNote(NoteKindFollowUp, []byte("- [ ] x\n")))+"edit\n")
	writeNote(t, tree.vault, "Inbox/users.md", "- [ ] the user's\n")
	if err := os.Symlink(filepath.Join(tree.vault, "Inbox", "follow.md"), filepath.Join(tree.vault, "Inbox", "link.md")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Inbox/capture.md", "Inbox/edited.md", "Inbox/users.md", "Inbox/link.md", "Inbox/absent.md"} {
		before := snapshot(t, tree.base)
		if _, _, err := writer.Tick(rel); !errors.Is(err, ErrUserModified) {
			t.Errorf("Tick(%s): err = %v", rel, err)
		}
		if after := snapshot(t, tree.base); after != before {
			t.Errorf("Tick(%s) changed the tree", rel)
		}
	}
	hash, ticked, err := writer.Tick("Inbox/follow.md")
	if err != nil || !ticked || hash == "" {
		t.Fatalf("Tick = %q, %v, %v", hash, ticked, err)
	}
	content := noteContent(t, tree.vault, "Inbox/follow.md")
	mark := ReadMark([]byte(content))
	if !mark.OwnedByOri || mark.Hash[:mirrorHashLength] != hash || !strings.HasSuffix(content, "- [x] A\n- [ ] B\n") {
		t.Fatalf("one line, and the hash rewritten: %+v\n%s", mark, content)
	}
	writeNote(t, tree.vault, "Inbox/none.md", string(RenderNote(NoteKindFollowUp, []byte("- [x] done\n"))))
	before := snapshot(t, tree.base)
	if _, ticked, err := writer.Tick("Inbox/none.md"); err != nil || ticked {
		t.Fatalf("nothing to tick = %v, %v", ticked, err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatal("a note with no open line was rewritten")
	}
}

func TestReconcile_AFullFollowUpLedgerMakesRoomFromClosedOnes(t *testing.T) {
	tree := newWriterTree(t)
	var held []HeldFollowUp
	for i := range MaxMirrorFollowUps {
		held = append(held, HeldFollowUp{ID: fmt.Sprintf("f-%d", i), Title: fmt.Sprintf("Item %d", i)})
	}
	var ledger []Mirror
	for range 10 {
		outcome := reconcileTree(t, tree.vault, ledger, followUpHoldings(held...))
		ledger = outcome.Mirrors
	}
	if len(ledger) != MaxMirrorFollowUps {
		t.Fatalf("ledger = %d", len(ledger))
	}
	// Every slot is taken by an open follow-up: a new one waits, and nothing is lost.
	extra := HeldFollowUp{ID: "f-new", Title: "Brand new"}
	full := reconcileTree(t, tree.vault, ledger, followUpHoldings(append(held[:len(held):len(held)], extra)...))
	if len(full.Mirrors) != MaxMirrorFollowUps || full.WrittenTotal() != 0 {
		t.Fatalf("over the cap = %d mirrors, %d written", len(full.Mirrors), full.WrittenTotal())
	}
	// One closes, and its entry makes room. Its note stays.
	done := held[0]
	done.Done = true
	rest := append([]HeldFollowUp{done}, held[1:]...)
	closed := reconcileTree(t, tree.vault, ledger, followUpHoldings(rest...))
	roomy := reconcileTree(t, tree.vault, closed.Mirrors, followUpHoldings(append(rest[:len(rest):len(rest)], extra)...))
	if len(roomy.Mirrors) != MaxMirrorFollowUps || roomy.Written[NoteKindFollowUp] != 1 {
		t.Fatalf("room = %d mirrors, written %v", len(roomy.Mirrors), roomy.Written)
	}
	if !strings.Contains(noteContent(t, tree.vault, "Inbox/item-0.md"), "- [x] Item 0") || !strings.Contains(noteContent(t, tree.vault, "Inbox/brand-new.md"), "Brand new") {
		t.Fatal("the closed note or the new one is wrong")
	}
	if err := (Record{Sources: []Source{withLedger(createdSource(tree.vault), roomy.Mirrors)}}).Validate(); err != nil {
		t.Fatalf("the ledger does not validate: %v", err)
	}
}

func withLedger(source Source, mirrors []Mirror) Source {
	source.FillIn = &FillIn{Mirrors: mirrors}
	return source
}

func TestReconcile_AWorkspaceBecomesAProjectNoteAndItsGoalRefreshesIt(t *testing.T) {
	tree := newWriterTree(t)
	project := HeldProject{
		ID: "ws-1", Name: "Launch: the album", Goal: "Ship the record.\n\n## Done looks like\n- [ ] Master is approved\nkey sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789",
		Folder: "/Users/sam/Music/Launch", Agents: []string{"Ada", "Bo"}, Date: "2 Nov 2026",
	}
	first := reconcileTree(t, tree.vault, nil, Holdings{Projects: []HeldProject{project}})
	if first.Written[NoteKindProject] != 1 || first.Added[NoteKindProject] != 1 {
		t.Fatalf("first = %+v", first)
	}
	rel := "Projects/Launch the album/README.md"
	content := noteContent(t, tree.vault, rel)
	if mark := ReadMark([]byte(content)); !mark.OwnedByOri || mark.Kind != NoteKindProject {
		t.Fatalf("mark = %+v", mark)
	}
	for _, want := range []string{"# Launch: the album\n", "Ship the record.\n", "## Done looks like\n- Master is approved\n", "Folder: /Users/sam/Music/Launch\n", "Agents: Ada, Bo\n", "[Open in Ori](/?workspace=ws-1)\n", "2 Nov 2026"} {
		if !strings.Contains(content, want) {
			t.Errorf("project note lacks %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "sk-ant") || strings.Contains(content, "- [ ]") {
		t.Errorf("a secret or a task line reached the note:\n%s", content)
	}

	project.Goal = "Ship the record by June."
	project.Name = "Renamed"
	second := reconcileTree(t, tree.vault, first.Mirrors, Holdings{Projects: []HeldProject{project}})
	if second.Written[NoteKindProject] != 1 || len(second.Added) != 0 {
		t.Fatalf("second = %+v", second)
	}
	// The note stays where it was: Ori never renames one.
	if got := noteContent(t, tree.vault, rel); !strings.Contains(got, "Ship the record by June.") || !strings.Contains(got, "# Renamed\n") {
		t.Fatalf("refreshed note:\n%s", got)
	}
	if _, err := os.Lstat(filepath.Join(tree.vault, "Projects", "Renamed")); !os.IsNotExist(err) {
		t.Fatalf("a rename made a second folder: %v", err)
	}
}

func TestProjectNoteRel_AlwaysAPathTheWriterAccepts(t *testing.T) {
	for _, name := range []string{"Launch", "a/b", "..", ".hidden", "name.", strings.Repeat("語", 200), strings.Repeat("&", 200), "README"} {
		rel := projectNoteRel(name)
		if _, _, err := splitOwnedPath(rel); err != nil {
			t.Errorf("projectNoteRel(%q) = %q: %v", name, rel, err)
		}
		if !mirrorRelFits(rel) {
			t.Errorf("projectNoteRel(%q) = %q does not fit", name, rel)
		}
	}
}

func TestReportSentence_SaysWhatWasAdded(t *testing.T) {
	for want, counts := range map[string]map[string]int{
		"Ori added 2 people and a project to your notes":                        {NoteKindPerson: 2, NoteKindProject: 1},
		"Ori added a person to your notes":                                      {NoteKindPerson: 1},
		"Ori added 3 follow-ups to your notes":                                  {NoteKindFollowUp: 3},
		"Ori added a person, 2 projects and a follow-up to your notes":          {NoteKindPerson: 1, NoteKindProject: 2, NoteKindFollowUp: 1},
		"Ori updated what it knows about you in your notes":                     {NoteKindFacts: 4},
		"Ori added a project to your notes and updated what it knows about you": {NoteKindProject: 1, NoteKindFacts: 1},
		"": {},
	} {
		if got := ReportSentence(counts); got != want {
			t.Errorf("ReportSentence(%v) = %q, want %q", counts, got, want)
		}
	}
}

func TestFillInReport_FoldsUntilShownThenStartsOver(t *testing.T) {
	var fillIn FillIn
	fillIn.AddToReport(map[string]int{}, fillInNow)
	if fillIn.Report != nil {
		t.Fatalf("nothing added = %+v", fillIn.Report)
	}
	fillIn.AddToReport(map[string]int{NoteKindPerson: 1}, fillInNow)
	fillIn.AddToReport(map[string]int{NoteKindPerson: 1, NoteKindProject: 1}, fillInNow.Add(time.Hour))
	if fillIn.Report.Counts[NoteKindPerson] != 2 || fillIn.Report.Counts[NoteKindProject] != 1 || !fillIn.Report.At.Equal(fillInNow.Add(time.Hour)) {
		t.Fatalf("report = %+v", fillIn.Report)
	}
	shown := fillInNow.Add(2 * time.Hour)
	fillIn.Report.ShownAt = &shown
	fillIn.AddToReport(map[string]int{NoteKindFollowUp: 1}, fillInNow.Add(3*time.Hour))
	if fillIn.Report.ShownAt != nil || fillIn.Report.Counts[NoteKindPerson] != 0 || fillIn.Report.Counts[NoteKindFollowUp] != 1 {
		t.Fatalf("after being shown = %+v", fillIn.Report)
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
