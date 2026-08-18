package reapersetup

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	migrationDiagnosticProject = "Ori could not identify this workspace's authoritative REAPER project, so its previous setup was preserved. Review the workspace project before choosing a live-control mode."
	migrationDiagnosticMode    = "Ori could not safely identify whether this workspace previously used File-only or Ori-assisted REAPER, so its previous setup was preserved. Review the operating mode to continue."
)

// PlanLegacyRuntimeMigration converts only durable evidence from the shipped
// pre-runtime Reaper Song wizard. It never probes REAPER, installs a plugin,
// grants access, or treats old ori_ready state as end-to-end verification.
func PlanLegacyRuntimeMigration(input setupwizard.RuntimeMigrationInput, contract *workspace.RuntimeRequirementsContract) setupwizard.RuntimeMigrationPlan {
	if contract == nil || !contract.StructurallyValid() || !hasAuthoritativeReaperEntry(input) {
		return setupwizard.RuntimeMigrationPlan{Diagnostic: migrationDiagnosticProject}
	}

	candidates := make(map[string]struct{}, 2)
	state := input.RuntimeState
	if state != nil && strings.TrimSpace(state.SelectedModeID) != "" {
		mode := workspace.NormalizeRuntimeIdentifier(state.SelectedModeID)
		if _, ok := contract.Mode(mode); !ok {
			return setupwizard.RuntimeMigrationPlan{Diagnostic: migrationDiagnosticMode}
		}
		candidates[mode] = struct{}{}
	}
	progress := input.Progress
	if progress != nil {
		for _, step := range progress.Steps {
			mode := workspace.NormalizeRuntimeIdentifier(step.SelectedOption)
			if mode == "" {
				continue
			}
			if _, ok := contract.Mode(mode); ok {
				candidates[mode] = struct{}{}
			}
		}
	}
	if len(candidates) > 1 {
		return setupwizard.RuntimeMigrationPlan{Diagnostic: migrationDiagnosticMode}
	}
	for mode := range candidates {
		return setupwizard.RuntimeMigrationPlan{SelectedModeID: mode}
	}

	// The legacy adapter could infer Ori-assisted from a fully ready plugin/
	// agent/native-access posture without persisting a selected option. A
	// completed legacy reaper_song wizard with no mode is therefore assisted.
	// File-only always persisted its explicit choice, so this does not turn an
	// ambiguous limited workspace into assisted control.
	provenance := input.Provenance
	if progress != nil && progress.CompletedAt != nil && hasLegacyReaperWizard(provenance) {
		if _, ok := contract.Mode(ModeOriAssisted); ok {
			return setupwizard.RuntimeMigrationPlan{SelectedModeID: ModeOriAssisted}
		}
	}
	return setupwizard.RuntimeMigrationPlan{Diagnostic: migrationDiagnosticMode}
}

func hasAuthoritativeReaperEntry(input setupwizard.RuntimeMigrationInput) bool {
	return strings.TrimSpace(input.ProjectPath) != "" && strings.TrimSpace(input.ProjectEntryPath) != "" && strings.EqualFold(filepath.Ext(input.ProjectEntryPath), ".rpp")
}

func hasLegacyReaperWizard(provenance *workspace.TemplateProvenance) bool {
	if provenance == nil || provenance.SetupWizard == nil {
		return false
	}
	for _, step := range provenance.SetupWizard.Steps {
		if step.Kind == workspace.SetupStepKindPluginReadiness && strings.EqualFold(strings.TrimSpace(step.Adapter), SetupAdapterID) {
			return true
		}
	}
	return false
}
