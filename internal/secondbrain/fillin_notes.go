package secondbrain

import (
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// factsRefID is the one facts note's ledger id.
const factsRefID = "facts"

// Holdings is what Ori holds in HQ that belongs in the notes. It is gathered
// fresh for every sync and never stored.
type Holdings struct {
	// Facts are the approved facts the facts note lists.
	Facts []HeldFact
	// WorkingAgreement is one line about how the user works with Ori.
	WorkingAgreement string
	// People are the approved sentences about people, in their stored order.
	People []HeldFact
}

// HeldFact is one approved sentence and how Ori came to know it.
type HeldFact struct {
	Text string
	// How says how Ori knows this, as a phrase ("You told Ori this").
	How string
	// Date is the approval's date, as the note shows it ("2 Nov 2026").
	Date string
}

// PlanNotes renders what HQ holds into the notes that should exist. Secret-like
// text never reaches a note. The facts note comes first, so a bound on writes
// never leaves the note a user reads first behind.
func PlanNotes(holdings Holdings) []PlannedNote {
	planned := []PlannedNote{factsNote(holdings)}
	return append(planned, personNotes(holdings.People)...)
}

func factsNote(holdings Holdings) PlannedNote {
	var lines []string
	newest := ""
	for _, fact := range holdings.Facts {
		if line := cleanNoteLine(fact.Text); line != "" {
			lines = append(lines, "- "+line)
			if fact.Date != "" {
				newest = fact.Date
			}
		}
	}
	agreement := cleanNoteLine(holdings.WorkingAgreement)
	var out strings.Builder
	out.WriteString("# What Ori knows about you\n")
	if len(lines) > 0 {
		out.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	if agreement != "" {
		out.WriteString("\nHow you work with Ori: " + agreement + "\n")
	}
	out.WriteString("\n## How Ori knows this\n\nYou approved each of these in Ori")
	if newest != "" {
		out.WriteString(", most recently on " + newest)
	}
	out.WriteString(". Change this note and it is yours: Ori stops updating it.\n")
	return PlannedNote{
		Ref: MirrorRef{Kind: NoteKindFacts, ID: MirrorID(factsRefID)}, Rel: FactsNoteRel, Kind: NoteKindFacts,
		Body: []byte(out.String()), OnlyIfTracked: len(lines) == 0 && agreement == "",
	}
}

// personNotes makes one note per person, however many approved sentences name
// them. A sentence that names nobody writes nothing, as at setup.
func personNotes(facts []HeldFact) []PlannedNote {
	type person struct {
		name  string
		facts []HeldFact
	}
	var order []string
	byKey := map[string]*person{}
	for _, fact := range facts {
		found, ok := PersonFromSentence(fact.Text)
		if !ok || sensitive.ContainsSecretLikeText(found.Name) || sensitive.ContainsSecretLikeText(found.How) {
			continue
		}
		key := strings.ToLower(found.Name)
		held := byKey[key]
		if held == nil {
			held = &person{name: found.Name}
			byKey[key] = held
			order = append(order, key)
		}
		held.facts = append(held.facts, HeldFact{Text: found.How, How: fact.How, Date: fact.Date})
	}
	notes := make([]PlannedNote, 0, len(order))
	for _, key := range order {
		held := byKey[key]
		notes = append(notes, PlannedNote{
			Ref: MirrorRef{Kind: NoteKindPerson, ID: MirrorID(key)}, Rel: personNoteRel(held.name), Kind: NoteKindPerson,
			Body: personFillInNote(held.name, held.facts),
		})
	}
	return notes
}

func personFillInNote(name string, facts []HeldFact) []byte {
	var out strings.Builder
	out.WriteString("# " + name + "\n")
	for _, fact := range facts {
		out.WriteString("\n" + fact.Text + "\n")
	}
	out.WriteString("\n## How Ori knows this\n\n")
	for _, fact := range facts {
		out.WriteString("- " + howLine(fact) + "\n")
	}
	return []byte(out.String())
}

func howLine(fact HeldFact) string {
	how := cleanNoteLine(fact.How)
	if how == "" {
		how = "You approved this in Ori"
	}
	if date := cleanNoteLine(fact.Date); date != "" {
		how += ", " + date
	}
	return how + "."
}

// cleanNoteLine folds text to one bounded line, or "" when it looks like a secret.
func cleanNoteLine(text string) string {
	line := cleanSeedLine(text)
	if sensitive.ContainsSecretLikeText(line) {
		return ""
	}
	return line
}

// personNoteRel is the person's note, keeping the name readable so a wikilink
// [[Sam Lee]] finds it. A very long name is cut in the file name only, and a
// person who is called README does not take the folder's own note.
func personNoteRel(name string) string {
	stem := personFileName(name)
	if strings.EqualFold(stem, "README") {
		stem += " (person)"
	}
	const prefix, suffix = FolderPeople + "/", ".md"
	for !mirrorRelFits(prefix + stem + suffix) {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = strings.TrimRight(stem[:len(stem)-size], ". ")
	}
	return prefix + stem + suffix
}
