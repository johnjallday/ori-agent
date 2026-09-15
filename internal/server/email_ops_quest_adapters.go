package server

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Email Ops host quest's step readers. Each is a read-only view over an
// existing owner: the workspace store for the created workspace and, in later
// steps, the mailbox readiness evaluator. None of them creates, links, or
// authorizes anything.

const (
	emailOpsTemplateTitle       = "Email Ops"
	maxQuestWorkspaceLabelBytes = 120
)

var errEmailOpsQuestUnavailable = errors.New("email ops quest owner is unavailable")

// emailOpsWorkspaceCreateReader resolves the user's Email Ops workspace with the
// same rule every Email Ops surface uses. The source must hydrate template
// provenance (the folder store); the SQLite-primary store drops it and would
// report no workspace forever.
type emailOpsWorkspaceCreateReader struct {
	source workspace.EmailOpsWorkspaceSource
}

func (r emailOpsWorkspaceCreateReader) Read(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	if r.source == nil || scope.ExpectedBlueprintID != workspace.EmailOpsTemplateID {
		return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
	}
	id, err := workspace.ResolveEmailOpsWorkspace(r.source, scope.OwnerUserID)
	if err != nil {
		return setupjourney.CanonicalStepRead{}, err
	}
	projection := &setupjourney.WorkspaceCreateProjection{TemplateTitle: emailOpsTemplateTitle}
	if id == "" {
		// Reading never creates a workspace: the creator does, through
		// POST /api/workspaces, after the user reviews the team.
		return setupjourney.CanonicalStepRead{
			AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewTeam},
			WorkspaceCreate:  projection,
		}, nil
	}
	ws, err := r.source.Get(id)
	if err != nil || ws == nil {
		return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
	}
	projection.WorkspaceID = ws.ID
	projection.WorkspaceLabel = questWorkspaceLabel(ws.Name)
	projection.WorkspaceRoute = questWorkspaceRoute(ws.FolderSlug)
	return setupjourney.CanonicalStepRead{
		Complete:         true,
		AvailableActions: []setupjourney.ActionID{setupjourney.ActionOpenWorkspace},
		Result:           setupjourney.CanonicalResult{ProjectWorkspaceID: ws.ID},
		WorkspaceCreate:  projection,
	}, nil
}

// emailOpsWorkspaceLocator answers the Home email card's one question with the
// same resolver and provenance-hydrated source as quest step 1.
type emailOpsWorkspaceLocator struct {
	source workspace.EmailOpsWorkspaceSource
}

func (l emailOpsWorkspaceLocator) HasEmailOpsWorkspace(userID string) (bool, error) {
	if l.source == nil {
		return false, errEmailOpsQuestUnavailable
	}
	id, err := workspace.ResolveEmailOpsWorkspace(l.source, userID)
	return id != "", err
}

// questWorkspaceLabel bounds a user-chosen name for display without splitting
// a character; an empty name falls back to the template title.
func questWorkspaceLabel(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return emailOpsTemplateTitle
	}
	for len(name) > maxQuestWorkspaceLabelBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// questWorkspaceRoute returns /workspaces/<slug> only for a slug that needs no
// escaping; otherwise the client resolves the workspace by ID.
func questWorkspaceRoute(slug string) string {
	if slug == "" || url.PathEscape(slug) != slug || strings.Contains(slug, "..") {
		return ""
	}
	return "/workspaces/" + slug
}

// setupSummaryReader supplies only closed navigation/continuation offers. The
// journey reconciler, not this reader, decides when all owners are ready and
// these actions may be exposed.
type setupSummaryReader struct {
	// modelAvailable reports whether a system model is configured and its
	// provider can serve it. Nil means no model.
	modelAvailable func() bool
}

func (r setupSummaryReader) Read(ctx context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	if scope.Shape == specialist.SetupJourneyShapeAccountLink {
		// FR 36: nothing Email-Ops-specific is offered until the workspace exists,
		// and triage is offered only when a model can run it. Mailbox readiness
		// itself never needs a model.
		actions := []setupjourney.ActionID{}
		if scope.ProjectWorkspaceID != "" {
			actions = append(actions, setupjourney.ActionOpenWorkspace)
			if r.modelAvailable != nil && r.modelAvailable() {
				actions = append(actions, setupjourney.ActionStartInboxTriage)
			} else {
				actions = append(actions, setupjourney.ActionOpenModelSettings)
			}
		}
		return setupjourney.CanonicalStepRead{AvailableActions: actions}, nil
	}
	return readSetupSummary(ctx, scope)
}
