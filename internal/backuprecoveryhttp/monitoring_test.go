package backuprecoveryhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMonitoringControlsKeepExactUserControlBoundary(t *testing.T) {
	for _, path := range []string{"/monitoring/review", "/monitoring"} {
		t.Run(path, func(t *testing.T) {
			for _, mode := range []string{"missing consent header", "different origin port", "unknown command", "duplicate intent", "valid exact JSON"} {
				t.Run(mode, func(t *testing.T) {
					spy := &domainSpy{}
					body := "{}"
					if mode == "unknown command" {
						body = `{"policy":{"command":"run a backup"}}`
					}
					if mode == "duplicate intent" {
						body = `{"intent":"active","Intent":"manual_only"}`
					}
					request := backupRequest(http.MethodPost, path, body)
					if mode == "missing consent header" {
						request.Header.Del(ActionHeader)
					}
					if mode == "different origin port" {
						request.Header.Set("Origin", "http://localhost:9999")
					}
					response := httptest.NewRecorder()
					backupMux(spy).ServeHTTP(response, request)
					if mode == "valid exact JSON" {
						if response.Code != 200 || spy.calls.Load() != 1 {
							t.Fatal("bounded request did not dispatch", response.Code, response.Body.String())
						}
					} else if response.Code < 400 || spy.calls.Load() != 0 {
						t.Fatal("unreviewable input reached monitoring domain", response.Code, spy.calls.Load())
					}
				})
			}
		})
	}
}
