package chathttp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fileCuratorWelcomeReader interface {
	ReadCuratorWelcome(context.Context, string, string, string) (filejanitor.CuratorWelcome, error)
}

// SetFileCuratorWelcomeReader supplies stored-only filing context. The chat
// handler receives no scan, setup, approval or file-action methods.
func (h *Handler) SetFileCuratorWelcomeReader(reader fileCuratorWelcomeReader) {
	h.fileCuratorWelcome = reader
}

func (h *Handler) leadWelcomeContext(ctx context.Context, ws *workspace.Workspace, owner, instanceID string) string {
	curator, ok := ws.FileCurator()
	if !ok || curator.ID != instanceID {
		return leadWelcomeAssessment(ws)
	}
	var saved filejanitor.CuratorWelcome
	available := false
	if h.fileCuratorWelcome != nil {
		var err error
		saved, err = h.fileCuratorWelcome.ReadCuratorWelcome(ctx, owner, ws.ID, instanceID)
		available = err == nil
	}
	return fileCuratorWelcomeText(saved, available)
}

func fileCuratorWelcomeText(saved filejanitor.CuratorWelcome, available bool) string {
	choices := []string{}
	recommendation := "I’d start by checking File Janitor’s current status."
	intro := "I can’t verify the saved filing state right now, but we can still choose how to proceed."
	switch {
	case !available:
		choices = append(choices, "Check the filing review — open File Janitor’s Review tab to see its current status.")
	case !saved.Configured:
		intro = "I don’t have a verified folder setup to base filing suggestions on yet."
		choices = append(choices, "Prepare a first filing review — use File Janitor’s existing setup controls to review folder access and preparation.")
		recommendation = "I’d start with the first filing review, after you’ve reviewed its folder access."
	case saved.Pending > 0:
		intro = fmt.Sprintf("The saved filing batch has %d %s waiting for review.", saved.Pending, curatorItems(saved.Pending))
		choices = append(choices, "Review proposed filing — open File Janitor’s Review tab to see what would go where before deciding.")
		if saved.NeedsReview > 0 {
			choices = append(choices, fmt.Sprintf("Discuss uncertain files — work through the %d %s marked Needs review in that batch.", saved.NeedsReview, curatorItems(saved.NeedsReview)))
		}
		recommendation = "I’d start with the proposed filing so you can see the whole batch before deciding."
	default:
		intro = "There’s no pending filing batch in the saved records. That doesn’t mean the folder is empty or that everything has been filed."
		choices = append(choices, "Prepare a fresh filing review — choose Scan now in File Janitor when you want a new batch.")
		recommendation = "I’d start with a fresh filing review when you’re ready, so we can work from actual proposals."
	}
	choices = append(choices, "Understand the filing rules — discuss how classification, privacy and your approval work.")
	var text strings.Builder
	text.WriteString(intro + "\n\nWhat would you like to do?\n")
	for i, choice := range choices {
		text.WriteString("\n" + strconv.Itoa(i+1) + ". " + choice)
	}
	text.WriteString("\n\n" + recommendation + " You can reply with a number or ask a question. Choosing a topic here does not scan or approve anything; file moves still require your explicit approval in the review screen.")
	if available && saved.Configured {
		text.WriteString("\n\nSource: saved File Janitor records")
		if saved.Pending > 0 {
			if saved.BatchCompletedAt.IsZero() {
				text.WriteString("; batch date unavailable")
			} else {
				text.WriteString("; batch completed " + saved.BatchCompletedAt.UTC().Format("2006-01-02 15:04 MST"))
			}
		}
		if !saved.ReadAt.IsZero() {
			text.WriteString("; read " + saved.ReadAt.UTC().Format("2006-01-02 15:04 MST"))
		}
		text.WriteString(". Opening this conversation did not scan the folder or read file contents.")
	}
	return text.String()
}

func curatorItems(n int) string {
	if n == 1 {
		return "item"
	}
	return "items"
}
