package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/homeprofile"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The one-card setup's part in a Home's profile: one plan line when the Home
// provider's installed package declares a profile, and one run step that fills
// it. Applications are named through the host's tool table and the package's
// own labels; nothing here names one.

// profileFacts returns what a plan's profile line is built from, or nil when
// the Home provider's installed package declares no profile (the plan then has
// no such line and the run no such step). It looks for installed applications:
// a plan is one of the moments a detection may run.
func (h *folderSetupHost) profileFacts(provider reviewedintegration.HomeProvider, integrationKey string) *foldersetup.ProfileFacts {
	b := h.builder
	if b == nil || b.pluginHandler == nil || b.sessionHandler == nil {
		return nil
	}
	installed, err := b.pluginHandler.Manager().List()
	if err != nil {
		return nil
	}
	var declared *projecttemplates.HomeProfileDeclaration
	for _, candidate := range installed {
		if !candidate.Enabled || candidate.Name != provider.PluginID || candidate.WorkspaceSurfaces == nil {
			continue
		}
		for _, home := range candidate.WorkspaceSurfaces.AssistantProgramHomes {
			if home.ID == provider.ProgramID && home.HomeProfile != nil {
				declared = home.HomeProfile.Clone()
			}
		}
	}
	// The line is about applications, so a card without either of those rows
	// has nothing to say on a setup card.
	if declared == nil || (!declared.Declares(projecttemplates.HomeProfileKindApps) && !declared.Declares(projecttemplates.HomeProfileKindMainApp)) {
		return nil
	}
	facts := &foldersetup.ProfileFacts{Title: declared.Title, MainLabel: declared.Label(projecttemplates.HomeProfileKindMainApp)}
	found := b.sessionHandler.HomeProfileInstalledApps()
	foundIDs := make(map[string]bool, len(found))
	for _, app := range found {
		foundIDs[app.ToolID] = true
		facts.Apps = append(facts.Apps, app.Name)
	}
	if declared.Declares(projecttemplates.HomeProfileKindTemplates) {
		if tool, ok := folderdigest.TemplatesToolForIntegration(integrationKey); ok && foundIDs[tool.ToolID] {
			facts.TemplatesApp = tool.ToolName
		}
	}
	return facts
}

// folderProfileStep is the run's profile step over the Home profile service.
// Its request id is derived from the card, so a resumed run replays the step
// instead of looking and reading again.
type folderProfileStep struct {
	handler *sessionhttp.Handler
	userID  string
	offerID string
}

func (s folderProfileStep) Setup(ctx context.Context, homeID string, grantTemplates bool) error {
	if s.handler == nil {
		return errSetupUnavailable
	}
	sum := sha256.Sum256([]byte(s.offerID))
	_, err := s.handler.HomeProfiles().Setup(ctx, s.userID, homeID, homeprofile.SetupInput{
		RequestID: "setup-" + hex.EncodeToString(sum[:16]), OfferID: s.offerID, GrantTemplates: grantTemplates,
	})
	return err
}

// profileReceiptRow is the finished setup's one line about the Home's profile,
// with the route to its card.
func profileReceiptRow(home *workspace.Workspace) (personalassistant.FolderReceiptRow, bool) {
	title, detail, ok := homeprofile.ReceiptSummary(home.GetAssistantProgramState().GetHomeProfile())
	if !ok || !workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		return personalassistant.FolderReceiptRow{}, false
	}
	return personalassistant.FolderReceiptRow{
		Kind: personalassistant.FolderPlanProfile, Name: title, Detail: detail,
		Route: "/workspaces/" + url.PathEscape(home.FolderSlug) + "/assistant#homeProfilePanel",
	}, true
}
