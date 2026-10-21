package secondbrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"
	"unicode/utf8"
)

// maxStoredItemBytes caps the encoded open items one look keeps, so the
// record stays well inside RecordMaxBytes whatever the items say.
const maxStoredItemBytes = 300 * 1024

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
	look.Items = lookItems(source, signals.OpenLoops)
	look.Headline, look.HeadlineBy = RulesHeadline(source.Name, look.Counts), HeadlineByRules
	headline, note := modelHeadline(ctx, l.Model, source.Name, look.Counts, signals, look.Items, now)
	if note == "" {
		look.Headline, look.HeadlineBy = headline, HeadlineByModel
	}
	look.AlsoNoticed = AlsoNoticed(signals, now)
	finished := l.now().UTC()
	look.FinishedAt = &finished

	run := LookRun{Look: look, Signals: signals, HeadlineNote: note}
	if previous != nil {
		run.Look.NewSince, run.Gone = compareLooks(previous.Items, run.Look.Items, reading)
		run.Look.DoneSince = len(run.Gone)
	}
	return run
}

// lookItems turns open loops into the items a look keeps, marking the
// suggested set, within MaxLookItems and the stored-bytes budget. Two
// identical lines in one note are one item.
func lookItems(source Source, loops []OpenLoop) []LookItem {
	suggested := Suggest(loops)
	items := make([]LookItem, 0, min(len(loops), MaxLookItems))
	seen := make(map[string]bool, len(loops))
	budget := maxStoredItemBytes
	for index, loop := range loops {
		if len(items) == MaxLookItems {
			break
		}
		item := LookItem{
			ID: ItemID(source.ID, loop.Note, loop.Text), Text: loop.Text, Note: loop.Note, Line: loop.Line,
			Due: loop.Due, Suggested: suggested[index], InDecision: loop.InDecision,
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
