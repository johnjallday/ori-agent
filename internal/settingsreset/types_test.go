package settingsreset

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCountFactDistinguishesUnavailableFromZero(t *testing.T) {
	zero := int64(0)
	for _, tc := range []struct {
		fact CountFact
		want string
	}{
		{CountFact{Name: "sessions", Count: nil, UnavailableReason: "store unavailable"}, `"count":null`},
		{CountFact{Name: "sessions", Count: &zero}, `"count":0`},
	} {
		data, err := json.Marshal(tc.fact)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), tc.want) {
			t.Fatalf("count encoding %s must contain %s", data, tc.want)
		}
	}
}

func TestExecuteRequestCarriesOnlyReviewedPlanReference(t *testing.T) {
	body, err := json.Marshal(ExecuteRequest{PreviewID: "preview", RequestID: "request", Confirmation: "RESET"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["preview_id"] == nil || fields["request_id"] == nil || fields["confirmation"] == nil {
		t.Fatal("execution body unexpectedly carries scope or targets")
	}
}

func TestOperationRoundTripKeepsPartialAndUnverifiedOutcomes(t *testing.T) {
	operation := Operation{
		SchemaVersion: SchemaVersion, ID: "operation", Intent: IntentSelectedData,
		State: StatePartialFailure, Revision: 2,
		Restart: RestartInfo{Mode: RestartProcessRelaunch},
		Results: []CategoryResult{
			{ID: CategorySettings, Outcome: OutcomeCompleted},
			{ID: CategoryAgents, Outcome: OutcomeFailed, Retryable: true},
			{ID: CategoryAppRecords, Outcome: OutcomeUnknown},
		},
	}
	data, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	var got Operation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != StatePartialFailure || got.Restart.Mode != RestartProcessRelaunch || len(got.Results) != 3 ||
		got.Results[0].Outcome != OutcomeCompleted || got.Results[1].Outcome != OutcomeFailed ||
		!got.Results[1].Retryable || got.Results[2].Outcome != OutcomeUnknown {
		t.Fatal("mixed outcomes were collapsed by the response contract")
	}
}
