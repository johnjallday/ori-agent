package folderagent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Report is a look that ended with something for the owner (a plan, a
// question or a failure), worded for the Commander's conversation on the
// workspace page (workspace chat sync, Plan B). The text is the host's: the
// folder's name, the host's facts and step titles, and the agent's bounded
// fields the card already shows. Nothing in it is executed.
type Report struct {
	WorkspaceID string
	OwnerUserID string
	AgentName   string
	FolderName  string
	// Key names this look's end, so a report is posted once however often it
	// is announced (a sweep after a restart announces it again).
	Key  string
	Text string
}

// Reports posts a finished look's report into the Commander's conversation,
// where the drawer draws the report's own card under it, and says where the
// latest one is. Nil posts nothing. Reported must not block for long: it runs
// on the look's goroutine.
type Reports interface {
	Reported(ctx context.Context, report Report)
	// Latest is the newest report message still in its conversation; ok is
	// false when there is none.
	Latest(ctx context.Context, workspaceID string) (ChatRef, bool)
	// Seen marks one report message seen by the owner.
	Seen(ctx context.Context, workspaceID, messageID string) error
}

// ChatRef is where the report was posted: the drawer opens SessionID and
// draws the report's card under MessageID. Unread until the owner has seen it.
type ChatRef struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Unread    bool   `json:"unread"`
}

// ErrChatUnavailable is a report message that is not (or no longer) this
// workspace's: deleted with its conversation, or never posted.
var ErrChatUnavailable = errors.New("folder agent: report message unavailable")

// ChatSeen marks the workspace's report message seen, for its owner only.
func (s *Service) ChatSeen(ctx context.Context, userID, workspaceID, messageID string) error {
	if _, err := s.Record(userID, workspaceID); err != nil {
		return err
	}
	if s.deps.Reports == nil {
		return ErrChatUnavailable
	}
	return s.deps.Reports.Seen(ctx, workspaceID, messageID)
}

// chatRef is the view's pointer to the report in the conversation, nil when
// none was posted (or Reports is not wired).
func (s *Service) chatRef(ctx context.Context, workspaceID string) *ChatRef {
	if s.deps.Reports == nil || workspaceID == "" {
		return nil
	}
	ref, ok := s.deps.Reports.Latest(ctx, workspaceID)
	if !ok {
		return nil
	}
	return &ref
}

// announce tells Reports that rec's look just ended with something for the
// owner. Only a report or a failure is announced; every other state is not.
func (s *Service) announce(ctx context.Context, rec *Record) {
	if s.deps.Reports == nil || rec == nil {
		return
	}
	if rec.State != StateReported && rec.State != StateFailed {
		return
	}
	v := s.viewOf(ctx, rec)
	s.deps.Reports.Reported(ctx, Report{
		WorkspaceID: rec.WorkspaceID,
		OwnerUserID: rec.OwnerUserID,
		AgentName:   rec.AgentName,
		FolderName:  rec.FolderName,
		Key:         ReportKey(rec),
		Text:        ReportText(v),
	})
}

// ReportKey is one look's end: its state and the moment it was saved.
func ReportKey(rec *Record) string {
	if rec == nil {
		return ""
	}
	return string(rec.State) + "@" + rec.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// ReportText is the report as the Commander says it in the conversation: what
// it found, what it suggests, and that the choice is in the card below. The
// same words are what the agent reads back if the owner asks about the plan.
func ReportText(v View) string {
	folder := strings.TrimSpace(v.FolderName)
	if folder == "" {
		folder = strings.TrimSpace(v.WorkspaceName)
	}
	if v.State == StateFailed {
		text := "I couldn't finish looking around " + folder + "."
		if failure := strings.TrimSpace(v.Failure); failure != "" {
			text += " " + failure
		}
		return text + "\n\nPress Look further below to try again."
	}
	parts := []string{"I looked around " + folder + "."}
	if what := strings.TrimSpace(v.WhatItIs); what != "" {
		parts[0] += " " + what
	} else if facts := strings.TrimSpace(v.FactsLine); facts != "" {
		parts[0] += " I found " + facts + "."
	}
	// The card under the message says why the agent could not look itself
	// (ModelSentence) and lists the steps with their details; the message keeps
	// to what it found and what it suggests, so the two do not say it twice.
	if v.Premise != nil && v.Premise.State == PremiseAsked && strings.TrimSpace(v.Premise.Sentence) != "" {
		parts = append(parts, "One question first: "+strings.TrimSpace(v.Premise.Sentence))
		return strings.Join(append(parts, "Answer it in the card below."), "\n\n")
	}
	var chosen []string
	if v.Plan != nil {
		for _, step := range v.Plan.Steps {
			if step.Chosen && strings.TrimSpace(step.Title) != "" {
				chosen = append(chosen, strings.TrimSpace(step.Title))
			}
		}
	}
	switch len(chosen) {
	case 0:
		parts = append(parts, "I have no setup to suggest for it yet.")
	case 1:
		parts = append(parts, "I suggest one step: "+chosen[0]+".")
	default:
		parts = append(parts, "I suggest "+wordCount(len(chosen))+" steps: "+strings.Join(chosen, "; ")+".")
	}
	if goal := strings.TrimSpace(v.SuggestedGoal); goal != "" {
		parts = append(parts, "A goal I'd work toward: "+goal)
	}
	parts = append(parts, "Choose Set it up, Keep the folder as it is, or Look further below.")
	return strings.Join(parts, "\n\n")
}

func wordCount(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}
