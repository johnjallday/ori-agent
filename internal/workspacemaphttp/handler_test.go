package workspacemaphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacemap"
)

type fakeService struct {
	layout     workspacemap.Layout
	result     workspacemap.Result
	loadErr    error
	applyErr   error
	resetErr   error
	lastUserID string
	lastPatch  workspacemap.Patch
	resetCalls int
}

func (f *fakeService) Load(_ context.Context, userID string) (workspacemap.Layout, error) {
	f.lastUserID = userID
	return f.layout, f.loadErr
}

func (f *fakeService) Apply(_ context.Context, userID string, patch workspacemap.Patch) (workspacemap.Result, error) {
	f.lastUserID = userID
	f.lastPatch = patch
	return f.result, f.applyErr
}

func (f *fakeService) Reset(_ context.Context, userID string) (workspacemap.Result, error) {
	f.lastUserID = userID
	f.resetCalls++
	return f.result, f.resetErr
}

type fakeUserProvider struct {
	id  string
	err error
}

func (f fakeUserProvider) CurrentUserID(context.Context) (string, error) { return f.id, f.err }

func newTestServer(t *testing.T, service LayoutService, provider fakeUserProvider) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(service, provider).Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func do(t *testing.T, server *httptest.Server, method, body string) (*http.Response, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, server.URL+"/api/workspace-map/layout", reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	decoded := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func TestGetLayoutReturnsCurrentUserLayout(t *testing.T) {
	viewport := workspacemap.Viewport{CenterX: 12, CenterY: -8, Zoom: 1.5}
	service := &fakeService{layout: workspacemap.Layout{
		SchemaVersion: workspacemap.SchemaVersion,
		Revision:      4,
		Positions:     map[string]workspacemap.Point{"ws-a": {X: 38, Y: 76}},
		Viewport:      &viewport,
		SnapToGrid:    true,
	}}
	server := newTestServer(t, service, fakeUserProvider{id: "alice"})

	resp, body := do(t, server, http.MethodGet, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if service.lastUserID != "alice" {
		t.Errorf("user = %q, want the request context's user", service.lastUserID)
	}
	layout, ok := body["layout"].(map[string]any)
	if !ok {
		t.Fatalf("response has no layout object: %v", body)
	}
	if layout["revision"] != float64(4) {
		t.Errorf("revision = %v, want 4", layout["revision"])
	}
	if layout["snap_to_grid"] != true {
		t.Errorf("snap_to_grid = %v, want true", layout["snap_to_grid"])
	}
	positions, ok := layout["positions"].(map[string]any)
	if !ok || positions["ws-a"] == nil {
		t.Fatalf("positions missing from response: %v", layout)
	}
}

func TestPatchLayoutCommitsOperationsAndReturnsRevision(t *testing.T) {
	service := &fakeService{result: workspacemap.Result{
		SchemaVersion: workspacemap.SchemaVersion,
		Revision:      9,
		Positions:     map[string]workspacemap.Point{"ws-a": {X: 76, Y: 0}},
		SnapToGrid:    true,
	}}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, body := do(t, server, http.MethodPatch,
		`{"operations":[{"op":"set_positions","positions":{"ws-a":{"x":76,"y":0}}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", resp.StatusCode, body)
	}
	if len(service.lastPatch.Operations) != 1 || service.lastPatch.Operations[0].Kind != workspacemap.OpSetPositions {
		t.Fatalf("patch = %+v, want one set_positions operation", service.lastPatch)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", body)
	}
	if result["revision"] != float64(9) {
		t.Errorf("revision = %v, want the server-issued 9 (FR-102)", result["revision"])
	}
}

func TestPatchLayoutRefusesClientSuppliedUserID(t *testing.T) {
	service := &fakeService{}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, _ := do(t, server, http.MethodPatch,
		`{"user_id":"someone-else","operations":[{"op":"reset"}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if len(service.lastPatch.Operations) != 0 {
		t.Error("a request naming another user must not reach the service (FR-98)")
	}
}

func TestPatchLayoutRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json", `{`},
		{"empty body", ``},
		{"no operations", `{"operations":[]}`},
		{"unknown operation", `{"operations":[{"op":"teleport"}]}`},
		{"non-finite coordinate", `{"operations":[{"op":"set_positions","positions":{"ws-a":{"x":1e400,"y":0}}}]}`},
		{"out of range coordinate", `{"operations":[{"op":"set_positions","positions":{"ws-a":{"x":99999999,"y":0}}}]}`},
		{"zoom out of range", `{"operations":[{"op":"set_viewport","viewport":{"center_x":0,"center_y":0,"zoom":42}}]}`},
		{"zoom below the framing floor", `{"operations":[{"op":"set_viewport","viewport":{"center_x":0,"center_y":0,"zoom":0.05}}]}`},
		{"zoom not a number", `{"operations":[{"op":"set_viewport","viewport":{"center_x":0,"center_y":0,"zoom":1e400}}]}`},
		{"reserved node id", `{"operations":[{"op":"set_positions","positions":{"__personal_hq_site__":{"x":0,"y":0}}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The real domain validation runs here, not a fake's opinion of it.
			service := &realValidationService{}
			server := newTestServer(t, service, fakeUserProvider{id: "local"})
			resp, _ := do(t, server, http.MethodPatch, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

// A camera Fit All framed a wide layout with has to survive the round trip it
// is saved through, or the view snaps back to 50% on the next load (#307).
func TestPatchLayoutAcceptsAFittedWideViewport(t *testing.T) {
	cases := []struct {
		name string
		zoom string
	}{
		{"the framing floor", "0.1"},
		{"a fitted wide layout", "0.3"},
		{"the interactive floor", "0.5"},
		{"the ceiling", "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t, &realValidationService{}, fakeUserProvider{id: "local"})
			resp, body := do(t, server, http.MethodPatch,
				`{"operations":[{"op":"set_viewport","viewport":{"center_x":900,"center_y":420,"zoom":`+tc.zoom+`}}]}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 for zoom %s: %v", resp.StatusCode, tc.zoom, body)
			}
		})
	}
}

// realValidationService runs the model's own normalization so the handler's
// error mapping is tested against real domain errors rather than invented ones.
type realValidationService struct{}

func (realValidationService) Load(context.Context, string) (workspacemap.Layout, error) {
	return workspacemap.NewLayout(), nil
}

func (realValidationService) Apply(_ context.Context, _ string, patch workspacemap.Patch) (workspacemap.Result, error) {
	if _, err := workspacemap.NormalizePatch(patch); err != nil {
		return workspacemap.Result{}, err
	}
	return workspacemap.Result{SchemaVersion: workspacemap.SchemaVersion, Revision: 1}, nil
}

func (realValidationService) Reset(context.Context, string) (workspacemap.Result, error) {
	return workspacemap.Result{SchemaVersion: workspacemap.SchemaVersion, Revision: 1}, nil
}

func TestPatchLayoutMapsDomainErrorsToStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown workspace", workspacemap.ErrNodeNotFound, http.StatusNotFound},
		{"not owned", workspacemap.ErrNodeNotOwned, http.StatusNotFound},
		{"newer schema", workspacemap.ErrUnsupportedSchemaVersion, http.StatusConflict},
		{"storage unavailable", workspacemap.ErrStoreUnavailable, http.StatusServiceUnavailable},
		{"unexpected", errors.New("disk exploded"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t, &fakeService{applyErr: tc.err}, fakeUserProvider{id: "local"})
			resp, _ := do(t, server, http.MethodPatch,
				`{"operations":[{"op":"set_positions","positions":{"ws-a":{"x":0,"y":0}}}]}`)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestDeleteLayoutResets(t *testing.T) {
	service := &fakeService{result: workspacemap.Result{SchemaVersion: workspacemap.SchemaVersion, Revision: 3, SnapToGrid: true}}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, body := do(t, server, http.MethodDelete, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if service.resetCalls != 1 {
		t.Errorf("reset calls = %d, want 1", service.resetCalls)
	}
	result, ok := body["result"].(map[string]any)
	if !ok || result["snap_to_grid"] != true {
		t.Errorf("reset response = %v, want the preserved snap preference (FR-110)", body)
	}
}

func TestUnsupportedMethodIsRejected(t *testing.T) {
	server := newTestServer(t, &fakeService{}, fakeUserProvider{id: "local"})
	resp, _ := do(t, server, http.MethodPost, `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestUnavailableServiceReports503(t *testing.T) {
	server := newTestServer(t, nil, fakeUserProvider{id: "local"})
	resp, _ := do(t, server, http.MethodGet, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestBlankCurrentUserFallsBackToLocal(t *testing.T) {
	service := &fakeService{layout: workspacemap.NewLayout()}
	server := newTestServer(t, service, fakeUserProvider{id: "  "})

	if resp, _ := do(t, server, http.MethodGet, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if service.lastUserID != "local" {
		t.Errorf("user = %q, want the local single user", service.lastUserID)
	}
}

// ---------------------------------------------------------------------------
// Group district presentation over the existing endpoint (#346 FR-176)
// ---------------------------------------------------------------------------

func TestGetLayoutIncludesGroupDistricts(t *testing.T) {
	frame := workspacemap.Frame{X: 300, Y: 200, Width: 900, Height: 700}
	service := &fakeService{layout: workspacemap.Layout{
		SchemaVersion: workspacemap.SchemaVersion,
		Revision:      2,
		Positions:     map[string]workspacemap.Point{},
		Groups: map[string]workspacemap.GroupPresentation{
			"grp-a": {
				SizingMode: workspacemap.SizingModeCustom,
				Frame:      &frame,
				Collapsed:  true,
				Accent:     "moss",
				Theme:      "blueprint",
			},
		},
		SnapToGrid: true,
	}}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, body := do(t, server, http.MethodGet, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	layout, _ := body["layout"].(map[string]any)
	groups, ok := layout["groups"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no groups object: %v", layout)
	}
	district, ok := groups["grp-a"].(map[string]any)
	if !ok {
		t.Fatalf("groups has no grp-a: %v", groups)
	}
	if district["sizing_mode"] != "custom" || district["collapsed"] != true ||
		district["accent"] != "moss" || district["theme"] != "blueprint" {
		t.Errorf("district = %v, want the stored presentation", district)
	}
	rect, ok := district["frame"].(map[string]any)
	if !ok {
		t.Fatalf("district carries no frame: %v", district)
	}
	if rect["x"] != float64(300) || rect["width"] != float64(900) {
		t.Errorf("frame = %v, want the saved rectangle", rect)
	}
}

func TestPatchAcceptsDistrictOperations(t *testing.T) {
	service := &fakeService{result: workspacemap.Result{Revision: 9}}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, _ := do(t, server, http.MethodPatch, `{"operations":[
		{"op":"set_group_frame","group_id":"grp-a","frame":{"x":10,"y":20,"width":400,"height":300}},
		{"op":"set_group_collapsed","group_id":"grp-a","collapsed":true},
		{"op":"set_group_appearance","group_id":"grp-a","accent":"moss","theme":"blueprint"},
		{"op":"fit_group_to_contents","group_id":"grp-a"},
		{"op":"reset_group_appearance","group_id":"grp-a"}
	]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(service.lastPatch.Operations) != 5 {
		t.Fatalf("operations = %d, want 5", len(service.lastPatch.Operations))
	}
	first := service.lastPatch.Operations[0]
	if first.Kind != workspacemap.OpSetGroupFrame || first.Frame == nil || first.Frame.Width != 400 {
		t.Errorf("first operation = %+v, want the decoded frame", first)
	}
	if collapsed := service.lastPatch.Operations[1].Collapsed; collapsed == nil || !*collapsed {
		t.Errorf("collapsed operation lost its value: %+v", service.lastPatch.Operations[1])
	}
}

func TestPatchReturnsCanonicalDistrictResult(t *testing.T) {
	frame := workspacemap.Frame{X: 0, Y: 0, Width: 400, Height: 300}
	service := &fakeService{result: workspacemap.Result{
		SchemaVersion: workspacemap.SchemaVersion,
		Revision:      12,
		Positions:     map[string]workspacemap.Point{},
		Groups: map[string]workspacemap.GroupPresentation{
			"grp-a": {SizingMode: workspacemap.SizingModeCustom, Frame: &frame, Accent: "default", Theme: "default"},
		},
	}}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, body := do(t, server, http.MethodPatch,
		`{"operations":[{"op":"set_group_frame","group_id":"grp-a","frame":{"x":0,"y":0,"width":400,"height":300}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	result, _ := body["result"].(map[string]any)
	if result["revision"] != float64(12) {
		t.Errorf("revision = %v, want 12 so the client can order writes", result["revision"])
	}
	groups, ok := result["groups"].(map[string]any)
	if !ok {
		t.Fatalf("result carries no committed districts: %v", result)
	}
	if _, ok := groups["grp-a"].(map[string]any); !ok {
		t.Errorf("committed districts = %v, want grp-a", groups)
	}
}

// A misspelled or invented key on a district operation is refused whole rather
// than half-applied (FR-180).
func TestPatchRejectsUnknownOperationFields(t *testing.T) {
	service := &fakeService{}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, body := do(t, server, http.MethodPatch,
		`{"operations":[{"op":"set_group_frame","group_id":"grp-a","frame":{"x":0,"y":0,"width":400,"height":300},"colour":"red"}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%v)", resp.StatusCode, body)
	}
	if service.lastPatch.Operations != nil {
		t.Error("a rejected payload must never reach the service")
	}
}

func TestPatchRejectsOversizedOperationList(t *testing.T) {
	service := &fakeService{}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	operations := make([]string, workspacemap.MaxOperationsPerPatch+1)
	for i := range operations {
		operations[i] = `{"op":"fit_group_to_contents","group_id":"grp-a"}`
	}
	resp, _ := do(t, server, http.MethodPatch, `{"operations":[`+strings.Join(operations, ",")+`]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if service.lastPatch.Operations != nil {
		t.Error("an oversized patch must never reach the service")
	}
}

func TestPatchMapsDistrictDomainErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"unsupported preset", workspacemap.ErrUnsupportedPreset, http.StatusBadRequest},
		{"invalid frame", workspacemap.ErrInvalidFrame, http.StatusBadRequest},
		{"ineligible group", workspacemap.ErrGroupNotEligible, http.StatusBadRequest},
		// A group owned by someone else is reported as missing, so the API never
		// confirms another user's record exists.
		{"missing group", workspacemap.ErrNodeNotFound, http.StatusNotFound},
		{"foreign group", workspacemap.ErrNodeNotOwned, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeService{applyErr: tc.err}
			server := newTestServer(t, service, fakeUserProvider{id: "local"})
			resp, _ := do(t, server, http.MethodPatch,
				`{"operations":[{"op":"set_group_collapsed","group_id":"grp-a","collapsed":true}]}`)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

// The district endpoint is the same current-user endpoint as everything else:
// a caller-supplied user_id is refused rather than ignored (FR-4, FR-98).
func TestPatchDistrictRefusesCallerSuppliedUser(t *testing.T) {
	service := &fakeService{}
	server := newTestServer(t, service, fakeUserProvider{id: "local"})

	resp, _ := do(t, server, http.MethodPatch,
		`{"user_id":"alice","operations":[{"op":"set_group_collapsed","group_id":"grp-a","collapsed":true}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if service.lastPatch.Operations != nil {
		t.Error("a request naming another user must never reach the service")
	}
}
