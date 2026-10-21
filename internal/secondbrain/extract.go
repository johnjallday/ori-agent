package secondbrain

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// Extraction limits and windows (FR18, FR19).
const (
	// MaxTaskTextRunes caps an open item's text.
	MaxTaskTextRunes = 200
	// StaleAfter is how long a note with open items sits untouched before it
	// is stale.
	StaleAfter = 60 * 24 * time.Hour
	// ActiveWithin is how recently a note was modified to count as active.
	ActiveWithin = 7 * 24 * time.Hour
	// maxFrontmatterLines bounds the YAML block read at the top of a note.
	maxFrontmatterLines = 200
)

// Task is one open item on a line of a note.
type Task struct {
	// Text is the item without its marker or Tasks-plugin metadata, at most
	// MaxTaskTextRunes runes, on one line.
	Text string
	// Line is 1-based.
	Line int
	// Due is the date on the line as YYYY-MM-DD, or "".
	Due string
}

// NoteFacts is what one note says, extracted without a model (FR18).
type NoteFacts struct {
	RelPath string
	Title   string
	Tags    []string
	Status  string
	Open    []Task
	Done    int
	// Links are the wikilink targets the note mentions, once each, in order.
	Links   []string
	ModTime time.Time
	Words   int
	// Decision says the note marks an open decision; DecisionText is the
	// line (or title) that says so.
	Decision     bool
	DecisionText string
	// Daily says the file is named for a day.
	Daily bool
	// DroppedSecrets counts lines left out because they look like a secret
	// (FR20). They never reach storage or a model.
	DroppedSecrets int
}

var (
	openCheckbox = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\[ \](?:\s+(.*))?$`)
	doneCheckbox = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\[[xX]\](?:\s+.*)?$`)
	logseqOpen   = regexp.MustCompile(`^\s*-\s+(?:TODO|DOING|LATER|NOW)\s+(.*)$`)
	logseqDone   = regexp.MustCompile(`^\s*-\s+DONE(?:\s+.*)?$`)
	// listMarker strips what precedes a line's words: a heading, a bullet, a
	// number, a checkbox or a Logseq marker.
	listMarker = regexp.MustCompile(`^\s*(?:#{1,6}\s+|(?:[-*+]|\d+[.)])\s+(?:\[.\]\s*)?(?:(?:TODO|DOING|LATER|NOW|DONE)\s+)?)`)
	heading    = regexp.MustCompile(`^#\s+(.+?)\s*#*\s*$`)
	wikilink   = regexp.MustCompile(`(!?)\[\[([^\[\]|#^]+)(?:[#^][^\[\]|]*)?(?:\|[^\[\]]*)?\]\]`)
	property   = regexp.MustCompile(`^([A-Za-z][\w-]*)::\s*(.*)$`)
	decideLine = regexp.MustCompile(`(?i)^(?:decide|need to decide|needs to decide|decision)\b`)
	decisionIn = regexp.MustCompile(`(?i)\bdecision\b`)

	// Dates on a task line, in the order they are tried: the Tasks plugin's
	// due date, a due: field, any ISO date, then "by <Month> <day>".
	tasksDue  = regexp.MustCompile(`\x{1F4C5}\x{FE0F}?\s*(\d{4}-\d{2}-\d{2})`)
	dueField  = regexp.MustCompile(`(?i)\bdue::?\s*(\d{4}-\d{2}-\d{2})`)
	isoDate   = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	byMonth   = regexp.MustCompile(`(?i)\bby\s+(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
	notDueISO = regexp.MustCompile(`[\x{2705}\x{2795}]\x{FE0F}?\s*\d{4}-\d{2}-\d{2}`)

	// Tasks-plugin metadata left out of an item's text: dated fields,
	// priorities, a recurrence rule, and Dataview's bracketed inline fields.
	tasksDated     = regexp.MustCompile(`[\x{1F4C5}\x{23F3}\x{1F6EB}\x{2705}\x{2795}]\x{FE0F}?\s*\d{4}-\d{2}-\d{2}`)
	tasksPriority  = regexp.MustCompile(`[\x{23EB}\x{1F53C}\x{1F53D}\x{1F53A}\x{23EC}]\x{FE0F}?`)
	tasksRecurring = regexp.MustCompile(`\x{1F501}\x{FE0F}?[^\x{1F4C5}\x{23F3}\x{1F6EB}\x{2705}\x{2795}]*`)
	inlineField    = regexp.MustCompile(`\[[A-Za-z][\w-]*::[^\]]*\]`)

	dailyName   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.(?i:md|markdown)$`)
	journalName = regexp.MustCompile(`^(\d{4})_(\d{2})_(\d{2})\.(?i:md|markdown)$`)
)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// closedStatuses mark a note whose decision is already made.
var closedStatuses = map[string]bool{
	"done": true, "complete": true, "completed": true, "closed": true, "decided": true,
	"archived": true, "resolved": true, "cancelled": true, "canceled": true,
}

// ExtractNote reads one note's facts (FR18). It is deterministic: the same
// bytes and modification time always yield the same facts.
func ExtractNote(note Note) NoteFacts {
	facts := NoteFacts{RelPath: note.RelPath, ModTime: note.ModTime, Daily: IsDailyNote(note.RelPath)}
	body, front := splitFrontmatter(note.Content)
	facts.Title, facts.Tags, facts.Status = front.title, front.tags, front.status

	var decisionLine string
	inFence := false
	inProperties := true
	scanner := bufio.NewScanner(bytes.NewReader(body.text))
	scanner.Buffer(make([]byte, 0, 64<<10), MaxNoteBytes+1)
	for number := body.firstLine; scanner.Scan(); number++ {
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		facts.Words += len(strings.Fields(line))
		if inProperties {
			// Logseq keeps a page's title, tags and status as "key:: value"
			// lines at the top; frontmatter wins when both are present.
			if match := property.FindStringSubmatch(trimmed); match != nil {
				facts.applyProperty(strings.ToLower(match[1]), match[2])
				continue
			}
			inProperties = trimmed == "" && facts.Words == 0
		}
		if facts.Title == "" {
			if match := heading.FindStringSubmatch(line); match != nil {
				facts.Title = strings.TrimSpace(match[1])
			}
		}
		if sensitive.ContainsSecretLikeText(line) {
			if openCheckbox.MatchString(line) || logseqOpen.MatchString(line) {
				facts.DroppedSecrets++
			}
			continue
		}
		facts.Links = appendLinks(facts.Links, line)
		switch {
		case doneCheckbox.MatchString(line), logseqDone.MatchString(line):
			facts.Done++
			continue
		case openCheckbox.MatchString(line):
			facts.addTask(openCheckbox.FindStringSubmatch(line)[1], number, note.ModTime)
		case logseqOpen.MatchString(line):
			facts.addTask(logseqOpen.FindStringSubmatch(line)[1], number, note.ModTime)
		}
		if decisionLine == "" {
			if words := strings.TrimSpace(listMarker.ReplaceAllString(line, "")); decideLine.MatchString(words) {
				decisionLine = words
			}
		}
	}

	if facts.Title == "" || sensitive.ContainsSecretLikeText(facts.Title) {
		facts.Title = noteStem(note.RelPath)
	}
	facts.Decision, facts.DecisionText = decisionFor(facts, decisionLine)
	return facts
}

func (facts *NoteFacts) applyProperty(key, value string) {
	switch key {
	case "title":
		if facts.Title == "" {
			facts.Title = strings.TrimSpace(value)
		}
	case "tags", "tag":
		if len(facts.Tags) == 0 {
			facts.Tags = splitTags(value)
		}
	case "status":
		if facts.Status == "" {
			facts.Status = strings.TrimSpace(value)
		}
	}
}

func (facts *NoteFacts) addTask(raw string, line int, modTime time.Time) {
	text := cleanTaskText(raw)
	if text == "" {
		return
	}
	facts.Open = append(facts.Open, Task{Text: text, Line: line, Due: dueDate(raw, modTime)})
}

// decisionFor applies FR19's decision rule: a status that says a decision is
// pending, a line that starts with "Decide" / "Need to decide" / "Decision",
// or a title that names a decision on a note that still has open items. A
// status that says the matter is closed overrides all three.
func decisionFor(facts NoteFacts, line string) (bool, string) {
	status := strings.ToLower(strings.TrimSpace(facts.Status))
	if closedStatuses[status] || strings.HasPrefix(status, "decided") {
		return false, ""
	}
	if strings.Contains(status, "decid") || strings.Contains(status, "decision") {
		if line != "" {
			return true, truncateRunes(line, MaxTaskTextRunes)
		}
		return true, facts.Title
	}
	if line != "" {
		return true, truncateRunes(line, MaxTaskTextRunes)
	}
	if len(facts.Open) > 0 && (decideLine.MatchString(facts.Title) || decisionIn.MatchString(facts.Title)) {
		return true, facts.Title
	}
	return false, ""
}

// cleanTaskText removes Tasks-plugin metadata and folds the item to one
// trimmed line of at most MaxTaskTextRunes runes.
func cleanTaskText(raw string) string {
	text := tasksDated.ReplaceAllString(raw, "")
	text = tasksRecurring.ReplaceAllString(text, "")
	text = tasksPriority.ReplaceAllString(text, "")
	text = inlineField.ReplaceAllString(text, "")
	return truncateRunes(strings.Join(strings.Fields(text), " "), MaxTaskTextRunes)
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit]))
}

// dueDate finds the date on a task line. "by <Month> <day>" takes the year of
// the note's last edit, or the next year when that would put the date more
// than half a year before the edit (a December note saying "by Jan 5").
func dueDate(line string, modTime time.Time) string {
	for _, re := range []*regexp.Regexp{tasksDue, dueField} {
		if match := re.FindStringSubmatch(line); match != nil && validDate(match[1]) {
			return match[1]
		}
	}
	for _, match := range isoDate.FindAllStringSubmatch(notDueISO.ReplaceAllString(line, ""), -1) {
		if validDate(match[1]) {
			return match[1]
		}
	}
	match := byMonth.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	month, ok := months[strings.ToLower(match[1])[:3]]
	day, err := strconv.Atoi(match[2])
	if !ok || err != nil {
		return ""
	}
	reference := modTime
	if reference.IsZero() {
		reference = time.Now()
	}
	year := reference.Year()
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Day() != day {
		return "" // "by Feb 30"
	}
	if date.Before(reference.AddDate(0, -6, 0)) {
		date = time.Date(year+1, month, day, 0, 0, 0, 0, time.UTC)
	}
	return date.Format("2006-01-02")
}

func validDate(text string) bool {
	_, err := time.Parse("2006-01-02", text)
	return err == nil
}

func appendLinks(links []string, line string) []string {
	for _, match := range wikilink.FindAllStringSubmatch(line, -1) {
		if match[1] == "!" {
			continue // an embed, usually an attachment
		}
		target := strings.TrimSpace(match[2])
		if target == "" || containsFold(links, target) {
			continue
		}
		links = append(links, target)
	}
	return links
}

func containsFold(values []string, value string) bool {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return true
		}
	}
	return false
}

// IsDailyNote reports whether a note is named for a day: YYYY-MM-DD.md
// anywhere, or Logseq's journals/YYYY_MM_DD.md.
func IsDailyNote(rel string) bool {
	base := path.Base(rel)
	if match := dailyName.FindStringSubmatch(base); match != nil {
		return validDate(match[1])
	}
	if match := journalName.FindStringSubmatch(base); match != nil && strings.EqualFold(path.Base(path.Dir(rel)), "journals") {
		return validDate(match[1] + "-" + match[2] + "-" + match[3])
	}
	return false
}

// noteStem is a note's file name without its extension, the name a wikilink
// uses for it.
func noteStem(rel string) string {
	base := path.Base(rel)
	return strings.TrimSuffix(base, path.Ext(base))
}

type frontmatter struct {
	title, status string
	tags          []string
}

type noteBody struct {
	text      []byte
	firstLine int
}

// splitFrontmatter separates a leading YAML block from the body. A block that
// does not close within maxFrontmatterLines, or does not parse, is body text.
func splitFrontmatter(content []byte) (noteBody, frontmatter) {
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	whole := noteBody{text: content, firstLine: 1}
	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines) == 0 || strings.TrimSpace(string(lines[0])) != "---" {
		return whole, frontmatter{}
	}
	for index := 1; index < len(lines) && index <= maxFrontmatterLines; index++ {
		marker := strings.TrimSpace(string(lines[index]))
		if marker != "---" && marker != "..." {
			continue
		}
		var fields map[string]any
		if err := yaml.Unmarshal(bytes.Join(lines[1:index], nil), &fields); err != nil {
			return whole, frontmatter{}
		}
		body := noteBody{text: bytes.Join(lines[index+1:], nil), firstLine: index + 2}
		front := frontmatter{title: scalar(fields["title"]), status: scalar(fields["status"])}
		front.tags = tagList(fields["tags"])
		if len(front.tags) == 0 {
			front.tags = tagList(fields["tag"])
		}
		return body, front
	}
	return whole, frontmatter{}
}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		if len(v) > 0 {
			return scalar(v[0])
		}
	case nil:
	default:
		if text, err := yaml.Marshal(v); err == nil {
			return strings.TrimSpace(string(text))
		}
	}
	return ""
}

func tagList(value any) []string {
	switch v := value.(type) {
	case string:
		return splitTags(v)
	case []any:
		var tags []string
		for _, item := range v {
			for _, tag := range splitTags(scalar(item)) {
				if !containsFold(tags, tag) {
					tags = append(tags, tag)
				}
			}
		}
		return tags
	}
	return nil
}

// splitTags reads "a, b", "#a #b" or Logseq's "[[a]], [[b]]".
func splitTags(value string) []string {
	var tags []string
	for _, field := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		tag := strings.Trim(field, "#[]\"'")
		if tag != "" && !containsFold(tags, tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}

// OpenLoop is an open item with the note it sits in (FR19).
type OpenLoop struct {
	Task
	Note         string
	NoteTitle    string
	NoteModified time.Time
	// AgeDays is how long since the note was last edited, the closest the
	// files come to saying how old the item is.
	AgeDays    int
	InDecision bool
	InActive   bool
}

// DecisionNote is a note that marks an open decision.
type DecisionNote struct {
	Note, Title, Text string
	Open              int
	Modified          time.Time
}

// StaleNote has open items and has not been edited for StaleAfter.
type StaleNote struct {
	Note, Title string
	Open        int
	Modified    time.Time
}

// Person is a person note the user links to from other notes.
type Person struct {
	Name, Note string
	// Mentions counts the other notes that link to this one.
	Mentions int
}

// ActiveNote was edited within ActiveWithin.
type ActiveNote struct {
	Note, Title string
	Open        int
	Modified    time.Time
}

// Signals are the derived facts a look is built from (FR19). Nothing here
// needs a model.
type Signals struct {
	// Notes counts the notes read; NotesWithOpen those with open items.
	Notes         int
	NotesWithOpen int
	// OpenLoops are ordered by the note's last edit, newest first, then by
	// note and line.
	OpenLoops      []OpenLoop
	Dated          int
	Done           int
	Decisions      []DecisionNote
	Stale          []StaleNote
	People         []Person
	Active         []ActiveNote
	DailyNotes     int
	DroppedSecrets int
}

// Extract reads every note in a reading and derives its signals as of now.
func Extract(reading Reading, now time.Time) Signals {
	facts := make([]NoteFacts, 0, len(reading.Notes))
	for _, note := range reading.Notes {
		facts = append(facts, ExtractNote(note))
	}
	return Derive(facts, now)
}

// Derive turns per-note facts into signals as of now (FR19).
func Derive(notes []NoteFacts, now time.Time) Signals {
	notes = append([]NoteFacts(nil), notes...)
	sort.SliceStable(notes, func(i, j int) bool {
		if !notes[i].ModTime.Equal(notes[j].ModTime) {
			return notes[i].ModTime.After(notes[j].ModTime)
		}
		return notes[i].RelPath < notes[j].RelPath
	})
	signals := Signals{Notes: len(notes)}
	for _, note := range notes {
		signals.Done += note.Done
		signals.DroppedSecrets += note.DroppedSecrets
		if note.Daily {
			signals.DailyNotes++
		}
		age := now.Sub(note.ModTime)
		active := age <= ActiveWithin
		if len(note.Open) > 0 {
			signals.NotesWithOpen++
		}
		for _, task := range note.Open {
			if task.Due != "" {
				signals.Dated++
			}
			signals.OpenLoops = append(signals.OpenLoops, OpenLoop{
				Task:         task,
				Note:         note.RelPath,
				NoteTitle:    note.Title,
				NoteModified: note.ModTime,
				AgeDays:      max(0, int(age/(24*time.Hour))),
				InDecision:   note.Decision,
				InActive:     active,
			})
		}
		if note.Decision {
			signals.Decisions = append(signals.Decisions, DecisionNote{Note: note.RelPath, Title: note.Title, Text: note.DecisionText, Open: len(note.Open), Modified: note.ModTime})
		}
		if len(note.Open) > 0 && age >= StaleAfter {
			signals.Stale = append(signals.Stale, StaleNote{Note: note.RelPath, Title: note.Title, Open: len(note.Open), Modified: note.ModTime})
		}
		if active {
			signals.Active = append(signals.Active, ActiveNote{Note: note.RelPath, Title: note.Title, Open: len(note.Open), Modified: note.ModTime})
		}
	}
	sort.SliceStable(signals.Stale, func(i, j int) bool {
		if signals.Stale[i].Open != signals.Stale[j].Open {
			return signals.Stale[i].Open > signals.Stale[j].Open
		}
		return signals.Stale[i].Modified.Before(signals.Stale[j].Modified)
	})
	signals.People = people(notes)
	return signals
}

// people resolves wikilinks the way Obsidian does, by file name (or by path
// when the link names one), and keeps the targets that are person notes:
// under a folder named People or Contacts, or tagged person. They are ranked
// by how many other notes mention them.
func people(notes []NoteFacts) []Person {
	byStem := map[string][]int{}
	for index, note := range notes {
		stem := strings.ToLower(noteStem(note.RelPath))
		byStem[stem] = append(byStem[stem], index)
	}
	mentions := map[int]map[int]bool{}
	for from, note := range notes {
		for _, link := range note.Links {
			to, ok := resolveLink(notes, byStem, link)
			if !ok || to == from || !isPersonNote(notes[to]) {
				continue
			}
			if mentions[to] == nil {
				mentions[to] = map[int]bool{}
			}
			mentions[to][from] = true
		}
	}
	found := make([]Person, 0, len(mentions))
	for index, from := range mentions {
		found = append(found, Person{Name: noteStem(notes[index].RelPath), Note: notes[index].RelPath, Mentions: len(from)})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Mentions != found[j].Mentions {
			return found[i].Mentions > found[j].Mentions
		}
		return found[i].Name < found[j].Name
	})
	return found
}

func resolveLink(notes []NoteFacts, byStem map[string][]int, link string) (int, bool) {
	target := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(link, ".md"), ".markdown"))
	candidates := byStem[path.Base(target)]
	if len(candidates) == 0 {
		return 0, false
	}
	if strings.Contains(target, "/") {
		for _, index := range candidates {
			stemPath := strings.ToLower(strings.TrimSuffix(notes[index].RelPath, path.Ext(notes[index].RelPath)))
			if stemPath == target || strings.HasSuffix(stemPath, "/"+target) {
				return index, true
			}
		}
		return 0, false
	}
	// Prefer a person note when several notes share the name.
	for _, index := range candidates {
		if isPersonNote(notes[index]) {
			return index, true
		}
	}
	return candidates[0], true
}

func isPersonNote(note NoteFacts) bool {
	for _, tag := range note.Tags {
		if strings.EqualFold(tag, "person") {
			return true
		}
	}
	for dir := path.Dir(note.RelPath); dir != "." && dir != "/"; dir = path.Dir(dir) {
		switch strings.ToLower(path.Base(dir)) {
		case "people", "contacts":
			return true
		}
	}
	return false
}
