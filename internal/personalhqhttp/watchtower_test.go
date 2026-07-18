package personalhqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type watchtowerSessionSource struct {
	errByWorkspace map[string]error
}

func (s watchtowerSessionSource) ListSessions(_ context.Context, filter *session.SessionFilter, _ *session.ListOptions) ([]session.SessionListItem, error) {
	if filter == nil || filter.FolderID == nil {
		return nil, nil
	}
	return nil, s.errByWorkspace[*filter.FolderID]
}

type watchtowerOpportunitySource struct {
	store   workspace.Store
	failFor map[string]error
}

func (s watchtowerOpportunitySource) List(workspaceID string) ([]workspace.Opportunity, error) {
	if err := s.failFor[workspaceID]; err != nil {
		return nil, err
	}
	ws, err := s.store.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, err
	}
	return append([]workspace.Opportunity(nil), ws.Opportunities...), nil
}

func watchtowerWorkspace(id, name string) *workspace.Workspace {
	return &workspace.Workspace{
		ID:          id,
		Name:        name,
		Kind:        "workspace",
		Status:      workspace.StatusActive,
		OwnerUserID: userprofile.LocalUserID,
	}
}

func newWatchtowerHandler(t *testing.T, store workspace.Store, sources dailybrief.SnapshotSources) *Handler {
	t.Helper()
	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := folderStore.Save(&workspace.Workspace{
		ID:          "hq-1",
		Name:        "Personal HQ",
		FolderSlug:  "personal-hq",
		Designation: "personal_hq",
	}); err != nil {
		t.Fatalf("save folder designation: %v", err)
	}
	service := personalhq.NewService(nil, nil)
	service.SetDesignationReader(folderStore)
	handler := NewHandler(service, nil, nil, userprofile.LocalUserProvider{})
	handler.SetWatchtowerSources(func() dailybrief.SnapshotSources { return sources })
	return handler
}

func decodeWatchtower(t *testing.T, recorder *httptest.ResponseRecorder) WatchtowerResponse {
	t.Helper()
	var response WatchtowerResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode Watchtower response: %v body=%s", err, recorder.Body.String())
	}
	return response
}

func TestWatchtower_AggregatesAndOrdersItemsAcrossWorkspaces(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := workspace.NewInMemoryStore()
	hq := watchtowerWorkspace("hq-1", "Personal HQ")
	hq.Tasks = []workspace.Task{{
		ID: "hq-failed", WorkspaceID: hq.ID, Description: "HQ task failed", Status: workspace.TaskStatusFailed, CreatedAt: now.Add(-time.Minute),
	}}
	alpha := watchtowerWorkspace("alpha-1", "Alpha")
	alpha.Tasks = []workspace.Task{{
		ID: "alpha-waiting", WorkspaceID: alpha.ID, Description: "Need a decision", Status: workspace.TaskStatusWaitingForChoice, CreatedAt: now.Add(-3 * time.Minute),
	}}
	alpha.ScheduledTasks = []workspace.ScheduledTask{{
		ID: "alpha-schedule", WorkspaceID: alpha.ID, Name: "Nightly sync", FailureCount: 1, LastError: "sync failed", UpdatedAt: now.Add(-4 * time.Minute),
	}}
	beta := watchtowerWorkspace("beta-1", "Beta")
	beta.Opportunities = []workspace.Opportunity{
		{ID: "beta-critical", WorkspaceID: beta.ID, Title: "Critical finding", Priority: "critical", Status: workspace.OpportunityNew, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "beta-high", WorkspaceID: beta.ID, Title: "High finding", Priority: "high", Status: workspace.OpportunityNew, UpdatedAt: now.Add(-5 * time.Minute)},
	}
	for _, ws := range []*workspace.Workspace{hq, alpha, beta} {
		if err := store.Save(ws); err != nil {
			t.Fatalf("save %s: %v", ws.ID, err)
		}
	}

	sources := dailybrief.SnapshotSources{
		Workspaces:    store,
		Opportunities: watchtowerOpportunitySource{store: store},
		Sessions:      watchtowerSessionSource{},
	}
	handler := newWatchtowerHandler(t, store, sources)
	recorder := httptest.NewRecorder()
	handler.Watchtower(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-hq/watchtower?workspace_id=hq-1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("Watchtower status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeWatchtower(t, recorder)
	if len(response.Gaps) != 0 || len(response.Items) != 5 {
		t.Fatalf("unexpected response: %#v", response)
	}

	want := []struct {
		workspaceID string
		itemType    string
		severity    string
	}{
		{"hq-1", "task", "failed"},
		{"beta-1", "opportunity", "critical"},
		{"alpha-1", "task", "waiting_for_choice"},
		{"alpha-1", "scheduled_task", "scheduled_failure"},
		{"beta-1", "opportunity", "high"},
	}
	for index, expected := range want {
		item := response.Items[index]
		if item.WorkspaceID != expected.workspaceID || item.ItemType != expected.itemType || item.Severity != expected.severity || item.EntityID == "" || item.Timestamp == "" {
			t.Fatalf("item %d = %#v, want workspace/type/severity %#v", index, item, expected)
		}
	}
}

func TestWatchtower_RequiresDesignatedHQContextFromFolderStore(t *testing.T) {
	store := workspace.NewInMemoryStore()
	for _, ws := range []*workspace.Workspace{watchtowerWorkspace("hq-1", "Personal HQ"), watchtowerWorkspace("ordinary-1", "Ordinary")} {
		if err := store.Save(ws); err != nil {
			t.Fatalf("save workspace: %v", err)
		}
	}
	handler := newWatchtowerHandler(t, store, dailybrief.SnapshotSources{
		Workspaces:    store,
		Opportunities: watchtowerOpportunitySource{store: store},
		Sessions:      watchtowerSessionSource{},
	})

	recorder := httptest.NewRecorder()
	handler.Watchtower(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-hq/watchtower?workspace_id=ordinary-1", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-HQ Watchtower status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "items") {
		t.Fatalf("non-HQ response must not contain attention data: %s", recorder.Body.String())
	}
}

func TestWatchtower_DegradesWithNamedGapWhenSourceFails(t *testing.T) {
	store := workspace.NewInMemoryStore()
	for _, ws := range []*workspace.Workspace{watchtowerWorkspace("hq-1", "Personal HQ"), watchtowerWorkspace("beta-1", "Beta")} {
		if err := store.Save(ws); err != nil {
			t.Fatalf("save workspace: %v", err)
		}
	}
	handler := newWatchtowerHandler(t, store, dailybrief.SnapshotSources{
		Workspaces: store,
		Opportunities: watchtowerOpportunitySource{
			store:   store,
			failFor: map[string]error{"beta-1": errors.New("temporary store failure")},
		},
		Sessions: watchtowerSessionSource{},
	})

	recorder := httptest.NewRecorder()
	handler.Watchtower(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-hq/watchtower?workspace_id=hq-1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("Watchtower status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeWatchtower(t, recorder)
	if len(response.Gaps) != 1 || !strings.Contains(response.Gaps[0], "opportunities for workspace Beta are unavailable") {
		t.Fatalf("gaps = %#v", response.Gaps)
	}
}
