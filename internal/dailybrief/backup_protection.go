package dailybrief

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ProtectionFact is one workspace's read-only Protection line: state, never an
// offer. Sentence is host-rendered from fixed parts; nothing here is a path,
// a provider-supplied label or an action an agent could take.
type ProtectionFact struct {
	State           string             `json:"state"`
	Reason          string             `json:"reason"`
	Provider        string             `json:"provider,omitempty"`
	ProviderLabel   string             `json:"provider_label,omitempty"`
	Covering        *CoverageWorkspace `json:"covering,omitempty"`
	LastCompletedAt *time.Time         `json:"last_completed_at,omitempty"`
	Sentence        string             `json:"sentence"`
	Route           string             `json:"route,omitempty"`
	RouteLabel      string             `json:"route_label,omitempty"`
	AsOf            time.Time          `json:"as_of"`
}

// ProviderLabel names a fixed provider id; an unknown id never echoes itself.
func ProviderLabel(provider string) string {
	switch provider {
	case "restic":
		return "restic"
	case "time_machine":
		return "Time Machine"
	default:
		return "a backup provider"
	}
}

// WorkspaceProtection reads the fact for ws from the same stored coverage
// read as Today, over scope plus ws itself (callers add it with Including, so
// its own folder is known), so the scope's Backup & Recovery workspaces can
// cover it. ok is false when ws has no adopted folder. chooserRoute is where
// "Check protection" leads with no console in scope.
func WorkspaceProtection(ctx context.Context, source BackupCoverageSource, owner string, scope WorkspaceScopeResult, ws *workspace.Workspace, chooserRoute string, now time.Time) (fact ProtectionFact, ok bool) {
	if ws == nil {
		return ProtectionFact{}, false
	}
	scope = scope.Including(nil, ws)
	// The viewed workspace's folders come first, so the scope's folder cap can
	// never leave it out.
	ordered := []ScopedWorkspace{}
	for _, candidate := range scope.Workspaces {
		if candidate.ID == ws.ID {
			ordered = append([]ScopedWorkspace{candidate}, ordered...)
		} else {
			ordered = append(ordered, candidate)
		}
	}
	scope.Workspaces = ordered
	if folders, _ := ScopedAdoptedFolders(WorkspaceScopeResult{Workspaces: ordered[:1], managedFolders: scope.managedFolders}); len(folders) == 0 {
		return ProtectionFact{}, false
	}
	coverage := ReadCoverage(ctx, source, owner, scope)

	fact = ProtectionFact{State: backuprecovery.CoverageUnknown, Reason: "evidence_unavailable", AsOf: now.UTC()}
	for _, row := range coverage.Workspaces {
		if row.Workspace.ID == ws.ID {
			fact.State, fact.Reason, fact.Covering = row.State, row.Reason, row.Covering
			if row.State == backuprecovery.CoverageCovered {
				fact.Provider, fact.ProviderLabel, fact.LastCompletedAt = row.Provider, ProviderLabel(row.Provider), row.LastCompletedAt
			}
		}
	}
	console := CoverageConsoleRoute(coverage.Console)
	switch fact.State {
	case backuprecovery.CoverageCovered:
		fact.Sentence = fmt.Sprintf("Protected by %s in %s, last completed backup %s", fact.ProviderLabel, fact.Covering.Name, relativeAge(*fact.LastCompletedAt, now))
		fact.Route, fact.RouteLabel = CoverageConsoleRoute(fact.Covering), "Open Backup & Recovery"
	case backuprecovery.CoverageNotCovered:
		fact.Sentence = "Not in any backup Ori observes"
		fact.Route, fact.RouteLabel = chooserRoute, "Check protection"
		if coverage.Console != nil {
			fact.Route = console
		}
	default:
		fact.Sentence = "Protection unknown: backup evidence is unavailable"
		if console != "" {
			fact.Route, fact.RouteLabel = console, "Open Backup & Recovery"
		}
	}
	return fact, true
}

// ProtectionPromptLine is the compiled, host-owned fact line for the workspace
// entry agent: the same sentence, labelled as stored evidence with its time.
// It names no folder and offers the agent nothing to run or open.
func ProtectionPromptLine(fact ProtectionFact) string {
	return fmt.Sprintf("Backup protection of this workspace's folder (stored evidence as of %s, not a new check): %s. Protection is set up in the Backup & Recovery workspace.",
		fact.AsOf.UTC().Format(time.RFC3339), fact.Sentence)
}

func relativeAge(at, now time.Time) string {
	age := now.Sub(at)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%d min ago", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(age.Hours()/24))
	}
}
