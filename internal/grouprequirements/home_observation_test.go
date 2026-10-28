package grouprequirements

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type observationOnlyHomeReceipts struct {
	ReceiptStore
	writes int
}

func (store *observationOnlyHomeReceipts) SaveReceipt(context.Context, Receipt) error {
	store.writes++
	return ErrUnavailable
}
func (store *observationOnlyHomeReceipts) SaveOperation(context.Context, Operation) error {
	store.writes++
	return ErrUnavailable
}
func (store *observationOnlyHomeReceipts) ConsumeReceipt(context.Context, string, time.Time) error {
	store.writes++
	return ErrUnavailable
}

func TestHomeOperationObservationIsReadOnlyAcrossReconstruction(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var receipts ReceiptStore = NewMemoryStore()
			if backend == "sqlite" {
				db, err := database.Open(t.Context(), &database.Config{InMemory: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				receipts, err = NewSQLiteStore(db)
				if err != nil {
					t.Fatal(err)
				}
			}
			workspaces, err := workspace.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(workspaces, receipts)
			confirmedAt := time.Now().UTC().Add(-time.Hour)
			service.now = func() time.Time { return confirmedAt }
			template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
			input := groupTemplateHomeInput(t, template, "Reviewed Home")
			review, err := service.Review(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := service.Claim(t.Context(), input, review.Token, "exact-home-operation")
			if err != nil || !claim.HomeCreated || !claim.Operation.HomeCreated {
				t.Fatalf("new Home creation evidence = (%#v, %v)", claim, err)
			}
			readOnly := &observationOnlyHomeReceipts{ReceiptStore: receipts}
			// No workspace store and no available evaluation clock: this reader
			// cannot create/re-evaluate a Home or renew the accepted review.
			observer := NewService(nil, readOnly)
			observer.now = func() time.Time { panic("observation evaluated or renewed consent") }
			operation, original, err := observer.ObserveHomeOperation(t.Context(), input.OwnerUserID, review.Token, "exact-home-operation", input.InputDigest)
			if err != nil || operation.Status != OperationHomeReady || !operation.HomeCreated {
				t.Fatalf("partial observation = (%#v, %v)", operation, err)
			}
			stored, err := receipts.GetOperation(t.Context(), input.OwnerUserID, OperationPrepareHome, "exact-home-operation")
			if err != nil || !reflect.DeepEqual(stored, operation) {
				t.Fatalf("reading advanced partial operation: %#v, %v", stored, err)
			}
			if err := service.Mark(t.Context(), claim.Operation, OperationSucceeded); err != nil {
				t.Fatal(err)
			}
			// Expiry does not erase an accepted outcome or grant fresh consent.
			operation, observed, err := observer.ObserveHomeOperation(t.Context(), input.OwnerUserID, review.Token, "exact-home-operation", input.InputDigest)
			if err != nil || operation.Status != OperationSucceeded || !operation.HomeCreated || !reflect.DeepEqual(observed, original) || !observed.ExpiresAt.Before(time.Now()) {
				t.Fatalf("accepted observation = (%#v, %#v, %v)", operation, observed, err)
			}
			if readOnly.writes != 0 {
				t.Fatalf("observation attempted %d journal writes", readOnly.writes)
			}
			ids, err := workspaces.List()
			if err != nil || len(ids) != 1 || ids[0] != operation.HomeWorkspaceID {
				t.Fatalf("observation changed canonical resources: %v, %v", ids, err)
			}

			// A separately confirmed reuse gets no new-resource geometry proof.
			reuseInput := groupTemplateHomeInput(t, template, "Name not applied")
			reuseReview, err := service.Review(t.Context(), reuseInput)
			if err != nil {
				t.Fatal(err)
			}
			reuse, err := service.Claim(t.Context(), reuseInput, reuseReview.Token, "reuse-original-home")
			if err != nil || reuse.HomeCreated || reuse.Operation.HomeCreated || reuse.Operation.HomeWorkspaceID != operation.HomeWorkspaceID {
				t.Fatalf("reuse creation evidence = (%#v, %v)", reuse, err)
			}
			got, _, err := observer.ObserveHomeOperation(t.Context(), input.OwnerUserID, reuseReview.Token, "reuse-original-home", reuseInput.InputDigest)
			if err != nil || got.HomeCreated {
				t.Fatalf("observed reuse acquired geometry authority: %#v, %v", got, err)
			}
		})
	}
}

func TestHomeOperationObservationRejectsChangedOrForeignIdentity(t *testing.T) {
	store := NewMemoryStore()
	digest := strings.Repeat("a", 64)
	reviewDigest := strings.Repeat("b", 64)
	receipt := Receipt{Token: "original-review", OwnerUserID: "owner", OperationKind: OperationPrepareHome, InputDigest: digest, ReviewDigest: reviewDigest}
	operation := Operation{OwnerUserID: "owner", OperationKind: OperationPrepareHome, IdempotencyKey: "original-operation", InputDigest: digest, ReviewDigest: reviewDigest, Status: OperationSucceeded, HomeWorkspaceID: "exact-home", HomeCreated: true}
	if err := store.SaveReceipt(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveOperation(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	observer := NewService(nil, store)
	for _, test := range []struct {
		name, owner, token, key, digest string
		want                            error
	}{
		{"foreign owner", "another-owner", receipt.Token, operation.IdempotencyKey, digest, ErrReviewStale},
		{"changed input", "owner", receipt.Token, operation.IdempotencyKey, strings.Repeat("c", 64), ErrReviewStale},
		{"another review", "owner", "another-review", operation.IdempotencyKey, digest, ErrNotFound},
		{"another operation", "owner", receipt.Token, "another-operation", digest, ErrNotFound},
		{"malformed digest", "owner", receipt.Token, operation.IdempotencyKey, "not-a-digest", ErrReviewRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := observer.ObserveHomeOperation(t.Context(), test.owner, test.token, test.key, test.digest); !errors.Is(err, test.want) {
				t.Fatalf("observation error = %v, want %v", err, test.want)
			}
		})
	}
	for _, mutate := range []func(*Operation){
		func(op *Operation) { op.ReviewDigest = strings.Repeat("d", 64) },
		func(op *Operation) { op.ChildWorkspaceID = "not-home-only" },
		func(op *Operation) { op.ProjectLinkID = "not-home-only" },
	} {
		changed := operation
		mutate(&changed)
		// Deliberately corrupt the backing fixture to verify the reader does
		// not trust the lookup key in place of the stored consequence.
		store.operations[operationKey("owner", OperationPrepareHome, operation.IdempotencyKey)] = changed
		if _, _, err := observer.ObserveHomeOperation(t.Context(), "owner", receipt.Token, operation.IdempotencyKey, digest); !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("changed operation observation = %v", err)
		}
	}
}

func TestLegacyHomeOperationHasNoNewResourceGeometryProof(t *testing.T) {
	var operation Operation
	if err := json.Unmarshal([]byte(`{"owner_user_id":"owner","home_workspace_id":"legacy-home","status":"succeeded"}`), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.HomeCreated {
		t.Fatal("legacy receipt manufactured new-Home geometry authority")
	}
}
