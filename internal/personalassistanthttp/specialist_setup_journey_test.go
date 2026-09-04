package personalassistanthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type specialistStateReader struct{ projection *personalassistant.Projection }

func (r *specialistStateReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	copy := *r.projection
	return &copy, nil
}

type specialistAnswerStub struct{ reader *specialistStateReader }

func (s specialistAnswerStub) Answer(_ context.Context, _ string, request personalassistant.SpecialistOfferRequest) (*personalassistant.State, error) {
	if request.Decision == string(personalassistant.SpecialistOfferAccepted) {
		s.reader.projection.SpecialistOffer = personalassistant.SpecialistOfferAccepted
		s.reader.projection.SpecialistSlug = request.Slug
	} else {
		s.reader.projection.SpecialistOffer = personalassistant.SpecialistOfferDeclined
		s.reader.projection.SpecialistSlug = ""
	}
	s.reader.projection.StateVersion++
	return &personalassistant.State{}, nil
}

func TestSpecialistAcceptanceAutoOpenFlagExistsOnlyOnNewSettledTransition(t *testing.T) {
	cases := []struct {
		name     string
		before   personalassistant.SpecialistOfferState
		decision string
		wantOpen bool
	}{
		{name: "new acceptance", before: personalassistant.SpecialistOfferUnanswered, decision: "accepted", wantOpen: true},
		{name: "accepted replay", before: personalassistant.SpecialistOfferAccepted, decision: "accepted", wantOpen: false},
		{name: "decline", before: personalassistant.SpecialistOfferUnanswered, decision: "declined", wantOpen: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reader := &specialistStateReader{projection: &personalassistant.Projection{
				State: personalassistant.APIStateActive, StateVersion: 4,
				SpecialistOffer: test.before, SpecialistSlug: map[bool]string{true: "music_production"}[test.before == personalassistant.SpecialistOfferAccepted],
			}}
			handler := &Handler{service: reader, specialistOffers: specialistAnswerStub{reader: reader}, provider: userprofile.LocalUserProvider{}}
			body, _ := json.Marshal(map[string]any{
				"if_version": 4, "decision": test.decision,
				"slug": map[bool]string{true: "music_production"}[test.decision == "accepted"],
			})
			request := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/specialist", bytes.NewReader(body))
			recorder := httptest.NewRecorder()
			handler.AnswerSpecialistOffer(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Open bool `json:"open_setup_journey"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Open != test.wantOpen {
				t.Fatalf("open_setup_journey = %t body=%s", response.Open, recorder.Body.String())
			}
		})
	}
}
