package projectlibrary

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CollectionBrief is the Manager's short description of the whole library,
// written by its bounded turn after one scan of a Home that reads song facts.
// It describes; it grants, changes and recommends nothing. A newer scan's
// brief replaces it.
type CollectionBrief struct {
	ScanID    string    `json:"scan_id"`
	Text      string    `json:"text"`
	AgentName string    `json:"agent_name"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

// Limits on a brief (FR 19). The prompt carries the rest of the rules (no
// claims that a song is done or good, no advice); the save enforces these.
const (
	maxBriefRunes     = 500
	maxBriefSentences = 3
)

var (
	// ErrBriefRefused means the text is not a plain, short description: too
	// long, too many sentences, not plain text, or holding a path or link.
	ErrBriefRefused = errors.New("collection brief refused")
	// ErrBriefAlreadySaved means this scan's turn already saved its one brief.
	ErrBriefAlreadySaved = errors.New("this scan already has its collection brief")

	// A token shaped like a file name ("Take 2.wav", "notes.txt") or a domain.
	briefFileLike = regexp.MustCompile(`(?i)[a-z0-9_\-]\.[a-z][a-z0-9]{1,5}\b`)
	// A sentence ends at . ! or ? followed by a space or the end. A decimal
	// point ("128.5") is not followed by a space.
	briefSentenceEnd = regexp.MustCompile(`[.!?]+(\s|$)`)
)

// briefTextProblem says why text cannot be saved as a brief, or "".
func briefTextProblem(text string) string {
	switch {
	case strings.TrimSpace(text) == "":
		return "the brief is empty"
	case strings.TrimSpace(text) != text:
		return "the brief has leading or trailing space"
	case !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxBriefRunes:
		return "the brief is longer than 500 characters"
	case !validText(text, 4*maxBriefRunes):
		return "the brief must be one plain paragraph without line breaks"
	case strings.ContainsAny(text, "<>`*|{}[]\\/~"):
		return "the brief must be plain text without markup, paths or links"
	case strings.Contains(strings.ToLower(text), "www.") || strings.Contains(strings.ToLower(text), "http") ||
		strings.Contains(text, "@") || briefFileLike.MatchString(text):
		return "the brief must not name a file, path or link"
	case len(briefSentenceEnd.FindAllStringIndex(text, -1)) > maxBriefSentences:
		return "the brief is longer than three sentences"
	}
	return ""
}

func (b *CollectionBrief) valid(scans map[string]bool) bool {
	return b.ScanID != "" && validText(b.ScanID, 160) && scans[b.ScanID] &&
		briefTextProblem(b.Text) == "" &&
		b.AgentName != "" && validText(b.AgentName, 160) && b.Model != "" && validText(b.Model, 160) &&
		!b.CreatedAt.IsZero()
}

// SaveCollectionBrief stores the brief for the scan the authority's run is
// reviewing. Only a scan-review turn of a Home whose song-details switch is on
// can save one, while its receipt is still started; one per scan, stamped with
// the turn's provenance, in one fenced Home write. The same text retried is a
// replay; a second, different brief for the scan is refused.
func (s *Store) SaveCollectionBrief(authority ManagerAuthority, text string) (CollectionBrief, bool, error) {
	run := authority.Run
	if run.ScanID == "" || !run.Brief || !validText(run.ScanID, 160) || !validText(run.Model, 160) || run.Model == "" {
		return CollectionBrief{}, false, ErrConflict // Not inside a brief turn: chat can never save one.
	}
	if problem := briefTextProblem(text); problem != "" {
		return CollectionBrief{}, false, errors.Join(ErrBriefRefused, errors.New(problem))
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return CollectionBrief{}, false, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return CollectionBrief{}, false, err
	}
	bindingRev := state.HomeBindings.StateRevision
	digest := proposalRunOpDigest(scope, run.ScanID, "brief", text)
	brief := CollectionBrief{ScanID: run.ScanID, Text: text, AgentName: authority.AgentName, Model: run.Model,
		CreatedAt: s.now().UTC()}
	_, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: "collection-brief:" + run.ScanID, action: "save_collection_brief", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return current.HomeBindings.StateRevision == bindingRev && boundManager(current, home, authority) &&
				current.GetSongDetailsConsent().Active()
		}, func(current *Document) (string, error) {
			receipt, ok := findProposalRun(*current, run.ScanID)
			if !ok || receipt.Status != runStarted {
				return "", ErrConflict // The turn ended; nothing more is filed under it.
			}
			if current.CollectionBrief != nil && current.CollectionBrief.ScanID == run.ScanID {
				return "", ErrBriefAlreadySaved
			}
			saved := brief
			current.CollectionBrief = &saved
			return run.ScanID, nil
		})
	if errors.Is(err, ErrConflict) && !replay {
		// The same scan's key with other text: the one brief is already saved.
		if fresh, readErr := s.Read(scope); readErr == nil && fresh.CollectionBrief != nil &&
			fresh.CollectionBrief.ScanID == run.ScanID && fresh.CollectionBrief.Text != text {
			return CollectionBrief{}, false, ErrBriefAlreadySaved
		}
	}
	if err != nil {
		return CollectionBrief{}, false, err
	}
	if replay {
		fresh, readErr := s.Read(scope)
		if readErr != nil || fresh.CollectionBrief == nil || fresh.CollectionBrief.ScanID != run.ScanID {
			return CollectionBrief{}, false, ErrConflict
		}
		return *fresh.CollectionBrief, true, nil
	}
	return brief, false, nil
}
