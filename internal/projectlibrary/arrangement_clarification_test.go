package projectlibrary

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func homeClarificationFixture(t *testing.T) (*runFixture, ManagerAuthority, workspacearrangement.Record) {
	t.Helper()
	f, authority, _ := homeArrangementFixture(t)
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := evidence.NewClarificationRecord(t.Context(), "Organize by client", workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return f, authority, record
}

func TestHomeClarification_CanonicalCompletionReplayRestartAndForget(t *testing.T) {
	f, authority, record := homeClarificationFixture(t)
	before := f.doc(t)
	answer := "Which projects belong to the same client?\nNo arrangement has been applied."
	if _, _, err := f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, false, record, answer); !errors.Is(err, ErrArrangementUpgradeRequired) {
		t.Fatal("silent upgrade", err)
	}
	if !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("declined upgrade mutated catalog")
	}
	saved, replay, err := f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record, answer)
	if err != nil || replay || saved.Kind != arrangementClarification || saved.Clarification != answer {
		t.Fatal("completion failed", saved, replay, err)
	}
	after := f.doc(t)
	if saved.CatalogRevision != after.Revision || len(after.Arrangements) != 1 || len(after.Collections) != 0 || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Roots, after.Roots) {
		t.Fatal("discussion created effects or stale own-save pins")
	}
	again, replay, err := f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record, answer)
	if err != nil || !replay || !reflect.DeepEqual(saved, again) {
		t.Fatal("lost response replay", err)
	}
	if _, _, err := f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record, "Different question"); !errors.Is(err, ErrConflict) {
		t.Fatal("question overwritten", err)
	}
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record); !errors.Is(err, ErrConflict) {
		t.Fatal("discussion converted into proposal", err)
	}
	restarted := NewStore(f.file)
	history, err := restarted.ReadArrangementHistory(t.Context(), f.scope, record.ID)
	if err != nil || history.Kind != arrangementClarification || history.Clarification != answer || !history.CatalogCurrent || history.Arrangement.ReviewAvailable {
		t.Fatal("restart lost outcome distinction", history, err)
	}
	refs, err := restarted.ArrangementRefs(t.Context(), f.scope)
	if err != nil || len(refs) != 1 || refs[0].Kind != arrangementClarification || refs[0].ReviewAvailable {
		t.Fatal("list claims a proposal", refs, err)
	}
	// Forget is separately reviewed and only available for unavailable sources.
	at := time.Now().UTC()
	if _, _, err := f.library.mutate(f.scope, after.Revision, operation{key: "revoke-clarification-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		for i := range doc.Roots {
			doc.Roots[i].RevokedAt = &at
		}
		return "fixture", nil
	}); err != nil {
		t.Fatal(err)
	}
	after = f.doc(t)
	review, err := f.library.ReviewForget(f.scope, after.Entries[0].ID, after.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.library.CommitForget(f.scope, after.Entries[0].ID, review.Token, "forget-clarification"); err != nil {
		t.Fatal(err)
	}
	history, err = restarted.ReadArrangementHistory(t.Context(), f.scope, record.ID)
	if err != nil || history.Kind != arrangementClarification || history.Clarification != "" || history.Arrangement.State != "stale" {
		t.Fatal("forgotten text recovered", history, err)
	}
	again, replay, err = f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record, answer)
	if err != nil || !replay || again.Clarification != "" || again.Record.State != "stale" {
		t.Fatal("replay resurrected discussion", again, err)
	}
}

func TestHomeClarification_CompetesWithProposalInSameCAS(t *testing.T) {
	f, authority, proposal := homeArrangementFixture(t)
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	question, err := evidence.NewClarificationRecord(t.Context(), proposal.Draft.Goal, workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, proposal)
		results <- err
	}()
	go func() {
		defer wg.Done()
		_, _, err := f.library.SaveArrangementClarification(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, question, "Which client?")
		results <- err
	}()
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	after := f.doc(t)
	if succeeded != 1 || len(after.Arrangements) != 1 || after.Revision != before.Revision+1 {
		t.Fatal("concurrent outcomes bypassed canonical CAS", succeeded)
	}
}

func TestHomeClarification_RejectsInvalidOrUnauthorizedCompletion(t *testing.T) {
	for _, failure := range []string{"empty", "oversize", "escaped_summary", "control", "utf8", "owner", "manager", "provider", "revision", "cancel", "manual", "effects", "references", "draft_state"} {
		t.Run(failure, func(t *testing.T) {
			f, authority, record := homeClarificationFixture(t)
			before := f.doc(t)
			owner, expected, answer := f.scope.OwnerUserID, before.Revision, "Which client?"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "empty":
				answer = " \n"
			case "oversize":
				answer = strings.Repeat("x", workspacearrangement.MaxDraftBytes+1)
			case "escaped_summary":
				answer = strings.Repeat("<", workspacearrangement.MaxDraftBytes)
			case "control":
				answer = "question\x00"
			case "utf8":
				answer = "question\xff"
			case "owner":
				owner = "foreign"
			case "manager":
				authority.AgentInstanceID = "foreign"
			case "provider":
				f.available.Store(false)
			case "revision":
				expected--
			case "cancel":
				cancel()
			case "manual":
				record.Origin = "manual"
			case "effects":
				record.Draft.Operations = []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Not a question"}}
			case "references":
				record.Draft.Unchanged.Handle = "guessed"
			case "draft_state":
				record.State = "draft"
			}
			if _, _, err := f.library.SaveArrangementClarification(ctx, owner, authority, expected, true, record, answer); err == nil {
				t.Fatal("invalid completion accepted")
			}
			if !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("failed completion changed canonical state")
			}
		})
	}
}
