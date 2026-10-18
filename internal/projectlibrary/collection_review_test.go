package projectlibrary

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func collectionReviewFixture(t *testing.T) (*runFixture, ManagerAuthority, Arrangement, ArrangementRuntimeResolver) {
	t.Helper()
	f, authority, record := homeArrangementFixture(t)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	resolve := ArrangementRuntimeResolver(func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, SkillDigest: record.SkillDigest, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
	})
	return f, authority, saved, resolve
}

func TestCollectionReview_ConfirmationAtomicReceiptReplayAndRestart(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	before := f.doc(t)
	review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, before.Revision, saved.Record.Revision, resolve)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := f.doc(t)
	if len(reviewed.Collections) != 0 || reviewed.Arrangements[0].Record.State != "awaiting_user_review" || !reflect.DeepEqual(before.Entries, reviewed.Entries) {
		t.Fatal("review performed effects")
	}
	if _, _, err := f.library.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "apply-once", false, resolve); err == nil {
		t.Fatal("implicit confirmation accepted")
	}
	receipt, replay, err := f.library.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "apply-once", true, resolve)
	if err != nil || replay {
		t.Fatal(receipt, replay, err)
	}
	after := f.doc(t)
	if len(after.Collections) != 1 || after.Arrangements[0].Record.State != "completed" || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Roots, after.Roots) {
		t.Fatal("wrong or extra effects")
	}
	if receipt.Effects[0].CollectionID != after.Collections[0].ID {
		t.Fatal("receipt did not name canonical consequence")
	}
	restarted := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	again, replay, err := restarted.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "apply-once", true, resolve)
	if err != nil || !replay || !reflect.DeepEqual(receipt, again) || !reflect.DeepEqual(after, f.doc(t)) {
		t.Fatal("replay wrote or invented a receipt", err)
	}
	if _, _, err := restarted.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "another-key", true, resolve); err == nil {
		t.Fatal("consumed approval reused")
	}
}

func TestCollectionReview_FailedPersistenceReturnsNoReceiptOrEffects(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, saved.Record.Revision, resolve)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	failed := &failActivationHomeWrite{Store: f.file, homeID: f.scope.HomeID, pending: true}
	store := NewStore(failed).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	receipt, replay, err := store.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "retryable", true, resolve)
	if err == nil || replay || receipt.Digest != "" || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("failed write exposed a receipt or effects", err)
	}
	if _, _, err := store.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "retryable", true, resolve); err != nil {
		t.Fatal("explicit retry after failed persistence", err)
	}
}

func TestCollectionReview_LifecycleStatesRequireCanonicalWitnesses(t *testing.T) {
	f, authority, saved, _ := collectionReviewFixture(t)
	doc := f.doc(t)
	for _, state := range []string{"awaiting_user_review", "completed"} {
		doc.Arrangements[0].Record.State = state
		if doc.valid(f.scope) {
			t.Fatal("lifecycle state accepted without witness", state)
		}
		record := saved.Record
		record.State = state
		if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, false, record); err == nil {
			t.Fatal("initial save accepted a host lifecycle transition", state)
		}
	}
}

func TestCollectionReview_ConcurrentSameAndDifferentKeys(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-key-%v", sameKey), func(t *testing.T) {
			f, authority, saved, resolve := collectionReviewFixture(t)
			review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, saved.Record.Revision, resolve)
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					<-start
					key := "same-key"
					if !sameKey {
						key = fmt.Sprintf("key-%d", i)
					}
					_, _, err := f.library.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, key, true, resolve)
					results <- err
				}(i)
			}
			close(start)
			successes := 0
			for i := 0; i < 2; i++ {
				if err := <-results; err == nil {
					successes++
				} else if !errors.Is(err, ErrConflict) {
					t.Fatal(err)
				}
			}
			expected := 1
			if sameKey {
				expected = 2
			}
			if successes != expected || len(f.doc(t).Collections) != 1 {
				t.Fatal("duplicate effects or failed canonical replay", successes)
			}
		})
	}
}

func TestCollectionReview_StaleAndForeignApprovalCannotWrite(t *testing.T) {
	for _, failure := range []string{"foreign", "token", "runtime", "expiry", "catalog", "provider", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			f, authority, saved, resolve := collectionReviewFixture(t)
			review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, saved.Record.Revision, resolve)
			if err != nil {
				t.Fatal(err)
			}
			owner, token := f.scope.OwnerUserID, review.Token
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "foreign":
				owner = "foreign"
			case "token":
				token = "invented-token"
			case "runtime":
				resolve = func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
					return ArrangementRuntime{}, ErrUnavailable
				}
			case "expiry":
				f.library.now = func() time.Time { return review.ExpiresAt }
			case "catalog":
				_, _, err = f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "intervening", action: "test", digest: "test"}, func(*Document) (string, error) { return "intervening", nil })
				if err != nil {
					t.Fatal(err)
				}
			case "provider":
				f.available.Store(false)
			case "cancelled":
				cancel()
			}
			before := f.doc(t)
			if _, _, err := f.library.commitCollectionChanges(ctx, owner, authority, saved.Record.ID, token, "apply", true, resolve); err == nil {
				t.Fatal("unsafe commit accepted")
			}
			if !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("failed confirmation changed state")
			}
		})
	}
}

func TestCollectionReview_RemovalKeepsHistoryReadableAndForgetCannotRestoreMembership(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	apply := func(saved Arrangement, key string) CollectionCommitReceipt {
		t.Helper()
		review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, saved.Record.Revision, resolve)
		if err != nil {
			t.Fatal(err)
		}
		receipt, _, err := f.library.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, key, true, resolve)
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	apply(saved, "create")
	doc := f.doc(t)
	collection := doc.Collections[0]
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := evidence.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record, err := evidence.NewRecord(t.Context(), workspacearrangement.Draft{Version: 1, SourceRevision: overview.Revision, Goal: "Remove only the logical collection", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.RemoveCollection, CollectionID: collection.ID, CollectionRevision: collection.Revision}}}, workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	removal, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, doc.Revision, false, record)
	if err != nil {
		t.Fatal(err)
	}
	// A separate unresolved revision can still reference the collection being
	// removed. Removal must stale it without leaving an invalid question witness.
	pending := removal
	pending.Record.ID = newID()
	pending.Record.Revision = 2
	pending.Record.State = "needs_more_evidence"
	pending.RevisionDiscussion = &RevisionDiscussion{Goal: "Discuss this collection", Answer: "Which client?", Provider: "openai", Model: "fixture", Revision: 2, At: pending.Record.UpdatedAt}
	_, _, err = f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "pending-question-fixture", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		pending.CatalogRevision = current.Revision + 1
		current.Arrangements = append(current.Arrangements, pending)
		current.Arrangements[arrangementIndex(*current, removal.Record.ID)].CatalogRevision = current.Revision + 1
		return pending.Record.ID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	apply(removal, "remove")
	after := f.doc(t)
	if len(after.Collections) != 0 || !reflect.DeepEqual(doc.Entries, after.Entries) {
		t.Fatal("removal changed catalog entries")
	}
	for _, item := range after.Arrangements {
		if item.Record.ID == pending.Record.ID && (item.Record.State != "stale" || item.RevisionDiscussion != nil) {
			t.Fatal("removed collection left a live question")
		}
		if _, err := arrangementHistory(item, after.Revision); err != nil {
			t.Fatal("removal broke history", err)
		}
	}
	// Revoke fixture sources before the existing explicit Forget gate is usable.
	at := f.library.now().UTC()
	_, _, err = f.library.mutate(f.scope, after.Revision, operation{key: "revoke-fixture", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		for i := range current.Roots {
			current.Roots[i].RevokedAt = &at
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after = f.doc(t)
	// The existing Forget CAS redacts all intent while keeping opaque receipts.
	forget, err := f.library.ReviewForget(f.scope, after.Entries[0].ID, after.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.library.CommitForget(f.scope, after.Entries[0].ID, forget.Token, "forget"); err != nil {
		t.Fatal(err)
	}
	redacted := f.doc(t)
	if redacted.Arrangements[0].CollectionReceipt == nil || len(redacted.Arrangements[0].Record.ItemRevisions) != 0 {
		t.Fatal("receipt lost or membership retained")
	}
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, redacted.Revision, false, record); err != nil && !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if len(f.doc(t).Collections) != 0 {
		t.Fatal("replay resurrected collection")
	}
}
