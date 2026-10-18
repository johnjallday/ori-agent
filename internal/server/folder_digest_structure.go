package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var _ personalassistant.FolderStructureAvailabilityReader = (*folderSetupHost)(nil)

// What a member's plain workspace was recognized as, kept on the workspace so
// a later integration can find it and offer to connect it (PRD FR15).
const (
	folderShapeKey  = "folder_shape"
	folderMarkerKey = "folder_marker"
)

// structureMemberOffer is the offer id a member's workspace is recorded under:
// the structure's own offer and the member's candidate, so a second pass of the
// same setup finds the workspace instead of making another.
func structureMemberOffer(offerID, candidateID string) string {
	return offerID + "." + candidateID
}

// structurePlan lists every consequence of Set up on a proposed group. It reads
// the group's roster the way the Create Workspace Team step does and creates
// nothing. A plain group needs no plugin, so this plan does not fail for one.
func (h *folderSetupHost) structurePlan(_ context.Context, req personalassistant.FolderSetupRequest) (personalassistant.FolderSetupPlan, error) {
	b, structure := h.builder, req.Offer.Structure
	if b == nil || b.sessionHandler == nil || !structure.ProposesWorkspaces() || structure.Group.Type != personalassistant.FolderGroupPlain {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	members := structure.Included()
	name := strings.TrimSpace(structure.Group.Name)
	if len(members) == 0 || name == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	if workspace.IsReservedTopLevelSlug(name) {
		return personalassistant.FolderSetupPlan{}, folderSetupPreviewError("name_taken")
	}
	roster, err := b.sessionHandler.FolderOfferGroup(name)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	facts := foldersetup.StructureFacts{
		GroupName: name, GroupType: personalassistant.FolderGroupPlain,
		Manager: roster.Manager, ManagerReused: roster.Action == "reuse", RosterRevision: roster.PlanRevision,
		RootKey: req.Offer.FolderKey, RootIdentity: req.Offer.RootIdentity,
		Unavailable: structure.AvailabilityNotes(), Arrangement: structure.Arrangement,
	}
	for _, member := range members {
		facts.Members = append(facts.Members, foldersetup.StructureMember{
			CandidateID: member.CandidateID, Name: member.Name, Folder: member.Folder, TypeLabel: member.TypeLabel,
			Blueprint: member.BlueprintID, BlueprintLabel: member.BlueprintLabel, Identity: member.Identity,
		})
	}
	return foldersetup.BuildStructurePlan(facts), nil
}

// structureRun creates the group and its member workspaces for a confirmed
// structure plan. It calls no model and reads no file in the folder.
func (h *folderSetupHost) structureRun(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	b := h.builder
	if b == nil || b.sessionHandler == nil || b.personalAssistantFolderDigest == nil || b.workspaceFileStore == nil ||
		req.Offer.Structure == nil || req.Plan.Intent.Structure == nil {
		return errSetupUnavailable
	}
	host := structureSetupHost{
		builder: b, userID: req.UserID, offer: req.Offer, root: req.Path,
		creator: folderWorkspaceCreator{
			store: b.workspaceStore, handler: b.sessionHandler,
			linker: folderWorkspaceLinker{files: b.workspaceFileStore, sessions: b.sessionStore, tasks: b.sessionHandler},
		},
	}
	result, err := (&foldersetup.StructureRunner{Host: host, Progress: progressFunc(req.Update)}).Run(ctx, req.Plan)
	if result.Cause != nil {
		// The card says only what is needed; the cause belongs in the log.
		logger.Warn("Folder structure setup stopped", logger.Fields{
			"offer_id": req.Offer.ID, "reason": result.StopReason, "error": result.Cause.Error(),
		})
	}
	return err
}

// structureSetupHost is the host seam of one structure run: the ordinary
// creation pipeline for the group and each workspace, and the ordinary folder
// link for each member.
type structureSetupHost struct {
	builder *ServerBuilder
	userID  string
	offer   personalassistant.FolderOffer
	// root is the collection's canonical path, from the offer the server holds.
	root    string
	creator folderWorkspaceCreator
}

func (s structureSetupHost) workspace(id string) (*workspace.Workspace, error) {
	ws, err := s.builder.workspaceFileStore.Get(id)
	if err != nil || ws == nil || !folderWorkspaceOwnedBy(ws, s.userID) || ws.GetStatus() != workspace.StatusActive || strings.TrimSpace(ws.FolderSlug) == "" {
		return nil, personalassistant.ErrFolderWorkspaceNotFound
	}
	return ws, nil
}

func (s structureSetupHost) EnsureGroup(ctx context.Context, intent personalassistant.FolderStructureIntent) (foldersetup.StructureGroup, error) {
	if intent.GroupType != personalassistant.FolderGroupPlain || intent.GroupExists {
		return foldersetup.StructureGroup{}, errSetupUnavailable
	}
	// A pass that stopped after creating the group left it recorded under this
	// offer: it is the group, and no second one is made.
	id, created := s.creator.existingForOffer(s.userID, s.offer.ID), false
	if id == "" {
		// The Group Manager is the one the review showed, or nothing is created.
		roster, err := s.builder.sessionHandler.FolderOfferGroup(intent.GroupName)
		if err != nil || roster.PlanRevision != intent.RosterRevision || roster.Manager != intent.Manager || roster.Action != intent.ManagerAction {
			return foldersetup.StructureGroup{}, personalassistant.ErrFolderPlanChanged
		}
		id, err = s.builder.sessionHandler.CreateFolderOfferWorkspace(ctx, sessionhttp.FolderOfferWorkspaceRequest{
			Name: intent.GroupName, OfferID: s.offer.ID, Group: &roster,
		})
		if errors.Is(err, sessionhttp.ErrFolderOfferRosterChanged) {
			return foldersetup.StructureGroup{}, personalassistant.ErrFolderPlanChanged
		}
		if err != nil {
			return foldersetup.StructureGroup{}, err
		}
		created = true
	}
	ws, err := s.workspace(id)
	if err != nil || ws.Kind != "group" {
		return foldersetup.StructureGroup{}, personalassistant.ErrFolderWorkspaceRefused
	}
	return foldersetup.StructureGroup{ID: ws.ID, Name: ws.Name, Route: "/workspaces/" + ws.FolderSlug, Created: created}, nil
}

func (s structureSetupHost) EnsureWorkspace(ctx context.Context, group foldersetup.StructureGroup, intent personalassistant.FolderStructureMemberIntent) (foldersetup.StructureWorkspace, error) {
	member := s.offer.Structure.Member(intent.CandidateID)
	if member == nil || !member.Included || member.Identity != intent.Identity {
		return foldersetup.StructureWorkspace{}, personalassistant.ErrFolderPlanChanged
	}
	// The member's own folder, re-checked now: still that directory, still a
	// plain directory. One folder that moved fails its own line, not the setup.
	path, err := s.builder.personalAssistantFolderDigest.StructureMemberRoot(s.root, *member)
	if err != nil {
		return foldersetup.StructureWorkspace{}, fmt.Errorf("the folder %s is no longer where it was", member.Folder)
	}
	memberOffer := structureMemberOffer(s.offer.ID, intent.CandidateID)
	id, created := s.creator.existingForOffer(s.userID, memberOffer), false
	if id == "" {
		id, err = s.builder.sessionHandler.CreateFolderOfferWorkspace(ctx, sessionhttp.FolderOfferWorkspaceRequest{
			Name: intent.Name, TemplateID: intent.Blueprint, OfferID: memberOffer, ParentID: group.ID,
		})
		if err != nil {
			return foldersetup.StructureWorkspace{}, err
		}
		created = true
	}
	shape, marker := member.Shape, member.MarkerName
	// A group setup makes no task: a blank workspace has no agent to give one
	// to, and a first task per workspace would only ask for attention.
	link, err := s.creator.linker.link(ctx, personalassistant.FolderLinkRequest{
		UserID: s.userID, WorkspaceID: id, OfferID: memberOffer, Name: member.Folder, Path: path, Shape: folderdigest.Shape(shape),
	}, folderLinkOptions{noFirstTask: true, record: func(shared map[string]any, _ string) (map[string]any, error) {
		if shared == nil {
			shared = map[string]any{}
		}
		if shape != "" {
			shared[folderShapeKey] = shape
		}
		if marker != "" {
			shared[folderMarkerKey] = marker
		}
		return shared, nil
	}})
	if err != nil {
		return foldersetup.StructureWorkspace{}, err
	}
	ws, err := s.workspace(id)
	if err != nil || ws.Kind == "group" || ws.ParentID != group.ID {
		return foldersetup.StructureWorkspace{}, personalassistant.ErrFolderWorkspaceRefused
	}
	return foldersetup.StructureWorkspace{ID: ws.ID, Name: ws.Name, Route: link.Route, Created: created}, nil
}

func (s structureSetupHost) Receipt(_ context.Context, group foldersetup.StructureGroup, _ []foldersetup.StructureWorkspace) ([]personalassistant.FolderReceiptRow, error) {
	result, err := structureGroupReceipt(s.builder, s.userID, s.offer, s.root, group.ID)
	return result.Receipt, err
}

// structureGroupReceipt re-reads, from canonical state, the group a structure
// setup made and the workspace of every member the offer includes. Each must be
// the user's, a child of that group, recorded under this offer, and linked to
// its own member folder as its primary directory; otherwise nothing is reported.
// It is both the run's receipt and the proof the offer is resolved on.
func structureGroupReceipt(b *ServerBuilder, userID string, offer personalassistant.FolderOffer, root, groupID string) (personalassistant.FolderCreateResult, error) {
	refused := personalassistant.FolderCreateResult{}
	if b == nil || b.workspaceFileStore == nil || b.personalAssistantFolderDigest == nil || offer.Structure == nil || strings.TrimSpace(groupID) == "" {
		return refused, personalassistant.ErrFolderOutcomeUnavailable
	}
	group, err := b.workspaceFileStore.Get(groupID)
	if err != nil || group == nil || group.Kind != "group" || !folderWorkspaceOwnedBy(group, userID) || group.GetStatus() != workspace.StatusActive ||
		strings.TrimSpace(group.FolderSlug) == "" {
		return refused, personalassistant.ErrFolderWorkspaceRefused
	}
	if recorded, _ := group.SharedData[projecttemplates.FolderOfferIDKey].(string); strings.TrimSpace(recorded) != offer.ID {
		return refused, personalassistant.ErrFolderWorkspaceRefused
	}
	groupRoute := "/workspaces/" + group.FolderSlug
	rows := []personalassistant.FolderReceiptRow{{Kind: "workspace", Name: group.Name, Detail: "group", Route: groupRoute}}
	creator := folderWorkspaceCreator{linker: folderWorkspaceLinker{files: b.workspaceFileStore}}
	for _, member := range offer.Structure.Included() {
		path, err := b.personalAssistantFolderDigest.StructureMemberRoot(root, member)
		if err != nil {
			return refused, err
		}
		id := creator.existingForOffer(userID, structureMemberOffer(offer.ID, member.CandidateID))
		ws, err := b.workspaceFileStore.Get(id)
		if id == "" || err != nil || ws == nil || ws.Kind == "group" || ws.ParentID != group.ID || strings.TrimSpace(ws.FolderSlug) == "" {
			return refused, personalassistant.ErrFolderWorkspaceRefused
		}
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		ref, err := ws.GetDirectoryReference(primary)
		if strings.TrimSpace(primary) == "" || err != nil || ref == nil || !sameCanonicalDirectory(ref.Path, path) {
			return refused, personalassistant.ErrFolderWorkspaceRefused
		}
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "workspace", Name: ws.Name, Detail: "linked to " + member.Folder, Route: "/workspaces/" + ws.FolderSlug,
		})
	}
	if len(rows) < 2 {
		return refused, personalassistant.ErrFolderWorkspaceRefused
	}
	return personalassistant.FolderCreateResult{WorkspaceID: group.ID, Route: groupRoute, Created: true, Receipt: rows}, nil
}

type cachedAvailability struct {
	at     time.Time
	reason string
}

// availability answers one of a proposal's two questions from the short cache,
// so a structure that is described on every turn and every poll reads a
// plugin's release once per planFactsTTL. A failed read is logged through
// notePlan, which keeps it to one line.
func (h *folderSetupHost) availability(userID, kind, pluginKey string, read func() (string, error)) string {
	key := userID + "|" + kind + "|" + pluginKey
	h.mu.Lock()
	cached, ok := h.availabilities[key]
	h.mu.Unlock()
	if ok && time.Since(cached.at) <= planFactsTTL {
		return cached.reason
	}
	reason, err := read()
	if err != nil {
		reason = personalassistant.FolderAvailabilityPlanUnavailable
		if reasoned, ok := err.(interface{ SetupAvailability() string }); ok && reasoned.SetupAvailability() != "" {
			reason = reasoned.SetupAvailability()
		}
		h.notePlan(userID, err)
	} else if reason != "" && h.firstReport(userID, pluginKey, reason) {
		// Nothing failed: the plugin is simply not there to use. The proposal
		// carries on in its plain form, and the log says why, once.
		logger.Info("Folder proposal carries on without a plugin", logger.Fields{
			"reason": reason, "provider_or_integration_key": pluginKey, "user_id": userID,
		})
	}
	h.mu.Lock()
	if h.availabilities == nil {
		h.availabilities = map[string]cachedAvailability{}
	}
	h.availabilities[key] = cachedAvailability{at: time.Now(), reason: reason}
	h.mu.Unlock()
	return reason
}

// HomeAvailability says whether a structure can use the shape's Home as its
// group. What this computer already holds is read first, so a folder that is
// only being attached never waits on a release lookup: the owner's existing
// Home is enough, and a provider that is not installed is known without asking
// anyone. Only an installed provider with no Home yet has its reviewed release
// verified. It installs and creates nothing.
func (h *folderSetupHost) HomeAvailability(ctx context.Context, userID, providerKey string) string {
	if h == nil || h.builder == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	var provider *reviewedintegration.HomeProvider
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == providerKey {
			value := candidate
			provider = &value
			break
		}
	}
	if provider == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	return h.availability(userID, "home", providerKey, func() (string, error) {
		home, err := h.station(userID, *provider)
		if err != nil {
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		if home != nil {
			if _, err := folderSetupHomeDestination(home); err != nil {
				return personalassistant.FolderAvailabilityPlanUnavailable, nil
			}
			return "", nil
		}
		// No Home yet: one can be created only from the provider's own template,
		// which is there once the provider is installed and enabled.
		if _, err := h.newHomeDestination(ctx, userID, *provider); err != nil {
			if errors.Is(err, errHomeTemplateNotReady) {
				return personalassistant.FolderAvailabilityProviderNotInstalled, nil
			}
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		// The template is there. A structure setup installs nothing, so the
		// installed provider must be the reviewed release, enabled and current.
		plugin, err := h.providerFacts(ctx, *provider)
		if err != nil {
			return "", err
		}
		if plugin.State != personalassistant.FolderInstallReady {
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		return "", nil
	})
}

// IntegrationAvailability says whether a workspace can start from a reviewed
// integration's blueprint with nothing to install: the integration is installed,
// enabled and current. A structure setup never installs a plugin, so an
// integration that could be installed is reported as not installed.
func (h *folderSetupHost) IntegrationAvailability(ctx context.Context, userID, integrationKey string) string {
	if h == nil || h.builder == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	entry, ok := reviewedintegration.Get(integrationKey)
	if !ok {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	return h.availability(userID, "integration", integrationKey, func() (string, error) {
		// A plugin that is not on this computer is known without reading its
		// releases; only an installed one is checked for being current.
		if handler := h.builder.pluginHandler; handler != nil {
			if installed, err := handler.Manager().List(); err == nil {
				present := false
				for _, item := range installed {
					present = present || item.Name == entry.PluginID
				}
				if !present {
					return personalassistant.FolderAvailabilityIntegrationNotInstalled, nil
				}
			}
		}
		plugin, err := h.integrationFacts(ctx, userID, entry)
		if err != nil {
			return "", err
		}
		switch plugin.State {
		case personalassistant.FolderInstallReady:
			return "", nil
		case personalassistant.FolderInstallInstall:
			return personalassistant.FolderAvailabilityIntegrationNotInstalled, nil
		default:
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
	})
}
