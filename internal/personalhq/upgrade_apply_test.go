package personalhq

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// fakeProvisioner records EnsureSpecialists calls and simulates attaching
// specialist agents to a workspace by name. It can be told to fail (whole or
// partial) to exercise the coordinator's outcome recording.
type fakeProvisioner struct {
	store     *session.SQLiteStore
	calls     int
	failAll   error
	failAfter int // if >0, attach this many then return an error (partial)
	lastRoles []SpecialistRole
}

func (f *fakeProvisioner) EnsureSpecialists(ctx context.Context, workspaceID string, roles []SpecialistRole) ([]string, error) {
	f.calls++
	f.lastRoles = roles
	ws, err := f.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var added []string
	for i, role := range roles {
		if f.failAfter > 0 && i >= f.failAfter {
			_ = f.store.UpdateWorkspace(ctx, ws)
			return added, errors.New("simulated provisioner failure")
		}
		if _, exists := FindRoleInstance(ws, role); exists {
			continue // idempotent no-op
		}
		ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{ID: role.Slug + "-id", Name: role.AgentName})
		added = append(added, role.AgentName)
	}
	if f.failAll != nil {
		return added, f.failAll
	}
	if err := f.store.UpdateWorkspace(ctx, ws); err != nil {
		return nil, err
	}
	return added, nil
}

// designatedHQ seeds the owner profile (owner_user_id is FK'd to users(id)),
// creates a workspace, designates it as the user's HQ, and returns its ID.
func designatedHQ(t *testing.T, svc *Service, profiles *userprofile.SQLiteStore, store *session.SQLiteStore, userID string, agentNames ...string) string {
	t.Helper()
	if err := profiles.Upsert(context.Background(), &userprofile.UserProfile{ID: userID}); err != nil {
		t.Fatalf("seed owner profile: %v", err)
	}
	ws := &session.Workspace{
		ID:          "hq-1",
		Name:        "My HQ",
		Kind:        session.WorkspaceKindWorkspace,
		OwnerUserID: userID,
		Status:      session.WorkspaceStatusActive,
	}
	for i, n := range agentNames {
		ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{ID: n + "-id", Name: n, EntryPoint: i == 0})
	}
	if err := store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := svc.Designate(context.Background(), userID, ws.ID); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	return ws.ID
}

func TestApplyUpgradeAddsMissingSpecialistsAndStampsVersion(t *testing.T) {
	svc, profiles, store := newTestHarness(t)
	userID := "user-1"
	// Arbitrary designated workspace: only a user entry agent, no specialists.
	hqID := designatedHQ(t, svc, profiles, store, userID, "My Assistant")

	prov := &fakeProvisioner{store: store}
	coord := NewUpgradeCoordinator(svc, store, prov)

	res, err := coord.ApplyUpgrade(context.Background(), userID, hqID)
	if err != nil {
		t.Fatalf("ApplyUpgrade: %v", err)
	}
	if res.Outcome != UpgradeOutcomeSuccess || res.Version != CurrentProvisioningVersion {
		t.Fatalf("expected success at current version, got %+v", res)
	}
	// All 3 specialist roles should have been requested and added.
	if len(res.AddedRoles) != 3 {
		t.Fatalf("expected 3 roles added, got %v", res.AddedRoles)
	}
	// The user's own agent must survive.
	ws, _ := store.GetWorkspace(context.Background(), hqID)
	if _, ok := findByName(ws, "My Assistant"); !ok {
		t.Fatal("user's entry agent must be preserved")
	}
	if ReadProvisionState(ws).Version != CurrentProvisioningVersion {
		t.Fatalf("version not stamped: %+v", ReadProvisionState(ws))
	}
}

func TestApplyUpgradeIsIdempotent(t *testing.T) {
	svc, profiles, store := newTestHarness(t)
	userID := "user-1"
	hqID := designatedHQ(t, svc, profiles, store, userID, "Personal Chief of Staff", "Inbox", "Journal")

	prov := &fakeProvisioner{store: store}
	coord := NewUpgradeCoordinator(svc, store, prov)

	// First apply on a full roster (version 0): stamps version, no additions.
	first, err := coord.ApplyUpgrade(context.Background(), userID, hqID)
	if err != nil || first.Outcome != UpgradeOutcomeSuccess {
		t.Fatalf("first apply: %+v err=%v", first, err)
	}
	// Second apply: up to date, no provisioner call beyond a no-op path.
	callsBefore := prov.calls
	second, err := coord.ApplyUpgrade(context.Background(), userID, hqID)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.Outcome != UpgradeOutcomeSuccess || len(second.AddedRoles) != 0 {
		t.Fatalf("second apply should be a clean no-op, got %+v", second)
	}
	if prov.calls != callsBefore {
		t.Fatalf("up-to-date apply must not call the provisioner again (before=%d after=%d)", callsBefore, prov.calls)
	}
}

func TestApplyUpgradeRecordsPartialFailureAndRetries(t *testing.T) {
	svc, profiles, store := newTestHarness(t)
	userID := "user-1"
	hqID := designatedHQ(t, svc, profiles, store, userID, "My Assistant")

	// Fail after attaching the first specialist -> partial.
	prov := &fakeProvisioner{store: store, failAfter: 1}
	coord := NewUpgradeCoordinator(svc, store, prov)

	res, err := coord.ApplyUpgrade(context.Background(), userID, hqID)
	if err == nil {
		t.Fatal("expected a partial-failure error")
	}
	if res == nil || res.Outcome != UpgradeOutcomePartial {
		t.Fatalf("expected partial outcome, got %+v", res)
	}
	// Version must NOT have advanced, and the partial outcome must be recorded
	// so the UI can offer retry.
	ws, _ := store.GetWorkspace(context.Background(), hqID)
	state := ReadProvisionState(ws)
	if state.Version >= CurrentProvisioningVersion {
		t.Fatalf("version must not advance on partial failure: %+v", state)
	}
	if state.LastUpgradeOutcome != UpgradeOutcomePartial || state.LastUpgradeError == "" {
		t.Fatalf("partial outcome/error must be recorded: %+v", state)
	}

	// Retry with a healthy provisioner: converges to success, idempotently
	// skipping the already-added specialist.
	prov.failAfter = 0
	retry, err := coord.ApplyUpgrade(context.Background(), userID, hqID)
	if err != nil {
		t.Fatalf("retry apply: %v", err)
	}
	if retry.Outcome != UpgradeOutcomeSuccess || retry.Version != CurrentProvisioningVersion {
		t.Fatalf("retry should succeed at current version, got %+v", retry)
	}
}

func TestApplyUpgradeRejectsNonDesignatedTarget(t *testing.T) {
	svc, profiles, store := newTestHarness(t)
	userID := "user-1"
	_ = designatedHQ(t, svc, profiles, store, userID, "My Assistant")

	// A workspace ID that is not the user's designated HQ must be refused
	// before any provisioning happens (contract §5.1 revalidation).
	coord := NewUpgradeCoordinator(svc, store, &fakeProvisioner{store: store})
	if _, err := coord.ApplyUpgrade(context.Background(), userID, "not-my-hq"); !errors.Is(err, ErrNotDesignatedHQ) {
		t.Fatalf("expected ErrNotDesignatedHQ, got %v", err)
	}
}

func findByName(ws *session.Workspace, name string) (*session.AgentInstance, bool) {
	for i := range ws.AgentInstances {
		if strings.EqualFold(ws.AgentInstances[i].Name, name) {
			return &ws.AgentInstances[i], true
		}
	}
	return nil, false
}
