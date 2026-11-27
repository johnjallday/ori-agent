package blueprintintake

import (
	"regexp"
	"strings"
	"time"
)

var unstatedDate = regexp.MustCompile(`(?i)\b(tbd|tba|to be (determined|announced))\b`)

// Reviewed files promise a verifiable answer. A source ID and plausible quote
// from a model aren't evidence on their own: require text from the very snapshot
// that ran. For ambiguous dates keep the quote, not a guessed deadline.
func supportedFileProposals(items []ProposalItem, text string, notice *ProposalNotice) []ProposalItem {
	normalized := strings.Join(strings.Fields(text), " ")
	kept := items[:0]
	for _, item := range items {
		quote := strings.Join(strings.Fields(item.Source.Quote), " ")
		if quote == "" || !strings.Contains(normalized, quote) {
			notice.Unsupported++
			continue
		}
		if item.Kind == ProposalKindTicket && item.DueAt != nil && (unstatedDate.MatchString(quote) || !quotedDate(quote, *item.DueAt)) {
			item.DueAt = nil
			item.DueInput = ""
			item.NoTimeGiven = false
		}
		kept = append(kept, item)
	}
	return kept
}

func quotedDate(quote string, date time.Time) bool {
	quote = strings.ToLower(quote)
	for _, layout := range []string{"2006-01-02", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "2 Jan 2006", "January 2 2006", "Jan 2 2006", "1/2/2006", "01/02/2006"} {
		if strings.Contains(quote, strings.ToLower(date.Format(layout))) {
			return true
		}
	}
	return false
}
