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
	suggestion, ok, _ := interviewSuggestionExcluding(offer, nil)
	return suggestion, ok
}

// interviewSuggestionExcluding is InterviewSuggestionFromOffer without the
// projects whose folder keys are in remembered: those are already known to the
// assistant, so the next project the scan found takes their place. The last
// result says the offer named projects and every one of them was remembered.
// The snapshot and a scan started in the wizard both word their suggestion
// here, so the two can never disagree about what is worth proposing.
func interviewSuggestionExcluding(offer FolderOffer, remembered map[string]bool) (InterviewSuggestion, bool, bool) {
	if offer.Portfolio != nil {
		return InterviewSuggestion{}, false, false
	}
	// An ambiguous root is also stored as a project subject, so the verdict
	// decides: only a project the scan recognized is worth proposing.
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject, folderdigest.KindMixed:
	default:
		return InterviewSuggestion{}, false, false
	}
	queue := offer.Queue
	subject := offer.Subject
	if remembered[subject.Key] {
		// The offer's own project is remembered: the first queued project that
		// is not becomes the proposal, or there is nothing to propose.
		next := -1
		for i, candidate := range queue {
			if _, ok := interviewSuggestionForCandidate(candidate); ok && !remembered[candidate.Key] {
				next = i
				break
			}
		}
		if next < 0 {
			return InterviewSuggestion{}, false, true
		}
		subject, queue = queue[next], queue[next+1:]
	}
	suggestion, ok := interviewSuggestionForCandidate(subject)
	if !ok {
		return InterviewSuggestion{}, false, false
	}
	// The other projects the scan found in the same folder are offered as
	// alternates, in the scan's own rank order. A tidy candidate is not one.
	seen := map[string]bool{suggestion.Text: true}
	for _, candidate := range queue {
		if len(suggestion.Alternates) == interviewSuggestionMaxAlternates {
			break
		}
		alternate, ok := interviewSuggestionForCandidate(candidate)
		if !ok || remembered[candidate.Key] || seen[alternate.Text] {
			continue
		}
		seen[alternate.Text] = true
		suggestion.Alternates = append(suggestion.Alternates, alternate)
	}
	return suggestion, true, false
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
	// A whole Downloads, Documents or Desktop is where work is kept, not the
	// work itself. A file picked straight out of one makes that folder the
	// scan's project; naming it would answer the question with "Downloads".
	if candidate.IsRoot && isKnownFolderName(name) {
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
// folder is reported as remembered and not proposed again. Both fields are
// optional, so a missing or failing reader only leaves its field out.
func (s *KnowledgeInterviewService) FolderSnapshot(ctx context.Context, userID string) (InterviewFolderSnapshot, error) {
	if s == nil || s.store == nil {
		return InterviewFolderSnapshot{}, ErrRepairNeeded
	}
	var snapshot InterviewFolderSnapshot
	// An unreadable dossier claims nothing as remembered and hides no offer.
	text, remembered, _ := s.rememberedFolderProjects(ctx, userID)
	snapshot.RememberedProject = text
	if s.folders == nil {
		return snapshot, nil
	}
	offers, err := s.folders.WaitingFolderOffers(ctx, userID)
	if err != nil {
		return snapshot, nil
	}
	for _, offer := range offers {
		if suggestion, ok, _ := interviewSuggestionExcluding(offer, remembered); ok {
			snapshot.Suggestion = &suggestion
			break
		}
	}
	return snapshot, nil
}

// SuggestionFromOffer words one offer as an answer to question 1, leaving out
// every project already remembered from a folder. The last result is true when
// the offer named only projects the assistant already remembers, so the wizard
// can say that instead of "I couldn't tell".
func (s *KnowledgeInterviewService) SuggestionFromOffer(ctx context.Context, userID string, offer FolderOffer) (InterviewSuggestion, bool, bool) {
	var remembered map[string]bool
	if s != nil && s.store != nil {
		_, remembered, _ = s.rememberedFolderProjects(ctx, userID)
	}
	return interviewSuggestionExcluding(offer, remembered)
}

// rememberedFolderProjects reads the project facts approved from a folder that
// are still live: it returns the newest one's text and the folder keys they
// all came from. "Live" is the dossier's own test (KnowledgeLearningService.
// ReviewItems): the source still revalidates and the canonical line still
// matches. A fact the dossier shows as needing review is therefore not claimed
// as remembered here either. It writes nothing.
func (s *KnowledgeInterviewService) rememberedFolderProjects(ctx context.Context, userID string) (string, map[string]bool, error) {
	keys := map[string]bool{}
	if s.learning == nil {
		return "", keys, nil
	}
	items, err := s.learning.ReviewItems(ctx, userID)
	if err != nil {
		return "", keys, err
	}
	live := map[string]KnowledgeReviewItem{}
	for _, item := range items {
		if item.State == KnowledgeApproved && item.Category == "projects" &&
			item.SourceKind == FolderScanSourceKind && strings.TrimSpace(item.Text) != "" {
			live[item.ID] = item
		}
	}
	if len(live) == 0 {
		return "", keys, nil
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", keys, err
	}
	var text string
	var newest time.Time
	for _, item := range doc.Items {
		view, ok := live[item.ID]
		if !ok {
			continue
		}
		// A reworded fact keeps no evidence on its new revision, so the folder
		// it came from is read from every revision, not only the current one.
		for _, revision := range item.Revisions {
			for _, evidence := range revision.Evidence {
				if evidence.SourceKind == FolderScanSourceKind {
					keys[evidence.SourceID] = true
				}
			}
		}
		if text == "" || view.UpdatedAt.After(newest) {
			text, newest = strings.TrimSpace(view.Text), view.UpdatedAt
		}
	}
	return text, keys, nil
}

// isKnownFolderName reports whether name is one of the chooser's fixed
// folders (Downloads, Documents, Desktop).
func isKnownFolderName(name string) bool {
	for _, chip := range folderChips {
		if strings.EqualFold(name, chip.Dir) {
			return true
		}
	}
	return false
}

// indefiniteArticle picks "an" before a vowel letter and "a" otherwise.
func indefiniteArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}
