package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	listed    int
	unreached int
	homeID    string
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
	lines []personalassistant.FolderPlanLine, setLine func(name, state string)) (homeReceipt, error) {
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

	// The Home: the user's one Home of this provider, or a new one.
	createLine := line(personalassistant.FolderPlanHome)
	setLine(createLine, personalassistant.FolderLineWorking)
	setup := folderHomeProviderSetup{builder: b}
	preview, err := setup.Preview(ctx, provider.Key)
	if err != nil {
		setLine(createLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopInstallFailed, err)
	}
	if !preview.Ready {
		if _, err := setup.Install(ctx, provider.Key, preview.Version); err != nil {
			setLine(createLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopInstallFailed, err)
		}
	}
	station, err := b.folderSetupHost.station(owner, provider)
	if err != nil {
		setLine(createLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopFailed, err)
	}
	homeID := ""
	if station != nil {
		homeID = station.ID
	} else {
		review, err := b.sessionHandler.ReviewPortfolioHome(ctx, provider.PluginID)
		if err != nil {
			setLine(createLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopFailed, err)
		}
		if review.Reuse && review.HomeID != "" {
			homeID = review.HomeID
		} else if homeID, err = b.sessionHandler.CommitPortfolioHome(ctx, review, key+"-home"); err != nil {
			setLine(createLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	staffing := portfolioHomeStaffing{builder: b}
	missing, err := staffing.Missing(ctx, homeID)
	if err != nil {
		setLine(createLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopFailed, err)
	}
	if len(missing) > 0 && !staffing.ModelReady(ctx) {
		setLine(createLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopNeedsModel, errors.New("the Home's roles need a model"))
	}
	for _, role := range missing {
		if err := staffing.Staff(ctx, homeID, role); err != nil {
			setLine(createLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	setLine(createLine, personalassistant.FolderLineDone)

	// The fed folder as a root of the Home's library.
	rootLine := line(personalassistant.FolderPlanLibrary)
	setLine(rootLine, personalassistant.FolderLineWorking)
	folder, err := folderAgentWorkspaces{builder: b}.AnchorRoot(rec.WorkspaceID)
	if err != nil {
		setLine(rootLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopNeedsPick, err)
	}
	state, err := b.sessionHandler.PortfolioLibraryState(owner, homeID, folder)
	if err != nil {
		setLine(rootLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopFailed, err)
	}
	if !state.Initialized {
		token, _, err := b.sessionHandler.ReviewPortfolioLibrary(owner, homeID)
		if err == nil {
			err = b.sessionHandler.CommitPortfolioLibrary(owner, homeID, token, key+"-library")
		}
		if err != nil {
			setLine(rootLine, personalassistant.FolderLineFailed)
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
			setLine(rootLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	setLine(rootLine, personalassistant.FolderLineDone)

	// The listings: the root, then the scopes leading to the songs.
	songsLine, scopesLine := line(personalassistant.FolderPlanSongs), line(folderagent.FolderPlanScopes)
	setLine(songsLine, personalassistant.FolderLineWorking)
	listed, _, err := b.sessionHandler.ScanFolderAgentRoot(ctx, owner, homeID, rootID, key+"-scan")
	if err != nil {
		setLine(songsLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopFailed, err)
	}
	needed := folderagent.ProjectParents(rec.Facts)
	scanned, skipped, failures := map[string]bool{}, map[string]bool{}, map[string]int{}
	scans := 0
	if len(needed) > 0 {
		setLine(scopesLine, personalassistant.FolderLineWorking)
	}
	for scans < folderagent.MaxScopedScans {
		issued, err := b.sessionHandler.FolderAgentScopes(owner, homeID, rootID)
		if err != nil {
			break
		}
		scopes := make([]folderagent.LibraryScope, 0, len(issued))
		for _, s := range issued {
			scopes = append(scopes, folderagent.LibraryScope{ID: s.ID, RelativeFolder: s.RelativeFolder})
		}
		done := make(map[string]bool, len(scanned)+len(skipped))
		for folder := range scanned {
			done[folder] = true
		}
		for folder := range skipped {
			done[folder] = true
		}
		next := folderagent.NextScopes(needed, scopes, done)
		if len(next) == 0 {
			break
		}
		for _, s := range next {
			if scans >= folderagent.MaxScopedScans {
				break
			}
			scans++
			count, _, err := b.sessionHandler.ScanFolderAgentScope(ctx, owner, homeID, rootID, s.ID, fmt.Sprintf("%s-scope-%d", key, scans))
			if err != nil {
				// Tried again in the next round; twice failed counts as not
				// reached.
				failures[s.RelativeFolder]++
				if failures[s.RelativeFolder] >= 2 {
					skipped[s.RelativeFolder] = true
				}
				logger.Warn("Folder agent scoped listing did not finish", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
				continue
			}
			scanned[s.RelativeFolder] = true
			listed = count
		}
	}
	unreached := folderagent.Unreached(needed, scanned)
	if len(needed) > 0 {
		state := personalassistant.FolderLineDone
		if len(unreached) > 0 {
			state = personalassistant.FolderLineFailed
		}
		setLine(scopesLine, state)
	}
	setLine(songsLine, personalassistant.FolderLineDone)
	receipt = homeReceipt{listed: listed, unreached: len(unreached), homeID: homeID}

	// Placement: the workspace inside the Home's district, its goal the
	// Home's; its agent stays.
	placeLine := line(folderagent.FolderPlanPlace)
	setLine(placeLine, personalassistant.FolderLineWorking)
	mover, ok := b.workspaceStore.(interface {
		MoveWorkspaceFolder(string, string) ([]workspace.MovedWorkspace, error)
	})
	if !ok {
		setLine(placeLine, personalassistant.FolderLineFailed)
		return stop(personalassistant.FolderStopFailed, errors.New("the workspace cannot be moved"))
	}
	if current, err := b.workspaceStore.Get(rec.WorkspaceID); err == nil && current != nil && current.ParentID != homeID {
		if _, err := mover.MoveWorkspaceFolder(rec.WorkspaceID, homeID); err != nil {
			setLine(placeLine, personalassistant.FolderLineFailed)
			return stop(personalassistant.FolderStopFailed, err)
		}
	}
	if err := (folderAgentWorkspaces{builder: b}).SetGoal(rec.WorkspaceID, folderagent.HomeGoal); err != nil {
		logger.Warn("Folder agent could not set the Home goal", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
	}
	setLine(placeLine, personalassistant.FolderLineDone)
	return receipt, nil
}
