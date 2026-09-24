package personalassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type failingInterviewProfileCAS struct {
	*userprofile.SQLiteStore
	failNext bool
}

func (s *failingInterviewProfileCAS) UpdateFieldCASAt(ctx context.Context, userID, field string, before time.Time, old, value string, writtenAt time.Time) (*userprofile.UserProfile, error) {
	if s.failNext {
		s.failNext = false
		return nil, errors.New("injected SQL failure")
	}
	return s.SQLiteStore.UpdateFieldCASAt(ctx, userID, field, before, old, value, writtenAt)
}

func interviewProfileOnlyRequest(t *testing.T, f *knowledgeFixture, version int64, id string) KnowledgeInterviewSaveRequest {
	t.Helper()
	profile, err := f.profileStore.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return KnowledgeInterviewSaveRequest{StateVersion: version, RequestID: id, Rows: []KnowledgeInterviewReviewedRow{{
		RowID: "communication", Destination: "profile", Category: "how_you_work", Text: "concise", Preference: "response_style",
		ExpectedProfileUpdatedAt: profile.UpdatedAt, ExpectedProfileValue: profile.Preferences["response_style"],
	}}}
}

func TestKnowledgeInterviewProfilePreparedSQLFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	f, service, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "prepared-profile-sql-failure")
	store := &failingInterviewProfileCAS{SQLiteStore: f.profileStore, failNext: true}
	service.SetCanonicalSavers(NewKnowledgeLifecycleService(service.store, f.memory, nil), store)
	result, err := service.SaveReviewed(ctx, "local", request)
	if err == nil || len(result.SavedRows) != 0 || len(result.PendingRows) != 1 {
		t.Fatalf("failed SQL write claimed a saved preference: %+v %v", result, err)
	}
	status, err := service.Read(ctx, "local")
	if err != nil || status.ProfileWrite == nil || len(status.RowReceipts) != 0 || status.ProfileWrite.AfterHash != hashKnowledgeLine("concise") {
		t.Fatalf("prepared record missing or leaked text: %+v %v", status, err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	doc.Interview.ProfileWrite.AfterHash = strings.Repeat("x", 64)
	if !errors.Is(validateKnowledge(doc), ErrKnowledgeCorrupt) {
		t.Fatal("malformed prepared profile hash was accepted")
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "brief" {
		t.Fatalf("failed SQL write changed the profile: %+v %v", profile, err)
	}
	restart := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restart.SetCanonicalSavers(NewKnowledgeLifecycleService(restart.store, f.memory, nil), f.profileStore)
	result, err = restart.SaveReviewed(ctx, "local", request)
	if err != nil || result.Status != KnowledgeInterviewCompleted || len(result.SavedRows) != 1 {
		t.Fatalf("prepared SQL retry failed: %+v %v", result, err)
	}
	status, err = restart.Read(ctx, "local")
	if err != nil || status.ProfileWrite != nil || len(status.RowReceipts) != 1 {
		t.Fatalf("SQL retry left an unfinished sidecar: %+v %v", status, err)
	}
}

func TestKnowledgeInterviewProfileRestartAfterSQLBeforeReceipt(t *testing.T) {
	ctx := context.Background()
	f, service, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "prepared-profile-receipt-failure")
	writes := 0
	service.store.beforeRename = func() error {
		writes++
		if writes == 4 { // offer, reserve, prepare, then fail final receipt
			return errors.New("injected receipt persistence failure")
		}
		return nil
	}
	result, err := service.SaveReviewed(ctx, "local", request)
	if err == nil || len(result.SavedRows) != 0 || len(result.PendingRows) != 1 {
		t.Fatalf("failed receipt claimed an interview save: %+v %v (writes=%d)", result, err, writes)
	}
	status, err := service.Read(ctx, "local")
	if err != nil || status.ProfileWrite == nil || len(status.RowReceipts) != 0 {
		t.Fatalf("prepared profile metadata missing on restart: %+v %v", status, err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "concise" || !profile.UpdatedAt.Equal(status.ProfileWrite.WrittenAt) {
		t.Fatalf("verified SQL version did not survive: %+v %v", profile, err)
	}
	restart := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restart.SetCanonicalSavers(NewKnowledgeLifecycleService(restart.store, f.memory, nil), f.profileStore)
	result, err = restart.SaveReviewed(ctx, "local", request)
	if err != nil || result.Status != KnowledgeInterviewCompleted || len(result.SavedRows) != 1 {
		t.Fatalf("exact SQL receipt recovery failed: %+v %v", result, err)
	}
	replayed, err := restart.SaveReviewed(ctx, "local", request)
	if err != nil || replayed.Status != KnowledgeInterviewCompleted {
		t.Fatalf("durable exact replay failed: %+v %v", replayed, err)
	}
}

func TestKnowledgeInterviewProfileRestartNeverRevertsOutsideEditAfterSQL(t *testing.T) {
	ctx := context.Background()
	f, service, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "profile-edited-after-sql")
	writes := 0
	service.store.beforeRename = func() error {
		writes++
		if writes == 4 {
			return errors.New("injected receipt failure")
		}
		return nil
	}
	if _, err := service.SaveReviewed(ctx, "local", request); err == nil {
		t.Fatal("expected lost receipt after SQL")
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "concise" {
		t.Fatalf("initial SQL operation was not durable: %+v %v", profile, err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "concise", "detailed"); err != nil {
		t.Fatal(err)
	}
	restarted := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restarted.SetCanonicalSavers(NewKnowledgeLifecycleService(restarted.store, f.memory, nil), f.profileStore)
	result, err := restarted.SaveReviewed(ctx, "local", request)
	if !errors.Is(err, ErrConflict) || len(result.SavedRows) != 0 {
		t.Fatalf("retry overwrote a newer profile edit: %+v %v", result, err)
	}
	profile, err = f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "detailed" {
		t.Fatalf("outside edit lost: %+v %v", profile, err)
	}
	status, err := restarted.Read(ctx, "local")
	if err != nil || status.ProfileWrite == nil || len(status.RowReceipts) != 0 {
		t.Fatalf("stale prepared profile was falsely finalized: %+v %v", status, err)
	}
}

func TestKnowledgeInterviewConcurrentProfileReplayUsesOneCanonicalVersion(t *testing.T) {
	ctx := context.Background()
	f, first, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "concurrent-profile-save")
	second := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	second.SetCanonicalSavers(NewKnowledgeLifecycleService(second.store, f.memory, nil), f.profileStore)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, service := range []*KnowledgeInterviewService{first, second} {
		wg.Add(1)
		go func(service *KnowledgeInterviewService) {
			defer wg.Done()
			_, err := service.SaveReviewed(ctx, "local", request)
			results <- err
		}(service)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent exact save failed: %v", err)
		}
	}
	status, err := first.Read(ctx, "local")
	if err != nil || status.Status != KnowledgeInterviewCompleted || len(status.RowReceipts) != 1 || status.ProfileWrite != nil {
		t.Fatalf("concurrent save duplicated or failed: %+v %v", status, err)
	}
}

func TestKnowledgeInterviewProfileSavedReceiptRecoversLostCompletion(t *testing.T) {
	ctx := context.Background()
	f, service, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "prepared-profile-lost-completion")
	writes := 0
	service.store.beforeRename = func() error {
		writes++
		if writes == 5 { // offer, reserve, prepare, saved receipt, completion
			return errors.New("injected completion failure")
		}
		return nil
	}
	result, err := service.SaveReviewed(ctx, "local", request)
	if err == nil || len(result.SavedRows) != 1 || result.Status == KnowledgeInterviewCompleted {
		t.Fatalf("failed completion misreported: %+v %v (writes=%d)", result, err, writes)
	}
	status, err := service.Read(ctx, "local")
	if err != nil || len(status.RowReceipts) != 1 || status.ProfileWrite != nil {
		t.Fatalf("saved receipt lost with completion: %+v %v", status, err)
	}
	restart := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restart.SetCanonicalSavers(NewKnowledgeLifecycleService(restart.store, f.memory, nil), f.profileStore)
	result, err = restart.SaveReviewed(ctx, "local", request)
	if err != nil || result.Status != KnowledgeInterviewCompleted || len(result.SavedRows) != 1 {
		t.Fatalf("saved receipt could not finish after restart: %+v %v", result, err)
	}
}

func TestKnowledgeInterviewPreparedProfileNeverAdoptsCoincidentallyMatchingOutsideSQL(t *testing.T) {
	ctx := context.Background()
	f, service, version := interviewSaveFixture(t)
	request := interviewProfileOnlyRequest(t, f, version, "prepared-outside-same-wording")
	store := &failingInterviewProfileCAS{SQLiteStore: f.profileStore, failNext: true}
	service.SetCanonicalSavers(NewKnowledgeLifecycleService(service.store, f.memory, nil), store)
	if _, err := service.SaveReviewed(ctx, "local", request); err == nil {
		t.Fatal("expected injected SQL failure")
	}
	before, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", before.UpdatedAt, "brief", "concise"); err != nil {
		t.Fatal(err)
	}
	restart := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restart.SetCanonicalSavers(NewKnowledgeLifecycleService(restart.store, f.memory, nil), f.profileStore)
	result, err := restart.SaveReviewed(ctx, "local", request)
	if !errors.Is(err, ErrConflict) || len(result.SavedRows) != 0 {
		t.Fatalf("outside matching SQL edit was adopted: %+v %v", result, err)
	}
	status, err := restart.Read(ctx, "local")
	if err != nil || len(status.RowReceipts) != 0 {
		t.Fatalf("outside edit got a saved receipt: %+v %v", status, err)
	}
	current, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	fresh := request
	fresh.RequestID = "new-reviewed-profile-write"
	fresh.ResetPartial = true
	fresh.Rows = append([]KnowledgeInterviewReviewedRow(nil), request.Rows...)
	fresh.Rows[0].ExpectedProfileUpdatedAt = current.UpdatedAt
	fresh.Rows[0].ExpectedProfileValue = "concise"
	finished, err := restart.SaveReviewed(ctx, "local", fresh)
	if err != nil || finished.Status != KnowledgeInterviewCompleted {
		t.Fatalf("new explicit review could not abandon stale intent: %+v %v", finished, err)
	}
}
