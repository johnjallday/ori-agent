package filejanitor

import (
	"os"
	"time"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// StationLook is current host evidence for the pre-install card. No path,
// grant or action is accepted from the browser.
type StationLook struct {
	ReferenceID string `json:"reference_id"`
	Manager     string `json:"manager"`
	Witness     string `json:"witness"`
	Revision    string `json:"revision"`
}

type stationBuild struct {
	Look      StationLook `json:"look"`
	ManagerID string      `json:"manager_id"`
}

type StationInstaller interface {
	InstallFilingStation(string, string, time.Time, func(*workspace.Workspace) error) error
}

func (s *Service) SetStationHost(look func(string, string) (StationLook, error), installer StationInstaller) {
	s.stationLook, s.stationInstaller = look, installer
}

func stationChanged() error {
	return setupErr(CodeFolderChanged, "The folder, manager or saved look changed. Review the current workspace before building.", RepairRetry, nil)
}

// StationAvailable is a stored-state check for an owner-facing offer, never
// Status (which probes persistence). Ambiguous orphaned state is not fresh.
func (s *Service) StationAvailable(owner, id string) bool {
	if s == nil || s.store == nil || s.stationInstaller == nil {
		return false
	}
	ws, err := s.readWorkspace(id)
	if err != nil || !stationWorkspace(ws, owner) || ws.HasInstalledCapability(workspace.CapabilityFileJanitor) || ws.CapabilityWasRemoved(workspace.CapabilityFileJanitor) {
		return false
	}
	scan, err := s.store.scanStatePath(id)
	if err != nil {
		return false
	}
	if _, err := os.Lstat(scan); !os.IsNotExist(err) { // #nosec G703 -- server workspace path, fixed scan-state filename
		return false
	}
	settingsPath, err := s.store.settingsPath(id)
	if err != nil {
		return false
	}
	if _, err := os.Lstat(settingsPath); os.IsNotExist(err) { // #nosec G703 -- server workspace path, fixed settings filename
		return true
	}
	// An exact stop before the install wrote an inert note but no access
	// or scan. Only this known state can bring back a fresh build review.
	v, err := s.store.LoadSettings(id)
	return err == nil && v.StoppedStationPart == "plot" && v.SetupPreparation == nil && v.RootID == "" && v.RootPath == "" && v.AccessRevokedAt.IsZero() && v.PendingFolderGrant == nil && v.LastFolderGrant == nil
}
func stationWorkspace(ws *workspace.Workspace, owner string) bool {
	return ws != nil && linkedSetupOwned(ws, owner) && ws.IsAnchored() && ws.GetStatus() == workspace.StatusActive && ws.Kind != "group" && ws.GetAssistantProgramState() == nil
}
func (s *Service) reviewStation(id, owner, reference string, monitor bool) (LinkedSetupReview, error) {
	if !s.StationAvailable(owner, id) || s.stationLook == nil {
		return LinkedSetupReview{}, stationChanged()
	}
	look, err := s.stationLook(owner, id)
	if err != nil || look.ReferenceID == "" || reference != "" && look.ReferenceID != reference || look.Witness == "" || len(look.Revision) != 64 {
		return LinkedSetupReview{}, stationChanged()
	}
	ws, err := s.readWorkspace(id)
	if err != nil || ws == nil {
		return LinkedSetupReview{}, stationChanged()
	}
	lead, ok := ws.FindUniqueAgentInstance(look.Manager, "")
	if !ok || lead.ID == "" {
		return LinkedSetupReview{}, stationChanged()
	}
	st := &stationBuild{look, lead.ID}
	return s.reviewSetup(id, owner, look.ReferenceID, "", monitor, false, st)
}

// ReviewStation returns the exact read-only review before the capability
// exists, or the canonical accepted preparation after the press.
func (s *Service) ReviewStation(id, owner, reference string, monitor bool) (LinkedSetupReview, error) {
	settings, err := s.store.LoadSettings(id)
	if err != nil {
		return LinkedSetupReview{}, err
	}
	if p := settings.SetupPreparation; p != nil && p.Station != nil {
		return s.observeLinkedPreparation(id, owner, settings)
	}
	return s.reviewStation(id, owner, reference, monitor)
}

func (st *stationBuild) matches(ws *workspace.Workspace, owner string) bool {
	if !stationWorkspace(ws, owner) || ws.CapabilityWasRemoved(workspace.CapabilityFileJanitor) || ws.AnchorDirectoryReferenceID() != st.Look.ReferenceID || ws.EntryAgentName() != st.Look.Manager {
		return false
	}
	lead, ok := ws.FindUniqueAgentInstance(st.Look.Manager, "")
	return ok && lead.ID == st.ManagerID
}
func stationInstallRecord(at time.Time) workspace.InstalledCapability {
	return workspace.InstalledCapability{ID: workspace.CapabilityFileJanitor, Version: workspacecapability.FileJanitorDefinition().Version, InstalledAt: at, Source: workspace.InstallSourceInPlace}
}
func stationAccess(ws *workspace.Workspace) (string, error) {
	install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	var resources []workspace.CapabilityResource
	for _, r := range install.OwnedResources {
		if r.Kind != workspace.ResourceCompanionAgent {
			resources = append(resources, r)
		}
	}
	return setupDigest([]any{ws.MCPBindings, resources})
}
func (s *Service) installStation(id string, p SetupPreparation) error {
	if s.stationInstaller == nil {
		return stationChanged()
	}
	// Revalidate the entire reviewed snapshot, including folder identity and
	// schedule, before installing. If the install already landed, only join THIS
	// operation; removal is not an invitation to reinstall.
	r, err := s.reviewSetup(id, p.OwnerUserID, p.ReferenceID, "", p.Monitoring, true, p.Station)
	if err != nil || r.State != "review" || r.bindingRevision != p.BindingRevision {
		return stationChanged()
	}
	ws, err := s.readWorkspace(id)
	if err != nil {
		return err
	}
	if ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return validatePreparationWorkspace(ws, &p, r.rootPath)
	}
	if s.stationLook == nil {
		return stationChanged()
	}
	look, err := s.stationLook(p.OwnerUserID, id)
	if err != nil || look != p.Station.Look {
		return stationChanged()
	}
	return s.stationInstaller.InstallFilingStation(id, p.Station.ManagerID, p.AcceptedAt, func(ws *workspace.Workspace) error {
		if !p.Station.matches(ws, p.OwnerUserID) {
			return stationChanged()
		}
		ref, err := ws.GetDirectoryReference(p.ReferenceID)
		if err != nil || ref == nil || ref.Path != r.rootPath {
			return stationChanged()
		}
		info, err := os.Lstat(ref.Path)
		if err != nil || !info.IsDir() || diridentity.Of(info) != p.Station.Look.Witness {
			return stationChanged()
		}
		rev, err := stationAccess(ws)
		if err != nil || rev != p.AccessRevision {
			return stationChanged()
		}
		return nil
	})
}
