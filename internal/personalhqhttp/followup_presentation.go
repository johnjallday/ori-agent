package personalhqhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// NoteSourceReader reads the user's connected notes folder, so a follow-up made
// from a note can name the vault or folder it came from.
// *personalassistant.SecondBrainService implements it.
type NoteSourceReader interface {
	ConnectedSource(ctx context.Context, userID string) (secondbrain.Source, bool, error)
}

// SetNoteSources wires the connected-folder reader. It is a factory because
// the second-brain service is built after this handler during startup.
func (h *Handler) SetNoteSources(factory func() NoteSourceReader) {
	if h == nil {
		return
	}
	h.noteSources = factory
}

// followUpPresentation is how a workspace's follow-up panel names where a
// follow-up came from (PRD hq-details-landing FR16). It is computed on every
// read and never stored; the follow-up's detail stays as it is.
type followUpPresentation struct {
	// SourceLabel names the place: the notes folder, "Email Ops", "Journal",
	// or nothing for a follow-up the user added.
	SourceLabel string `json:"source_label"`
	// SourceName names the thing in that place: the note, or the person.
	SourceName string `json:"source_name"`
	// Link opens the note. Only an obsidian://open? link is ever set.
	Link string `json:"link"`
}

// workspaceFollowUpView is a follow-up as a workspace's follow-up panel reads it.
type workspaceFollowUpView struct {
	followUpView
	Presentation followUpPresentation `json:"presentation"`
}

// Labels a follow-up's source line uses when nothing more specific is known.
const (
	noteSourceFallbackLabel = "Notes"
	emailSourceLabel        = "Email Ops"
	journalSourceLabel      = "Journal"
	obsidianOpenLinkPrefix  = "obsidian://open?"
)

// workspaceFollowUpViews adds the presentation to each follow-up view. The
// connected notes folder is read at most once, and only when a note follow-up
// needs its name; a failed read falls back to "Notes".
func (h *Handler) workspaceFollowUpViews(ctx context.Context, userID string, items []*followup.FollowUp) []workspaceFollowUpView {
	base := h.followUpViews(items)
	noteLabel := ""
	views := make([]workspaceFollowUpView, 0, len(base))
	for _, view := range base {
		out := workspaceFollowUpView{followUpView: view}
		if view.FollowUp != nil {
			if view.Source.Type == secondbrain.FollowUpSourceType && noteLabel == "" {
				noteLabel = h.noteSourceLabel(ctx, userID)
			}
			out.Presentation = presentFollowUp(view.FollowUp, noteLabel)
		}
		views = append(views, out)
	}
	return views
}

// noteSourceLabel is the connected folder's name, or "Notes" when it cannot be
// read.
func (h *Handler) noteSourceLabel(ctx context.Context, userID string) string {
	if h == nil || h.noteSources == nil {
		return noteSourceFallbackLabel
	}
	reader := h.noteSources()
	if reader == nil {
		return noteSourceFallbackLabel
	}
	source, connected, err := reader.ConnectedSource(ctx, userID)
	if err != nil || !connected {
		return noteSourceFallbackLabel
	}
	if name := strings.TrimSpace(source.Name); name != "" {
		return name
	}
	return noteSourceFallbackLabel
}

// presentFollowUp builds one follow-up's presentation. noteLabel names the
// notes folder for a note follow-up.
func presentFollowUp(item *followup.FollowUp, noteLabel string) followUpPresentation {
	switch item.Source.Type {
	case secondbrain.FollowUpSourceType:
		note, link := secondbrain.SplitDetail(item.Detail)
		if noteLabel == "" {
			noteLabel = noteSourceFallbackLabel
		}
		out := followUpPresentation{SourceLabel: noteLabel, SourceName: noteDisplayName(note)}
		if strings.HasPrefix(link, obsidianOpenLinkPrefix) {
			out.Link = link
		}
		return out
	case "email_thread":
		return followUpPresentation{SourceLabel: emailSourceLabel, SourceName: strings.TrimSpace(item.Counterparty)}
	case "journal":
		return followUpPresentation{SourceLabel: journalSourceLabel}
	default:
		return followUpPresentation{}
	}
}

// noteDisplayName is a note path's file name without its Markdown extension.
func noteDisplayName(note string) string {
	note = strings.TrimSpace(strings.ReplaceAll(note, "\\", "/"))
	if index := strings.LastIndex(note, "/"); index >= 0 {
		note = note[index+1:]
	}
	lower := strings.ToLower(note)
	for _, ext := range []string{".markdown", ".md"} {
		if strings.HasSuffix(lower, ext) && len(note) > len(ext) {
			return note[:len(note)-len(ext)]
		}
	}
	return note
}
