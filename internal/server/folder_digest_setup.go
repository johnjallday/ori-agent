package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// errSetupUnavailable keeps a card on the step-by-step journey when the
// one-card path cannot be planned or run honestly.
var errSetupUnavailable = errors.New("one-card folder setup is unavailable")

// folderSetupHost plans and runs a recognized project's one-card setup over the
// real setup journey. It owns no state: the runner re-reads the journey each
// pass, and the digest service stores the run on the offer.
type folderSetupHost struct{ builder *ServerBuilder }

var _ personalassistant.FolderSetupRunner = folderSetupHost{}

// setupTarget is what a recognized project offer resolves to.
type setupTarget struct {
	row      folderdigest.CapabilityRow
	entry    reviewedintegration.Entry
	pluginID string
	questID  string
}

func (h folderSetupHost) target(ctx context.Context, offer personalassistant.FolderOffer) (setupTarget, error) {
	b := h.builder
	if b == nil || b.setupJourneyService == nil {
		return setupTarget{}, errSetupUnavailable
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok || row.Offer == nil {
		return setupTarget{}, errSetupUnavailable
	}
	entry, ok := reviewedintegration.Get(row.Offer.IntegrationKey)
	if !ok {
		return setupTarget{}, errSetupUnavailable
	}
	// The plugin's own quest exists only once the plugin is installed. Until the
	// install is folded into the run, an uninstalled plugin keeps the journey.
	pluginID, questID, found := reviewedProjectQuest(ctx, b, row.Offer.IntegrationKey, row.Blueprint.BlueprintID)
	if !found {
		return setupTarget{}, errSetupUnavailable
	}
	return setupTarget{row: row, entry: entry, pluginID: pluginID, questID: questID}, nil
}

// Plan lists every consequence of Set up for the offer, in the order the card
// shows them. Every string is plain text and names no path.
func (h folderSetupHost) Plan(ctx context.Context, req personalassistant.FolderSetupRequest) (personalassistant.FolderSetupPlan, error) {
	target, err := h.target(ctx, req.Offer)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	name := strings.TrimSpace(req.Offer.Subject.Name)
	if name == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	app := target.entry.DisplayName
	return personalassistant.NewFolderSetupPlan([]personalassistant.FolderPlanLine{
		{Kind: personalassistant.FolderPlanIntegration, Name: "Uses the installed reviewed " + app + " integration", Detail: "Nothing is installed."},
		{Kind: personalassistant.FolderPlanWorkspace, Name: "Creates a " + target.row.Blueprint.Label + " workspace named " + name},
		{Kind: personalassistant.FolderPlanFolder, Name: "Links " + name + " where it is", Detail: "Nothing is moved or copied."},
		{Kind: personalassistant.FolderPlanMode, Name: "Uses File-only mode", Detail: "Ori does not control " + app + "."},
		{Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents this blueprint requires"},
		{Kind: personalassistant.FolderPlanTask, Name: "Queues a first read-only task for when you open it"},
	}), nil
}

// Run drives the plugin's quest to the end or to the first stop. Progress is
// reported through req.Update; only an unexpected error is returned.
func (h folderSetupHost) Run(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	target, err := h.target(ctx, req.Offer)
	if err != nil {
		return err
	}
	scoped, err := h.builder.setupJourneyService.ForQuest(ctx, req.UserID, target.pluginID, target.questID)
	if err != nil {
		return fmt.Errorf("resolve the setup quest: %w", err)
	}
	runner := &foldersetup.Runner{
		Journey:    scopedJourney{service: scoped, userID: req.UserID},
		Selections: folderSelections{builder: h.builder, path: req.Path},
		Progress:   progressFunc(req.Update),
	}
	result, err := runner.Run(ctx, foldersetup.Config{
		Plan: req.Plan, WorkspaceName: strings.TrimSpace(req.Offer.Subject.Name), EntryName: req.EntryName,
	})
	if result.Cause != nil {
		// The card says only what is needed; the cause belongs in the log.
		logger.Warn("One-card folder setup stopped", logger.Fields{
			"offer_id": req.Offer.ID, "reason": result.StopReason, "error": result.Cause.Error(),
		})
	}
	return err
}

// scopedJourney binds one user to a quest-scoped journey service.
type scopedJourney struct {
	service *setupjourney.Service
	userID  string
}

func (j scopedJourney) Read(ctx context.Context, runID string) (*setupjourney.JourneyProjection, error) {
	return j.service.Read(ctx, j.userID, runID)
}

func (j scopedJourney) Mutate(ctx context.Context, runID string, action setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	return j.service.Mutate(ctx, j.userID, runID, action, request)
}

// folderSelections mints the project picker's selection token for the folder
// the server remembered for the offer. The browser never supplies it.
type folderSelections struct {
	builder *ServerBuilder
	path    string
}

func (s folderSelections) Select(context.Context) (string, string, error) {
	if strings.TrimSpace(s.path) == "" || s.builder == nil || s.builder.pathSelectionStore == nil {
		return "", "", foldersetup.ErrNeedsPick
	}
	token, err := s.builder.pathSelectionStore.Issue(s.path)
	if err != nil {
		return "", "", foldersetup.ErrNeedsPick
	}
	return token, s.path, nil
}

type progressFunc func(ctx context.Context, update personalassistant.FolderSetupUpdate) error

func (f progressFunc) Update(ctx context.Context, update personalassistant.FolderSetupUpdate) error {
	return f(ctx, update)
}
