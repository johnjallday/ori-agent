package userprofile

import (
	"errors"
	"testing"
)

func TestHQFirstVisitParsing(t *testing.T) {
	for _, value := range []string{"", "pending", "done", " pending "} {
		if _, ok := ParseHQFirstVisitState(value); !ok {
			t.Fatalf("rejected %q", value)
		}
	}
	if _, ok := ParseHQFirstVisitState("completed"); ok {
		t.Fatal("accepted unknown state")
	}
	if got := NormalizeHQFirstVisitState("unknown"); got != HQFirstVisitUnoffered {
		t.Fatalf("normalize = %q", got)
	}
}

func TestHQFirstVisitTransitions(t *testing.T) {
	for _, initial := range []HQFirstVisitState{HQFirstVisitUnoffered, HQFirstVisitPending, HQFirstVisitDone} {
		for _, finish := range []bool{false, true} {
			t.Run(string(initial)+map[bool]string{false: "/offer", true: "/finish"}[finish], func(t *testing.T) {
				store := newTestStore(t)
				ctx := t.Context()
				if _, err := store.db.ExecContext(ctx, `UPDATE users SET hq_first_visit_state = ?, hq_onboarding_state = 'completed' WHERE id = 'local'`, initial); err != nil {
					t.Fatal(err)
				}
				want := initial
				var err error
				if finish {
					err = store.FinishHQFirstVisit(ctx, "local")
					want = HQFirstVisitDone
				} else {
					err = store.OfferHQFirstVisit(ctx, "local")
					if initial == HQFirstVisitUnoffered {
						want = HQFirstVisitPending
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				state, err := store.GetPersonalHQState(ctx, "local")
				if err != nil || state.FirstVisitState != want || state.OnboardingState != HQOnboardingCompleted {
					t.Fatalf("got %+v, %v; want %q with onboarding intact", state, err, want)
				}
			})
		}
	}
}

func TestHQFirstVisitMissingProfileAndGenericWriteProtection(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	state, err := store.GetPersonalHQState(ctx, "new")
	if err != nil || state.FirstVisitState != HQFirstVisitUnoffered {
		t.Fatal(state, err)
	}
	if err := store.OfferHQFirstVisit(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishHQFirstVisit(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[string]HQFirstVisitState{"new": HQFirstVisitPending, "other": HQFirstVisitDone} {
		state, err := store.GetPersonalHQState(ctx, user)
		if err != nil || state.FirstVisitState != want {
			t.Fatal(state, err)
		}
	}
	if _, err := store.SetFields(ctx, "new", map[string]any{"hq_first_visit_state": ""}); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("generic edit accepted: %v", err)
	}
}
