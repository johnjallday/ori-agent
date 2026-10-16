package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const collectionReviewAction = "apply_collection_arrangement"

// CollectionReviewWitness contains no copied names or membership. The exact
// effects are recomputed from canonical intent at the same preparation time.
// A token is usable only through its owning Home's canonical review receipt.
type CollectionReviewWitness struct {
	ArrangementRevision int64     `json:"arrangement_revision"`
	BindingDigest       string    `json:"binding_digest"`
	PreparedAt          time.Time `json:"prepared_at"`
}

// CollectionCommitReceipt is metadata-only history, never an approval token.
// It deliberately stores neither entry labels nor membership copies, so Forget
// cannot resurrect an entry through a second historical projection.
type CollectionCommitReceipt struct {
	Digest          string             `json:"digest"`
	CatalogRevision int64              `json:"catalog_revision"`
	AppliedAt       time.Time          `json:"applied_at"`
	Effects         []collectionEffect `json:"effects"`
}

func collectionChangesDigest(changes collectionChanges) (string, error) {
	raw, err := json.Marshal(changes)
	if err != nil {
		return "", ErrCorrupt
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func arrangementIndex(doc Document, id string) int {
	for i := range doc.Arrangements {
		if doc.Arrangements[i].Record.ID == id {
			return i
		}
	}
	return -1
}

// checkCollectionRuntime does not ask a model or materialize general/native
// tools. Model-authored intent must still match its exact disclosed runtime.
func checkCollectionRuntime(ctx context.Context, authority ManagerAuthority, saved Arrangement, resolve ArrangementRuntimeResolver) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if saved.Record.Origin != "model" {
		return nil
	}
	if resolve == nil {
		return ErrUnavailable
	}
	runtime, err := resolve(ctx, authority)
	if err != nil {
		return err
	}
	if runtime.Revalidate == nil || runtime.Provider == nil || runtime.Provider.Name() != saved.Record.Provider ||
		runtime.Model != saved.Record.Model || runtime.ProviderFingerprint != saved.Record.ProviderFingerprint || runtime.SkillDigest != saved.Record.SkillDigest {
		return ErrConflict
	}
	return runtime.Revalidate(ctx)
}

func (s *Store) authorizeCollectionOwner(owner string, authority ManagerAuthority) (Scope, error) {
	if s == nil || s.workspaces == nil || owner == "" || !collectionLabel(authority.HomeID, 160) {
		return Scope{}, ErrUnavailable
	}
	home, err := s.workspaces.Get(authority.HomeID)
	if err != nil || home == nil || home.OwnerUserID != owner {
		return Scope{}, ErrUnavailable
	}
	scope, err := s.authorizeManager(authority)
	if err != nil || scope.OwnerUserID != owner {
		return Scope{}, ErrUnavailable
	}
	return scope, nil
}

// reviewCollectionChanges is the host-only boundary behind the owner review UI.
// It persists a ten-minute witness, but writes no collections. No model tool
// exposes this transition.
func (s *Store) reviewCollectionChanges(ctx context.Context, owner string, authority ManagerAuthority, id string, catalogRevision, recordRevision int64, resolve ArrangementRuntimeResolver) (ReviewReceipt, error) {
	scope, err := s.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ReviewReceipt{}, err
	}
	i := arrangementIndex(doc, id)
	if i < 0 || doc.Revision != catalogRevision || doc.Arrangements[i].Record.Revision != recordRevision {
		return ReviewReceipt{}, ErrConflict
	}
	saved := doc.Arrangements[i]
	pin := arrangementBinding(state, authority)
	if saved.BindingDigest != pin || saved.ManagerInstanceID != authority.AgentInstanceID || saved.ManagerName != authority.AgentName || saved.CollectionReceipt != nil {
		return ReviewReceipt{}, ErrConflict
	}
	if err := checkCollectionRuntime(ctx, authority, saved, resolve); err != nil {
		return ReviewReceipt{}, err
	}
	at := s.now().UTC()
	changes, err := compileCollectionChanges(doc, id, at)
	if err != nil {
		return ReviewReceipt{}, err
	}
	digest, err := collectionChangesDigest(changes)
	if err != nil {
		return ReviewReceipt{}, err
	}
	review := ReviewReceipt{Token: newID(), Action: collectionReviewAction, TargetID: id, Digest: digest, Revision: doc.Revision + 1, ExpiresAt: at.Add(10 * time.Minute), Collection: &CollectionReviewWitness{ArrangementRevision: recordRevision, BindingDigest: pin, PreparedAt: at}}
	_, _, err = s.mutateWithHomePolicy(scope, doc.Revision, operation{key: review.Token, action: "review_collection_arrangement", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(current, home, authority) && arrangementBinding(current, authority) == pin
		},
		func(current *Document) (string, error) {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			checked, err := compileCollectionChanges(*current, id, at)
			if err != nil {
				return "", err
			}
			actual, err := collectionChangesDigest(checked)
			if err != nil || actual != digest {
				return "", ErrConflict
			}
			n := arrangementIndex(*current, id)
			current.Arrangements[n].Record.State = "awaiting_user_review"
			current.Arrangements[n].Record.UpdatedAt = at
			current.Arrangements[n].CatalogRevision = current.Revision + 1
			current.Reviews = append(current.Reviews, review)
			if _, err := arrangementHistory(current.Arrangements[n], -1); err != nil {
				return "", err
			}
			return id, nil
		})
	if err != nil {
		return ReviewReceipt{}, err
	}
	return review, nil
}

// commitCollectionChanges requires an explicit host confirmation. It applies
// the entire exact metadata result, optional activation-review queue, and receipt
// in one canonical Home CAS. No workspace creator, source file or link is touched;
// every queued project still requires its independent activation review.
func (s *Store) commitCollectionChanges(ctx context.Context, owner string, authority ManagerAuthority, id, token, key string, confirmed bool, resolve ArrangementRuntimeResolver) (CollectionCommitReceipt, bool, error) {
	fail := CollectionCommitReceipt{}
	if !confirmed || !collectionLabel(id, 160) || !collectionLabel(token, 160) || !collectionLabel(key, 160) || ctx.Err() != nil {
		return fail, false, ErrConflict
	}
	scope, err := s.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return fail, false, ErrUnavailable
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return fail, false, err
	}
	review, ok := findReview(doc, token, collectionReviewAction)
	i := arrangementIndex(doc, id)
	if !ok || i < 0 || review.TargetID != id || review.Collection == nil {
		return fail, false, ErrConflict
	}
	saved := doc.Arrangements[i]
	for _, prior := range doc.Operations {
		if prior.Key != key {
			continue
		}
		if prior.Action != collectionReviewAction || prior.Digest != review.Digest || prior.ConsequenceID != id || saved.CollectionReceipt == nil || saved.CollectionReceipt.Digest != review.Digest {
			return fail, false, ErrConflict
		}
		return *saved.CollectionReceipt, true, nil
	}
	pin := arrangementBinding(state, authority)
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now().UTC()) || review.Revision != doc.Revision ||
		saved.Record.State != "awaiting_user_review" || saved.Record.Revision != review.Collection.ArrangementRevision ||
		saved.BindingDigest != pin || review.Collection.BindingDigest != pin || saved.CollectionReceipt != nil {
		return fail, false, ErrConflict
	}
	if err := checkCollectionRuntime(ctx, authority, saved, resolve); err != nil {
		return fail, false, err
	}
	_, replay, err := s.mutateWithHomePolicy(scope, doc.Revision, operation{key: key, action: collectionReviewAction, digest: review.Digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(current, home, authority) && arrangementBinding(current, authority) == pin
		},
		func(current *Document) (string, error) {
			at := s.now().UTC()
			if ctx.Err() != nil || at.Before(review.Collection.PreparedAt) || !review.ExpiresAt.After(at) {
				return "", ErrConflict
			}
			live, ok := findReview(*current, token, collectionReviewAction)
			n := arrangementIndex(*current, id)
			if !ok || n < 0 || live.TargetID != id || live.Digest != review.Digest || live.Revision != current.Revision || !live.ExpiresAt.Equal(review.ExpiresAt) || !live.ExpiresAt.After(at) || live.ConsumedAt != nil || live.Collection == nil || *live.Collection != *review.Collection ||
				current.Arrangements[n].Record.State != "awaiting_user_review" || current.Arrangements[n].Record.Revision != live.Collection.ArrangementRevision {
				return "", ErrConflict
			}
			changes, err := compileCollectionChanges(*current, id, review.Collection.PreparedAt)
			if err != nil {
				return "", err
			}
			digest, err := collectionChangesDigest(changes)
			if err != nil || digest != review.Digest {
				return "", ErrConflict
			}
			current.Collections = changes.Collections
			if changes.Queue != nil {
				archiveTerminalQueue(current)
				current.Queue = changes.Queue
			}
			for _, effect := range changes.Effects {
				if effect.Kind != "remove_collection" {
					continue
				}
				for j := range current.Arrangements {
					if j == n {
						continue
					}
					r := &current.Arrangements[j].Record
					if _, references := r.CollectionRevisions[effect.CollectionID]; references && r.State != "completed" {
						r.State, r.Revision, r.UpdatedAt = "stale", r.Revision+1, at
						current.Arrangements[j].RevisionDiscussion = nil
					}
				}
			}
			receipt := CollectionCommitReceipt{Digest: digest, CatalogRevision: current.Revision + 1, AppliedAt: at, Effects: changes.Effects}
			current.Arrangements[n].CollectionReceipt = &receipt
			current.Arrangements[n].Record.State = "completed"
			current.Arrangements[n].Record.Revision++
			current.Arrangements[n].Record.UpdatedAt = at
			current.Arrangements[n].CatalogRevision = current.Revision + 1
			if _, err := arrangementHistory(current.Arrangements[n], -1); err != nil {
				return "", err
			}
			for j := range current.Reviews {
				if current.Reviews[j].Token == token {
					current.Reviews[j].ConsumedAt = &at
				}
			}
			return id, nil
		})
	if err != nil {
		return fail, false, err
	}
	// Return the stored receipt, including when a simultaneous identical commit won.
	fresh, err := s.Read(scope)
	if err != nil {
		return fail, replay, err
	}
	n := arrangementIndex(fresh, id)
	if n < 0 || fresh.Arrangements[n].CollectionReceipt == nil || fresh.Arrangements[n].CollectionReceipt.Digest != review.Digest {
		return fail, replay, ErrConflict
	}
	return *fresh.Arrangements[n].CollectionReceipt, replay, nil
}
