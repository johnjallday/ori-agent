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
	pageRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), preview.OperationID) {
		t.Fatalf("recovery page = %d %s", pageResponse.Code, pageResponse.Body.String())
	}
	if strings.Contains(pageResponse.Body.String(), `<script>alert("unsafe")</script>`) {
		t.Fatal("recovery page rendered private evidence as executable markup")
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
}
