package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// Prerequisite is a thin presentation adapter over the reviewed integration
// journey and the existing reviewed Home-provider installer. It never calls Run,
// creates a workspace, grants folder access or staffs/executes an agent.
func (h *folderSetupHost) Prerequisite(ctx context.Context, req personalassistant.FolderSetupRequest, input personalassistant.FolderPrerequisiteInput) (personalassistant.FolderPrerequisiteReview, error) {
	target, err := h.target(req.Offer)
	if err != nil {
		return personalassistant.FolderPrerequisiteReview{}, err
	}
	if input.Kind == personalassistant.FolderPlanIntegration {
		return h.integrationPrerequisite(ctx, req, target, input)
	}
	if input.Kind != personalassistant.FolderPlanProvider || target.provider == nil {
		return personalassistant.FolderPrerequisiteReview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	installer := folderHomeProviderSetup{builder: h.builder}
	preview, err := installer.Preview(ctx, target.provider.Key)
	if err != nil {
		return personalassistant.FolderPrerequisiteReview{}, err
	}
	operation := "install"
	switch {
	case preview.Ready:
		operation = "ready"
	case preview.Update:
		operation = "update"
	case preview.Installed:
		operation = "enable"
	}
	review := personalassistant.FolderPrerequisiteReview{
		Kind: input.Kind, Operation: operation, Name: target.provider.DisplayName, Version: preview.Version, InstalledVersion: preview.InstalledVersion,
		Source: preview.Source, EnabledAfter: true, Disclosure: preview.Disclosure,
	}
	// Include the installed generation as well as the exact pinned source and
	// complete disclosure. Another tab's replacement cannot reuse this consent.
	var generation uint64
	plugins, err := h.builder.pluginHandler.Manager().List()
	if err != nil {
		return review, personalassistant.ErrFolderOutcomeUnavailable
	}
	for _, installed := range plugins {
		if installed.Name == preview.PluginID {
			generation = installed.Generation
		}
	}
	material, err := json.Marshal(struct {
		Review     personalassistant.FolderPrerequisiteReview
		Generation uint64
	}{review, generation})
	if err != nil {
		return review, personalassistant.ErrFolderOutcomeUnavailable
	}
	digest := sha256.Sum256(material)
	review.Digest = hex.EncodeToString(digest[:])
	if !input.Confirm {
		return review, nil
	}
	if input.RunID != "" || input.ReviewToken != "" || input.Action != "" || input.Revision != 0 || input.Digest != review.Digest || input.ReviewedVersion != review.Version {
		return review, personalassistant.ErrFolderPlanChanged
	}
	if !preview.Ready {
		if _, err := installer.Install(ctx, target.provider.Key, review.Version); err != nil {
			if errors.Is(err, personalassistant.ErrFolderWorkspaceRefused) {
				return review, personalassistant.ErrFolderPlanChanged
			}
			return review, err
		}
	}
	h.InvalidateSetupReadiness(req.UserID)
	review.Completed = true
	return review, nil
}

func (h *folderSetupHost) integrationPrerequisite(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget, input personalassistant.FolderPrerequisiteInput) (personalassistant.FolderPrerequisiteReview, error) {
	out := personalassistant.FolderPrerequisiteReview{Kind: input.Kind, Name: target.entry.DisplayName}
	if input.Confirm && (input.RunID == "" || input.Revision <= 0 || input.ReviewToken == "" ||
		(input.Action != "install" && input.Action != "update" && input.Action != "enable") || input.Digest != "" || input.ReviewedVersion != "") {
		return out, personalassistant.ErrFolderPlanChanged
	}
	if h.builder.setupJourneyService == nil {
		return out, personalassistant.ErrFolderOutcomeUnavailable
	}
	service, err := h.builder.setupJourneyService.ForHostQuest(ctx, req.UserID, target.entry.InstallQuestID())
	if err != nil {
		return out, err
	}
	journey, err := service.Read(ctx, req.UserID, "")
	if err != nil {
		return out, err
	}
	if input.Confirm {
		// Only these three package actions can reach the existing durable consent
		// boundary. Never accept a project/group/staffing action or arbitrary Input.
		if input.RunID != journey.RunID || (input.Action != "install" && input.Action != "update" && input.Action != "enable") || input.Digest != "" || input.ReviewedVersion != "" {
			return out, personalassistant.ErrFolderPlanChanged
		}
		_, err := service.Mutate(ctx, req.UserID, journey.RunID, setupjourney.ActionID(input.Action), setupjourney.ActionMutation{
			IfRevision: input.Revision, IdempotencyKey: input.RequestID, ReviewToken: input.ReviewToken, Input: json.RawMessage(`{}`),
		})
		if err != nil {
			return out, err
		}
		h.InvalidateSetupReadiness(req.UserID)
		out.Completed = true
		return out, nil
	}
	if input.RunID != "" || input.Action != "" || input.ReviewToken != "" || input.Digest != "" || input.ReviewedVersion != "" || input.Revision != 0 {
		return out, personalassistant.ErrValidation
	}
	var action setupjourney.ActionID
	for _, step := range journey.Steps {
		if step.Integration == nil {
			continue
		}
		for _, offered := range step.Actions {
			switch offered.ID {
			case setupjourney.ActionReviewInstall, setupjourney.ActionReviewUpdate, setupjourney.ActionReviewEnable:
				action = offered.ID
			}
		}
	}
	if action == "" {
		return out, personalassistant.ErrFolderOutcomeUnavailable
	}
	result, err := service.Mutate(ctx, req.UserID, journey.RunID, action, setupjourney.ActionMutation{IfRevision: journey.StateRevision, IdempotencyKey: input.RequestID, Input: json.RawMessage(`{}`)})
	if err != nil {
		return out, err
	}
	if result.Review == nil || result.Review.Integration == nil {
		return out, personalassistant.ErrFolderOutcomeUnavailable
	}
	shown := result.Review.Integration
	out.Operation, out.Action = string(result.Review.CommitAction), string(result.Review.CommitAction)
	out.RunID, out.Revision, out.ReviewToken = result.Journey.RunID, result.Journey.StateRevision, result.Review.Token
	out.Version, out.InstalledVersion, out.Source = shown.ExpectedVersion, shown.InstalledVersion, shown.SourceURL
	out.EnabledAfter = out.Action == "enable" || shown.Enabled
	out.Disclosure, out.Details = shown.Trust, shown
	return out, nil
}
