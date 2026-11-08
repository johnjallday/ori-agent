package personalassistant

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

type folderStaffingActionHost struct {
	*fakeFolderSetup
	contextDigest string
	recommend     func(context.Context, FolderSetupRequest) (*rosterstaffing.Advice, error)
	choose        func(context.Context, FolderSetupRequest, rosterstaffing.RoleChoice, *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error)
}

func (h *folderStaffingActionHost) Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	plan, err := h.fakeFolderSetup.Plan(ctx, req)
	plan.HomeStaffing = &FolderHomeStaffingView{ContextDigest: h.contextDigest, State: "needs_choice"}
	return plan, err
}
func (h *folderStaffingActionHost) RecommendFolderHomeStaffing(ctx context.Context, req FolderSetupRequest) (*rosterstaffing.Advice, error) {
	return h.recommend(ctx, req)
}
func (h *folderStaffingActionHost) SelectFolderHomeStaffing(ctx context.Context, req FolderSetupRequest, choice rosterstaffing.RoleChoice, advice *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error) {
	return h.choose(ctx, req, choice, advice)
}

func folderStaffingActionsFixture(t *testing.T) (*buildSetupFixture, *folderStaffingActionHost, string) {
	t.Helper()
	fx := newBuildSetupFixture(t)
	view, err := fx.service.ReviewBuild(t.Context(), fx.draft, "session-1", fx.observation.ID, fx.candidate, "My song")
	if err != nil {
		t.Fatal(err)
	}
	host := &folderStaffingActionHost{fakeFolderSetup: fx.setup, contextDigest: strings.Repeat("c", 64)}
	fx.folderDigestFixture.service.deps.Setup = host
	return fx, host, view.ID
}

func TestFolderHomeStaffingActionsPreserveSeparateAdviceAndUneditedWitnesses(t *testing.T) {
	fx, host, id := folderStaffingActionsFixture(t)
	s := fx.folderDigestFixture.service
	calls := 0
	host.recommend = func(context.Context, FolderSetupRequest) (*rosterstaffing.Advice, error) {
		calls++
		return folderHomeReviewFixture().Advice, nil
	}
	host.choose = func(_ context.Context, _ FolderSetupRequest, choice rosterstaffing.RoleChoice, _ *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error) {
		return &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{{RoleID: choice.RoleID, Scope: "home", Mode: choice.Mode, Name: choice.Name, SourceWitness: "original-" + choice.RoleID}}}, nil
	}
	input := FolderHomeStaffingInput{RequestID: "recommend", Action: "recommend_staffing", ContextDigest: host.contextDigest}
	for range 2 {
		if _, err := s.ReviewHomeStaffing(t.Context(), "local", id, input); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := s.StoredOffer(t.Context(), "local", id)
	if err != nil || stored.HomeStaffingReview.Team != nil || stored.HomeStaffingReview.Advice == nil || calls != 1 || stored.Setup != nil {
		t.Fatal("advice selected/executed a team or replay inferred", err)
	}
	for i, role := range []string{"manager", "helper"} {
		input = FolderHomeStaffingInput{RequestID: role, Action: "choose_staffing", Revision: int64(i + 1), ContextDigest: host.contextDigest,
			Choice: &rosterstaffing.RoleChoice{RoleID: role, Mode: "assign", Name: role}}
		if _, err := s.ReviewHomeStaffing(t.Context(), "local", id, input); err != nil {
			t.Fatal(err)
		}
	}
	stored, _ = s.StoredOffer(t.Context(), "local", id)
	if len(stored.HomeStaffingReview.Team.Roles) != 2 || stored.HomeStaffingReview.Team.Roles[0].SourceWitness != "original-manager" || calls != 1 {
		t.Fatal("neighbouring choice renewed a witness or inferred")
	}
	input = FolderHomeStaffingInput{RequestID: "clear", Action: "clear_staffing", Revision: 3, RoleID: "manager"}
	if _, err := s.ReviewHomeStaffing(t.Context(), "local", id, input); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.StoredOffer(t.Context(), "local", id)
	if len(stored.HomeStaffingReview.Team.Roles) != 1 || stored.HomeStaffingReview.Team.Roles[0].SourceWitness != "original-helper" || stored.Setup != nil || fx.setup.runCount() != 0 {
		t.Fatal("clear renewed another role or executed setup")
	}
	// Clearing bounded draft state does not depend on the provider/setup host.
	s.deps.Setup = nil
	input = FolderHomeStaffingInput{RequestID: "clear-all", Action: "clear_staffing", Revision: 4}
	if _, err := s.ReviewHomeStaffing(t.Context(), "local", id, input); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.StoredOffer(t.Context(), "local", id)
	if stored.HomeStaffingReview.Team != nil || stored.HomeStaffingReview.Advice != nil || stored.HomeStaffingReview.Revision != 5 {
		t.Fatal("clear lost its late-reply fence")
	}
}

func TestFolderHomeStaffingLateInferenceLosesToClearWithoutHoldingLease(t *testing.T) {
	fx, host, id := folderStaffingActionsFixture(t)
	s := fx.folderDigestFixture.service
	var held atomic.Bool
	fx.service.ConfigureBuildReviews(func(context.Context, FolderOffer) error { return nil }, func(target foldercontext.Target) (func(), bool) {
		if target != fx.review || !held.CompareAndSwap(false, true) {
			return func() {}, false
		}
		return func() { held.Store(false) }, true
	})
	entered, finish := make(chan struct{}), make(chan struct{})
	host.recommend = func(context.Context, FolderSetupRequest) (*rosterstaffing.Advice, error) {
		if held.Load() {
			t.Error("inference held the mutation lease")
		}
		close(entered)
		<-finish
		return folderHomeReviewFixture().Advice, nil
	}
	input := FolderHomeStaffingInput{RequestID: "late", Action: "recommend_staffing", ContextDigest: host.contextDigest}
	done := make(chan error, 1)
	go func() { _, err := s.ReviewHomeStaffing(t.Context(), "local", id, input); done <- err }()
	<-entered
	duplicate := input
	duplicate.RequestID = "duplicate"
	_, duplicateErr := s.ReviewHomeStaffing(t.Context(), "local", id, duplicate)
	_, clearErr := s.ReviewHomeStaffing(t.Context(), "local", id, FolderHomeStaffingInput{RequestID: "clear", Action: "clear_staffing"})
	close(finish)
	lateErr := <-done
	if !errors.Is(duplicateErr, ErrFolderScanBusy) || clearErr != nil || !errors.Is(lateErr, ErrConflict) {
		t.Fatalf("duplicate/clear/late = %v/%v/%v", duplicateErr, clearErr, lateErr)
	}
	stored, err := s.StoredOffer(t.Context(), "local", id)
	if err != nil || stored.HomeStaffingReview.Revision != 1 || stored.HomeStaffingReview.Advice != nil || stored.HomeStaffingReview.Team != nil || stored.Setup != nil || held.Load() {
		t.Fatal("late advice overwrote clear or kept a lease", err)
	}
}

func TestFolderHomeStaffingChangedMaterialDuringInferenceDoesNotPersistAdvice(t *testing.T) {
	fx, host, id := folderStaffingActionsFixture(t)
	host.recommend = func(context.Context, FolderSetupRequest) (*rosterstaffing.Advice, error) {
		host.contextDigest = strings.Repeat("d", 64)
		return folderHomeReviewFixture().Advice, nil
	}
	_, err := fx.folderDigestFixture.service.ReviewHomeStaffing(t.Context(), "local", id, FolderHomeStaffingInput{RequestID: "changed", Action: "recommend_staffing", ContextDigest: host.contextDigest})
	if !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatal("changed declaration/target was accepted", err)
	}
	stored, _ := fx.folderDigestFixture.service.StoredOffer(t.Context(), "local", id)
	if stored.HomeStaffingReview != nil || stored.Setup != nil || fx.setup.runCount() != 0 {
		t.Fatal("changed context acquired draft/effect authority")
	}
}
