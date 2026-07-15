package personalhqhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// fakeSpecialistProvisioner attaches requested roles to a workspace by name,
// idempotently — enough to exercise the HTTP upgrade endpoints without the real
// sessionhttp agent-creation stack.
type fakeSpecialistProvisioner struct{ store *session.SQLiteStore }

func (f *fakeSpecialistProvisioner) EnsureSpecialists(ctx context.Context, workspaceID string, roles []personalhq.SpecialistRole) ([]string, error) {
	ws, err := f.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, role := range roles {
		exists := false
		for _, inst := range ws.AgentInstances {
			if strings.EqualFold(inst.Name, role.AgentName) {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{ID: role.Slug + "-id", Name: role.AgentName})
		added = append(added, role.AgentName)
	}
	if len(added) > 0 {
		if err := f.store.UpdateWorkspace(ctx, ws); err != nil {
			return added, err
		}
	}
	return added, nil
}

func newUpgradeTestHandler(t *testing.T) (*Handler, *session.SQLiteStore, string) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	profiles := userprofile.NewSQLiteStore(db)
	workspaces := session.NewSQLiteStore(db)
	service := personalhq.NewService(profiles, workspaces)
	upgrade := personalhq.NewUpgradeCoordinator(service, workspaces, &fakeSpecialistProvisioner{store: workspaces})
	handler := NewHandler(service, nil, upgrade, userprofile.LocalUserProvider{})

	userID := userprofile.LocalUserID
	if err := profiles.Upsert(context.Background(), &userprofile.UserProfile{ID: userID}); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	return handler, workspaces, userID
}

func designateArbitraryHQ(t *testing.T, service *personalhq.Service, store *session.SQLiteStore, userID string) string {
	t.Helper()
	ws := &session.Workspace{
		ID:             "hq-1",
		Name:           "My HQ",
		Kind:           session.WorkspaceKindWorkspace,
		OwnerUserID:    userID,
		Status:         session.WorkspaceStatusActive,
		AgentInstances: []session.AgentInstance{{ID: "mine", Name: "My Assistant", EntryPoint: true}},
	}
	if err := store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := service.Designate(context.Background(), userID, ws.ID); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	return ws.ID
}

func TestUpgradePreviewAndApplyFlow(t *testing.T) {
	handler, store, userID := newUpgradeTestHandler(t)
	_ = designateArbitraryHQ(t, handler.service, store, userID)

	// Preview: 3 specialist roles missing, not blocked.
	rec := httptest.NewRecorder()
	handler.UpgradePreview(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/upgrade/preview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var previewResp struct {
		Plan personalhq.UpgradePlan `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("decode preview: %v (%s)", err, rec.Body.String())
	}
	if len(previewResp.Plan.MissingRoles) != 3 {
		t.Fatalf("expected 3 missing roles, got %v", previewResp.Plan.MissingRoles)
	}

	// Apply: succeeds, adds the 3 specialists.
	rec = httptest.NewRecorder()
	handler.UpgradeApply(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/upgrade/apply", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Preview again: now up to date, no missing roles.
	rec = httptest.NewRecorder()
	handler.UpgradePreview(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/upgrade/preview", nil))
	previewResp.Plan = personalhq.UpgradePlan{}
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("decode preview 2: %v", err)
	}
	if !previewResp.Plan.UpToDate || len(previewResp.Plan.MissingRoles) != 0 {
		t.Fatalf("expected up-to-date after apply, got %+v", previewResp.Plan)
	}
}

func TestUpgradePreviewConflictsWithoutDesignatedHQ(t *testing.T) {
	handler, _, _ := newUpgradeTestHandler(t)
	rec := httptest.NewRecorder()
	handler.UpgradePreview(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/upgrade/preview", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without a designated HQ, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUpgradeUnavailableWhenCoordinatorMissing(t *testing.T) {
	handler := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	rec := httptest.NewRecorder()
	handler.UpgradeApply(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/upgrade/apply", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when upgrade coordinator is missing, got %d", rec.Code)
	}
}
