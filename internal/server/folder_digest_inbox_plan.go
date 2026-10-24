package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// inboxRow returns the host's built-in inbox-folder setup for an offer about an
// inbox folder's root. A suppressed capability, a supporting link and a
// collection have none.
func inboxRow(offer personalassistant.FolderOffer) (folderdigest.CapabilityRow, bool) {
	if !personalassistant.IsInboxOffer(offer) {
		return folderdigest.CapabilityRow{}, false
	}
	return folderdigest.InboxCapability()
}

// inboxTeam is the reviewed File Janitor creator and the roster readers an
// inbox setup is planned and run with (the session handler).
type inboxTeam interface {
	ReviewFileJanitorCreation(ctx context.Context) (sessionhttp.ReviewedTemplateCreationPlan, error)
	CreateReviewedFileJanitor(ctx context.Context, request sessionhttp.ReviewedTemplateCreationRequest) (sessionhttp.ReviewedTemplateCreationResult, error)
	FolderOfferRoster(templateID, assistantName string) (sessionhttp.FolderOfferRoster, error)
	WorkspaceNameTaken(ctx context.Context, name string) bool
	SavedAgentModel(name string) (provider, model string, found bool)
}

// inboxJanitor is the slice of File Janitor an inbox setup needs: the root and
// ownership checks, the grant, the manual-scan decision and the first batch.
type inboxJanitor interface {
	FolderOwner(root string) (*filejanitor.RootOwner, bool)
	ReviewSelectedSetup(workspaceID, path string, recipes []workspace.AutomationRecipe) (filejanitor.SelectedSetupReview, error)
	ConfirmSetup(req filejanitor.SetupRequest) (filejanitor.Status, error)
	Status(workspaceID string) (filejanitor.Status, error)
	RecordManualSetupDecision(workspaceID, rootID string, reviewedAt time.Time) (filejanitor.Status, error)
	ScanOperationReceipt(workspaceID, operationID string) (filejanitor.ScanOperationReceipt, bool, error)
	ListBatches(workspaceID string) ([]filejanitor.JanitorBatch, error)
}

// inboxReviewer reads the monitoring terms and prepares the first review: the
// assisted-setup adapter's own preparation step, with the monitoring choice
// made by the card.
type inboxReviewer interface {
	ReviewMonitoring(ctx context.Context, ownerUserID, workspaceID string) (assistantsetup.MonitoringFacts, error)
	prepareFirstReview(ctx context.Context, request assistantsetup.PrepareReviewRequest, monitoring bool) (assistantsetup.PrepareReviewResult, error)
}

// inboxCollaborators are resolved at call time: the session handler, File
// Janitor and the workspace store are wired in later phases than the folder
// digest. Tests supply fakes through folderSetupHost.inbox.
type inboxCollaborators struct {
	team      inboxTeam
	janitor   inboxJanitor
	review    inboxReviewer
	workspace func(id string) (*workspace.Workspace, error)
}

func (h *folderSetupHost) inboxDeps() (inboxCollaborators, bool) {
	if h == nil {
		return inboxCollaborators{}, false
	}
	if h.inbox != nil {
		return *h.inbox, true
	}
	b := h.builder
	if b == nil || b.sessionHandler == nil || b.fileJanitorService == nil || b.workspaceFileStore == nil {
		return inboxCollaborators{}, false
	}
	return inboxCollaborators{
		team: b.sessionHandler, janitor: b.fileJanitorService, review: b.assistantSetupAdapter(),
		workspace: b.workspaceFileStore.Get,
	}, true
}

// inboxNameTries bounds the search for a free workspace name ("Downloads 2").
const inboxNameTries = 20

// freeInboxWorkspaceName is the folder's own name, or the next free one when a
// workspace already has it: the same " 2", " 3" rule a hire from the Agents
// page applies. Empty when none of the tries is free.
func freeInboxWorkspaceName(ctx context.Context, team inboxTeam, name string) string {
	name = strings.TrimSpace(name)
	for n := 1; n <= inboxNameTries && name != ""; n++ {
		candidate := name
		if n > 1 {
			suffix := fmt.Sprintf(" %d", n)
			base := []rune(name)
			if limit := personalassistant.HireWorkspaceNameMaxRunes - len(suffix); len(base) > limit {
				base = base[:limit]
			}
			candidate = strings.TrimSpace(string(base)) + suffix
		}
		if !team.WorkspaceNameTaken(ctx, candidate) {
			return candidate
		}
	}
	return ""
}

// inboxPlan builds the review of an inbox folder's setup from canonical state:
// the selection's witness, the File Janitor team plan the strict creator will
// compare, and the roster's create-or-reuse answer. It reads no file in the
// folder, calls no model and creates nothing. A state it cannot describe
// honestly yields no plan.
func (h *folderSetupHost) inboxPlan(ctx context.Context, req personalassistant.FolderSetupRequest, row folderdigest.CapabilityRow) (personalassistant.FolderSetupPlan, error) {
	deps, ok := h.inboxDeps()
	review := req.Offer.ConversationReview
	if !ok || review == nil || strings.TrimSpace(review.CandidateIdentity) == "" || strings.TrimSpace(req.Offer.Subject.Name) == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	team, err := deps.team.ReviewFileJanitorCreation(ctx)
	if err != nil || len(team.Roles) != 1 {
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("assistant_unavailable")
	}
	role := team.Roles[0]
	// The roster's answer is the one the creator will act on: a saved agent of
	// the blueprint's name is assigned, never made a second time.
	roster, err := deps.team.FolderOfferRoster(row.Blueprint.BlueprintID, "")
	if err != nil || roster.Assistant != role.Name || roster.Action != role.Action {
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("assistant_unavailable")
	}
	name := freeInboxWorkspaceName(ctx, deps.team, setupWorkspaceName(req))
	if name == "" {
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("name_taken")
	}
	intent := personalassistant.FolderInboxIntent{
		WorkspaceName: name, SubjectKey: req.Offer.Subject.Key, SubjectIdentity: review.CandidateIdentity,
		BlueprintID: team.BlueprintID, BlueprintVersion: team.BlueprintVersion, BlueprintDigest: team.BlueprintDigest,
		PlanRevision: team.PlanRevision,
		Agent: personalassistant.FolderInboxAgent{
			RoleID: role.RoleID, Name: role.Name, Action: role.Action, Provider: role.Provider, Model: role.Model,
			ModelConfigured: role.ModelConfigured, ConfigDigest: role.ConfigDigest, Warning: role.Warning,
		},
		Conversation: review.Target.ConversationID + ":" + review.ObservationID + ":" + review.CandidateID,
	}
	if intent.Validate() != nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	plan := foldersetup.BuildInboxPlan(foldersetup.InboxFacts{
		Recipe: row.Builtin.Recipe, RecipeVersion: row.Builtin.Version, BlueprintLabel: row.Blueprint.Label, Intent: intent,
	})
	plan.Hire = &personalassistant.FolderHire{
		Mode: personalassistant.FolderHireCreate, AgentName: role.Name,
		RoleLabel: hireRoleLabel(roster.Role, row.Blueprint.Label), Model: strings.TrimSpace(role.Model),
	}
	if role.Action == personalassistant.FolderSoftwareReuse {
		plan.Hire.Mode = personalassistant.FolderHireBind
		if _, model, found := deps.team.SavedAgentModel(role.Name); found {
			plan.Hire.Model = strings.TrimSpace(model)
		}
	} else if plan.Hire.Model == "" {
		// Neither the blueprint nor the system setting names a model, so the new
		// agent is saved with the agent store's own default; the card says which.
		// The first batch never uses it.
		plan.Hire.Model = loadDefaultSettings().Model
	}
	plan.BriefDefault = personalassistant.FolderDefaultBrief(folderdigest.ShapeInbox, roster.Goal)
	return plan, nil
}
