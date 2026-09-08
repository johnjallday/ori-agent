package agentmaphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentmap"
)

type stubService struct {
	layout   agentmap.Layout
	applied  []agentmap.Patch
	resets   int
	loadErr  error
	applyErr error
}

func (s *stubService) Load(context.Context, string) (agentmap.Layout, error) {
	return s.layout, s.loadErr
}

func (s *stubService) Apply(_ context.Context, _ string, patch agentmap.Patch) (agentmap.Result, error) {
	s.applied = append(s.applied, patch)
	if s.applyErr != nil {
		return agentmap.Result{}, s.applyErr
	}
	return agentmap.Result{Layout: s.layout}, nil
}

func (s *stubService) Reset(context.Context, string) (agentmap.Result, error) {
	s.resets++
	if s.applyErr != nil {
		return agentmap.Result{}, s.applyErr
	}
	return agentmap.Result{Layout: s.layout}, nil
}

func newTestServer(service LayoutService) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(func() LayoutService { return service }, nil).Register(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestGetLayoutReturnsTheStoredLayout(t *testing.T) {
	service := &stubService{layout: agentmap.Layout{
		SchemaVersion: agentmap.SchemaVersion,
		Revision:      3,
		Positions:     map[string]agentmap.Point{"Atlas": {X: 1, Y: 2}},
		SnapToGrid:    true,
	}}
	rec := do(t, newTestServer(service), http.MethodGet, "/api/agent-map/layout", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Layout agentmap.Layout `json:"layout"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if got.Layout.Revision != 3 {
		t.Fatalf("revision = %d, want 3", got.Layout.Revision)
	}
	if got.Layout.Positions["Atlas"] != (agentmap.Point{X: 1, Y: 2}) {
		t.Fatalf("Atlas = %v, want (1, 2)", got.Layout.Positions["Atlas"])
	}
}

func TestPatchLayoutAppliesOperations(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	rec := do(t, newTestServer(service), http.MethodPatch, "/api/agent-map/layout", `{
		"expected_revision": 4,
		"operations": [{"op":"set_positions","positions":{"Atlas":{"x":10,"y":20}}}]
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(service.applied) != 1 {
		t.Fatalf("applied %d patches, want 1", len(service.applied))
	}
	patch := service.applied[0]
	if patch.ExpectedRevision != 4 {
		t.Fatalf("expected revision = %d, want 4 — the echoed revision must reach the service", patch.ExpectedRevision)
	}
	if patch.Operations[0].Positions["Atlas"] != (agentmap.Point{X: 10, Y: 20}) {
		t.Fatalf("positions = %v", patch.Operations[0].Positions)
	}
}

// encoding/json ignores keys it does not recognise, so without strict decoding
// a typo would be accepted and silently do only part of what was asked.
func TestPatchLayoutRefusesUnknownFields(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	rec := do(t, newTestServer(service), http.MethodPatch, "/api/agent-map/layout",
		`{"operations":[{"op":"set_positions","positions":{"Atlas":{"x":1,"y":2}},"colour":"red"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(service.applied) != 0 {
		t.Fatalf("applied %d patches, want 0", len(service.applied))
	}
}

// The layout is always the requesting user's. A client that supplies a user_id
// is told plainly rather than having it silently ignored.
func TestPatchLayoutRefusesAClientSuppliedUserID(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	rec := do(t, newTestServer(service), http.MethodPatch, "/api/agent-map/layout",
		`{"user_id":"someone-else","operations":[{"op":"reset"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(service.applied) != 0 {
		t.Fatalf("applied %d patches, want 0", len(service.applied))
	}
}

func TestPatchLayoutBoundsTheOperationCount(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	ops := make([]string, agentmap.MaxOperationsPerPatch+1)
	for i := range ops {
		ops[i] = `{"op":"reset"}`
	}
	rec := do(t, newTestServer(service), http.MethodPatch, "/api/agent-map/layout",
		`{"operations":[`+strings.Join(ops, ",")+`]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestResetLayoutClearsTheArrangement(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	rec := do(t, newTestServer(service), http.MethodDelete, "/api/agent-map/layout", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if service.resets != 1 {
		t.Fatalf("resets = %d, want 1", service.resets)
	}
}

func TestErrorsMapToStableStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"malformed patch", agentmap.ErrInvalidPatch, http.StatusBadRequest},
		{"coordinate out of range", agentmap.ErrInvalidCoordinate, http.StatusBadRequest},
		{"zoom out of range", agentmap.ErrInvalidZoom, http.StatusBadRequest},
		{"bad agent name", agentmap.ErrInvalidAgentName, http.StatusBadRequest},
		{"patch too large", agentmap.ErrPatchTooLarge, http.StatusBadRequest},
		// A stale revision is a conflict, not a bad request: the body was well
		// formed and would have been accepted a moment ago.
		{"stale revision", agentmap.ErrStaleRevision, http.StatusConflict},
		{"newer stored format", agentmap.ErrUnsupportedSchemaVersion, http.StatusConflict},
		{"unknown agent", agentmap.ErrAgentNotFound, http.StatusNotFound},
		{"no storage", agentmap.ErrStoreUnavailable, http.StatusServiceUnavailable},
		{"no service", agentmap.ErrServiceUnavailable, http.StatusServiceUnavailable},
		{"something else", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &stubService{layout: agentmap.NewLayout(), applyErr: tc.err}
			rec := do(t, newTestServer(service), http.MethodPatch, "/api/agent-map/layout",
				`{"operations":[{"op":"reset"}]}`)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// An unwired map answers 503 rather than panicking: the client degrades to
// automatic placement with read-only navigation, which is a usable map, while a
// panic would take unrelated API routes down with it.
func TestAnUnwiredServiceIsUnavailableRatherThanAPanic(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(func() LayoutService { return nil }, nil).Register(mux)

	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		body := ""
		if method == http.MethodPatch {
			body = `{"operations":[{"op":"reset"}]}`
		}
		rec := do(t, mux, method, "/api/agent-map/layout", body)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", method, rec.Code)
		}
	}
}

// A nil resolver is the same story as a nil service: unavailable, never a
// panic. This is the shape that fails when a handler captures a store before
// the phase that builds it.
func TestANilResolverIsUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(nil, nil).Register(mux)
	rec := do(t, mux, http.MethodGet, "/api/agent-map/layout", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// The route shape carries no identifier, so there is nothing to address another
// user's map with.
func TestOnlyTheThreeLayoutRoutesExist(t *testing.T) {
	service := &stubService{layout: agentmap.NewLayout()}
	mux := newTestServer(service)

	rec := do(t, mux, http.MethodPost, "/api/agent-map/layout", `{}`)
	if rec.Code == http.StatusOK {
		t.Fatal("POST should not be a registered method on the layout route")
	}
	rec = do(t, mux, http.MethodGet, "/api/agent-map/layout/someone-else", "")
	if rec.Code == http.StatusOK {
		t.Fatal("there must be no per-user layout route")
	}
}
