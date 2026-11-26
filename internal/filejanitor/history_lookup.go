package filejanitor

import (
	"context"
	"sort"
	"strings"
	"time"
)

// FilingHistoryMatch is a historical event, not a locator or an Undo handle.
// History and chat render the same journal-derived account; neither needs a
// fingerprint, a directory reference, an approval, or access to the disk filed into.
type FilingHistoryMatch struct {
	Name    string    `json:"name"`
	When    time.Time `json:"when"`
	Outcome string    `json:"outcome"`
}

type FilingHistoryLookup struct {
	Matches []FilingHistoryMatch `json:"matches"`
	Total   int                  `json:"total"`
	Note    string               `json:"note"`
}

const historyMatchLimit = 30

// LookupFilingHistory searches the entire retained journal before bounding the
// response. Words need not include the punctuation separating them in the name.
func (s *Service) LookupFilingHistory(ctx context.Context, owner, workspaceID, query string) (FilingHistoryLookup, error) {
	var empty FilingHistoryLookup
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 512 {
		return empty, ErrInvalidAction
	}
	if s == nil || s.store == nil || ctx.Err() != nil {
		return empty, ErrReviewUnavailable
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil || ws.ID != workspaceID || owner == "" || !ownedBy(ws, owner) {
		return empty, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return empty, ErrReviewUnavailable
	}
	words := strings.Fields(strings.ToLower(query))
	out := FilingHistoryLookup{Matches: []FilingHistoryMatch{}, Note: "Retained Filing history only. This does not check where a file is now."}
	for i := len(state.Actions) - 1; i >= 0; i-- {
		a := state.Actions[i]
		if a.WorkspaceID != workspaceID || a.ApprovedBy != owner || a.Operation == OperationTrash || !a.Result.Terminal() || a.CompletedAt.IsZero() {
			continue
		}
		name := strings.ToLower(a.SourceName)
		matched := true
		for _, w := range words {
			if !strings.Contains(name, w) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		var outcome string
		switch a.Result {
		case ResultStale, ResultFailed:
			outcome = "No verified filing from this attempt."
		case ResultApplied:
			switch a.Operation {
			case OperationMove, OperationPlace, OperationDeliver:
				if validateDestinationRelative(a.DestinationRelative) != nil {
					continue
				}
				outcome = "Filed to " + DisplayFileName(a.DestinationRelative) + "."
			case OperationSend:
				if a.Send == nil {
					continue
				}
				outcome = "Sent to another workspace."
				if receiver, e := s.readWorkspace(a.Send.ReceiverWorkspaceID); e == nil && receiver != nil && ownedBy(receiver, owner) {
					outcome = "Sent to workspace: " + DisplayFileName(receiver.Name) + "."
				}
			default:
				continue
			}
			switch a.Undo {
			case UndoDone:
				outcome += " Undo verified: put back in the original folder."
			case UndoFailed:
				outcome += " Undo failed; no return was verified."
			case UndoInProgress:
				outcome += " Undo started; no return has been verified."
			}
		}
		out.Matches = append(out.Matches, FilingHistoryMatch{Name: DisplayFileName(a.SourceName), When: a.CompletedAt.UTC(), Outcome: outcome})
	}
	sort.SliceStable(out.Matches, func(i, j int) bool { return out.Matches[i].When.After(out.Matches[j].When) })
	out.Total = len(out.Matches)
	out.Matches = out.Matches[:min(historyMatchLimit, out.Total)]
	if out.Total == 0 {
		out.Note = "No matching filing record in retained history. Older records may no longer be available."
	} else if out.Total > len(out.Matches) {
		out.Note += " Showing the newest matches; narrow the filename to see others."
	}
	return out, nil
}
