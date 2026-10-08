package agenthttp

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// evidenceLedger is one accepted turn's record of what Ori delivered to the
// model. It enforces the aggregate workspace-evidence budget before provider
// input, across every call in every round, and it is the only thing a citation
// can resolve against. It holds references and counts, never a source body.
type evidenceLedger struct {
	mu      sync.Mutex
	used    int
	sources []assistantcontext.SourceRef
	now     func() time.Time
	// Reader outcomes and time, kept for the turn's diagnostics line.
	outcomes map[string]int
	readTime time.Duration
}

func newEvidenceLedger() *evidenceLedger {
	return &evidenceLedger{now: time.Now, outcomes: map[string]int{}}
}

// observe counts one reader call by its outcome. Outcomes are the readers' own
// closed status words; nothing a source or the model wrote is recorded.
func (l *evidenceLedger) observe(outcome string, elapsed time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch assistantcontext.Availability(outcome) {
	case assistantcontext.Available, assistantcontext.Empty, assistantcontext.Unavailable, assistantcontext.Denied,
		assistantcontext.Unsupported, assistantcontext.Partial, "over_budget", "failed":
	default:
		outcome = "other"
	}
	l.outcomes[outcome]++
	l.readTime += elapsed
}

// diagnostics summarizes the turn's reading in counts and durations only.
func (l *evidenceLedger) diagnostics() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	calls, partial := 0, 0
	outcomes := make(map[string]int, len(l.outcomes))
	for outcome, count := range l.outcomes {
		outcomes[outcome], calls = count, calls+count
	}
	kinds := map[string]int{}
	for _, source := range l.sources {
		kinds[source.Kind]++
		if source.Coverage == assistantcontext.CoveragePartial {
			partial++
		}
	}
	return map[string]any{
		"reader_calls": calls, "reader_outcomes": outcomes, "read_us": l.readTime.Microseconds(),
		"sources_read": len(l.sources), "sources_by_kind": kinds, "sources_partial": partial,
		"evidence_chars": l.used, "evidence_limit": assistantcontext.EvidenceLimit,
	}
}

// charge reserves size characters of the turn's budget. It refuses, and
// reserves nothing, when they do not fit.
func (l *evidenceLedger) charge(size int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if size < 0 || l.used+size > assistantcontext.EvidenceLimit {
		return false
	}
	l.used += size
	return true
}

func (l *evidenceLedger) remaining() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return assistantcontext.EvidenceLimit - l.used
}

// prior returns what this turn most recently read of one source, if anything.
// It is the latest record: after a source changed and was read again from the
// start, a continuation is checked against that newer read, not the stale one.
func (l *evidenceLedger) prior(kind, workspaceID, id string) (assistantcontext.SourceRef, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.sources) - 1; i >= 0; i-- {
		if source := l.sources[i]; source.Kind == kind && source.WorkspaceID == workspaceID && source.ID == id {
			return source, true
		}
	}
	return assistantcontext.SourceRef{}, false
}

// record notes that content of a source was delivered and returns its
// reference for this turn. A second read of the same version extends the range
// already read; it does not become a second source.
func (l *evidenceLedger) record(source assistantcontext.SourceRef) assistantcontext.SourceRef {
	l.mu.Lock()
	defer l.mu.Unlock()
	source.ReadAt = l.now().UTC()
	for i := range l.sources {
		prior := &l.sources[i]
		if prior.Kind != source.Kind || prior.WorkspaceID != source.WorkspaceID || prior.ID != source.ID || prior.Version != source.Version {
			continue
		}
		// Two separate parts stay two records; joining them would claim the
		// unread part between them.
		if source.Start > prior.End || source.End < prior.Start {
			continue
		}
		prior.Start, prior.End = min(prior.Start, source.Start), max(prior.End, source.End)
		prior.ReadAt = source.ReadAt
		prior.Coverage = sourceCoverage(prior.Start, prior.End, prior.Total, source.Coverage)
		return *prior
	}
	source.Key = "S" + strconv.Itoa(len(l.sources)+1)
	source.Coverage = sourceCoverage(source.Start, source.End, source.Total, source.Coverage)
	l.sources = append(l.sources, source)
	return source
}

// sourceCoverage is full only when the whole source was delivered untruncated.
func sourceCoverage(start, end, total int, reported string) string {
	if reported == assistantcontext.CoveragePartial || start > 0 || end < total {
		return assistantcontext.CoveragePartial
	}
	return assistantcontext.CoverageFull
}

// A marker and the single space before it, so removing one leaves no gap.
var citationMarker = regexp.MustCompile(` ?\[S\d{1,9}\]`)

// withoutCitationMarkers removes every source marker from text. Keys are issued
// afresh each turn, so a marker kept from an earlier answer would name whatever
// source receives that key now; earlier answers are replayed without them.
func withoutCitationMarkers(text string) string { return citationMarker.ReplaceAllString(text, "") }

// cite checks the reply's source markers against what this turn actually
// delivered. A marker for anything else is removed from the text: the model
// cannot cite a source it did not read, another turn's source, or a URL. It
// returns the bounded list of sources read, in the order they were read, each
// marked cited or not. When more were read than the list holds, cited sources
// are kept ahead of uncited ones, and a marker whose source still did not fit
// is removed too: every marker left in the text has a source shown with it.
func (l *evidenceLedger) cite(answer string) (string, []assistantcontext.SourceRef) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cited := make(map[string]bool, len(l.sources))
	for _, source := range l.sources {
		cited[source.Key] = false
	}
	for _, marker := range citationMarker.FindAllString(answer, -1) {
		key := strings.Trim(marker, " []")
		if _, read := cited[key]; read {
			cited[key] = true
		}
	}
	shown := make(map[string]bool, len(l.sources))
	for _, wanted := range []bool{true, false} {
		for _, source := range l.sources {
			if cited[source.Key] == wanted && len(shown) < assistantcontext.SourceLimit {
				shown[source.Key] = true
			}
		}
	}
	var sources []assistantcontext.SourceRef
	for _, source := range l.sources {
		if shown[source.Key] {
			source.Cited = cited[source.Key]
			sources = append(sources, source)
		}
	}
	answer = citationMarker.ReplaceAllStringFunc(answer, func(marker string) string {
		if shown[strings.Trim(marker, " []")] {
			return marker
		}
		return ""
	})
	return answer, sources
}

// evidenceChunk cuts text to the part a reader may deliver now: from offset, at
// most limit characters, never splitting a character. Positions are counted in
// characters of the text as given, so a continuation neither skips nor repeats.
func evidenceChunk(text string, offset, limit int) (chunk string, start, end, total int) {
	runes := []rune(text)
	total = len(runes)
	start = min(max(offset, 0), total)
	end = min(start+max(limit, 0), total)
	return string(runes[start:end]), start, end, total
}

func evidenceSize(text string) int { return utf8.RuneCountInString(text) }

// contentEnvelope is the delivered size of a content result apart from the
// content itself: its names, labels, times and counters as the provider will
// receive them. It is measured on the result with the widest values its varying
// fields can take, so a read charged this plus its content is never charged
// less than it delivers, however long a name is or however it is escaped.
func contentEnvelope(result map[string]any, total int, nextStep string) int {
	probe := make(map[string]any, len(result)+10)
	for name, value := range result {
		probe[name] = value
	}
	probe["content"], probe["source"], probe["cite_as"], probe["coverage"] = "", "S999", "[S999]", assistantcontext.CoveragePartial
	probe["read_at"], probe["next_step"] = readerTime(time.Now()), nextStep
	for _, counter := range []string{"start", "end", "total", "next_offset", "withheld_lines"} {
		probe[counter] = total
	}
	encoded, err := json.Marshal(probe)
	if err != nil {
		return assistantcontext.EvidenceLimit // nothing fits a result that cannot be measured
	}
	return utf8.RuneCount(encoded)
}

// withheldLine replaces a line of source text that looks like a secret.
const withheldLine = "[withheld by Ori: secret-like text]"

// secretLineWindow is how far past a part's edge a line is followed when it is
// checked, so a token cut by the edge is still seen whole.
const secretLineWindow = 4096

// A private key is many lines, and only its first says what it is. Every line
// from that one through the matching last line is withheld. privateKeyWindow is
// how far before a part the first line is looked for; a PEM private key is a
// few thousand characters at most.
var (
	privateKeyFirstLine = regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----`)
	privateKeyLastLine  = regexp.MustCompile(`-----END[A-Z ]*PRIVATE KEY-----`)
)

const privateKeyWindow = 16384

// insidePrivateKey reports whether position falls inside a private key block
// that began within the window before it and has not ended.
func insidePrivateKey(text string, position int) bool {
	back := max(0, position-privateKeyWindow)
	for back < position && !utf8.RuneStart(text[back]) {
		back++
	}
	before := text[back:position]
	first := privateKeyFirstLine.FindAllStringIndex(before, -1)
	if len(first) == 0 {
		return false
	}
	last := privateKeyLastLine.FindAllStringIndex(before, -1)
	return len(last) == 0 || last[len(last)-1][0] < first[len(first)-1][0]
}

// runeByteOffset is the byte position of the character at index.
func runeByteOffset(text string, index int) int {
	if index <= 0 {
		return 0
	}
	count := 0
	for position := range text {
		if count == index {
			return position
		}
		count++
	}
	return len(text)
}

// readerChunk is the part of text from offset, at most limit characters, with
// secret-like lines and whole private key blocks withheld. start, end and total
// count characters of the source text. Only the lines this part touches are
// examined, each as a whole line: a secret cut by the part's edge is withheld
// on both sides of the edge, a part that starts inside a private key knows it,
// and one part of a very large file does not cost a scan of all of it.
func readerChunk(text string, offset, limit int) (chunk string, start, end, total, withheld int) {
	total = utf8.RuneCountInString(text)
	start = min(max(offset, 0), total)
	end = min(start+max(limit, 0), total)
	from := runeByteOffset(text, start)
	to := from + runeByteOffset(text[from:], end-start)
	lineFrom, lineTo := strings.LastIndexByte(text[:from], '\n')+1, len(text)
	if next := strings.IndexByte(text[to:], '\n'); next >= 0 {
		lineTo = to + next
	}
	// A line with no break for a long way is followed only so far, on a
	// character boundary.
	for lineFrom = max(lineFrom, from-secretLineWindow); lineFrom < from && !utf8.RuneStart(text[lineFrom]); lineFrom++ {
	}
	for lineTo = min(lineTo, to+secretLineWindow); lineTo > to && lineTo < len(text) && !utf8.RuneStart(text[lineTo]); lineTo-- {
	}
	inKey := insidePrivateKey(text, lineFrom)
	if !inKey && !sensitive.ContainsSecretLikeText(text[lineFrom:lineTo]) {
		return text[from:to], start, end, total, 0
	}
	var kept strings.Builder
	for position := lineFrom; position < lineTo; {
		stop := lineTo
		if next := strings.IndexByte(text[position:lineTo], '\n'); next >= 0 {
			stop = position + next
		}
		line := text[position:stop]
		secret := inKey || sensitive.ContainsSecretLikeText(line)
		if privateKeyFirstLine.MatchString(line) {
			inKey = true
		}
		if inKey && privateKeyLastLine.MatchString(line) {
			inKey = false
		}
		if inside, until := max(position, from), min(stop, to); inside < until {
			if secret {
				kept.WriteString(withheldLine)
				withheld++
			} else {
				kept.WriteString(text[inside:until])
			}
		}
		if stop >= from && stop < to {
			kept.WriteByte('\n')
		}
		position = stop + 1
	}
	return kept.String(), start, end, total, withheld
}

// fitReaderChunk is the longest part from offset, at most limit characters,
// whose delivered form fits budget. The budget is counted on the text as the
// provider receives it, after secret-like lines are withheld: JSON escaping can
// make hostile text several times longer, and that expansion must not be free.
// size is the delivered size of the part.
func fitReaderChunk(text string, offset, limit, budget int) (chunk string, start, end, total, size, withheld int) {
	for attempt := 0; ; attempt++ {
		chunk, start, end, total, withheld = readerChunk(text, offset, limit)
		encoded, err := json.Marshal(chunk)
		size = utf8.RuneCount(encoded)
		if err == nil && size <= budget {
			return chunk, start, end, total, size, withheld
		}
		if end == start || budget <= 0 || attempt == 16 {
			return "", start, start, total, 0, 0
		}
		// Shrink in proportion to the overshoot, and always by at least one.
		limit = min(end-start-1, (end-start)*budget/max(size, 1))
	}
}

// fitListing shortens a listing that is too long for what is left of the turn's
// budget: it keeps as many leading rows as fit and says that it stopped. A long
// folder listing is then partial, with a way to continue, instead of refused
// whole. ok is false when not even one row fits.
func fitListing(data map[string]any, budget int) (encoded string, ok bool) {
	key, rows := "", []map[string]any(nil)
	for _, candidate := range []string{"entries", "attachments", "notes", "tasks"} {
		if list, _ := data[candidate].([]map[string]any); len(list) > len(rows) {
			key, rows = candidate, list
		}
	}
	shown := make(map[string]any, len(data)+4)
	for name, value := range data {
		shown[name] = value
	}
	shown["status"], shown["reason"], shown["truncated"] = assistantcontext.Partial, "listing_shortened_to_fit_reading_budget", true
	shown["next_step"] = "Only the first part of this listing fit this turn's reading budget. Say that the listing is partial and do not describe what was not listed; a narrower listing, such as one subfolder, can be read in a follow-up."
	for low, high := 1, len(rows)-1; low <= high; {
		middle := (low + high) / 2
		shown[key], shown["listed"] = rows[:middle], middle
		candidate, err := json.Marshal(shown)
		if err == nil && evidenceSize(string(candidate)) <= budget {
			encoded, low = string(candidate), middle+1
		} else {
			high = middle - 1
		}
	}
	return encoded, encoded != ""
}
