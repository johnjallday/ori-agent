package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// RevisionDiscussion preserves a canonical question without replacing exact
// typed intent. It is never executable and blocks review until resolved.
type RevisionDiscussion struct {
	Goal     string    `json:"goal"`
	Answer   string    `json:"answer"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Revision int64     `json:"revision"`
	At       time.Time `json:"at"`
}

func validRevisionDiscussion(saved Arrangement) bool {
	d := saved.RevisionDiscussion
	return d == nil || (saved.outcomeKind() == "proposal" && saved.CollectionReceipt == nil &&
		(saved.Record.State == "needs_more_evidence" || saved.Record.State == "cancelled") && d.Revision >= 2 && d.Revision == saved.Record.Revision &&
		workspacearrangement.ValidGoal(d.Goal) && validClarification(d.Answer) &&
		validText(d.Provider, 80) && d.Provider != "" && validText(d.Model, 512) && d.Model != "" &&
		!d.At.Before(saved.Record.CreatedAt) && !d.At.After(saved.Record.UpdatedAt))
}

// saveArrangementRevision is a private completion boundary. Callers must use a
// host-created, evidence-bound model record; this is not a browser JSON writer.
// Both catalog and proposal revision are fenced. Discussion retains prior exact
// intent; neither outcome changes collections or retains old approval.
func (s *Store) saveArrangementRevision(ctx context.Context, owner string, authority ManagerAuthority, expected, baseRevision int64, id string, record workspacearrangement.Record, answer string) (Arrangement, error) {
	fail := Arrangement{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fail, ErrCorrupt
	}
	candidate, err := workspacearrangement.DecodeRecord(raw)
	if err != nil || candidate.Origin != "model" || candidate.Revision != 1 || candidate.SourceKind != workspacearrangement.HomeLibrary || candidate.SourceID != authority.HomeID || (candidate.State != "draft" && candidate.State != "needs_more_evidence") {
		return fail, ErrCorrupt
	}
	if answer != "" && !(Arrangement{Record: candidate, Kind: arrangementClarification, Clarification: answer}).validOutcome() {
		return fail, ErrCorrupt
	}
	scope, err := s.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return fail, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return fail, err
	}
	index := arrangementIndex(doc, id)
	if doc.Revision != expected || index < 0 || baseRevision < 1 {
		return fail, ErrConflict
	}
	prior := doc.Arrangements[index]
	pin := arrangementBinding(state, authority)
	if prior.Record.Revision != baseRevision || prior.outcomeKind() != "proposal" || prior.CollectionReceipt != nil || prior.BindingDigest != pin || prior.ManagerInstanceID != authority.AgentInstanceID || prior.ManagerName != authority.AgentName || candidate.UpdatedAt.Before(prior.Record.UpdatedAt) {
		return fail, ErrConflict
	}
	// Do not invalidate another proposal's live review as a side effect of revision.
	for _, other := range doc.Arrangements {
		if other.Record.ID != id && other.Record.State == "awaiting_user_review" && other.CatalogRevision == doc.Revision {
			return fail, ErrConflict
		}
	}
	evidence, err := workspacearrangement.NewEvidence(ctx, func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return s.ArrangementEvidence(ctx, owner, authority)
	})
	if err != nil {
		return fail, err
	}
	if _, err := evidence.RebindDraft(ctx, prior.Record); err != nil {
		return fail, err
	}
	if err := evidence.ValidateRecord(ctx, candidate); err != nil {
		return fail, err
	}
	encoded, err := json.Marshal(struct {
		ID     string
		Base   int64
		Record workspacearrangement.Record
		Answer string
	}{id, baseRevision, candidate, answer})
	if err != nil {
		return fail, ErrCorrupt
	}
	sum := sha256.Sum256(encoded)
	var result Arrangement
	_, replay, err := s.mutateWithHomePolicy(scope, expected, operation{key: candidate.ID, action: "revise_arrangement", digest: hex.EncodeToString(sum[:])},
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(state, home, authority) && arrangementBinding(state, authority) == pin
		},
		func(current *Document) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n := arrangementIndex(*current, id)
			if n < 0 || current.Arrangements[n].Record.Revision != baseRevision {
				return "", ErrConflict
			}
			result = current.Arrangements[n]
			if answer != "" {
				result.Record.Revision++
				result.Record.State = "needs_more_evidence"
				result.Record.UpdatedAt = candidate.UpdatedAt
				result.RevisionDiscussion = &RevisionDiscussion{Goal: candidate.Draft.Goal, Answer: answer, Provider: candidate.Provider, Model: candidate.Model, Revision: result.Record.Revision, At: candidate.UpdatedAt}
			} else {
				candidate.ID = id
				candidate.Revision = baseRevision + 1
				candidate.CreatedAt = result.Record.CreatedAt
				result.RevisionDiff = arrangementRevisionDiff(result.Record, candidate)
				result.Record = candidate
				result.RevisionDiscussion = nil
				result.CollectionCorrection = nil
			}
			result.CatalogRevision = current.Revision + 1
			if result.Record.Validate() != nil || !validRevisionDiscussion(result) {
				return "", ErrCorrupt
			}
			if _, err := arrangementHistory(result, -1); err != nil {
				return "", err
			}
			current.Arrangements[n] = result
			return id, nil
		})
	if err != nil {
		return fail, err
	}
	if replay {
		latest, err := s.Read(scope)
		if err != nil {
			return fail, err
		}
		n := arrangementIndex(latest, id)
		if n < 0 {
			return fail, ErrConflict
		}
		return latest.Arrangements[n], nil
	}
	return result, nil
}
