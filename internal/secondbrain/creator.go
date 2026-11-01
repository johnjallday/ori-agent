package secondbrain

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

const (
	// DefaultFolderName is the name Ori proposes for a folder it sets up.
	DefaultFolderName = config.SecondBrainFolderName
	// MaxSeedFollowUps and MaxSeedPeople bound what one setup seeds (FR8).
	MaxSeedFollowUps = 200
	MaxSeedPeople    = 100

	maxSeedLineRunes = 500
	maxSeedSlugRunes = 80

	// OwnedFoldersSentence opens the root README and the confirmation step, so
	// the user reads what Ori will write before it writes.
	OwnedFoldersSentence = "I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours."

	obsidianAppJSON = "{\n  \"alwaysUpdateLinks\": false,\n  \"promptDelete\": true\n}\n"
)

var (
	// ErrTargetNotEmpty refuses a target that exists with content, or that is
	// not a real folder (FR5).
	ErrTargetNotEmpty = errors.New("secondbrain: that folder already has something in it")
	// ErrInvalidFolderName refuses a name that is not one plain folder name.
	ErrInvalidFolderName = errors.New("secondbrain: that is not a folder name")
)

// createdLayout is the top-level structure with each folder's README (FR6).
var createdLayout = []struct{ folder, readme string }{
	{FolderInbox, "# Inbox\n\nCaptures from chat, your open follow-ups, and anything not sorted yet.\n\nOri writes here: one new note per capture. It never changes a note you edited.\n"},
	{FolderDaily, "# Daily\n\nOne note per day, from your HQ Journal.\n\nOri writes here: it updates a day's note when you save that Journal again, unless you edited the note.\n"},
	{"Projects", "# Projects\n\nThings with an end date.\n\nOri writes here: a README for each of your workspaces, in a folder of its own. It never changes a note you edited.\n"},
	{"Areas", "# Areas\n\nOngoing responsibilities.\n\nOri does not write here.\n"},
	{"Resources", "# Resources\n\nReference material.\n\nOri does not write here.\n"},
	{"Archive", "# Archive\n\nFinished projects and areas.\n\nOri does not write here.\n"},
	{"People", "# People\n\nOne note per person.\n\nOri writes here: a note for each person you tell it about, including the people it already knew when it set this up. It never changes a note you edited.\n"},
}

// SeedFollowUp is one open follow-up to copy into Inbox.
type SeedFollowUp struct {
	Title string
	// Due is YYYY-MM-DD or "".
	Due string
	// Source says where the follow-up came from, as the Follow-ups UI shows it.
	Source string
}

// SeedPerson is one person Ori already knows.
type SeedPerson struct {
	Name string
	// How says how Ori knows them.
	How string
}

// Seeds is what Ori already holds and copies in, once, at creation (FR8). The
// facts become the facts note, rendered as the fill-in renders it, so the first
// sync finds it as it would have written it and says nothing.
type Seeds struct {
	FollowUps        []SeedFollowUp
	Facts            []HeldFact
	WorkingAgreement string
	People           []SeedPerson
}

// Creation is what one Create made.
type Creation struct {
	// Path is the new folder's absolute path.
	Path string
	// FollowUps and People count the notes written, after the secret filter
	// and the caps.
	FollowUps int
	People    int
	// Facts says the facts note was written.
	Facts bool

	parent     string
	name       string
	made       []string
	madeTarget bool
}

// Undo removes what this Create made and nothing else, for a caller whose next
// step failed (FR11). A folder the user has since put something in is kept.
func (c Creation) Undo() {
	parent, err := pinFolder(c.parent)
	if err != nil {
		return
	}
	defer func() { _ = parent.Close() }()
	if target, err := parent.OpenRoot(c.name); err == nil {
		for index := len(c.made) - 1; index >= 0; index-- {
			_ = target.Remove(c.made[index])
		}
		_ = target.Close()
	}
	if c.madeTarget {
		_ = parent.Remove(c.name)
	}
}

// Creator makes a second brain's folder (FR5–FR8, FR11).
type Creator struct {
	// beforeWrite sees every path about to be written, so tests can fail one.
	beforeWrite func(rel string) error
}

// ValidFolderName reports whether name is one plain, visible folder name. It
// never names a path.
func ValidFolderName(name string) bool {
	switch {
	case name == "" || name != strings.TrimSpace(name) || len(name) > maxNameLength || !utf8.ValidString(name),
		name == "." || name == ".." || strings.HasPrefix(name, "."),
		strings.ContainsAny(name, `/\:`),
		strings.HasSuffix(name, "."),
		strings.IndexFunc(name, unicode.IsControl) >= 0:
		return false
	}
	return true
}

// Create makes <parent>/<name> with the layout and seeds. The target must not
// exist, or must be an empty real folder. If anything fails, everything this
// call made is removed and nothing else is touched.
func (c Creator) Create(parent, name string, seeds Seeds) (Creation, error) {
	if !ValidFolderName(name) {
		return Creation{}, ErrInvalidFolderName
	}
	parentRoot, err := pinFolder(parent)
	if err != nil {
		return Creation{}, err
	}
	defer func() { _ = parentRoot.Close() }()

	madeTarget, err := prepareTarget(parentRoot, name)
	if err != nil {
		return Creation{}, err
	}
	target, err := parentRoot.OpenRoot(name)
	if err != nil {
		if madeTarget {
			_ = parentRoot.Remove(name)
		}
		return Creation{}, err
	}
	defer func() { _ = target.Close() }()

	run := &creatorRun{root: target, before: c.beforeWrite}
	creation, err := run.populate(seeds)
	if err != nil {
		run.rollback()
		if madeTarget {
			_ = parentRoot.Remove(name)
		}
		return Creation{}, fmt.Errorf("secondbrain: creating the folder: %w", err)
	}
	creation.Path = filepath.Join(filepath.Clean(parent), name)
	creation.parent, creation.name, creation.made, creation.madeTarget = filepath.Clean(parent), name, run.made, madeTarget
	return creation, nil
}

// prepareTarget makes the target or confirms it is an empty real folder. It
// reports whether this call made it. A .DS_Store is not content: Finder leaves
// one in a folder the user just made.
func prepareTarget(parent *os.Root, name string) (bool, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := parent.Mkdir(name, noteFolderMode); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return false, ErrTargetNotEmpty
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, ErrTargetNotEmpty
	}
	dir, err := parent.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() != ".DS_Store" {
			return false, ErrTargetNotEmpty
		}
	}
	return false, nil
}

// creatorRun remembers what it made, so a failure can remove exactly that.
type creatorRun struct {
	root   *os.Root
	before func(rel string) error
	made   []string
	names  map[string]bool
}

func (r *creatorRun) populate(seeds Seeds) (Creation, error) {
	if err := r.mkdir(ObsidianFolder); err != nil {
		return Creation{}, err
	}
	if err := r.write(ObsidianFolder+"/app.json", []byte(obsidianAppJSON)); err != nil {
		return Creation{}, err
	}
	for _, entry := range createdLayout {
		if err := r.mkdir(entry.folder); err != nil {
			return Creation{}, err
		}
		if err := r.write(entry.folder+"/README.md", []byte(entry.readme)); err != nil {
			return Creation{}, err
		}
	}
	facts := factsNote(Holdings{Facts: seeds.Facts, WorkingAgreement: seeds.WorkingAgreement})
	hasFacts := !facts.OnlyIfTracked
	if err := r.write("README.md", rootReadme(hasFacts)); err != nil {
		return Creation{}, err
	}
	if hasFacts {
		if err := r.write(FactsNoteRel, RenderNote(NoteKindFacts, facts.Body)); err != nil {
			return Creation{}, err
		}
	}
	followUps, err := r.seedFollowUps(seeds.FollowUps)
	if err != nil {
		return Creation{}, err
	}
	people, err := r.seedPeople(seeds.People)
	if err != nil {
		return Creation{}, err
	}
	return Creation{FollowUps: followUps, People: people, Facts: hasFacts}, nil
}

func (r *creatorRun) seedFollowUps(seeds []SeedFollowUp) (int, error) {
	written := 0
	for _, seed := range seeds {
		if written == MaxSeedFollowUps {
			break
		}
		body, ok := followUpNote(seed)
		if !ok {
			continue
		}
		rel := r.uniqueName(FolderInbox, noteSlug(seed.Title, "follow-up"))
		if err := r.write(rel, RenderNote(NoteKindSeed, body)); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func (r *creatorRun) seedPeople(seeds []SeedPerson) (int, error) {
	written := 0
	for _, seed := range seeds {
		if written == MaxSeedPeople {
			break
		}
		body, ok := personNote(seed)
		if !ok {
			continue
		}
		rel := r.uniqueName("People", personFileName(cleanSeedLine(seed.Name)))
		if err := r.write(rel, RenderNote(NoteKindSeed, body)); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// uniqueName is <folder>/<stem>.md, numbered past any name already used. The
// check folds case, since most Macs' disks do. README stays the folder's own.
func (r *creatorRun) uniqueName(folder, stem string) string {
	if r.names == nil {
		r.names = map[string]bool{}
	}
	for attempt := 1; ; attempt++ {
		candidate := stem
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", stem, attempt)
		}
		key := strings.ToLower(folder + "/" + candidate)
		if !r.names[key] && !strings.EqualFold(candidate, "README") {
			r.names[key] = true
			return folder + "/" + candidate + ".md"
		}
	}
}

func (r *creatorRun) mkdir(rel string) error {
	if r.before != nil {
		if err := r.before(rel); err != nil {
			return err
		}
	}
	if err := r.root.Mkdir(rel, noteFolderMode); err != nil {
		return err
	}
	r.made = append(r.made, rel)
	return nil
}

// write creates one new file. The folder is new and nothing reads it yet, so a
// failure is handled by the rollback rather than by a temp file per note. No
// fsync per note either: up to 300 of them would stall the request, and a crash
// here leaves a folder the user can simply choose again.
func (r *creatorRun) write(rel string, content []byte) error {
	if r.before != nil {
		if err := r.before(rel); err != nil {
			return err
		}
	}
	file, err := r.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, noteFileMode) // #nosec G302 -- plain notes the user owns and reads in any editor (FR13)
	if err != nil {
		return err
	}
	r.made = append(r.made, rel)
	if err := file.Chmod(noteFileMode); err != nil { // #nosec G302 -- plain notes the user owns and reads in any editor (FR13)
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// rollback removes what this run made, newest first. Remove refuses a folder
// the user has since put something in, which is the right answer.
func (r *creatorRun) rollback() {
	for index := len(r.made) - 1; index >= 0; index-- {
		_ = r.root.Remove(r.made[index])
	}
}

// rootReadme says what Ori keeps in the folder, and links the facts note when
// there is one rather than carrying the facts itself (FR11).
func rootReadme(hasFacts bool) []byte {
	var out strings.Builder
	out.WriteString(OwnedFoldersSentence + "\n\n")
	out.WriteString("This is your second brain: plain Markdown files you own. They open in Obsidian or any editor, and stay useful without Ori. Ori only writes to `Inbox/`, `Daily/`, `People/`, one `README.md` in each folder under `Projects/`, and `What Ori knows about you.md`, and never changes a note you edited there.\n\n")
	out.WriteString("## Folders\n\n")
	out.WriteString("- `Inbox/`: captures from chat, your open follow-ups, unsorted notes\n")
	out.WriteString("- `Daily/`: one note per day, from your HQ Journal\n")
	out.WriteString("- `Projects/`: things with an end date, a note for each workspace\n")
	out.WriteString("- `Areas/`: ongoing responsibilities\n")
	out.WriteString("- `Resources/`: reference material\n")
	out.WriteString("- `Archive/`: finished projects and areas\n")
	out.WriteString("- `People/`: one note per person\n")
	if hasFacts {
		out.WriteString("\n## What Ori knows about you\n\nSee [[What Ori knows about you]].\n")
	}
	return []byte(out.String())
}

// followUpNote writes a follow-up as a note, not an export: a heading and one
// task line the extractor reads back as an open item and Obsidian Tasks
// renders. A title that looks like a secret drops the whole note.
func followUpNote(seed SeedFollowUp) ([]byte, bool) {
	title := cleanSeedLine(seed.Title)
	if title == "" || sensitive.ContainsSecretLikeText(title) {
		return nil, false
	}
	var out strings.Builder
	out.WriteString("# " + title + "\n\n- [ ] " + title)
	if isoDayPattern.MatchString(seed.Due) && validDate(seed.Due) {
		out.WriteString(" 📅 " + seed.Due)
	}
	out.WriteString("\n")
	if source := cleanSeedLine(seed.Source); source != "" && !sensitive.ContainsSecretLikeText(source) {
		out.WriteString("\nFrom: " + source + "\n")
	}
	return []byte(out.String()), true
}

func personNote(seed SeedPerson) ([]byte, bool) {
	name := cleanSeedLine(seed.Name)
	if name == "" || sensitive.ContainsSecretLikeText(name) {
		return nil, false
	}
	var out strings.Builder
	out.WriteString("# " + name + "\n")
	if how := cleanSeedLine(seed.How); how != "" && !sensitive.ContainsSecretLikeText(how) {
		out.WriteString("\n" + how + "\n")
	}
	return []byte(out.String()), true
}

// cleanSeedLine folds text to one bounded line with no control characters, so
// a seeded value can never add a line of its own.
func cleanSeedLine(text string) string {
	folded := strings.Join(strings.Fields(strings.ToValidUTF8(text, "")), " ")
	folded = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, folded)
	return truncateRunes(folded, maxSeedLineRunes)
}

// noteSlug is a lower-case, hyphenated file stem, or fallback when nothing in
// the text survives.
func noteSlug(text, fallback string) string {
	var out strings.Builder
	pending := false
	count := 0
	for _, r := range strings.ToLower(text) {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			pending = out.Len() > 0
			continue
		}
		if count >= maxSeedSlugRunes {
			break
		}
		if pending {
			out.WriteByte('-')
			count++
			pending = false
		}
		out.WriteRune(r)
		count++
	}
	if out.Len() == 0 {
		return fallback
	}
	return out.String()
}

// personStopwords start a sentence about someone without naming them.
var personStopwords = map[string]bool{
	"i": true, "my": true, "me": true, "we": true, "our": true, "you": true, "your": true, "the": true, "a": true, "an": true,
	"he": true, "she": true, "they": true, "his": true, "her": true, "their": true, "this": true, "that": true,
}

// PersonFromSentence names the person an approved People entry is about. The
// entries are one free-text sentence ("Dana Lee is my distributor"), so the
// name is the first run of up to three capitalised words that is not a
// pronoun or article, and the whole sentence is how Ori knows them. A sentence
// that names nobody seeds nothing (FR8).
func PersonFromSentence(sentence string) (SeedPerson, bool) {
	text := cleanSeedLine(sentence)
	words := strings.Fields(text)
	var run []string
	for index, word := range words {
		trimmed := strings.TrimRight(word, ".,;:!?)\"'")
		possessive := strings.HasSuffix(trimmed, "'s") || strings.HasSuffix(trimmed, "’s")
		trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "'s"), "’s")
		trimmed = strings.TrimLeft(trimmed, "(\"'")
		first, _ := utf8.DecodeRuneInString(trimmed)
		if trimmed != "" && unicode.IsUpper(first) && !personStopwords[strings.ToLower(trimmed)] {
			// Every sentence starts with a capital, so a lone first word is a
			// name only when what follows reads like a statement about a person.
			if index == 0 && !possessive && !startsPersonStatement(words[1:]) {
				continue
			}
			run = append(run, trimmed)
			if len(run) == 3 || possessive || strings.ContainsAny(word, ".,;:!?") {
				break
			}
			continue
		}
		if len(run) > 0 {
			break
		}
	}
	if len(run) == 0 {
		return SeedPerson{}, false
	}
	return SeedPerson{Name: strings.Join(run, " "), How: text}, true
}

var personVerbs = map[string]bool{
	"is": true, "was": true, "are": true, "works": true, "lives": true, "prefers": true, "likes": true, "manages": true,
	"runs": true, "handles": true, "replies": true, "wants": true, "needs": true, "asks": true, "usually": true, "always": true,
}

// startsPersonStatement reports whether the words after a sentence's first
// word continue it as a statement about a person: another capitalised word, or
// a verb that people do.
func startsPersonStatement(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	next := strings.TrimRight(rest[0], ".,;:!?")
	first, _ := utf8.DecodeRuneInString(next)
	return unicode.IsUpper(first) && !personStopwords[strings.ToLower(next)] || personVerbs[strings.ToLower(next)]
}

// personFileName keeps a person's name readable, so a wikilink [[Sam Lee]]
// finds the note, and removes what a file name cannot hold.
func personFileName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(`/\:*?"<>|#^[]`, r):
			return ' '
		default:
			return r
		}
	}, name)
	cleaned = strings.TrimLeft(strings.Join(strings.Fields(cleaned), " "), ".")
	cleaned = truncateRunes(strings.TrimRight(cleaned, ". "), maxSeedSlugRunes)
	if path.Base(cleaned) != cleaned || cleaned == "" {
		return "person"
	}
	return cleaned
}
