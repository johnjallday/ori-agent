package emailsetuphttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/emailsetup"
)

type stubService struct {
	got     emailsetup.ConnectRequest
	user    string
	failure *emailsetup.Failure
}

func (s *stubService) Detect(_ context.Context, address string) (emailsetup.Profile, error) {
	if s.failure != nil {
		return emailsetup.Profile{}, s.failure
	}
	return emailsetup.Profile{Address: address, Provider: emailsetup.ProviderGmail, Label: "Gmail", Supported: true}, nil
}

func (s *stubService) Connect(_ context.Context, userID string, req emailsetup.ConnectRequest) (emailsetup.ConnectResult, error) {
	s.got, s.user = req, userID
	if s.failure != nil {
		return emailsetup.ConnectResult{}, s.failure
	}
	return emailsetup.ConnectResult{Address: req.Address, AccountID: "acct-1", Workspace: emailsetup.Workspace{ID: "ws-1"}}, nil
}

func serve(t *testing.T, h *Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "localhost:8765"
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestConnectNeverEchoesThePassword(t *testing.T) {
	svc := &stubService{}
	rec := serve(t, NewHandler(svc), http.MethodPost, "/api/email-setup/connect",
		`{"address":"me@gmail.com","password":"hunter2-app-password"}`, map[string]string{"Origin": "http://localhost:8765"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if svc.got.Password != "hunter2-app-password" || svc.user == "" {
		t.Fatalf("service got %+v for %q", svc.got, svc.user)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("response echoes the password: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the response may be cached")
	}
}

func TestConnectFailureCarriesOnlySafeFields(t *testing.T) {
	svc := &stubService{failure: &emailsetup.Failure{Code: emailsetup.FailureWrongPassword, Field: "password", Message: "Google didn't accept that password."}}
	rec := serve(t, NewHandler(svc), http.MethodPost, "/api/email-setup/connect",
		`{"address":"me@gmail.com","password":"hunter2"}`, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != emailsetup.FailureWrongPassword || body["field"] != "password" || body["message"] == "" {
		t.Fatalf("body = %v", body)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("failure echoes the password: %s", rec.Body.String())
	}
}

func TestConnectRejectsCrossOriginRequests(t *testing.T) {
	svc := &stubService{}
	rec := serve(t, NewHandler(svc), http.MethodPost, "/api/email-setup/connect",
		`{"address":"me@gmail.com","password":"x"}`, map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden || svc.got.Address != "" {
		t.Fatalf("status %d, service called with %+v; want a refusal before the service runs", rec.Code, svc.got)
	}
}

func TestConnectRejectsMalformedBodies(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `address=me@gmail.com`,
		"unknown field": `{"address":"me@gmail.com","password":"x","admin":true}`,
		"too large":     `{"address":"me@gmail.com","password":"` + strings.Repeat("a", maxConnectBody) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &stubService{}
			rec := serve(t, NewHandler(svc), http.MethodPost, "/api/email-setup/connect", body, nil)
			if rec.Code != http.StatusBadRequest || svc.got.Address != "" {
				t.Fatalf("status %d, service called with %+v", rec.Code, svc.got)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	rec := serve(t, NewHandler(&stubService{}), http.MethodGet, "/api/email-setup/detect?address=me@gmail.com", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"provider":"gmail"`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	bad := serve(t, NewHandler(&stubService{failure: &emailsetup.Failure{Code: emailsetup.FailureInvalidAddress, Message: "Enter one email address."}}),
		http.MethodGet, "/api/email-setup/detect?address=nope", "", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid address status = %d", bad.Code)
	}
	if wrong := serve(t, NewHandler(&stubService{}), http.MethodPost, "/api/email-setup/detect", "", nil); wrong.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST detect status = %d", wrong.Code)
	}
}
