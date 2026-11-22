package folderagent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
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
	// Lead is the words the drawer shows for the message in place of its
	// stored text: the lead-in while the card under it carries the question
	// (see ReportLead), and the plain opening once the owner has answered it
	// (see ReportOpening). Empty when the message is drawn as stored. The
	// stored text stays whole, because the agent reads it back on its next
	// turn.
	Lead string `json:"lead,omitempty"`
	// DeliveredAt is when the message was posted. It is not sent to the page:
	// the view compares it with the owner's answer to tell the question's own
	// message from a later look's.
	DeliveredAt time.Time `json:"-"`
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

// reportFolder is the folder as the Commander's message names it.
func reportFolder(v View) string {
	folder := strings.TrimSpace(v.FolderName)
	if folder == "" {
		folder = strings.TrimSpace(v.WorkspaceName)
	}
	return folder
}

// askedQuestion is the first question's sentence when the message being
// written (or drawn) is the one that asks it: the look reported, and the
// owner has not answered yet. A failed look asks nothing.
func askedQuestion(v View) string {
	if v.State == StateFailed || v.Premise == nil || v.Premise.State != PremiseAsked {
		return ""
	}
	return strings.TrimSpace(v.Premise.Sentence)
}

// questionAsked says the question the sentence before it asks, and the answers
// the card offers. The sentence alone is a statement ("This looks like a music
// production folder: …"): the question is whether that is right, and the answers
// are the card's two buttons. The agent reads the stored message on its next
// turn but never sees the card, so without this it cannot say what it asked or
// what the owner may answer (a real turn answered "the question itself never
// made it into the message").
func questionAsked(p *PremiseView) string {
	confirm, other := strings.TrimSpace(p.Confirm), strings.TrimSpace(p.Other)
	if confirm == "" || other == "" {
		return "Is that right?"
	}
	return "Is that right? The card offers two answers: “" + confirm + "” and “" + other + "”."
}

// reportFound is what the look found, in the message's own words: the agent's
// sentence about the folder, else the host's facts.
func reportFound(v View) string {
	if what := strings.TrimSpace(v.WhatItIs); what != "" {
		return what
	}
	if facts := strings.TrimSpace(v.FactsLine); facts != "" {
		return "I found " + facts + "."
	}
	return ""
}

// ReportOpening is the first words of a report: "I looked around <folder>."
// and what it found. A plan's message starts with it, and it is what the
// drawer shows for the question's message once the owner has answered, where
// the stored question, its answers and "answer it in the card below" no longer
// describe a card that has no buttons.
func ReportOpening(v View) string {
	opening := "I looked around " + reportFolder(v) + "."
	if found := reportFound(v); found != "" {
		opening += " " + found
	}
	return opening
}

// answeredQuestionMessage reports whether the report message the view points
// at is the one that asked the first question and the owner has since
// answered it: the answer came after the message was posted. A later look's
// report is posted after the answer, so it is not this.
func answeredQuestionMessage(rec *Record, ref *ChatRef) bool {
	answer := currentPremise(rec)
	return answer != nil && ref != nil && !ref.DeliveredAt.IsZero() && !answer.At.Before(ref.DeliveredAt)
}

// ReportLead is the Commander's whole message as the drawer shows it while
// the question is asked: a lead-in, with the card under it carrying the
// question. The stored message says the question too (the agent reads that
// when asked "what did you ask me?"), so the drawer shows this line instead of
// the stored text, and the owner sees the question once. "" when this message
// does not ask the question (a plan, a failure, an answered question), which
// leaves it drawn as stored. It is always the first paragraph of ReportText.
func ReportLead(v View) string {
	if askedQuestion(v) == "" {
		return ""
	}
	return "I looked around " + reportFolder(v) + ". One question first:"
}

// ReportText is the report as the Commander says it in the conversation: what
// it found, what it suggests, and that the choice is in the card below. The
// same words are what the agent reads back if the owner asks about the plan
// (or about the question it asked).
func ReportText(v View) string {
	folder := reportFolder(v)
	if v.State == StateFailed {
		text := "I couldn't finish looking around " + folder + "."
		if failure := strings.TrimSpace(v.Failure); failure != "" {
			text += " " + failure
		}
		return text + "\n\nPress Look further below to try again."
	}
	found := reportFound(v)
	// While the question is asked the message is its lead-in, the question, and
	// what the agent found. The drawer shows the lead-in and the card, so the
	// owner reads the question once; the stored text keeps all of it for the
	// agent's next turn.
	if question := askedQuestion(v); question != "" {
		parts := []string{ReportLead(v), question + " " + questionAsked(v.Premise)}
		if found != "" {
			parts = append(parts, found)
		}
		return strings.Join(append(parts, "Answer it in the card below."), "\n\n")
	}
	parts := []string{ReportOpening(v)}
	if v.RootPolicy == folderlook.RootDownloads {
		return strings.Join(append(parts, "Choose Review Downloads to open Filing. Nothing is scanned or moved just by opening the review."), "\n\n")
	}
	// The card under the message says why the agent could not look itself
	// (ModelSentence) and lists the steps with their details; the message keeps
	// to what it found and what it suggests, so the two do not say it twice.
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
