package secondbrain

import (
	"net/url"
	"strings"
	"unicode"
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
	// FollowUps are the follow-ups the user has confirmed, open or completed.
	FollowUps []HeldFollowUp
	// Projects are the user's workspaces, Personal HQ and groups left out.
	Projects []HeldProject
}

// HeldFollowUp is one confirmed follow-up.
type HeldFollowUp struct {
	ID    string
	Title string
	// Due is YYYY-MM-DD or "".
	Due string
	// Source says where it came from, as the Follow-ups page does.
	Source string
	// Done says it was completed; a note Ori keeps for it gets ticked.
	Done bool
	// FromNotes says it was made from a line in the notes. It is never written
	// back, or the next look would find it again (FR4).
	FromNotes bool
}

// HeldProject is one workspace.
type HeldProject struct {
	ID   string
	Name string
	// Goal is the workspace's goal charter, as the user wrote it.
	Goal string
	// Folder is the folder the workspace is anchored to, or "".
	Folder string
	Agents []string
	// Date is when the workspace was made, as the note shows it.
	Date string
}

// maxProjectGoalRunes matches the most a workspace's goal can hold.
const maxProjectGoalRunes = 4000

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
	planned = append(planned, personNotes(holdings.People)...)
	planned = append(planned, projectNotes(holdings.Projects)...)
	return append(planned, followUpNotes(holdings.FollowUps)...)
}

func followUpNotes(followUps []HeldFollowUp) []PlannedNote {
	notes := make([]PlannedNote, 0, len(followUps))
	for _, held := range followUps {
		if held.FromNotes {
			continue
		}
		body, ok := followUpNote(SeedFollowUp{Title: held.Title, Due: held.Due, Source: held.Source})
		if !ok {
			continue
		}
		notes = append(notes, PlannedNote{
			Ref: MirrorRef{Kind: NoteKindFollowUp, ID: MirrorID(held.ID)}, Rel: followUpNoteRel(held.Title), Kind: NoteKindFollowUp,
			Body: body, Numbered: true, Tick: held.Done,
		})
	}
	return notes
}

// followUpNoteRel is the setup's name for the same follow-up, so a seed is found
// and adopted, cut to leave the ledger's path bound room for a number.
func followUpNoteRel(title string) string {
	stem := noteSlug(cleanSeedLine(title), "follow-up")
	const prefix, suffix, numberRoom = FolderInbox + "/", ".md", 3
	for !mirrorRelFits(prefix + stem + suffix + strings.Repeat("0", numberRoom)) {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = strings.TrimRight(stem[:len(stem)-size], "-")
	}
	if strings.EqualFold(stem, "README") {
		stem = "follow-up-readme"
	}
	return prefix + stem + suffix
}

// ProjectAnswerLine folds the user's answer to the project question to one line,
// or reports that it cannot be a note: empty, or looking like a secret.
func ProjectAnswerLine(answer string) (string, bool) {
	line := cleanSeedLine(answer)
	return line, line != "" && !sensitive.ContainsSecretLikeText(line)
}

// ProjectAnswerNote is the README the project question writes: the project's name
// as the heading, the user's own words as the first paragraph, and where the note
// came from. A workspace made later with the same name replaces it with the
// workspace's own README, the same file (first-week FR21, FR22).
func ProjectAnswerNote(name, answer, date string) []byte {
	var out strings.Builder
	out.WriteString("# " + name + "\n\n" + answer + "\n")
	out.WriteString("\n## How Ori knows this\n\n- " + howLine(HeldFact{How: "You told Ori this", Date: date}) + "\n")
	return []byte(out.String())
}

// ProjectNoteRel is the path of a project's note: Projects/<name>/README.md. ok is
// false for a name that is not one plain folder name.
func ProjectNoteRel(name string) (string, bool) {
	if !ValidFolderName(name) {
		return "", false
	}
	rel := FolderProjects + "/" + name + "/" + projectNoteName
	_, _, err := splitOwnedPath(rel)
	return rel, err == nil && mirrorRelFits(rel)
}

func projectNotes(projects []HeldProject) []PlannedNote {
	notes := make([]PlannedNote, 0, len(projects))
	for _, project := range projects {
		name := cleanNoteLine(project.Name)
		if name == "" {
			continue
		}
		notes = append(notes, PlannedNote{
			Ref: MirrorRef{Kind: NoteKindProject, ID: MirrorID(project.ID)}, Rel: projectNoteRel(name), Kind: NoteKindProject,
			Body: projectNote(name, project),
		})
	}
	return notes
}

func projectNote(name string, project HeldProject) []byte {
	var out strings.Builder
	out.WriteString("# " + name + "\n")
	if goal := projectGoal(project.Goal); goal != "" {
		out.WriteString("\n" + goal + "\n")
	}
	if folder := cleanNoteLine(project.Folder); folder != "" {
		out.WriteString("\nFolder: " + folder + "\n")
	}
	var agents []string
	for _, agent := range project.Agents {
		if line := cleanNoteLine(agent); line != "" {
			agents = append(agents, line)
		}
	}
	if len(agents) > 0 {
		out.WriteString("\nAgents: " + strings.Join(agents, ", ") + "\n")
	}
	if id := cleanNoteLine(project.ID); id != "" {
		out.WriteString("\n[Open in Ori](/?workspace=" + url.QueryEscape(id) + ")\n")
	}
	out.WriteString("\n## How Ori knows this\n\n- " + howLine(HeldFact{How: "This is your workspace in Ori", Date: project.Date}) + "\n")
	return []byte(out.String())
}

// projectGoal keeps the charter's lines and its headings. A line that looks like
// a secret is dropped, and a task line loses its box, so the look never reads a
// goal Ori copied as an open item the user wrote.
func projectGoal(goal string) string {
	var lines []string
	for _, raw := range strings.Split(truncateRunes(strings.ToValidUTF8(goal, ""), maxProjectGoalRunes), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if sensitive.ContainsSecretLikeText(line) {
			continue
		}
		line = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\t' {
				return -1
			}
			return r
		}, line)
		trimmed := strings.TrimLeft(line, " \t")
		for _, box := range []string{"- [ ] ", "- [x] ", "- [X] ", "* [ ] ", "* [x] ", "* [X] "} {
			if rest, found := strings.CutPrefix(trimmed, box); found {
				line = "- " + rest
			}
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// projectNoteRel keeps the name readable, without what a folder name cannot hold.
// A very long name is cut in the folder name only.
func projectNoteRel(name string) string {
	stem := strings.TrimLeft(personFileName(name), ".")
	const prefix, suffix = FolderProjects + "/", "/" + projectNoteName
	for stem != "" && !mirrorRelFits(prefix+stem+suffix) {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = strings.TrimRight(stem[:len(stem)-size], ". ")
	}
	if !ValidFolderName(stem) {
		stem = "Project"
	}
	return prefix + stem + suffix
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
