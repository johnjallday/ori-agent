package agenthttp

import (
	"context"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// arrangedOutcomes answers ArrangementOutcome from a fixed table; an offer it
// does not list reads as pruned.
type arrangedOutcomes map[string]personalassistant.FolderArrangementOutcome

func (arrangedOutcomes) ReviewArrangementGroup(context.Context, foldercontext.Target, string, personalassistant.FolderArrangementGroup) (personalassistant.FolderOfferView, bool, error) {
	return personalassistant.FolderOfferView{}, false, personalassistant.ErrFolderSelection
}

func (o arrangedOutcomes) ArrangementOutcome(_ context.Context, _ foldercontext.Target, offerID string) (personalassistant.FolderArrangementOutcome, error) {
	outcome, ok := o[offerID]
	if !ok {
		return personalassistant.FolderArrangementOutcome{}, personalassistant.ErrFolderOfferNotFound
	}
	return outcome, nil
}

func (o arrangedOutcomes) ConversationOfferStatus(_ context.Context, _ foldercontext.Target, offerID string) (personalassistant.FolderOfferStatus, error) {
	outcome, ok := o[offerID]
	if !ok {
		return "", personalassistant.ErrFolderOfferNotFound
	}
	return outcome.Status, nil
}

// A decline closes only reviews it can read back as open or closed. A review
// that was pruned, rebound elsewhere, or confirmed without the journal hearing
// of it is never declined as "not applied".
func TestArrangementDeclineReviews_RefusesWhatItCannotReadBack(t *testing.T) {
	saved := func(states ...string) *PersonalAssistantSavedArrangement {
		out := &PersonalAssistantSavedArrangement{Record: workspacearrangement.Record{ID: "record"}}
		for i, state := range states {
			out.Effects = append(out.Effects, PersonalAssistantArrangementEffect{Operation: i, OfferID: "offer-" + string(rune('a'+i)), State: state})
		}
		return out
	}
	bound := func(operation int, status personalassistant.FolderOfferStatus) personalassistant.FolderArrangementOutcome {
		return personalassistant.FolderArrangementOutcome{Binding: personalassistant.FolderStructureArrangement{RecordID: "record", RecordRevision: 1, Operation: operation}, Status: status}
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		saved    *PersonalAssistantSavedArrangement
		outcomes arrangedOutcomes
		open     []string
		refusal  string
	}{
		"open and closed reviews": {
			saved:    saved(arrangementEffectAwaiting, arrangementEffectAwaiting, arrangementEffectClosed),
			outcomes: arrangedOutcomes{"offer-a": bound(0, personalassistant.FolderOfferPending), "offer-b": bound(1, personalassistant.FolderOfferClosed)},
			open:     []string{"offer-a"},
		},
		"a recorded confirmation": {
			saved:   saved(arrangementEffectAwaiting, arrangementEffectPartial),
			refusal: "arrangement_effects_applied",
		},
		"a pruned review": {
			saved:    saved(arrangementEffectAwaiting),
			outcomes: arrangedOutcomes{},
			refusal:  "arrangement_effects_unverified",
		},
		"a review bound to another intention": {
			saved:    saved(arrangementEffectAwaiting),
			outcomes: arrangedOutcomes{"offer-a": bound(3, personalassistant.FolderOfferPending)},
			refusal:  "arrangement_effects_unverified",
		},
		"a confirmation the journal missed": {
			saved:    saved(arrangementEffectAwaiting),
			outcomes: arrangedOutcomes{"offer-a": bound(0, personalassistant.FolderOfferAwaitingOutcome)},
			refusal:  "arrangement_effects_applied",
		},
		"a finished group the journal missed": {
			saved:    saved(arrangementEffectAwaiting),
			outcomes: arrangedOutcomes{"offer-a": bound(0, personalassistant.FolderOfferResolved)},
			refusal:  "arrangement_effects_applied",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var setups personalAssistantArrangementSetups
			if tc.outcomes != nil {
				setups = tc.outcomes
			}
			open, refusal := arrangementDeclineReviews(ctx, setups, foldercontext.Target{}, tc.saved)
			if refusal != tc.refusal || (tc.refusal == "" && !slices.Equal(open, tc.open)) {
				t.Fatalf("open %v, refusal %q; want %v, %q", open, refusal, tc.open, tc.refusal)
			}
		})
	}
	// Without a way to read reviews back, an awaiting review is never declined.
	if _, refusal := arrangementDeclineReviews(ctx, nil, foldercontext.Target{}, saved(arrangementEffectAwaiting)); refusal != "arrangement_effects_unverified" {
		t.Fatalf("no reader: %q", refusal)
	}
}
