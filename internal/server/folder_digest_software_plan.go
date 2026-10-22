package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectinspect"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Existing pages a prerequisite can point at. Nothing new is opened for them.
const (
	softwareModelRoute  = "/settings#system-model"
	softwareGitHubRoute = "/settings#github-account"
)

// softwareRow returns the host's built-in software setup for an offer about one
// recognized software project. A suppressed capability and a collection have
// none.
func softwareRow(offer personalassistant.FolderOffer) (folderdigest.CapabilityRow, bool) {
	if offer.CapabilitySuppressed || offer.Portfolio != nil || offer.Subject.Kind != personalassistant.FolderChoiceProject {
		return folderdigest.CapabilityRow{}, false
	}
	return folderdigest.BuiltinCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName)
}

// inspectSoftwareProject runs the bounded inspector for one explicit click and
// splits what it found: structural facts that may be stored on the offer, and
// the excerpts copied out of the project, which the service keeps in memory.
func inspectSoftwareProject(ctx context.Context, folderPath string) (personalassistant.FolderSoftwareInspection, error) {
	evidence, err := projectinspect.Inspect(ctx, folderPath, projectinspect.Options{})
	if err != nil {
		return personalassistant.FolderSoftwareInspection{}, err
	}
	review := personalassistant.FolderSoftwareReview{
		InspectedAt: evidence.InspectedAt, EvidenceRevision: evidence.Revision,
		Stack: append([]string(nil), evidence.Stack...), Partial: evidence.Partial, PartialReason: evidence.PartialReason,
	}
	for _, doc := range evidence.Documents {
		review.Files = append(review.Files, doc.Path)
		if doc.Class == projectinspect.ClassInstructions {
			review.Guides = append(review.Guides, doc.Path)
		}
	}
	if evidence.Git.Layout != "" {
		review.Git = &personalassistant.FolderSoftwareGit{
			Layout: evidence.Git.Layout, Branch: evidence.Git.Branch, Remote: evidence.Git.Remote, RepositoryKey: evidence.Git.RepositoryKey,
		}
	}
	for _, tool := range evidence.Tools {
		review.Tools = append(review.Tools, personalassistant.FolderSoftwareTool{Name: tool.Name, Source: tool.Source})
	}
	for _, gap := range evidence.Gaps {
		review.Gaps = append(review.Gaps, personalassistant.FolderSoftwareGap{Code: gap.Code, Source: gap.Source})
	}
	held := personalassistant.FolderSoftwareEvidence{}
	if evidence.Purpose != nil {
		held.Purpose, held.PurposeSource = evidence.Purpose.Text, evidence.Purpose.Source
	}
	for _, command := range evidence.Commands {
		held.Commands = append(held.Commands, personalassistant.FolderSoftwareCommand{
			Kind: command.Kind, Command: command.Command, Source: command.Source, Declared: command.Declared,
		})
	}
	return personalassistant.FolderSoftwareInspection{Review: review, Evidence: held}, nil
}

// softwarePlan builds the review of one software project's setup from canonical
// state: the selection's witness and the workspaces already bound to it (from
// the service), the installed blueprint and its roster plan, the stored review,
// and the live prerequisites. It reads no file in the project and calls no
// model. A state it cannot describe honestly yields no plan.
func (h *folderSetupHost) softwarePlan(ctx context.Context, req personalassistant.FolderSetupRequest, row folderdigest.CapabilityRow) (personalassistant.FolderSetupPlan, error) {
	b := h.builder
	software := req.Software
	if b == nil || b.sessionHandler == nil || software == nil || software.Review == nil || software.Review.EvidenceRevision == "" ||
		strings.TrimSpace(software.Identity) == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	review := software.Review
	facts := foldersetup.SoftwareFacts{
		Recipe: row.Builtin.Recipe, RecipeVersion: row.Builtin.Version,
		ProjectName: strings.TrimSpace(req.Offer.Subject.Name), WorkspaceName: strings.TrimSpace(review.WorkspaceName),
		BlueprintLabel: row.Blueprint.Label, Template: row.Blueprint.BlueprintID,
		SubjectKey: req.Offer.Subject.Key, SubjectIdentity: software.Identity,
		Linked: req.Offer.Subject.Repository == string(folderdigest.RepositoryFile),
		Guides: append([]string(nil), review.Guides...), EvidenceRevision: review.EvidenceRevision,
	}
	// The workflow a new workspace gets is read from what its settings would
	// really do. Defaults that would start work unasked, skip confirmation or
	// write into the project are not something this review can promise.
	workflow, safe := softwareWorkflowSignature()
	if facts.ProjectName == "" || !safe {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	facts.Workflow = workflow
	if facts.WorkspaceName == "" {
		facts.WorkspaceName = facts.ProjectName
	}
	if conversation := req.Offer.ConversationReview; conversation != nil {
		facts.Conversation = conversation.Target.ConversationID + ":" + conversation.ObservationID + ":" + conversation.CandidateID
		facts.Destination = conversation.Destination
	}

	switch len(software.Existing) {
	case 0:
	case 1:
		existing, err := h.softwareExisting(req.UserID, software.Existing[0])
		if err != nil {
			return personalassistant.FolderSetupPlan{}, err
		}
		facts.Existing = existing
	default:
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("project_exists")
	}

	roster, err := b.sessionHandler.FolderOfferRoster(row.Blueprint.BlueprintID, review.AssistantName)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("assistant_unavailable")
	}
	facts.TemplateRevision = roster.TemplateRevision
	facts.Assistant = foldersetup.SoftwareAssistant{
		Name: roster.Assistant, Action: roster.Action, Provider: roster.Provider, Model: roster.Model, RosterRevision: roster.PlanRevision,
	}
	facts.Prerequisites = h.softwarePrerequisites(ctx, facts, roster, review)
	plan := foldersetup.BuildSoftwarePlan(facts)
	plan.Hire, plan.BriefDefault = h.softwareHire(plan, roster, row)
	return plan, nil
}

// softwareExisting re-reads the one workspace already linked to the folder, so
// the plan says what it has rather than what a listing said a moment ago. A
// workspace that was set up for the folder already, or for a directory since
// replaced, is not something a review may quietly take over.
func (h *folderSetupHost) softwareExisting(userID string, listed personalassistant.FolderExistingProject) (*foldersetup.SoftwareExisting, error) {
	if listed.Software || listed.Replaced || h.builder.workspaceStore == nil {
		return nil, folderSetupPreviewError("project_exists")
	}
	ws, err := h.builder.workspaceStore.Get(listed.WorkspaceID)
	if err != nil || ws == nil || ws.Kind == "group" || ws.GetStatus() != workspace.StatusActive || !folderWorkspaceOwnedBy(ws, userID) {
		return nil, folderSetupPreviewError("project_exists")
	}
	return &foldersetup.SoftwareExisting{
		WorkspaceID: ws.ID, Name: ws.Name, EntryAgent: strings.TrimSpace(ws.EntryAgentName()),
		HasFirstLook: personalassistant.FindFolderFirstTask(ws) != nil,
	}, nil
}

// softwarePrerequisites reads what the setup needs and what it can use, as it
// stands now. Each row says where its fact came from. Nothing is probed that
// Ori does not already have a reader for: no project command is run, no
// network call is made, and a thing that cannot be checked is "unknown".
func (h *folderSetupHost) softwarePrerequisites(ctx context.Context, facts foldersetup.SoftwareFacts, roster sessionhttp.FolderOfferRoster, review *personalassistant.FolderSoftwareReview) []personalassistant.FolderPrerequisite {
	rows := []personalassistant.FolderPrerequisite{{
		ID: "folder", Label: "Folder access", State: personalassistant.FolderPrerequisiteVerified, Required: true,
		Detail: "Ori holds this folder from your selection and read its project files for this review.",
		Source: "your folder selection",
	}}

	model := personalassistant.FolderPrerequisite{ID: "model", Label: "Assistant's model", Required: true, Route: softwareModelRoute}
	assistant, provider, name, source := facts.Assistant.Name, roster.Provider, roster.Model, "Ori's system model setting"
	switch {
	case facts.Existing != nil && facts.Existing.EntryAgent != "":
		assistant, source = facts.Existing.EntryAgent, "the workspace's saved assistant"
		provider, name, _ = h.builder.sessionHandler.SavedAgentModel(assistant)
	case roster.ModelSource == "existing":
		source = "your saved agent"
	case roster.ModelSource == "template":
		source = "the blueprint"
	}
	model.Source = source
	if strings.TrimSpace(name) == "" {
		model.State = personalassistant.FolderPrerequisiteAbsent
		model.Detail = assistant + " has no model yet. Setup pauses, before anything is created, until you choose one."
		if facts.Existing != nil && facts.Existing.EntryAgent != "" {
			model.Detail = assistant + " has no model yet. Setup does not change that; its first assessment cannot start until it has one."
		}
	} else {
		model.State = personalassistant.FolderPrerequisiteConfigured
		model.Detail = assistant + " is set to use " + name
		if strings.TrimSpace(provider) != "" {
			model.Detail += " through " + provider
		}
		model.Detail += ". It has not been called, so this does not show that it works."
	}
	rows = append(rows, model)

	github := personalassistant.FolderPrerequisite{ID: "github", Label: "GitHub", Source: "Ori's saved connections", Route: softwareGitHubRoute}
	switch connected, err := h.githubConnected(ctx); {
	case err != nil:
		github.State = personalassistant.FolderPrerequisiteUnknown
		github.Detail = "Ori could not read whether GitHub is connected. Local setup does not need it."
	case connected:
		github.State = personalassistant.FolderPrerequisiteConfigured
		github.Detail = "A GitHub connection is saved. It was not checked now, and it does not show that this repository can be reached."
	default:
		github.State = personalassistant.FolderPrerequisiteAbsent
		github.Detail = "GitHub is not connected. Local setup and the first assessment work without it."
	}
	rows = append(rows, github)

	tool := personalassistant.FolderPrerequisite{ID: "execution_tool", Label: "Development tools", Source: "this project's files"}
	if len(review.Tools) > 0 {
		names := make([]string, 0, len(review.Tools))
		for _, found := range review.Tools {
			names = append(names, found.Name+" ("+found.Source+")")
		}
		tool.State = personalassistant.FolderPrerequisiteConfigured
		tool.Detail = "The project carries configuration for " + strings.Join(names, ", ") + ". Ori did not check that it is installed or signed in, and setup does not start it."
	} else {
		tool.State = personalassistant.FolderPrerequisiteAbsent
		tool.Detail = "No development-tool configuration was found in this project. Setup needs none."
	}
	return append(rows, tool)
}

// githubConnected reads whether a GitHub token is saved, without calling
// GitHub. An unavailable connection reader is an error, not "not connected".
func (h *folderSetupHost) githubConnected(ctx context.Context) (bool, error) {
	if h.builder.githubHandler == nil || h.builder.githubHandler.Connection() == nil {
		return false, errSetupUnavailable
	}
	return h.builder.githubHandler.Connection().IsConnected(ctx)
}
