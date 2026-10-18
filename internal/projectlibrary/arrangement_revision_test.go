package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func revisionCandidate(t *testing.T, f *runFixture, authority ManagerAuthority, prior Arrangement, question bool) workspacearrangement.Record {
	t.Helper()
	e, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	p := workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: prior.Record.Provider, Model: prior.Record.Model, ProviderFingerprint: prior.Record.ProviderFingerprint, SkillDigest: prior.Record.SkillDigest}
	if question {
		record, err := e.NewClarificationRecord(t.Context(), "Separate the chosen client projects", p)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	wire, err := e.RebindDraft(t.Context(), prior.Record)
	if err != nil {
		t.Fatal(err)
	}
	wire.Goal = "Separate the chosen client projects"
	wire.Operations[0].Name = "Revised client references"
	raw, _ := json.Marshal(wire)
	draft, err := e.Resolve(t.Context(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.NewRecord(t.Context(), draft, p)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestArrangementRevision_ClarificationPreservesIntentAndRequiresResolution(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	review, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, 1, resolve)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	prior := before.Arrangements[0]
	candidate := revisionCandidate(t, f, authority, prior, true)
	revised, err := f.library.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, before.Revision, 1, saved.Record.ID, candidate, "Which client should remain catalog-only?")
	if err != nil {
		t.Fatal(err)
	}
	if revised.Record.Revision != 2 || revised.RevisionDiscussion == nil || !reflect.DeepEqual(revised.Record.Draft, prior.Record.Draft) || !reflect.DeepEqual(revised.Record.ItemRevisions, prior.Record.ItemRevisions) || len(f.doc(t).Collections) != 0 {
		t.Fatal("clarification discarded or applied intent")
	}
	if _, _, err := f.library.commitCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, review.Token, "obsolete", true, resolve); !errors.Is(err, ErrConflict) {
		t.Fatal("old review survived", err)
	}
	if _, err := f.library.reviewCollectionChanges(t.Context(), f.scope.OwnerUserID, authority, saved.Record.ID, f.doc(t).Revision, 2, resolve); err == nil {
		t.Fatal("unresolved discussion became reviewable")
	}
	// Owner answers: fresh evidence, same canonical ID, next exact revision.
	answer := revisionCandidate(t, f, authority, revised, false)
	resolved, err := f.library.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, 2, saved.Record.ID, answer, "")
	if err != nil || resolved.Record.ID != saved.Record.ID || resolved.Record.Revision != 3 || resolved.RevisionDiscussion != nil || resolved.Record.Draft.Operations[0].Name != "Revised client references" {
		t.Fatal(resolved, err)
	}
	if resolved.RevisionDiff == nil || resolved.RevisionDiff.BaseRevision != 2 || resolved.RevisionDiff.Revision != 3 || resolved.RevisionDiff.ChangedOperations != 1 || resolved.RevisionDiff.AddedMembers != 0 || resolved.RevisionDiff.RemovedMembers != 0 {
		t.Fatal("missing exact compact diff", resolved.RevisionDiff)
	}
	if len(f.doc(t).Arrangements) != 1 || len(f.doc(t).Collections) != 0 {
		t.Fatal("revision created a second proposal or effects")
	}
}

func TestArrangementRevision_LateCompletionCannotOverwriteOwnerCorrection(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	base := f.doc(t)
	candidate := revisionCandidate(t, f, authority, base.Arrangements[0], false)
	name := "Owner correction wins"
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, Name: &name}); err != nil {
		t.Fatal(err)
	}
	current := f.doc(t)
	if _, err := f.library.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, base.Revision, 1, saved.Record.ID, candidate, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("late model reply replaced owner correction", err)
	}
	if !reflect.DeepEqual(current, f.doc(t)) {
		t.Fatal("stale completion changed state")
	}
}

func TestArrangementRevision_ForgetRedactsQuestionAndFailedWritePreservesProposal(t *testing.T) {
	f, authority, saved, _ := collectionReviewFixture(t)
	candidate := revisionCandidate(t, f, authority, saved, true)
	before := f.doc(t)
	failed := &failActivationHomeWrite{Store: f.file, homeID: f.scope.HomeID, pending: true}
	store := NewStore(failed).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	if _, err := store.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, before.Revision, 1, saved.Record.ID, candidate, "Sensitive source question"); err == nil || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("failed save replaced intent", err)
	}
	result, err := store.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, before.Revision, 1, saved.Record.ID, candidate, "Sensitive source question")
	if err != nil {
		t.Fatal(err)
	}
	history, err := f.library.ReadArrangementHistory(t.Context(), f.scope, result.Record.ID)
	if err != nil || history.RevisionDiscussion == nil {
		t.Fatal(err)
	}
	doc := f.doc(t)
	forgetArrangements(&doc, "unused-entry", f.library.now())
	if doc.Arrangements[0].RevisionDiscussion != nil || !doc.valid(f.scope) {
		t.Fatal("Forget retained question or corrupted envelope")
	}
}
