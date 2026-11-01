package secondbrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"unicode/utf8"
)

// seededNotes are the notes in Inbox that Ori copied from the user's own
// follow-ups, at setup or since, for a folder Ori created. A connected folder has
// none: a note there that happens to carry the same frontmatter is the user's.
func seededNotes(source Source, reading Reading) map[string]bool {
	if !source.CreatedByOri() {
		return nil
	}
	seeded := map[string]bool{}
	for _, note := range reading.Notes {
		if !strings.HasPrefix(note.RelPath, FolderInbox+"/") {
			continue
		}
		if kind := ReadMark(note.Content).Kind; kind == NoteKindSeed || kind == NoteKindFollowUp {
			seeded[note.RelPath] = true
		}
	}
	return seeded
}

// maxStoredItemBytes caps the encoded open items one look keeps, so the
// record stays well inside RecordMaxBytes whatever the items say.
const maxStoredItemBytes = 300 * 1024

// maxStoredSignalBytes does the same for the people, stale notes and decisions
// together. With the items at their budget and every item triaged, the record
// still fits RecordMaxBytes (TestSecondBrainCodec_ALookWithEveryCapFullFitsTheRecord).
const maxStoredSignalBytes = 80 * 1024

// Looker runs one look: read the folder within bounds, extract, choose the
// suggested items, and write the headline. It never waits on the user and
// never asks a question (FR27): every run ends finished or failed.
type Looker struct {
	// Options bounds the read; zero values take the reader's bounds.
	Options ReadOptions
	// Model writes the headline from the signals; nil keeps the rules-only
	// headline (FR23).
	Model HeadlineModel
	// Now defaults to time.Now.
	Now func() time.Time

	// read is Read; tests replace it to fail on purpose.
	read func(ctx context.Context, root string, opts ReadOptions) (Reading, error)
}

// LookRun is a finished or failed look plus what it found that is not kept.
type LookRun struct {
	Look Look
	// Signals are in memory only, for the headline and the Also noticed line.
	Signals Signals
	// Gone are the ids of the previous look's items that are no longer open:
	// ticked, marked DONE, or removed (FR29).
	Gone []string
	// HeadlineNote says why the rules-only headline stands ("" when the
	// model wrote it), for the server log. It never holds the model's answer.
	HeadlineNote string
}

func (l Looker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Run looks at source. previous is the last finished look, for the new and
// done counts (FR36); lookID and requestID name this look.
func (l Looker) Run(ctx context.Context, source Source, previous *Look, lookID, requestID string) LookRun {
	return l.run(ctx, source, previous, lookID, requestID, nil)
}

// RunSetup is the first look at a folder Ori has just set up: the same read,
// with a rules-only headline that reports what the setup seeded (FR10).
func (l Looker) RunSetup(ctx context.Context, source Source, lookID, requestID string, setup SetupCounts) LookRun {
	return l.run(ctx, source, nil, lookID, requestID, &setup)
}

// SetupCounts is what one setup seeded.
type SetupCounts struct {
	FollowUps int
	People    int
}

// EmptySetupHeadline is what a fresh second brain says when nothing was seeded:
// it is empty because Ori does not know the user yet, and the offer under it
// is the first thing to show it (FR13).
const EmptySetupHeadline = "Your second brain is empty because I don't know you yet. Show me a folder and I'll start filling it."

// SetupHeadline reports a fresh second brain honestly (FR10): what was seeded,
// or that it is empty and why when nothing was.
func SetupHeadline(counts SetupCounts) string {
	var parts []string
	if counts.FollowUps > 0 {
		parts = append(parts, plural(counts.FollowUps, "follow-up", "follow-ups")+" in Inbox")
	}
	if counts.People > 0 {
		parts = append(parts, plural(counts.People, "person", "people"))
	}
	if len(parts) == 0 {
		return EmptySetupHeadline
	}
	return "Your second brain is set up: " + strings.Join(parts, ", ") + "."
}

func (l Looker) run(ctx context.Context, source Source, previous *Look, lookID, requestID string, setup *SetupCounts) LookRun {
	started := l.now().UTC()
	look := Look{ID: lookID, RequestID: requestID, StartedAt: started}
	read := l.read
	if read == nil {
		read = Read
	}
	reading, err := read(ctx, source.Path, l.Options)
	if err != nil {
		finished := l.now().UTC()
		look.FinishedAt = &finished
		look.State = LookFailed
		look.Failure = FailureSentence(source.Name, err)
		return LookRun{Look: look}
	}

	now := l.now().UTC()
	signals := Extract(reading, now)
	look.State = LookFinished
	look.Counts = LookCounts{
		Notes: signals.Notes, OpenItems: len(signals.OpenLoops), NotesWithOpen: signals.NotesWithOpen,
		Dated: signals.Dated, Done: signals.Done, Partial: reading.Partial,
	}
	look.Items = lookItems(source, signals.OpenLoops, seededNotes(source, reading), doneItems(previous))
	look.People, look.Stale, look.Decisions = lookSignals(source, signals)
	look.Headline, look.HeadlineBy = RulesHeadline(source.Name, look.Counts), HeadlineByRules
	var note string
	if setup != nil {
		look.Headline, note = SetupHeadline(*setup), "setup"
	} else {
		var headline string
		headline, note = modelHeadline(ctx, l.Model, source.Name, look.Counts, signals, look.Items, now)
		if note == "" {
			look.Headline, look.HeadlineBy = headline, HeadlineByModel
		}
	}
	look.AlsoNoticed = AlsoNoticed(signals, now)
	finished := l.now().UTC()
	look.FinishedAt = &finished

	run := LookRun{Look: look, Signals: signals, HeadlineNote: note}
	if previous != nil {
		run.Look.CarryMarks(previous)
		run.Look.Items = keepUnreadMarks(previous.Items, run.Look.Items, reading)
		run.Look.NewSince, run.Gone = compareLooks(previous.Items, run.Look.Items, reading)
		run.Look.DoneSince = len(run.Gone)
	}
	return run
}

// doneItems are the ids the user marked done on the look before: they are not
// suggested again while their line stays open.
func doneItems(previous *Look) map[string]bool {
	if previous == nil {
		return nil
	}
	done := map[string]bool{}
	for _, item := range previous.Items {
		if item.ActedAs == ActedDone {
			done[item.ID] = true
		}
	}
	return done
}

// keepUnreadMarks keeps a triaged item whose note a partial read left out. The
// line was not seen either way, so a done mark must not lapse and bring the item
// back the next time the note is read.
func keepUnreadMarks(before, after []LookItem, reading Reading) []LookItem {
	if !reading.Partial {
		return after
	}
	read := make(map[string]bool, len(reading.Notes))
	for _, note := range reading.Notes {
		read[note.RelPath] = true
	}
	have := make(map[string]bool, len(after))
	for _, item := range after {
		have[item.ID] = true
	}
	for _, item := range before {
		if len(after) == MaxLookItems {
			break
		}
		if !have[item.ID] && !read[item.Note] && (item.ActedAs == ActedDone || item.ActedAs == ActedFollowUp) {
			item.Suggested = false
			after = append(after, item)
		}
	}
	return after
}

// lookItems turns open loops into the items a look keeps, marking the
// suggested set, within MaxLookItems and the stored-bytes budget. Two
// identical lines in one note are one item.
func lookItems(source Source, loops []OpenLoop, seeded, done map[string]bool) []LookItem {
	// Items in seeded notes are never suggested: the user already has them as
	// follow-ups (FR10). Items the user marked done are not suggested either.
	eligible := make([]OpenLoop, 0, len(loops))
	eligibleAt := make([]int, 0, len(loops))
	for index, loop := range loops {
		if !seeded[loop.Note] && !done[ItemID(source.ID, loop.Note, loop.Text)] {
			eligible, eligibleAt = append(eligible, loop), append(eligibleAt, index)
		}
	}
	suggested := make(map[int]bool, MaxSuggested)
	for chosen := range Suggest(eligible) {
		suggested[eligibleAt[chosen]] = true
	}
	items := make([]LookItem, 0, min(len(loops), MaxLookItems))
	seen := make(map[string]bool, len(loops))
	budget := maxStoredItemBytes
	for index, loop := range loops {
		if len(items) == MaxLookItems {
			break
		}
		item := LookItem{
			ID: ItemID(source.ID, loop.Note, loop.Text), Text: loop.Text, Note: loop.Note, Line: loop.Line,
			Due: loop.Due, Suggested: suggested[index], InDecision: loop.InDecision, Seeded: seeded[loop.Note],
		}
		if source.Kind == KindObsidian {
			item.Link = ObsidianLink(source.Name, loop.Note)
		}
		if seen[item.ID] {
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil || len(encoded) > budget {
			break
		}
		budget -= len(encoded)
		seen[item.ID] = true
		items = append(items, item)
	}
	return items
}

// lookSignals keeps the first MaxLookSignals of each signal the Notes panel
// shows, in the extractor's order. They hold names, paths, counts and dates and
// nothing else: a decision's own line is left behind. A signal whose note could
// not be kept is skipped, so one odd file name never costs the whole look.
func lookSignals(source Source, signals Signals) (people []LookPerson, stale []LookStaleNote, decisions []LookDecision) {
	link := func(note string) string {
		if source.Kind != KindObsidian {
			return ""
		}
		if built := ObsidianLink(source.Name, note); validLink(built) {
			return built
		}
		return ""
	}
	budget := maxStoredSignalBytes
	fits := func(signal any) bool {
		encoded, err := json.Marshal(signal)
		if err != nil || len(encoded) > budget {
			return false
		}
		budget -= len(encoded)
		return true
	}
	for _, person := range signals.People {
		if len(people) == MaxLookSignals {
			break
		}
		name := foldTitle(person.Name, maxNameLength)
		if name == "" || len(name) > maxNameLength || !validSignalNote(person.Note) {
			continue
		}
		kept := LookPerson{Name: name, Note: person.Note, Mentions: person.Mentions, Link: link(person.Note)}
		if fits(kept) {
			people = append(people, kept)
		}
	}
	for _, note := range signals.Stale {
		if len(stale) == MaxLookSignals {
			break
		}
		if !validSignalNote(note.Note) || note.Modified.IsZero() {
			continue
		}
		kept := LookStaleNote{
			Note: note.Note, Title: foldTitle(note.Title, MaxSignalTitleRunes), Open: note.Open, Modified: note.Modified.UTC(), Link: link(note.Note),
		}
		if fits(kept) {
			stale = append(stale, kept)
		}
	}
	for _, note := range signals.Decisions {
		if len(decisions) == MaxLookSignals {
			break
		}
		if !validSignalNote(note.Note) {
			continue
		}
		kept := LookDecision{Note: note.Note, Title: foldTitle(note.Title, MaxSignalTitleRunes), Open: note.Open, Link: link(note.Note)}
		if fits(kept) {
			decisions = append(decisions, kept)
		}
	}
	return people, stale, decisions
}

// foldTitle puts text on one line within limit runes.
func foldTitle(text string, limit int) string {
	return truncateRunes(strings.Join(strings.Fields(strings.ToValidUTF8(text, "")), " "), limit)
}

// compareLooks counts the items new since the previous look and lists the
// previous ones no longer open. When the read was partial, an item counts as
// gone only if its note was read this time, so a note the bounds left out is
// never mistaken for finished work.
func compareLooks(before, after []LookItem, reading Reading) (newSince int, gone []string) {
	now := make(map[string]bool, len(after))
	for _, item := range after {
		now[item.ID] = true
	}
	was := make(map[string]bool, len(before))
	for _, item := range before {
		was[item.ID] = true
	}
	for _, item := range after {
		if !was[item.ID] {
			newSince++
		}
	}
	read := make(map[string]bool, len(reading.Notes))
	for _, note := range reading.Notes {
		read[note.RelPath] = true
	}
	for _, item := range before {
		if !now[item.ID] && (!reading.Partial || read[item.Note]) {
			gone = append(gone, item.ID)
		}
	}
	return newSince, gone
}

// RulesHeadline is the headline written without a model (FR23): "{N} open
// items across {M} notes, {K} with dates." It always fits MaxHeadlineRunes.
func RulesHeadline(name string, counts LookCounts) string {
	var headline string
	switch {
	case counts.Notes == 0:
		headline = fmt.Sprintf("No Markdown notes found in %s.", name)
	case counts.OpenItems == 0:
		headline = fmt.Sprintf("No open items in %s.", plural(counts.Notes, "note", "notes"))
	default:
		dated := fmt.Sprintf("%d with dates", counts.Dated)
		if counts.Dated == 1 {
			dated = "1 with a date"
		}
		headline = fmt.Sprintf("%s across %s, %s.", plural(counts.OpenItems, "open item", "open items"), plural(counts.NotesWithOpen, "note", "notes"), dated)
	}
	if counts.Partial {
		headline += " (partial look)"
	}
	if utf8.RuneCountInString(headline) > MaxHeadlineRunes {
		// Only a very long folder name gets here.
		headline = truncateRunes(headline, MaxHeadlineRunes-1) + "…"
	}
	return headline
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// FailureSentence is the one sentence a failed look shows (FR27, FR38).
func FailureSentence(name string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("Can't find %s. It may have been moved or renamed.", name)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("Ori isn't allowed to read %s. Check its permissions, then choose it again.", name)
	case errors.Is(err, ErrNotDirectory):
		return fmt.Sprintf("%s is no longer a folder Ori can read.", name)
	default:
		return fmt.Sprintf("Ori couldn't read %s.", name)
	}
}
