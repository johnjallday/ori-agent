package learningmissionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type testSource struct{ calls int }

func (s *testSource) Candidate(_ context.Context, owner, id string) (learningmissions.Candidate, error) {
	s.calls++
	if owner != "local" || id != "workspace" {
		return learningmissions.Candidate{}, learningmissions.ErrUnavailable
	}
	return learningmissions.Candidate{Ref: "candidate", WorkspaceID: id, Incarnation: "incarnation", ScopeDigest: strings.Repeat("a", 64), Name: "Practice"}, nil
}
func (s *testSource) Validate(ctx context.Context, owner string, d learningmissions.Definition) error {
	_, err := s.Candidate(ctx, owner, d.WorkspaceID)
	return err
}

type fixture struct {
	mux    *http.ServeMux
	db     *database.DB
	source *testSource
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "learning.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('workspace','Practice',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	source := &testSource{}
	h := NewHandler(learningmissions.NewService(learningmissions.NewSQLiteStore(db), &resetstate.WorkGate{}, source), userprofile.LocalUserProvider{})
	mux := http.NewServeMux()
	h.Register(mux)
	return fixture{mux, db, source}
}

func request(f fixture, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8931"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://localhost:8931")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func TestStatusNeverEnrollsOrCallsSource(t *testing.T) {
	f := newFixture(t)
	for range 3 {
		w := request(f, http.MethodGet, "/api/learning-missions", "")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_owners`).Scan(&count); err != nil || count != 0 || f.source.calls != 0 {
		t.Fatalf("GET caused activity: %d owners, %d source calls, %v", count, f.source.calls, err)
	}
}

func TestStrictRequestsCannotMutateWithExtraDuplicateOrTrailingFields(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{
		`null`, `[]`, `{`, `{"operation_id":"a","expected_revision":0,"studio_id":"workspace"} {}`,
		`{"operation_id":"a","operation_id":"b","studio_id":"workspace"}`,
		`{"operation_id":"a","Operation_ID":"b","studio_id":"workspace"}`,
		`{"operation_id":"a","studio_id":"workspace","actor":"user"}`,
		`{"operation_id":"a","studio_id":"workspace","owner_id":"local"}`,
		`{"operation_id":"a","studio_id":"workspace","complete":true}`,
		`{"operation_id":"a","studio_id":"workspace","automatic":true}`,
		`{"operation_id":"a","studio_id":"workspace","milestone_id":"forged"}`,
		`{"operation_id":"a","studio_id":null}`,
		`{"operation_id":"a","studio_id":"workspace","expected_revision":1.5}`,
		`{"operation_id":"` + strings.Repeat("x", learningmissions.MaxInputBytes) + `"}`,
	} {
		w := request(f, http.MethodPost, "/api/learning-missions/proposals", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body[:min(len(body), 120)], w.Code)
		}
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_owners`).Scan(&count); err != nil || count != 0 || f.source.calls != 0 {
		t.Fatal("rejected request performed preflight or mutation")
	}
}

func TestOwnerBoundaryRequiresExactLocalOriginAndJSON(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		host, origin, content string
		status                int
	}{
		{"attacker.test", "http://attacker.test", "application/json", 403},
		{"localhost:8931", "http://attacker.test", "application/json", 403},
		{"localhost:8931", "http://localhost:9999", "application/json", 403},
		{"localhost:8931", "", "application/json", 403},
		{"localhost:8931", "null", "application/json", 403},
		{"localhost:8931", "https://localhost:8931", "application/json", 403},
		{"localhost:8931", "http://localhost:8931", "text/plain", 415},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/learning-missions/proposals", strings.NewReader(`{"operation_id":"request","studio_id":"workspace"}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		f.mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%+v: %d", tc, w.Code)
		}
	}
	if f.source.calls != 0 {
		t.Fatal("refused boundary reached the source")
	}
}

func TestExplicitProposalDecisionAndForeignReferences(t *testing.T) {
	f := newFixture(t)
	w := request(f, http.MethodPost, "/api/learning-missions/proposals", `{"operation_id":"request","expected_revision":0,"studio_id":"workspace"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("proposal: %d %s", w.Code, w.Body.String())
	}
	var offer learningmissions.Outcome
	if err := json.Unmarshal(w.Body.Bytes(), &offer); err != nil || offer.Instance == nil {
		t.Fatalf("decode proposal: %v", err)
	}
	id := offer.Instance.ID
	w = request(f, http.MethodPost, "/api/learning-missions/instances/foreign/decisions", `{"operation_id":"bad","expected_revision":1,"instance_revision":1,"decision":"accept"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign reference: %d %s", w.Code, w.Body.String())
	}
	w = request(f, http.MethodPost, "/api/learning-missions/instances/"+id+"/decisions", `{"operation_id":"accept","expected_revision":1,"instance_revision":1,"decision":"accept"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	w = request(f, http.MethodGet, "/api/learning-missions/operations/accept", "")
	if w.Code != http.StatusOK {
		t.Fatalf("operation recovery: %d", w.Code)
	}
	var got learningmissions.Outcome
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Instance == nil || got.Instance.State != learningmissions.Active || got.Instance.Progress.TicketID != "" {
		t.Fatalf("acceptance must not run work: %+v %v", got, err)
	}
}
