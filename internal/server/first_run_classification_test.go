package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// erroringProfileStore satisfies personalhq.ProfileStore and fails every
// call, so tests can prove degradation when the Personal HQ dependency
// itself is unavailable (PRD FR136/task 8.7).
type erroringProfileStore struct{}

func (erroringProfileStore) GetPersonalHQState(context.Context, string) (*userprofile.PersonalHQState, error) {
	return nil, errors.New("boom: profile store unavailable")
}

func (erroringProfileStore) SetPersonalWorkspaceID(context.Context, string, string) error {
	return errors.New("boom: profile store unavailable")
}

func (erroringProfileStore) SetHQOnboardingState(context.Context, string, userprofile.HQOnboardingState) error {
	return errors.New("boom: profile store unavailable")
}

// newFirstRunTestServer builds a full server (same wiring as production)
// rooted at a temp directory, mirroring newDailyBriefTestServer. HOME is
// sandboxed since some of these tests actually create a workspace, which
// would otherwise write into the real user's home directory
// (DefaultWorkspaceRoot ignores CWD).
func newFirstRunTestServer(t *testing.T) (*ServerBuilder, http.Handler) {
	t.Helper()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWD) })
	t.Setenv("HOME", tmpDir)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return builder, srv.Handler()
}

// TestIsBrandNewProfile_GenuinelyFreshInstallIsBrandNew is the baseline: a
// profile with hq_onboarding_state=unseen (the default for every new row),
// no completed app-level onboarding, and no workspaces is correctly
// classified brand-new.
func TestIsBrandNewProfile_GenuinelyFreshInstallIsBrandNew(t *testing.T) {
	builder, _ := newFirstRunTestServer(t)
	if !builder.server.isBrandNewProfile(context.Background()) {
		t.Fatal("expected a genuinely fresh install (unseen, no onboarding, no workspaces) to be classified brand-new")
	}
}

// TestIsBrandNewProfile_CompletedAppOnboardingAloneDoesNotSuppressBrandNew
// covers a regression found while writing this feature's Playwright
// coverage: the separate app-level onboarding wizard ("what should we call
// each other?") also completes on a genuinely brand-new profile's very
// first session, before the user has ever seen HQ setup. An earlier version
// of this fix treated that completion flag as evidence of "established
// user," which would have wrongly suppressed the HQ guided takeover the
// moment a brand-new user dismissed the unrelated wizard and reloaded —
// deliberately NOT the current behavior.
func TestIsBrandNewProfile_CompletedAppOnboardingAloneDoesNotSuppressBrandNew(t *testing.T) {
	builder, _ := newFirstRunTestServer(t)
	if builder.server.Storage.OnboardingMgr == nil {
		t.Fatal("expected OnboardingMgr to be wired")
	}
	if err := builder.server.Storage.OnboardingMgr.CompleteOnboarding(); err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}

	if !builder.server.isBrandNewProfile(context.Background()) {
		t.Fatal("expected completing the unrelated app-level wizard alone (no workspaces yet) to NOT suppress the HQ brand-new classification")
	}
}

// TestIsBrandNewProfile_ExistingWorkspaceHistoryIsNotBrandNew covers the
// other independent signal named in task 8.1 ("workspace history"): even
// with app-level onboarding never marked complete, having a real workspace
// already is strong evidence of prior use.
func TestIsBrandNewProfile_ExistingWorkspaceHistoryIsNotBrandNew(t *testing.T) {
	builder, handler := newFirstRunTestServer(t)

	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Command Post"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setupRec.Code, setupRec.Body.String())
	}

	if builder.server.isBrandNewProfile(context.Background()) {
		t.Fatal("expected existing workspace history to rule out brand-new classification")
	}
}

// TestIsBrandNewProfile_DegradesToFalseWhenPersonalHQErrors covers task 8.7:
// a failed Personal HQ dependency (e.g. a broken profile store) must never
// block the app from starting or force the intrusive guided takeover — it
// degrades to an ordinary Home launch instead.
func TestIsBrandNewProfile_DegradesToFalseWhenPersonalHQErrors(t *testing.T) {
	builder, _ := newFirstRunTestServer(t)
	builder.server.Storage.PersonalHQ = personalhq.NewService(erroringProfileStore{}, nil)

	if builder.server.isBrandNewProfile(context.Background()) {
		t.Fatal("expected a failed Personal HQ dependency to degrade to false (normal Home launch), not brand-new")
	}
}

// TestIsBrandNewProfile_DesignatedOrPastHQStateIsNotBrandNew covers the
// unaffected baseline: once hq_onboarding_state has genuinely moved past
// "unseen" through the app's own state-transition code (not a migration
// default), the original check alone already handles it correctly.
func TestIsBrandNewProfile_DesignatedOrPastHQStateIsNotBrandNew(t *testing.T) {
	builder, _ := newFirstRunTestServer(t)
	if _, err := builder.personalHQService.SetOnboardingState(context.Background(), userprofile.LocalUserID, userprofile.HQOnboardingSkipped); err != nil {
		t.Fatalf("SetOnboardingState: %v", err)
	}

	if builder.server.isBrandNewProfile(context.Background()) {
		t.Fatal("expected a profile that has already moved past unseen to not be classified brand-new")
	}
}
