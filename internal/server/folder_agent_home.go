package server

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// folderAgentRootResolver hands the library the folder agent's own anchored
// folder, read from the workspace's directory reference: never a client
// path. It is the PortfolioRootResolver the Home's root review asks for.
type folderAgentRootResolver struct{ path string }

func (r folderAgentRootResolver) PortfolioRoot(_ context.Context, _, _, _ string) (string, string, error) {
	info, err := os.Lstat(r.path)
	if err != nil {
		return "", "", err
	}
	identity, err := projectlibrary.DirectoryIdentity(info)
	if err != nil {
		return "", "", err
	}
	return r.path, identity, nil
}

// homeStageStop is a Home-stage run that ended early with the reason the
// card explains.
// homeReceipt is what the Home stage reports back: how many songs the
// library lists under the root, and how many folders holding songs no
// listing reached.
type homeReceipt struct {
	listed     int
	listedNoun string
	unreached  int
	homeID     string
	homeName   string
	created    bool
	hired      []string
}

type homeStageStop struct {
	reason string
	cause  error
}

func (s *homeStageStop) Error() string { return s.reason + ": " + s.cause.Error() }

// runFolderAgentHome is the Home stage (PRD FR37, FR38, FR39): the provider at
// its reviewed release, the user's Home (found, or created through the
// reviewed Home creation with its required roles), the library on, the fed
// folder as a root, the root listing, then scoped listings for the scopes the
// library itself issued that lead to the folders the walk found holding
// songs, and finally the workspace placed inside the Home. setLine reports
// each plan line.
func (b *ServerBuilder) runFolderAgentHome(ctx context.Context, rec folderagent.Record, home folderlook.Candidate,
	lines []personalassistant.FolderPlanLine, setLine func(name, state, progress string)) (homeReceipt, error) {
	var receipt homeReceipt
	stop := func(reason string, cause error) (homeReceipt, error) {
		return receipt, &homeStageStop{reason: reason, cause: cause}
	}
	line := func(kind string) string {
		for _, l := range lines {
			if l.Kind == kind {
				return l.Name
			}
		}
		return ""
	}
	if b.sessionHandler == nil || b.folderSetupHost == nil {
		return stop(personalassistant.FolderStopFailed, errors.New("home setup is unavailable"))
	}
	var provider reviewedintegration.HomeProvider
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == home.HomeProviderKey {
			provider = candidate
		}
	}
	if provider.Key == "" {
		return stop(personalassistant.FolderStopPlanChanged, errors.New("unknown Home provider"))
	}
	owner, key := rec.OwnerUserID, "folder-agent-"+rec.WorkspaceID
	homeName := home.Name
	if homeName == "" {
		homeName = "Home"
	}
	// first is the first of the named lines the plan has: an action whose own
	// line a plan from before this build lacks reports on the next one.
	first := func(names ...string) string {
		for _, name := range names {
			if name != "" {
				return name
			}
		}
		return ""
	}
	var hireLines []string
	for _, l := range lines {
		if l.Kind == folderagent.FolderPlanHire {
			hireLines = append(hireLines, l.Name)
		}
	}
	providerLine, createLine := line(personalassistant.FolderPlanProvider), line(personalassistant.FolderPlanHome)
	rootLine := line(personalassistant.FolderPlanLibrary)

	// The provider at its reviewed release.
	installLine := first(providerLine, createLine, rootLine)
	setLine(installLine, personalassistant.FolderLineWorking, "Checking the reviewed release")
	setup := folderHomeProviderSetup{builder: b}
	preview, err := setup.Preview(ctx, provider.Key)
	if err != nil {
		setLine(installLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopInstallFailed, err)
	}
	release := provider.DisplayName
	if preview.Version != "" {
		release += " " + preview.Version
	}
	if !preview.Ready {
		setLine(installLine, personalassistant.FolderLineWorking, "Downloading "+release)
		if _, err := setup.Install(ctx, provider.Key, preview.Version); err != nil {
			setLine(installLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopInstallFailed, err)
		}
		if providerLine != "" {
			setLine(providerLine, personalassistant.FolderLineDone, "Installed "+release)
		}
	} else if providerLine != "" {
		setLine(providerLine, personalassistant.FolderLineDone, "")
	}

	// The app plugin the Home's songs get their team from, when the plan's own
	// tools step does not install it (it would have run first).
	if toolsLine := line(folderagent.FolderPlanHomeTools); toolsLine != "" {
		if err := b.installFolderAgentHomeTools(ctx, rec, home.ProjectIntegrationKey, toolsLine, setLine); err != nil {
			setLine(toolsLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopInstallFailed, err)
		}
	}

	// The Home: the user's one Home of this provider, or a new one.
	homeLine := first(createLine, rootLine)
	station, err := b.folderSetupHost.station(owner, provider)
	if err != nil {
		setLine(homeLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopFailed, err)
	}
	homeID := ""
	created := false
	if station != nil {
		homeID = station.ID
	} else {
		setLine(homeLine, personalassistant.FolderLineWorking, "Creating "+homeName)
		review, err := b.sessionHandler.ReviewPortfolioHome(ctx, provider.PluginID)
		if err != nil {
			setLine(homeLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopFailed, err)
		}
		if review.Reuse && review.HomeID != "" {
			homeID = review.HomeID
		} else if homeID, err = b.sessionHandler.CommitPortfolioHome(ctx, review, key+"-home"); err != nil {
			setLine(homeLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopFailed, err)
		}
		created = true
	}
	receipt.homeID, receipt.homeName, receipt.created = homeID, homeName, created
	if createLine != "" {
		words := ""
		if created {
			words = "Created your " + homeName
		}
		setLine(createLine, personalassistant.FolderLineDone, words)
	}

	// The roles the Home still lacks, each on its own line ("Hires <role>"),
	// or on the one "Fills the Home's roles" line, named as each is hired.
	staffing := portfolioHomeStaffing{builder: b}
	missing, err := staffing.Missing(ctx, homeID)
	if err != nil {
		setLine(first(append(hireLines, rootLine)...), personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopFailed, err)
	}
	// The shared assistant the card named: hired after the Home's own roles,
	// only when the plan has its line (the press approved exactly that).
	assistant := b.folderAgentHomeAssistant(homeID, line(folderagent.FolderPlanAssistant))
	if (len(missing) > 0 || assistant.creates()) && !staffing.ModelReady(ctx) {
		setLine(first(append(append(hireLines, assistant.line), rootLine)...), personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopNeedsModel, errors.New("the Home's agents need a model"))
	}
	hired, err := hireFolderAgentRoles(missing, hireLines, rootLine, setLine, func(role foldersetup.HomeRole) error {
		return staffing.Staff(ctx, homeID, role)
	})
	receipt.hired = hired
	if err != nil {
		return stop(personalassistant.FolderStopFailed, err)
	}
	if note := line(folderagent.FolderPlanAssistantNote); note != "" {
		setLine(note, personalassistant.FolderLineDone, "")
	}
	// A failed assistant hire keeps the Home and its roles, and the folder is
	// still added and listed; the run then ends stopped so Set up again retries.
	var assistantStop *homeStageStop
	if assistant.line != "" {
		name, stopped := b.hireFolderAgentAssistant(ctx, rec, assistant, staffing.ModelReady(ctx), setLine)
		if name != "" {
			receipt.hired = append(receipt.hired, name)
		}
		assistantStop = stopped
	}

	// The fed folder as a root of the Home's library.
	setLine(rootLine, personalassistant.FolderLineWorking, "Adding the folder")
	folder, err := folderAgentWorkspaces{builder: b}.AnchorRoot(rec.WorkspaceID)
	if err != nil {
		setLine(rootLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopNeedsPick, err)
	}
	state, err := b.sessionHandler.PortfolioLibraryState(owner, homeID, folder)
	if err != nil {
		setLine(rootLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopFailed, err)
	}
	if !state.Initialized {
		token, _, err := b.sessionHandler.ReviewPortfolioLibrary(owner, homeID)
		if err == nil {
			err = b.sessionHandler.CommitPortfolioLibrary(owner, homeID, token, key+"-library")
		}
		if err != nil {
			setLine(rootLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	rootID := state.RootID
	if rootID == "" {
		token, _, _, err := b.sessionHandler.ReviewPortfolioRoot(ctx, owner, homeID, key, folderAgentRootResolver{path: folder})
		if err == nil {
			rootID, err = b.sessionHandler.CommitPortfolioRoot(owner, homeID, token, key+"-root")
		}
		if err != nil {
			setLine(rootLine, personalassistant.FolderLineFailed, "")
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	setLine(rootLine, personalassistant.FolderLineDone, "Added "+rec.FolderName+" to your "+homeName)

	// The listings: the root, then the scopes leading to the songs.
	listing, err := listFolderAgentSongs(ctx, b.sessionHandler, rec, owner, homeID, rootID, key,
		line(personalassistant.FolderPlanSongs), line(folderagent.FolderPlanScopes), setLine)
	if err != nil {
		return stop(personalassistant.FolderStopFailed, err)
	}
	receipt.listed, receipt.listedNoun, receipt.unreached = listing.listed, listing.noun, listing.unreached

	// Placement: the workspace inside the Home's district; its agent stays.
	// No goal is written: the agent's suggested goal is offered once the run
	// is done, and only the owner's answer sets one.
	placeLine := line(folderagent.FolderPlanPlace)
	setLine(placeLine, personalassistant.FolderLineWorking, "Placing "+rec.WorkspaceName)
	store, ok := b.workspaceStore.(folderHomePlacementStore)
	if !ok {
		setLine(placeLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopFailed, errors.New("the workspace cannot be moved"))
	}
	if err := placeFolderWorkspaceInHome(store, rec.WorkspaceID, homeID); err != nil {
		setLine(placeLine, personalassistant.FolderLineFailed, "")
		return stop(personalassistant.FolderStopFailed, err)
	}
	setLine(placeLine, personalassistant.FolderLineDone, "Placed "+rec.WorkspaceName+" inside your "+homeName)
	if assistantStop != nil {
		return receipt, assistantStop
	}
	return receipt, nil
}

// hireFolderAgentRoles hires each role the Home lacks on its own plan line:
// "Hires <role>" when the plan named it, else the one HomeRolesLine (named as
// each is hired), else the next open hire line (a name taken meanwhile), else
// fallback. A hire line left with nothing to hire is done without words.
func hireFolderAgentRoles(roles []foldersetup.HomeRole, hireLines []string, fallback string,
	setLine func(name, state, progress string), staff func(foldersetup.HomeRole) error) ([]string, error) {
	pending := append([]string(nil), hireLines...)
	generic := false
	for _, name := range hireLines {
		generic = generic || name == folderagent.HomeRolesLine
	}
	var names []string
	hired := map[string][]string{}
	for _, role := range roles {
		target := ""
		for i, name := range pending {
			if name == "Hires "+role.Name {
				target = name
				pending = append(pending[:i], pending[i+1:]...)
				break
			}
		}
		switch {
		case target != "":
		case generic:
			target = folderagent.HomeRolesLine
		case len(pending) > 0:
			target, pending = pending[0], pending[1:]
		default:
			target = fallback
		}
		setLine(target, personalassistant.FolderLineWorking, "Hiring "+role.Name)
		if err := staff(role); err != nil {
			setLine(target, personalassistant.FolderLineFailed, "")
			return names, err
		}
		hired[target] = append(hired[target], role.Name)
		names = append(names, role.Name)
		if target != folderagent.HomeRolesLine && target != fallback {
			setLine(target, personalassistant.FolderLineDone, "Hired "+role.Name)
		}
	}
	for _, name := range hireLines {
		switch {
		case name == folderagent.HomeRolesLine:
			words := ""
			if len(hired[name]) > 0 {
				words = "Hired " + strings.Join(hired[name], " · ")
			}
			setLine(name, personalassistant.FolderLineDone, words)
		case len(hired[name]) == 0:
			// Filled meanwhile: nothing to hire.
			setLine(name, personalassistant.FolderLineDone, "")
		}
	}
	return names, nil
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// folderAgentHomeFacts is what the plan needs to word one line per Home
// action (PRD FR1, FR5): whether the reviewed provider release is in place,
// whether the owner's Home exists, and which required roles it still lacks.
// It returns names and a version only, never a path or an id. false means the
// host could not tell, and the plan keeps its generic lines.
func folderAgentHomeFacts(ctx context.Context, b *ServerBuilder, userID, providerKey string) (folderlook.HomeFacts, bool) {
	var provider reviewedintegration.HomeProvider
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == providerKey {
			provider = candidate
		}
	}
	if provider.Key == "" || b == nil || b.folderSetupHost == nil {
		return folderlook.HomeFacts{}, false
	}
	facts := folderlook.HomeFacts{ProviderName: provider.DisplayName}
	preview, declared, err := folderHomeProviderSetup{builder: b}.previewHome(ctx, provider.Key)
	if err != nil {
		return facts, false
	}
	facts.ProviderVersion, facts.ProviderReady = preview.Version, preview.Ready
	station, err := b.folderSetupHost.station(userID, provider)
	if err != nil {
		return facts, false
	}
	staffing := portfolioHomeStaffing{builder: b}
	if station == nil {
		// No Home yet, so its roles are the ones the reviewed release declares:
		// the release the preview just inspected (nothing is installed to read
		// it). A release whose Home cannot be told, or that hires nobody,
		// leaves the roles unknown and the plan says "Fills the Home's roles".
		if hires := staffing.declaredHires(declared); len(hires) > 0 {
			facts.MissingRoles, facts.Hires = homeFactHires(hires)
			facts.RolesKnown = true
		}
		return facts, true
	}
	facts.HomeExists = true
	// A folder placed in a Home that already reads song details is read by that
	// Home's own scans as it is set up, so the plan says so.
	if consent := station.GetAssistantProgramState().GetSongDetailsConsent(); consent.Active() {
		facts.SongDetails = folderlook.SongDetailsNumbers
		if consent.CoversNames() {
			facts.SongDetails = folderlook.SongDetailsNames
		}
	}
	hires, err := staffing.missingHires(station.ID)
	if err != nil {
		return facts, true
	}
	facts.MissingRoles, facts.Hires = homeFactHires(hires)
	facts.RolesKnown = true
	return facts, true
}

// homeFactHires are the facts' words for the agents a Home hires: each name,
// the provider's description of its job (bounded when the facts are cleaned)
// and, when one of the owner's agents already holds the role's own name, that
// name, since the run hires a new agent beside it and never adopts it.
func homeFactHires(hires []homeHire) ([]string, []folderlook.HomeHire) {
	names := make([]string, 0, len(hires))
	out := make([]folderlook.HomeHire, 0, len(hires))
	for _, hire := range hires {
		entry := folderlook.HomeHire{Name: hire.Name, Job: hire.Description}
		if hire.Base != "" && !strings.EqualFold(hire.Base, hire.Name) {
			entry.Taken = hire.Base
		}
		names = append(names, hire.Name)
		out = append(out, entry)
	}
	return names, out
}
