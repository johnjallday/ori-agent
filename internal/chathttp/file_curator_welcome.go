package chathttp

import (
	"context"
	"fmt"

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
	// Readiness can be saved before preparation's scan commits. An unavailable
	// read or no saved scan is not an assessment: keep its one delivery slot
	// open for the real saved result, without retrying any preparation here.
	if !available || !saved.Configured || !saved.HasSavedScan {
		return ""
	}
	return fileCuratorWelcomeText(saved, true)
}

func fileCuratorWelcomeText(saved filejanitor.CuratorWelcome, available bool) string {
	text := "I can’t verify the saved filing state right now. Check File Janitor’s Review tab or try again later."
	switch {
	case !available:
	case !saved.Configured:
		text = "Folder setup isn’t verified yet. Use File Janitor’s existing setup controls when you’re ready."
	case saved.Pending > 0:
		text = fmt.Sprintf("The saved filing batch has %d %s ready for review.", saved.Pending, curatorItems(saved.Pending))
		if saved.NeedsReview > 0 {
			text += fmt.Sprintf(" %d marked Needs review.", saved.NeedsReview)
		}
		text += " Use Review files for the current proposals."
	default:
		text = "There’s no pending filing batch in the saved records. That doesn’t mean the folder is empty. Choose Scan now explicitly if you want fresh proposals."
	}
	text += "\n\nYou can ask about filing rules or uncertain files. Nothing moves without your explicit approval in File Janitor. Opening this conversation does not scan the folder or read file contents."
	if available && saved.Configured {
		text += "\n\nSource: saved File Janitor records"
		if saved.Pending > 0 {
			if saved.BatchCompletedAt.IsZero() {
				text += "; batch date unavailable"
			} else {
				text += "; batch completed " + saved.BatchCompletedAt.UTC().Format("2006-01-02 15:04 MST")
			}
		}
		text += "."
	}
	return text
}

func curatorItems(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}
