package personalassistant

import (
	"errors"
	"testing"
)

func TestHQFirstVisitOfferedOnceAndFailureDoesNotFailBuild(t *testing.T) {
	for _, offerErr := range []error{nil, errors.New("offer unavailable")} {
		store, _ := newTestStore(t)
		fixture := newHQFixture(t, store)
		fixture.hq.firstVisitErr = offerErr
		for attempt := 0; attempt < 2; attempt++ {
			result, err := fixture.coordinator.Setup(t.Context(), "local", fixture.request())
			if err != nil || result.State.Status != StatusActive {
				t.Fatalf("attempt %d: %+v, %v", attempt, result, err)
			}
		}
		if fixture.hq.firstVisitCalls != 1 {
			t.Fatalf("offer calls = %d; replay must not reoffer", fixture.hq.firstVisitCalls)
		}
	}
}
