package personalassistant

import (
	"context"
	"errors"
	"testing"
)

type todayReviewFixture struct {
	items []KnowledgeReviewItem
	err   error
}

func (r todayReviewFixture) ReviewItems(context.Context, string) ([]KnowledgeReviewItem, error) {
	return r.items, r.err
}

type todayInterviewFixture struct {
	interview   *KnowledgeInterview
	preferences []ConfirmedInterviewPreference
	readErr     error
	profileErr  error
}

func (r todayInterviewFixture) Read(context.Context, string) (*KnowledgeInterview, error) {
	return r.interview, r.readErr
}
func (r todayInterviewFixture) ConfirmedProfilePreferences(context.Context, string) ([]ConfirmedInterviewPreference, error) {
	return r.preferences, r.profileErr
}

func TestTodayRememberedIsTruthfulForMissingFailedPartialAndPausedSources(t *testing.T) {
	ctx := context.Background()
	service := &TodayService{remembered: todayReviewFixture{err: errors.New("canonical source unavailable")}}
	var out TodayProjection
	service.loadRemembered(ctx, "local", APIStateActive, &out)
	if out.Remembered.Health.Status != TodaySectionUnavailable || len(out.Remembered.Items) != 0 {
		t.Fatalf("failed source misrepresented: %+v", out.Remembered)
	}
	service.remembered = todayReviewFixture{}
	out = TodayProjection{}
	service.loadRemembered(ctx, "local", APIStateActive, &out)
	if out.Remembered.Health.Status != TodaySectionHealthyEmpty || len(out.Remembered.Items) != 0 {
		t.Fatalf("healthy empty source misrepresented: %+v", out.Remembered)
	}
	service.remembered = todayReviewFixture{items: []KnowledgeReviewItem{{ID: "priority", Category: "projects", State: KnowledgeApproved, Text: "Finish the plan"}}}
	service.interviewPreferences = todayInterviewFixture{profileErr: errors.New("unavailable")}
	out = TodayProjection{}
	service.loadRemembered(ctx, "local", APIStateActive, &out)
	if out.Remembered.Health.Status != TodaySectionPartial || len(out.Remembered.Items) != 1 || out.Remembered.Items[0].Title != "Finish the plan" {
		t.Fatalf("partially available recap misrepresented: %+v", out.Remembered)
	}
	out = TodayProjection{}
	service.loadRemembered(ctx, "local", APIStatePaused, &out)
	if out.Remembered.Health.Status != TodaySectionUnavailable || len(out.Remembered.Items) != 0 {
		t.Fatalf("paused assistant received recap: %+v", out.Remembered)
	}
}
