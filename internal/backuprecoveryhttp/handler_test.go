package backuprecoveryhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
)

type domainSpy struct {
	calls            atomic.Int32
	err              error
	owner, workspace string
	review           backuprecovery.ReviewRequest
}

func (s *domainSpy) called(owner, workspace string) error {
	s.calls.Add(1)
	s.owner, s.workspace = owner, workspace
	return s.err
}
func (s *domainSpy) GetStatus(_ context.Context, o, w string) (backuprecovery.Status, error) {
	return backuprecovery.Status{}, s.called(o, w)
}
func (s *domainSpy) RecordRecoveryReport(_ context.Context, o, w string, _ backuprecovery.RecoveryReportRequest) (backuprecovery.OperationResult, error) {
	return backuprecovery.OperationResult{}, s.called(o, w)
}
func (s *domainSpy) GetRecovery(_ context.Context, o, w string) (backuprecovery.RecoveryView, error) {
	return backuprecovery.RecoveryView{}, s.called(o, w)
}
func (s *domainSpy) GetSettings(_ context.Context, o, w string) (backuprecovery.Settings, error) {
	return backuprecovery.Settings{}, s.called(o, w)
}
func (s *domainSpy) GetHistory(_ context.Context, o, w string, _ uint64) (backuprecovery.History, error) {
	return backuprecovery.History{}, s.called(o, w)
}
func (s *domainSpy) Pick(_ context.Context, o, w, _ string) (string, error) {
	return "opaque-invented-selection", s.called(o, w)
}
func (s *domainSpy) DescribeCredential(_ context.Context, o, w, _, _ string) (backuprecovery.CredentialReference, error) {
	return backuprecovery.CredentialReference{}, s.called(o, w)
}
func (s *domainSpy) Review(_ context.Context, o, w string, request backuprecovery.ReviewRequest) (backuprecovery.Review, error) {
	s.review = request
	return backuprecovery.Review{}, s.called(o, w)
}
func (s *domainSpy) Connect(_ context.Context, o, w string, _ backuprecovery.ConnectRequest) (backuprecovery.OperationResult, error) {
	return backuprecovery.OperationResult{}, s.called(o, w)
}
func (s *domainSpy) Refresh(_ context.Context, o, w string, _ backuprecovery.RefreshRequest) (backuprecovery.OperationResult, error) {
	return backuprecovery.OperationResult{}, s.called(o, w)
}
func (s *domainSpy) Revoke(_ context.Context, o, w string, _ backuprecovery.ControlRequest, _ bool) (backuprecovery.OperationResult, error) {
	return backuprecovery.OperationResult{}, s.called(o, w)
}

func (s *domainSpy) ReviewMonitoring(_ context.Context, o, w string, _ backuprecovery.MonitoringReviewRequest) (backuprecovery.MonitoringReview, error) {
	return backuprecovery.MonitoringReview{}, s.called(o, w)
}
func (s *domainSpy) SetMonitoring(_ context.Context, o, w string, _ backuprecovery.MonitoringChangeRequest) (backuprecovery.OperationResult, error) {
	return backuprecovery.OperationResult{}, s.called(o, w)
}

type ownerProvider string

func (p ownerProvider) CurrentUserID(context.Context) (string, error) { return string(p), nil }
func backupRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "http://localhost:8765/api/workspaces/invented-backup/backup-recovery"+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Origin", "http://localhost:8765")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ActionHeader, ActionValue)
	return request
}
func backupMux(spy Domain) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(spy, nil).Register(mux)
	return mux
}
func TestBackupHTTPMutationsRequireExactLocalOrigin(t *testing.T) {
	cases := map[string]func(*http.Request){
		"missing origin":        func(r *http.Request) { r.Header.Del("Origin") },
		"another local port":    func(r *http.Request) { r.Header.Set("Origin", "http://localhost:9999") },
		"another scheme":        func(r *http.Request) { r.Header.Set("Origin", "https://localhost:8765") },
		"another loopback host": func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8765") },
		"null origin":           func(r *http.Request) { r.Header.Set("Origin", "null") },
		"origin path":           func(r *http.Request) { r.Header.Set("Origin", "http://localhost:8765/extra") },
		"userinfo":              func(r *http.Request) { r.Header.Set("Origin", "http://user@localhost:8765") },
		"rebinding host": func(r *http.Request) {
			r.Host = "evil.example:8765"
			r.Header.Set("Origin", "http://evil.example:8765")
		},
		"forwarded bypass": func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.example")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Host", "evil.example")
		},
		"nonlocal peer":         func(r *http.Request) { r.RemoteAddr = "192.0.2.1:5678"; r.Header.Set("X-Forwarded-For", "127.0.0.1") },
		"missing action header": func(r *http.Request) { r.Header.Del(ActionHeader) },
		"cross-site fetch":      func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			spy := &domainSpy{}
			request := backupRequest(http.MethodPost, "/connect", `{}`)
			change(request)
			response := httptest.NewRecorder()
			backupMux(spy).ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || spy.calls.Load() != 0 {
				t.Fatal("rejected request reached domain", response.Code, spy.calls.Load())
			}
		})
	}
	spy := &domainSpy{}
	response := httptest.NewRecorder()
	backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, "/connect", `{}`))
	if response.Code != http.StatusOK || spy.calls.Load() != 1 || spy.owner != "local" || spy.workspace != "invented-backup" {
		t.Fatal("own local request lost canonical scope", response.Code, spy.owner, spy.workspace)
	}
}
func TestBackupHTTPRejectsAmbiguousOrExecutablePayloadsBeforeDomain(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate consent":  `{"allow_metadata_read":false,"allow_metadata_read":true}`,
		"case alias consent": `{"ALLOW_METADATA_READ":true}`,
		"command override":   `{"command":"restic backup"}`,
		"password":           `{"password":"invented-private-password"}`,
		"raw path":           `{"source_paths":["/Invented/Private"]}`,
		"trailing document":  `{} {}`,
		"null root":          `null`,
		"invalid surrogate":  `{"review_token":"\ud800"}`,
	} {
		t.Run(name, func(t *testing.T) {
			spy := &domainSpy{}
			response := httptest.NewRecorder()
			backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, "/connect", body))
			if response.Code != http.StatusBadRequest || spy.calls.Load() != 0 {
				t.Fatal("ambiguous payload reached domain", response.Code)
			}
			if strings.Contains(response.Body.String(), "invented-private") || strings.Contains(response.Body.String(), "/Invented") {
				t.Fatal("payload echoed")
			}
		})
	}
	spy := &domainSpy{}
	request := backupRequest(http.MethodPost, "/connect", `{}`)
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	backupMux(spy).ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType || spy.calls.Load() != 0 {
		t.Fatal("simple-form payload reached domain")
	}
	request = backupRequest(http.MethodPost, "/review", `{"volume_selection":"`+strings.Repeat("a", maxRequestBytes)+`"}`)
	response = httptest.NewRecorder()
	backupMux(spy).ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || spy.calls.Load() != 0 {
		t.Fatal("oversized body reached domain", response.Code)
	}
}
func TestBackupHTTPReviewCarriesOnlyTheFixedProviderID(t *testing.T) {
	spy := &domainSpy{}
	response := httptest.NewRecorder()
	backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, "/review", `{"generation":"","revision":0,"provider":"time_machine","source_selections":["opaque"]}`))
	if response.Code != http.StatusOK || spy.review.Provider != "time_machine" || len(spy.review.SourceSelections) != 1 {
		t.Fatal("the provider did not reach the domain", response.Code, spy.review)
	}
	for name, body := range map[string]string{
		"case alias":  `{"PROVIDER":"time_machine"}`,
		"provider fn": `{"provider":"time_machine","provider_command":"tmutil startbackup"}`,
		"raw path":    `{"provider":"time_machine","source_paths":["/Invented/Private"]}`,
	} {
		spy := &domainSpy{}
		response := httptest.NewRecorder()
		backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, "/review", body))
		if response.Code != http.StatusBadRequest || spy.calls.Load() != 0 {
			t.Fatal(name, "reached the domain", response.Code)
		}
	}
}
func TestBackupHTTPReadRoutesNeverDispatchAMutation(t *testing.T) {
	for _, path := range []string{"", "/guide", "/recovery", "/settings", "/history?cursor=12"} {
		spy := &domainSpy{}
		response := httptest.NewRecorder()
		request := backupRequest(http.MethodGet, path, "")
		request.Header.Del("Origin")
		request.Header.Del(ActionHeader)
		backupMux(spy).ServeHTTP(response, request)
		if response.Code != http.StatusOK || spy.calls.Load() != 1 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("stored read unavailable", path, response.Code)
		}
	}
	for _, path := range []string{"/connect", "/refresh", "/review", "/revoke", "/selection", "/credential"} {
		spy := &domainSpy{}
		response := httptest.NewRecorder()
		backupMux(spy).ServeHTTP(response, backupRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusMethodNotAllowed || spy.calls.Load() != 0 {
			t.Fatal("GET reached mutation", path, response.Code)
		}
	}
}
func TestRecoveryGuidanceExposesNoExecutionRoute(t *testing.T) {
	for _, path := range []string{"/recovery/restore", "/recovery/repair", "/recovery/prune"} {
		spy := &domainSpy{}
		response := httptest.NewRecorder()
		backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, path, `{}`))
		if response.Code != http.StatusNotFound || spy.calls.Load() != 0 {
			t.Fatal("execution route materialized", path, response.Code)
		}
	}
}

func TestBackupHTTPExactActorAndPrivateErrors(t *testing.T) {
	for _, owner := range []string{"LOCAL", "", "other"} {
		spy := &domainSpy{}
		mux := http.NewServeMux()
		NewHandler(spy, ownerProvider(owner)).Register(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, backupRequest(http.MethodGet, "/settings", ""))
		if response.Code != http.StatusNotFound || spy.calls.Load() != 0 {
			t.Fatal("noncanonical actor reached domain", owner, response.Code)
		}
	}
	spy := &domainSpy{err: errors.New("/Users/Invented/private-vault secret provider stderr")}
	response := httptest.NewRecorder()
	backupMux(spy).ServeHTTP(response, backupRequest(http.MethodPost, "/connect", `{}`))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "Invented") || strings.Contains(response.Body.String(), "stderr") {
		t.Fatal("private diagnostics escaped", response.Body.String())
	}
	response = httptest.NewRecorder()
	backupMux(nil).ServeHTTP(response, backupRequest(http.MethodGet, "", ""))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("nil runtime was ready", response.Code)
	}
}
