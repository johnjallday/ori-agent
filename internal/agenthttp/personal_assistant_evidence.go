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
}

func newEvidenceLedger() *evidenceLedger { return &evidenceLedger{now: time.Now} }

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

// prior returns what this turn already read of one source, if anything.
func (l *evidenceLedger) prior(kind, workspaceID, id string) (assistantcontext.SourceRef, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, source := range l.sources {
		if source.Kind == kind && source.WorkspaceID == workspaceID && source.ID == id {
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
var citationMarker = regexp.MustCompile(` ?\[S\d{1,3}\]`)

// cite checks the reply's source markers against what this turn actually
// delivered. A marker for anything else is removed from the text: the model
// cannot cite a source it did not read, another turn's source, or a URL. It
// returns the bounded list of sources read, each marked cited or not.
func (l *evidenceLedger) cite(answer string) (string, []assistantcontext.SourceRef) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cited := map[string]bool{}
	answer = citationMarker.ReplaceAllStringFunc(answer, func(marker string) string {
		key := strings.Trim(marker, " []")
		for _, source := range l.sources {
			if source.Key == key {
				cited[key] = true
				return marker
			}
		}
		return ""
	})
	if len(l.sources) == 0 {
		return answer, nil
	}
	sources := make([]assistantcontext.SourceRef, 0, len(l.sources))
	for _, source := range l.sources {
		source.Cited = cited[source.Key]
		sources = append(sources, source)
	}
	if len(sources) > assistantcontext.SourceLimit {
		sources = sources[:assistantcontext.SourceLimit]
	}
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

// fitChunk is the longest part from offset, at most limit characters, whose
// delivered form fits budget. The budget is counted on the text as the provider
// receives it: JSON escaping can make hostile text several times longer, and
// that expansion must not be free. size is the delivered size of the part.
func fitChunk(text string, offset, limit, budget int) (chunk string, start, end, total, size int) {
	for attempt := 0; ; attempt++ {
		chunk, start, end, total = evidenceChunk(text, offset, limit)
		encoded, err := json.Marshal(chunk)
		size = utf8.RuneCount(encoded)
		if err == nil && size <= budget {
			return chunk, start, end, total, size
		}
		if end == start || budget <= 0 || attempt == 16 {
			return "", start, start, total, 0
		}
		// Shrink in proportion to the overshoot, and always by at least one.
		limit = min(end-start-1, (end-start)*budget/max(size, 1))
	}
}
