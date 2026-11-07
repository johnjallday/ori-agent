package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

type leadConversationStore interface {
	EnsureLeadConversation(context.Context, session.LeadConversationInput) (session.LeadConversationResult, error)
}

// LeadConversationOpen delivers stored evidence into the lead's ordinary chat.
// It never calls a model, starts a review, reads project files, or executes tools.
// POST because creating a conversation/message is a durable, idempotent effect.
func (h *Handler) LeadConversationOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
	decoder.DisallowUnknownFields()
	var empty struct{}
	if err := decoder.Decode(&empty); err != nil && !errors.Is(err, io.EOF) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&empty); !errors.Is(err, io.EOF) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	store, ok := h.sessionStore.(leadConversationStore)
	if !ok || h.workspaceStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id := strings.TrimSpace(r.PathValue("workspaceID"))
	var current func(context.Context) (string, error)
	if h.userProvider != nil {
		current = h.userProvider.CurrentUserID
	}
	owner, err := workspaceupdates.ResolveOwner(r.Context(), current)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	out, err := workspaceupdates.Deliver(r.Context(), h, store, owner, id)
	if errors.Is(err, workspaceupdates.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if errors.Is(err, session.ErrWorkspaceVersionConflict) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, out)
}

// ComposeLeadWelcome reads the same owner-scoped canonical workspace and exact
// runtime lead for both HTTP fallback and background delivery. It does not write
// messages, activate setup, inspect project files, or call a provider.
func (h *Handler) ComposeLeadWelcome(ctx context.Context, owner, id string) (session.LeadConversationInput, string, error) {
	var input session.LeadConversationInput
	if h.workspaceStore == nil {
		return input, "", workspaceupdates.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return input, "", err
	}
	ws, err := workspaceupdates.ReadWorkspace(h.workspaceStore, id)
	if err != nil {
		return input, "", err
	}
	wsOwner := strings.TrimSpace(ws.OwnerUserID)
	if wsOwner == "" {
		wsOwner = userprofile.LocalUserID
	}
	if owner != wsOwner {
		return input, "", workspaceupdates.ErrNotFound
	}
	unavailable := func() (session.LeadConversationInput, string, error) {
		return input, workspaceupdates.StateUnavailable, nil
	}
	if ws.Kind == "group" || ws.AssistantProgramState != nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return unavailable()
	}
	if setup := ws.SetupWizardState(); setup != workspace.SetupWizardStateNotApplicable && setup != workspace.SetupWizardStateReady {
		return input, workspaceupdates.StateWaitingSetup, nil
	}
	name := strings.TrimSpace(ws.EntryAgentName())
	if name == "" {
		return unavailable()
	}
	ag, err := h.resolveEffectiveAgent(name, normalizedChatRouteContext{WorkspaceID: id})
	if err != nil || ag.WorkspaceTools == nil {
		return unavailable()
	}
	live, err := ag.WorkspaceTools.assessmentWorkspace()
	if err != nil {
		return unavailable()
	}
	if live.Version != ws.Version {
		return input, "", session.ErrWorkspaceVersionConflict
	}
	intro := "Hi, I’m " + welcomeText(name, 100) + ", the lead for " + welcomeText(ws.Name, 120) + "."
	if goal := workspace.GoalOutcomeLine(ws.Mission); goal != "" {
		intro += " I’ll help you work toward: " + welcomeText(goal, 500)
	}
	assessment := h.leadWelcomeContext(ctx, ws, owner, ag.WorkspaceTools.executingInstanceID)
	curator, isCurator := ws.FileCurator()
	if assessment == "" && isCurator && curator.ID == ag.WorkspaceTools.executingInstanceID {
		intro += "\n\nYour saved filing review will appear here when it is available. Nothing moves without your approval in File Janitor."
	} else if assessment == "" {
		intro += "\n\nI don’t have a saved assessment yet. Ask me about your goal, or choose a review in Goal settings when you’re ready."
		if strings.TrimSpace(ws.Mission) == "" {
			intro += "\n\nWhat outcome would you like us to work toward?"
		}
	}
	return session.LeadConversationInput{
		WorkspaceID: id, WorkspaceVersion: ws.Version, OwnerUserID: owner,
		AgentInstanceID: ag.WorkspaceTools.executingInstanceID, AgentName: name,
		Introduction: intro, Assessment: assessment,
	}, workspaceupdates.StateReady, nil
}

// Reuse the assessment and task suggestions produced by the authorized review,
// rather than silently invoking another model just to open a conversation.
func leadWelcomeAssessment(ws *workspace.Workspace) string {
	view := assessmentView(ws, time.Now())
	if view.Assessment == nil {
		if view.LatestRun == nil {
			return ""
		}
		text := "The review finished without a written assessment. We can discuss your goal and look at any existing suggestions together."
		if !view.LatestRun.Succeeded {
			text = "The review didn’t finish successfully, so I don’t have saved findings to recommend from. You can inspect it and choose Run now in Goal settings; I haven’t retried it from this conversation. What outcome matters most to you?"
		}
		return text + "\n\n" + assessmentViewSourceNote(view)
	}
	a := view.Assessment
	text := "Here’s what the saved review found:\n\n" + welcomeText(a.Text, workspace.MissionAssessmentMax)
	if len(view.RecentlyFinished) > 0 {
		finished := make([]string, 0, len(view.RecentlyFinished))
		for _, task := range view.RecentlyFinished {
			finished = append(finished, welcomeText(task.Title, 200))
		}
		text += "\n\nCurrent task records mark these as finished: " + strings.Join(finished, "; ") + ". Those are not next steps to repeat."
	}
	if a.GoalMatch != "current" {
		text += "\n\nThat review’s goal is older or unverified. Let’s use your current goal before choosing work. What matters most now?"
	} else {
		var chosen *workspace.Task
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.ParentTaskID != "" || (task.CanonicalState() != workspace.TicketStateBacklog && task.CanonicalState() != workspace.TicketStateReady) {
				continue
			}
			if task.SourceType != workspace.TicketSourceGoalSuggestion {
				continue
			}
			suggestion, ok := task.Context["goal_suggestion"].(map[string]any)
			if !ok || suggestion["run_id"] != a.RunID {
				continue
			}
			if chosen == nil || task.Priority > chosen.Priority {
				chosen = task
			}
		}
		if chosen != nil {
			text += "\n\nI recommend starting with “" + welcomeText(chosen.Description, 200) + "” — it’s the highest-priority unfinished suggestion."
			if suggestion, ok := chosen.Context["goal_suggestion"].(map[string]any); ok {
				if why, ok := suggestion["why"].(string); ok && strings.TrimSpace(why) != "" {
					text += " " + welcomeText(why, 300)
				}
				if step, ok := suggestion["first_step"].(string); ok && strings.TrimSpace(step) != "" {
					text += "\n\nFirst step: " + welcomeText(step, 300)
				}
			}
			text += "\n\nWe can talk through that here. Nothing has been started by this conversation."
			if chosen.CanonicalState() == workspace.TicketStateBacklog {
				text += " Review the existing suggestion in this workspace’s Backlog when you’re ready."
			}
		} else {
			text += "\n\nThere’s no suggested task from that review waiting to be started. We can discuss these findings and choose the next step together."
		}
	}
	return text + "\n\n" + assessmentViewSourceNote(view)
}

func welcomeText(text string, limit int) string {
	// The drawer renders message text, not HTML or Markdown. Keep the host
	// message plain, with the same bounded evidence used by the reader.
	return truncateRunes(strings.TrimSpace(text), limit)
}
