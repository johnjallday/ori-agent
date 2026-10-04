package personalassistant

import (
	"context"
	"strings"
	"time"

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

// InterviewFolderOffers is the interview's read-only view of the folders the
// user has shown the assistant. An implementation must neither scan nor write.
type InterviewFolderOffers interface {
	// WaitingFolderOffers returns the offers the user has not answered: the
	// pending one first, then those set aside for later, newest first.
	WaitingFolderOffers(ctx context.Context, userID string) ([]FolderOffer, error)
}

// SetFolderOffers lets the interview prefill its first question from a folder
// the user already showed the assistant on Home.
func (s *KnowledgeInterviewService) SetFolderOffers(reader InterviewFolderOffers) {
	if s != nil {
		s.folders = reader
	}
}

// InterviewFolderSnapshot is what folders already shown add to question 1.
type InterviewFolderSnapshot struct {
	// Suggestion words an offer still waiting on Home; nil when there is none.
	Suggestion *InterviewSuggestion
	// RememberedProject is the current text of the newest project fact that
	// was approved from a folder; empty when there is none.
	RememberedProject string
}

// FolderSnapshot reads what the assistant already knows from folders. It is
// pure: it never scans and never writes. A project already remembered from a
// folder is reported as remembered and not proposed again. The suggestion is
// optional, so a missing or failing folder reader only leaves it out.
func (s *KnowledgeInterviewService) FolderSnapshot(ctx context.Context, userID string) (InterviewFolderSnapshot, error) {
	if s == nil || s.store == nil {
		return InterviewFolderSnapshot{}, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return InterviewFolderSnapshot{}, err
	}
	var snapshot InterviewFolderSnapshot
	var newest time.Time
	// remembered holds the folder keys the approved project facts came from.
	remembered := map[string]bool{}
	for _, item := range doc.Items {
		if item.State != KnowledgeApproved || item.Category != "projects" || item.SourceKind != FolderScanSourceKind {
			continue
		}
		for _, revision := range item.Revisions {
			if revision.ID != item.CurrentRevisionID {
				continue
			}
			for _, evidence := range revision.Evidence {
				if evidence.SourceKind == FolderScanSourceKind {
					remembered[evidence.SourceID] = true
				}
			}
			text := strings.TrimSpace(revision.Text)
			if text != "" && (snapshot.RememberedProject == "" || item.UpdatedAt.After(newest)) {
				snapshot.RememberedProject, newest = text, item.UpdatedAt
			}
		}
	}
	if s.folders == nil {
		return snapshot, nil
	}
	offers, err := s.folders.WaitingFolderOffers(ctx, userID)
	if err != nil {
		return snapshot, nil
	}
	for _, offer := range offers {
		if remembered[offer.Subject.Key] {
			continue
		}
		queue := make([]FolderCandidateRecord, 0, len(offer.Queue))
		for _, candidate := range offer.Queue {
			if !remembered[candidate.Key] {
				queue = append(queue, candidate)
			}
		}
		offer.Queue = queue
		if suggestion, ok := InterviewSuggestionFromOffer(offer); ok {
			snapshot.Suggestion = &suggestion
			break
		}
	}
	return snapshot, nil
}

// indefiniteArticle picks "an" before a vowel letter and "a" otherwise.
func indefiniteArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}
