package settingsreset

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestRecoveryHandlerServesOnlyDurableReadOnlyStatus(t *testing.T) {
	_, _, coordinator, _ := coordinatorFixture(t)
	preview, request := coordinatorRequest(t, coordinator, CategorySetupSteps)
	if _, err := coordinator.Stage(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	data, err := coordinator.lease.Read(resetstate.OperationRecord)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := decodeJournal(data)
	if err != nil {
		t.Fatal(err)
	}
	journal.Operation.State = StateBlocked
	journal.Operation.Revision++
	journal.Operation.Blockers = []Blocker{{Code: "recovery_unavailable", Message: `<script>alert("unsafe")</script>`, Recovery: "Fully relaunch."}}
	data, err = encodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.lease.Replace(resetstate.OperationRecord, data); err != nil {
		t.Fatal(err)
	}

	handler := NewRecoveryHandler(coordinator.lease)
	statusURL := ResetStatusPath + "?operation=" + preview.OperationID
	for _, route := range []struct{ from, to string }{{"/", ResetStatusPath}, {ResetStatusPath, statusURL}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route.from, nil))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != route.to || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s did not pin to %s: %d %v", route.from, route.to, w.Code, w.Header())
		}
	}
	pageRequest := httptest.NewRequest(http.MethodGet, statusURL, nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), preview.OperationID) {
		t.Fatalf("recovery page = %d %s", pageResponse.Code, pageResponse.Body.String())
	}
	if strings.Contains(pageResponse.Body.String(), `<script>alert("unsafe")</script>`) {
		t.Fatal("recovery page rendered private evidence as executable markup")
	}
	for _, text := range []string{"Reset needs attention", "Fully relaunch.", "&lt;script&gt;", `role="status"`, "Control+C", "Start Server", `name="operation" value="` + preview.OperationID + `"`} {
		if !strings.Contains(pageResponse.Body.String(), text) {
			t.Errorf("recovery page omitted %q", text)
		}
	}
	if pageResponse.Header().Get("Cache-Control") != "no-store" || pageResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("recovery page omitted private-state response headers")
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/reset/recovery", nil)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	var response struct {
		Success   bool      `json:"success"`
		Operation Operation `json:"operation"`
	}
	if err := json.NewDecoder(apiResponse.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if apiResponse.Code != http.StatusOK || response.Success || response.Operation.ID != preview.OperationID || response.Operation.State != StateBlocked {
		t.Fatalf("recovery API = %d %+v", apiResponse.Code, response)
	}

	before, err := coordinator.lease.Read(resetstate.OperationRecord)
	if err != nil {
		t.Fatal(err)
	}
	postRequest := httptest.NewRequest(http.MethodPost, "/api/reset/recovery", bytes.NewReader([]byte(`{}`)))
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, postRequest)
	after, err := coordinator.lease.Read(resetstate.OperationRecord)
	if err != nil {
		t.Fatal(err)
	}
	if postResponse.Code != http.StatusMethodNotAllowed || !bytes.Equal(before, after) {
		t.Fatal("recovery host accepted mutation")
	}

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodHead, statusURL, http.StatusOK},
		{http.MethodPost, statusURL, http.StatusMethodNotAllowed},
		{http.MethodGet, ResetStatusPath + "/next", http.StatusNotFound},
		{http.MethodGet, "/reset/%73tatus", http.StatusNotFound},
		{http.MethodGet, ResetStatusPath + "?operation=" + preview.OperationID + "&operation=other", http.StatusBadRequest},
		{http.MethodGet, ResetStatusPath + "?operation=%3Cscript%3E", http.StatusBadRequest},
		{http.MethodGet, ResetStatusPath + "?operation=another-operation", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), preview.OperationID) {
			t.Errorf("%s %s = %d %v", tc.method, tc.path, w.Code, w.Header())
		}
		if tc.want == http.StatusNotFound && strings.HasSuffix(tc.path, "=another-operation") &&
			(!strings.Contains(w.Body.String(), "Reset status unavailable") || !strings.Contains(w.Body.String(), "another-operation")) {
			t.Fatal("unknown explicit ID did not get recoverable unavailable page")
		}
	}
}
