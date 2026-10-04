package personalassistant

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// interviewSuggestionMaxAlternates bounds the "Also in this folder" choices.
const interviewSuggestionMaxAlternates = 3

// InterviewSuggestion is a proposed answer to the interview's first question,
// worded from a folder the user showed the assistant. It is text to confirm or
// edit, never a saved fact: Folder is a base name, and nothing here is a path.
type InterviewSuggestion struct {
	Text       string                `json:"text"`
	Folder     string                `json:"folder"`
	Alternates []InterviewSuggestion `json:"alternates,omitempty"`
}

// InterviewSuggestionFromOffer words a folder offer as an answer to "What's
// the main thing you're working on right now?". Only an offer that names one
// project qualifies: a dump, an empty or declined folder, a folder the scan
// could not classify, and a whole collection (a portfolio) all give nothing,
// so the user types the answer instead.
func InterviewSuggestionFromOffer(offer FolderOffer) (InterviewSuggestion, bool) {
	if offer.Portfolio != nil {
		return InterviewSuggestion{}, false
	}
	// An ambiguous root is also stored as a project subject, so the verdict
	// decides: only a project the scan recognized is worth proposing.
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject, folderdigest.KindMixed:
	default:
		return InterviewSuggestion{}, false
	}
	suggestion, ok := interviewSuggestionForCandidate(offer.Subject)
	if !ok {
		return InterviewSuggestion{}, false
	}
	// The other projects the scan found in the same folder are offered as
	// alternates, in the scan's own rank order. A tidy candidate is not one.
	seen := map[string]bool{suggestion.Text: true}
	for _, candidate := range offer.Queue {
		if len(suggestion.Alternates) == interviewSuggestionMaxAlternates {
			break
		}
		alternate, ok := interviewSuggestionForCandidate(candidate)
		if !ok || seen[alternate.Text] {
			continue
		}
		seen[alternate.Text] = true
		suggestion.Alternates = append(suggestion.Alternates, alternate)
	}
	return suggestion, true
}

// interviewSuggestionForCandidate builds "<Name>, a <Marker>" (or the bare
// name) from one project candidate. The marker is already a plain label from
// the host table. A text the memory validator would refuse, which includes one
// over the interview's 500-byte limit, is never proposed: the wizard could not
// save it.
func interviewSuggestionForCandidate(candidate FolderCandidateRecord) (InterviewSuggestion, bool) {
	name := strings.TrimSpace(candidate.Name)
	if candidate.Kind != FolderChoiceProject || name == "" {
		return InterviewSuggestion{}, false
	}
	text := name
	if marker := strings.TrimSpace(candidate.Marker); marker != "" {
		text = name + ", " + indefiniteArticle(marker) + " " + marker
	}
	clean, err := workspace.ValidateMemoryText(text)
	if err != nil {
		return InterviewSuggestion{}, false
	}
	return InterviewSuggestion{Text: clean, Folder: name}, true
}

// indefiniteArticle picks "an" before a vowel letter and "a" otherwise.
func indefiniteArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}
