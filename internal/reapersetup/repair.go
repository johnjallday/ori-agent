package reapersetup

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
)

// RepairPlan previews the changes repair would make to a conservatively
// identified REAPER workspace, keeping plugin installation, plugin enablement,
// and component attachment as separate, clearly-labeled steps. It never includes
// native-CLI access, agent, task, or .rpp changes — repair does not touch those.
type RepairPlan struct {
	Identified      bool                        `json:"identified"`
	PluginInstalled bool                        `json:"plugin_installed"`
	NeedsInstall    bool                        `json:"needs_install"` // plugin missing: use the install/trust flow first
	NeedsEnable     bool                        `json:"needs_enable"`  // installed but disabled: requires explicit confirmation
	AttachPlan      []pluginworkspace.Component `json:"attach_plan,omitempty"`
	NoOp            bool                        `json:"no_op"` // already fully repaired
	Explanation     string                      `json:"explanation"`
}

// RepairResult reports what repair actually did and the resulting readiness.
type RepairResult struct {
	Identified   bool                        `json:"identified"`
	Enabled      bool                        `json:"enabled"` // the plugin was enabled during this repair
	Attached     []pluginworkspace.Component `json:"attached,omitempty"`
	NeedsInstall bool                        `json:"needs_install"` // plugin missing: nothing attached
	NeedsConfirm bool                        `json:"needs_confirm"` // disabled plugin, confirmation not given
	NoOp         bool                        `json:"no_op"`
	Status       Status                      `json:"status"` // post-repair normalized readiness
	Warnings     []string                    `json:"warnings,omitempty"`
	Explanation  string                      `json:"explanation"`
}

// Repairer reconciles an existing REAPER workspace's plugin bindings using the
// same readiness resolver and reconciliation service as creation. It is
// idempotent and structurally cannot enable native-CLI access, mutate an agent,
// create a task, or touch an .rpp file: it only enables the plugin (on
// confirmation) and attaches the plugin's recorded components.
type Repairer struct {
	store      workspaceReader
	reconciler *pluginworkspace.Reconciler
	resolver   *Resolver
}

// NewRepairer builds a repairer over the workspace store, the shared reconciler,
// and the readiness resolver (used to report post-repair state).
func NewRepairer(store workspaceReader, reconciler *pluginworkspace.Reconciler, resolver *Resolver) *Repairer {
	return &Repairer{store: store, reconciler: reconciler, resolver: resolver}
}

// identifiedReaper returns whether the workspace is a conservatively-identified
// REAPER workspace repair may operate on.
func (rp *Repairer) identifiedReaper(workspaceID string) (bool, error) {
	if rp.store == nil {
		return false, nil
	}
	ws, err := rp.store.Get(workspaceID)
	if err != nil {
		return false, err
	}
	if ws == nil {
		return false, nil
	}
	ok, _ := identify(ws)
	return ok, nil
}

// Preview lists the changes repair would make without applying them.
func (rp *Repairer) Preview(workspaceID string) (RepairPlan, error) {
	var plan RepairPlan
	identified, err := rp.identifiedReaper(workspaceID)
	if err != nil {
		return plan, err
	}
	if !identified {
		plan.Explanation = "This workspace is not identified as a REAPER workspace, so there is nothing to repair."
		return plan, nil
	}
	plan.Identified = true

	results, err := rp.reconciler.Inspect(workspaceID, []string{ReaperPluginName})
	if err != nil {
		return plan, err
	}
	if len(results) == 0 {
		return plan, nil
	}
	pr := results[0]
	plan.PluginInstalled = pr.Installed
	switch pr.State {
	case pluginworkspace.PluginStateMissing:
		plan.NeedsInstall = true
		plan.Explanation = "reaper-plugin is not installed. Install it (with the trust preview) before repair can attach its components."
	case pluginworkspace.PluginStateDisabled:
		plan.NeedsEnable = true
		plan.AttachPlan = pr.Components
		plan.Explanation = "reaper-plugin is installed but globally disabled. Repair will enable it (after your confirmation) and attach its components."
	case pluginworkspace.PluginStateDetached:
		plan.AttachPlan = pr.Missing
		plan.Explanation = fmt.Sprintf("Repair will attach %d missing reaper-plugin component(s) to this workspace.", len(pr.Missing))
	default: // attached
		plan.NoOp = true
		plan.Explanation = "reaper-plugin is already fully attached. No repair is needed."
	}
	return plan, nil
}

// Apply performs the repair. It attaches the plugin's recorded components
// idempotently and, only when confirmEnable is true, enables a globally-disabled
// plugin first. A missing plugin is reported (install is a separate step) and a
// fully-attached plugin is a no-op. It never enables native access, mutates an
// agent, creates a task, or touches an .rpp file.
func (rp *Repairer) Apply(workspaceID string, confirmEnable bool) (RepairResult, error) {
	var res RepairResult
	identified, err := rp.identifiedReaper(workspaceID)
	if err != nil {
		return res, err
	}
	if !identified {
		res.Explanation = "This workspace is not identified as a REAPER workspace; repair did nothing."
		return res, nil
	}
	res.Identified = true

	// Inspect current state to decide the safe path.
	results, err := rp.reconciler.Inspect(workspaceID, []string{ReaperPluginName})
	if err != nil {
		return res, err
	}
	var state pluginworkspace.PluginState
	if len(results) > 0 {
		state = results[0].State
	}
	switch state {
	case pluginworkspace.PluginStateMissing:
		res.NeedsInstall = true
		res.Explanation = "reaper-plugin is not installed; install it first, then repair to attach its components."
	case pluginworkspace.PluginStateDisabled:
		if !confirmEnable {
			res.NeedsConfirm = true
			res.Explanation = "reaper-plugin is disabled. Confirm enabling it to attach its components."
		}
	}

	// Attach (and, when confirmed, enable) via the shared reconciler. For a
	// missing plugin or an unconfirmed disabled plugin, this is a safe no-op that
	// attaches nothing.
	if !res.NeedsInstall && !res.NeedsConfirm {
		out, rerr := rp.reconciler.Reconcile(pluginworkspace.Request{
			WorkspaceID: workspaceID,
			Plugins:     []string{ReaperPluginName},
			AllowEnable: confirmEnable,
		})
		if rerr != nil {
			return res, rerr
		}
		if out.SaveErr != nil {
			res.Warnings = append(res.Warnings, "workspace save failed: "+out.SaveErr.Error())
		}
		for _, pr := range out.Plugins {
			res.Attached = append(res.Attached, pr.Applied...)
			res.Warnings = append(res.Warnings, pr.Warnings...)
			if pr.Enabled && state == pluginworkspace.PluginStateDisabled && confirmEnable {
				res.Enabled = true
			}
		}
		if len(res.Attached) == 0 && res.Explanation == "" {
			res.NoOp = true
			res.Explanation = "reaper-plugin is already fully attached; repair made no changes."
		} else if res.Explanation == "" {
			res.Explanation = fmt.Sprintf("Attached %d reaper-plugin component(s) to this workspace.", len(res.Attached))
		}
	}

	// Report the resulting normalized readiness so the UI can refresh in place.
	if rp.resolver != nil {
		if readiness, rerr := rp.resolver.Resolve(workspaceID); rerr == nil {
			res.Status = readiness.Status
		}
	}
	res.Explanation = strings.TrimSpace(res.Explanation)
	return res, nil
}
