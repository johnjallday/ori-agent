package backuprecoveryhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryReportHTTPUsesExactUserControlsAndRejectsVerificationClaims(t *testing.T) {
	body := `{"generation":"invented","revision":1,"operation_id":"invented","tested_at":"2026-10-18T00:00:00Z","result":"user_reported_passed","note_code":"sample_compared"}`
	for _, check := range []struct {
		name, body, origin, header string
		want, calls                int
	}{
		{"local", body, "http://localhost:8765", ActionValue, 200, 1},
		{"no consent header", body, "http://localhost:8765", "", 403, 0},
		{"cross port", body, "http://localhost:8766", ActionValue, 403, 0},
		{"forged verification", `{"verified_by_ori":true}`, "http://localhost:8765", ActionValue, 400, 0},
		{"executable", `{"command":"restore"}`, "http://localhost:8765", ActionValue, 400, 0},
		{"duplicate result", `{"result":"user_reported_passed","result":"ori_verified"}`, "http://localhost:8765", ActionValue, 400, 0},
	} {
		t.Run(check.name, func(t *testing.T) {
			spy := &domainSpy{}
			response := httptest.NewRecorder()
			request := backupRequest(http.MethodPost, "/recovery-report", check.body)
			request.Header.Set("Origin", check.origin)
			request.Header.Set(ActionHeader, check.header)
			backupMux(spy).ServeHTTP(response, request)
			if response.Code != check.want || int(spy.calls.Load()) != check.calls || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(response.Code, spy.calls.Load())
			}
		})
	}
}
