package filejanitor

import (
	"context"
	"errors"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StationBuildProgress is presentation, never permission to complete a stage.
// Install, folder grant and saved scan receipts survive reload. Only a live
// request can report how far a scan is; a restart never reconstructs a count.
type StationBuildProgress struct {
	Part      string `json:"part"`
	Operation string `json:"operation_id,omitempty"`
	Running   bool   `json:"running,omitempty"`
	Looking   bool   `json:"looking,omitempty"`
	Counted   bool   `json:"counted,omitempty"`
	Checked   int    `json:"checked,omitempty"`
	Total     int    `json:"total,omitempty"`
	Eligible  int    `json:"eligible"`
	Repair    bool   `json:"repair,omitempty"`
}

type buildActivity struct {
	sync.Mutex
	owner, id, op  string
	cancel         context.CancelFunc
	checked, total int
}

func (a *buildActivity) start(ctx context.Context, owner, id, op string) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.Lock()
	a.owner, a.id, a.op, a.cancel = owner, id, op, cancel
	a.checked, a.total = 0, -1
	a.Unlock()
	// PrepareLinkedSetup serializes these lifetimes under prepareMu.
	return ctx, func() {
		cancel()
		a.Lock()
		defer a.Unlock()
		a.owner, a.id, a.op, a.cancel = "", "", "", nil
	}
}
func (a *buildActivity) count(op string, n, total int) {
	a.Lock()
	defer a.Unlock()
	if a.op == op {
		a.checked, a.total = n, total
	}
}
func (a *buildActivity) stop(owner, id, op string) {
	a.Lock()
	defer a.Unlock()
	if a.cancel != nil && a.op == op && a.id == id && a.owner == owner {
		a.cancel()
	}
}
func (a *buildActivity) project(owner, id, op string, p *StationBuildProgress) {
	a.Lock()
	defer a.Unlock()
	if a.cancel != nil && a.op == op && a.id == id && a.owner == owner {
		p.Running = true
		if a.total >= 0 {
			p.Counted = true
			p.Total, p.Checked = a.total, a.checked
		}
	}
}

func (s *Service) StationProgress(owner, id string) (StationBuildProgress, error) {
	var no StationBuildProgress
	if s == nil || s.store == nil || s.workspaces == nil {
		return no, stationChanged()
	}
	primary, err := s.workspaces.Get(id)
	if err != nil || !progressWorkspace(primary, owner) {
		return no, stationChanged()
	}
	ws, err := s.readWorkspace(id)
	if err != nil || !progressWorkspace(ws, owner) {
		return no, stationChanged()
	}
	settings, err := s.store.LoadSettings(id)
	if err != nil {
		return no, err
	}
	out := StationBuildProgress{Part: "plot"}
	install, installed := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if installed {
		out.Part = "foundation"
	}
	p := settings.SetupPreparation
	if p == nil {
		if installed && settings.IsSetUp() {
			out.Part = "open"
		}
		if part := settings.StoppedStationPart; part != "" {
			out.Part, out.Repair = part, true
		}
		return out, nil
	}
	if p.OwnerUserID != owner {
		return no, stationChanged()
	}
	out.Operation = p.ID
	s.buildActivity.project(owner, id, p.ID, &out)
	install.OwnedResources = nil
	digest, err := setupDigest(install)
	if err != nil {
		return no, err
	}
	if !installed || digest != p.CapabilityRevision {
		out.Part = "plot"
		out.Repair = !out.Running
		return out, nil
	}
	if grant := settings.LastFolderGrant; settings.IsSetUp() && grant != nil && grant.RunID == p.ID && grant.RootID == settings.RootID && grant.OperationID == p.ID+":folder" {
		out.Part = "walls"
		out.Looking = !p.ScanDispatchedAt.IsZero()
	}
	if out.Part == "walls" && out.Looking {
		receipt, found, err := s.preparedScanReceipt(id, *p)
		if err != nil {
			var domain *SetupError
			if !errors.As(err, &domain) || domain.Code != CodeInitialReceiptUnavailable {
				// The install and grant still happened. An explicit stop can
				// keep those parts even if the scan record cannot be read.
				return out, err
			}
		} else if found && receipt.RootID == settings.RootID {
			out.Part = "roof"
			out.Looking = false
			out.Eligible = receipt.EligibleCount
			if p.ScanReceiptDigest != "" {
				out.Part = "open"
			}
		}
	}
	out.Repair = out.Part != "open" && !out.Running
	if !out.Running {
		out.Looking = false
	}
	return out, nil
}

func progressWorkspace(ws *workspace.Workspace, owner string) bool {
	return ws != nil && linkedSetupOwned(ws, owner) && (ws.Status == "" || ws.Status == workspace.StatusActive)
}
