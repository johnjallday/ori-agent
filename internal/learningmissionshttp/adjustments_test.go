package learningmissionshttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
)

func TestAdjustmentRouteIsStrictOwnerMutationAndNeverRewritesOriginal(t *testing.T) {
	f := newFixture(t)
	w := request(f, http.MethodPost, "/api/learning-missions/proposals", `{"operation_id":"offer","expected_revision":0,"studio_id":"workspace"}`)
	var offered learningmissions.Outcome
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &offered) != nil || offered.Instance == nil {
		t.Fatal(w.Body.String())
	}
	path := "/api/learning-missions/instances/" + offered.Instance.ID + "/alternatives"
	for _, field := range []string{`"input_limit":100`, `"replaces_id":"foreign"`, `"owner_id":"foreign"`, `"complete":true`, `"personalize":true`, `"steps":[]`} {
		w = request(f, http.MethodPost, path, `{"operation_id":"smaller","expected_revision":1,"instance_revision":1,"kind":"smaller",`+field+`}`)
		if w.Code != 400 {
			t.Fatalf("forged contract accepted: %s %s", field, w.Body.String())
		}
	}
	body := `{"operation_id":"smaller","expected_revision":1,"instance_revision":1,"kind":"smaller"}`
	w = request(f, http.MethodPost, path, body)
	var smaller learningmissions.Outcome
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &smaller) != nil || smaller.Instance == nil || smaller.Instance.ID == offered.Instance.ID || smaller.Instance.Definition.InputLimit != learningmissions.MaxDraftBytes/2 {
		t.Fatal(w.Body.String())
	}
	w = request(f, http.MethodPost, path, body)
	var replay learningmissions.Outcome
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &replay) != nil || replay.Instance.ID != smaller.Instance.ID {
		t.Fatal("uncertain response replayed adjustment", w.Body.String())
	}
	var definition string
	if err := f.db.QueryRowContext(t.Context(), `SELECT definition FROM learning_instances WHERE id=?`, offered.Instance.ID).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	var original learningmissions.Definition
	if json.Unmarshal([]byte(definition), &original) != nil || original.InputLimit != 0 || original.Adjustment != "" {
		t.Fatal("original definition rewritten")
	}
}
