package personalassistant

import (
	"context"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

// SetRememberedReview reads only approved current canonical revisions through
// the same source/target eligibility gate used by the dossier. It creates no
// tasks, brief, source checks, provider calls, or new memory.
func (s *TodayService) SetRememberedReview(reader interface {
	ReviewItems(context.Context, string) ([]KnowledgeReviewItem, error)
}) {
	if s != nil {
		s.remembered = reader
	}
}

func (s *TodayService) SetInterviewPreferences(reader interface {
	ConfirmedProfilePreferences(context.Context, string) ([]ConfirmedInterviewPreference, error)
	Read(context.Context, string) (*KnowledgeInterview, error)
}) {
	if s != nil {
		s.interviewPreferences = reader
	}
}

func (s *TodayService) loadRemembered(ctx context.Context, userID string, state APIState, out *TodayProjection) {
	if out == nil {
		return
	}
	// For an existing HQ, lack of a recorded offer is not a blocker: the
	// voluntary interview can always be started from the dossier. This is a
	// read, not a new automatic offer or a prompt on each Home refresh.
	if s != nil && s.interviewPreferences != nil {
		if interview, err := s.interviewPreferences.Read(ctx, userID); err == nil {
			out.InterviewStatus = "available"
			if interview != nil {
				out.InterviewStatus = string(interview.Status)
			}
		}
	}
	if state == APIStatePaused {
		out.Remembered.Health = todayUnavailable("assistant_paused")
		return
	}
	if s == nil || s.remembered == nil {
		out.Remembered.Health = todayUnavailable("review_store_unavailable")
		return
	}
	views, err := s.remembered.ReviewItems(ctx, userID)
	if err != nil {
		out.Remembered.Health = todayUnavailable("review_read_failed")
		return
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].UpdatedAt.After(views[j].UpdatedAt) })
	appendFact := func(fact KnowledgeReviewItem) {
		out.Remembered.Items = append(out.Remembered.Items, TodayItem{
			ID: fact.ID, Kind: "reviewed_memory", Title: fact.Text,
			Detail: "Confirmed " + strings.ReplaceAll(fact.Category, "_", " ") + " · Personal HQ",
			Route:  "/profile#personalHQKnowledge", Ref: dailybrief.SourceRef{}, SourceAt: fact.UpdatedAt,
		})
	}
	for _, fact := range views {
		if fact.State == KnowledgeApproved && fact.ReviewUnavailable == "" && fact.Category == "projects" && strings.TrimSpace(fact.Text) != "" {
			appendFact(fact)
			break
		}
	}
	var profileErr error
	if s.interviewPreferences != nil {
		preferences, err := s.interviewPreferences.ConfirmedProfilePreferences(ctx, userID)
		profileErr = err
		if err == nil && len(preferences) > 0 {
			current := preferences[0]
			out.Remembered.Items = append(out.Remembered.Items, TodayItem{
				ID: "profile-" + strings.TrimPrefix(current.Field, "preferences."), Kind: "reviewed_preference",
				Title: current.Text, Detail: "Confirmed global " + strings.ReplaceAll(strings.TrimPrefix(current.Field, "preferences."), "_", " "),
				Route: "/profile", Ref: dailybrief.SourceRef{}, SourceAt: current.ReviewedAt,
			})
		}
	}
	if len(out.Remembered.Items) < 2 {
		for _, fact := range views {
			if fact.State == KnowledgeApproved && fact.ReviewUnavailable == "" && fact.Category == "how_you_work" && strings.TrimSpace(fact.Text) != "" {
				appendFact(fact)
				break
			}
		}
	}
	if len(out.Remembered.Items) == 0 {
		if profileErr != nil {
			out.Remembered.Health = todayUnavailable("profile_read_failed")
		} else {
			out.Remembered.Health = TodaySourceHealth{Status: TodaySectionHealthyEmpty}
		}
		return
	}
	// Link one *existing* priority/follow-up when available; this is navigation
	// only, not a newly created action or a synthetic Daily Brief claim.
	var next *TodayItem
	if len(out.Priorities.Items) > 0 {
		next = &out.Priorities.Items[0]
	} else if len(out.FollowUps.Items) > 0 {
		next = &out.FollowUps.Items[0]
	}
	if next != nil {
		out.Remembered.Items = append(out.Remembered.Items, TodayItem{
			ID: next.ID, Kind: "existing_next_action", Title: "Next from Today: " + next.Title,
			Detail: "Existing action — review it at its source", Route: next.Route, Ref: next.Ref, SourceAt: next.SourceAt,
		})
	}
	if profileErr != nil {
		out.Remembered.Health = TodaySourceHealth{Status: TodaySectionPartial, Reason: "profile_read_failed"}
	} else {
		out.Remembered.Health = TodaySourceHealth{Status: TodaySectionAvailable}
	}
}
