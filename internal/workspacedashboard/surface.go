// Package workspacedashboard resolves user-authored workspace dashboards into
// the generic Workspace Surface contract.
//
// A dashboard is an HTML file the workspace's own user dropped into their
// workspace folder. It is never registered in the process-global
// workspacesurface.Registry: that registry is keyed by an owner key with no
// workspace in it, so one workspace's dashboard would become visible to all of
// them. Instead this package synthesizes the surface descriptor and its trusted
// binding on demand, per workspace, from what is on disk.
//
// Everything the dashboard itself contributes — HTML, CSS, JavaScript — is
// untrusted. Trust lives only in the binding synthesized here: the asset root,
// the fixed entry asset, and the read-only runtime. The dashboard's own bytes
// never influence any of them.
package workspacedashboard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

const (
	// OwnerID identifies user dashboards as a class. It is deliberately not
	// per-workspace: the owner id has to satisfy the surface id grammar, which
	// workspace UUIDs do not, and per-workspace scoping is enforced by resolving
	// only ever through the caller's workspace id rather than by the key.
	OwnerID = "ori.dashboard"

	// OwnerVersion and OwnerGeneration are fixed. A dashboard has no install
	// lifecycle to track: its freshness is carried by AssetVersion, and its
	// existence is re-read from disk on every request.
	OwnerVersion    = "1"
	OwnerGeneration = 1

	CapabilityID = "dashboard"
	SurfaceID    = "main"

	// SurfaceLabel names the surface in the catalog and the view switcher.
	SurfaceLabel       = "Dashboard"
	SurfaceDescription = "Your own dashboard for this workspace."

	// IconToken must be one of the host icon tokens the surface host maps.
	IconToken = "grid"

	// Placement renders the dashboard inline as its own workspace view mode,
	// beside Details, Map, and Tickets, rather than as a modal.
	Placement = workspacesurface.PlacementWorkspaceView
)

// ErrDashboardUnavailable reports a dashboard that exists but cannot currently
// be turned into a servable surface. It is distinct from "no dashboard", which
// is not an error, and it carries the reason so the host can show the user
// something better than a blank panel.
var ErrDashboardUnavailable = errors.New("workspace dashboard is unavailable")

// Finder is the discovery seam. *workspace.DashboardStore satisfies it.
type Finder interface {
	Find(workspaceID string) (workspace.CustomDashboard, bool, error)
}

// Owner is the fixed identity every user dashboard registers under.
func Owner() workspacesurface.Owner {
	return workspacesurface.Owner{
		Kind: workspacesurface.OwnerUser, ID: OwnerID, Version: OwnerVersion,
		Generation: OwnerGeneration, ProtocolMin: workspacesurface.ProtocolVersion,
		ProtocolMax: workspacesurface.ProtocolVersion,
	}
}

// Key is the qualified surface key every user dashboard resolves under. It is
// the same string for every workspace; the workspace is supplied separately at
// every resolution, never encoded here.
func Key() string {
	return workspacesurface.SurfaceKey(Owner(), CapabilityID, SurfaceID)
}

// Source resolves the dashboard surface for one workspace at a time.
type Source struct {
	dashboards Finder
	runtime    workspacesurface.Runtime
}

// NewSource wires discovery to the runtime that serves dashboard data
// operations.
func NewSource(dashboards Finder, runtime workspacesurface.Runtime) *Source {
	return &Source{dashboards: dashboards, runtime: runtime}
}

// Resolve returns the synthesized surface and binding for workspaceID. The
// three outcomes are distinct:
//
//   - ok=false, err=nil — this workspace has no dashboard. The overwhelmingly
//     common case, and not a failure.
//   - ok=true, err=nil — the dashboard is usable and the binding is valid.
//   - ok=true, err!=nil — a dashboard is present but cannot be served. The
//     returned surface still carries the dashboard's identity and is marked
//     unavailable, so the host can tell the user their file was found and
//     rejected rather than silently showing nothing.
func (s *Source) Resolve(workspaceID string) (workspacesurface.RegisteredSurface, workspacesurface.Binding, bool, error) {
	if s == nil || s.dashboards == nil || s.runtime == nil {
		return workspacesurface.RegisteredSurface{}, workspacesurface.Binding{}, false, nil
	}
	dashboard, ok, err := s.dashboards.Find(workspaceID)
	if err != nil {
		if !ok {
			// The folder itself is unresolvable; there is no dashboard identity
			// to show and nothing useful to say about a file we never found.
			return workspacesurface.RegisteredSurface{}, workspacesurface.Binding{}, false,
				fmt.Errorf("%w: %v", ErrDashboardUnavailable, err)
		}
		return unavailableSurface(explain(dashboard, err)), workspacesurface.Binding{}, true,
			fmt.Errorf("%w: %v", ErrDashboardUnavailable, err)
	}
	if !ok {
		return workspacesurface.RegisteredSurface{}, workspacesurface.Binding{}, false, nil
	}

	owner := Owner()
	surface := workspacesurface.Surface{
		ID: SurfaceID, Label: SurfaceLabel, Description: SurfaceDescription,
		Icon:      workspacesurface.Icon{Kind: "host", Value: IconToken},
		Placement: Placement,
		Modal:     workspacesurface.Modal{Width: 1200, Height: 800},
		// The host polls a surface's status; a dashboard's status is a local
		// filesystem check, so the slowest permitted cadence is plenty.
		Polling:      workspacesurface.Polling{MapSeconds: 60, OpenSeconds: 60},
		OperationIDs: operationIDs(),
	}
	capability := workspacesurface.Capability{
		ID: CapabilityID, Version: 1,
		Display:  workspacesurface.Display{Name: SurfaceLabel, Description: SurfaceDescription},
		Surfaces: []workspacesurface.Surface{surface},
	}
	binding := workspacesurface.Binding{
		CapabilityID: CapabilityID, SurfaceID: SurfaceID,
		AssetRoot: dashboard.AssetRoot, AssetVersion: dashboard.AssetVersion,
		EntryAsset: dashboard.EntryAsset,
		Operations: operations(),
		Runtime:    s.runtime,
	}

	// Validate through the same path RegisterTrusted uses. Discovery reads a
	// user-controlled directory, so an asset root or version that would fail
	// binding validation must fail here rather than reaching the asset server.
	registration := workspacesurface.Registration{
		Owner: owner, Capabilities: []workspacesurface.Capability{capability},
		Bindings: []workspacesurface.Binding{binding},
	}
	if err := workspacesurface.ValidateRegistration(registration); err != nil {
		return unavailableSurface(explain(dashboard, err)), workspacesurface.Binding{}, true,
			fmt.Errorf("%w: %v", ErrDashboardUnavailable, err)
	}

	return workspacesurface.RegisteredSurface{
		Key: Key(), Owner: owner, Capability: capability, Surface: surface, Available: true,
	}, binding, true, nil
}

// maxReasonBytes bounds the failure text. Surface.Description is validated at
// 500 bytes, and a path plus a reason has to fit inside that.
const maxReasonBytes = 400

// explain turns a resolution failure into text a user can act on. It names the
// entry file, because that is the one thing they can go and look at — the frame
// is opaque and gives them no other signal.
func explain(dashboard workspace.CustomDashboard, err error) string {
	path := dashboard.EntryPath()
	reason := "Ori could not open this workspace dashboard."
	if path != "" {
		reason = "Ori could not open " + path + "."
	}

	// The error already names the path and carries an internal sentence stem
	// ("workspace dashboard entry file is unusable: <path> is empty"). Strip
	// both so the user reads one sentence about their file, not the plumbing.
	detail := err.Error()
	for _, noise := range []string{
		workspace.ErrDashboardEntryUnusable.Error(),
		ErrDashboardUnavailable.Error(),
		path,
	} {
		if noise != "" {
			detail = strings.ReplaceAll(detail, noise, "")
		}
	}
	detail = strings.Trim(strings.Join(strings.Fields(detail), " "), " :.,")
	if detail != "" {
		reason += " It " + detail + "."
	}
	if len(reason) > maxReasonBytes {
		reason = strings.TrimSpace(reason[:maxReasonBytes]) + "…"
	}
	return reason
}

// unavailableSurface is the dashboard's identity with no working binding behind
// it. It carries the same key as a healthy dashboard so the host renders it in
// the same place, marked unavailable, with reason as its description.
func unavailableSurface(reason string) workspacesurface.RegisteredSurface {
	owner := Owner()
	if strings.TrimSpace(reason) == "" {
		reason = SurfaceDescription
	}
	surface := workspacesurface.Surface{
		ID: SurfaceID, Label: SurfaceLabel, Description: reason,
		Icon:      workspacesurface.Icon{Kind: "host", Value: IconToken},
		Placement: Placement,
		Modal:     workspacesurface.Modal{Width: 1200, Height: 800},
		Polling:   workspacesurface.Polling{MapSeconds: 60, OpenSeconds: 60},
	}
	return workspacesurface.RegisteredSurface{
		Key: Key(), Owner: owner,
		Capability: workspacesurface.Capability{
			ID: CapabilityID, Version: 1,
			Display:  workspacesurface.Display{Name: SurfaceLabel, Description: SurfaceDescription},
			Surfaces: []workspacesurface.Surface{surface},
		},
		Surface: surface, Available: false, UnavailableCode: "dashboard_unavailable",
	}
}
