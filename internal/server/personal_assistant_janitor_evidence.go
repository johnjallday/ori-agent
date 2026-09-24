package server

import (
	"context"
	"errors"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	errJanitorKnowledgeSourceUnavailable = errors.New("personal assistant: File Janitor evidence unavailable")
	errJanitorKnowledgeAccessRevoked     = errors.New("personal assistant: File Janitor read access missing or revoked")
)

// janitorKnowledgeEvidence contains only server-verified metadata. A FileAction
// never leaves the adapter: source and destination filenames, absolute root
// paths, fingerprints and Trash tokens cannot enter sidecar, API or prompts.
type janitorKnowledgeEvidence struct {
	WorkspaceID string
	RootID      string
	Category    filejanitor.Category
	ActionIDs   []string
	CompletedAt []time.Time
	SupportIDs  []string // bounded, server-only set for approval/context revalidation
}

type janitorKnowledgeReader struct {
	bindings *personalassistant.KnowledgeResolver
	briefs   interface {
		GetConfig(context.Context, string) (*dailybrief.Config, error)
	}
	workspaces dailybrief.WorkspaceSource
	janitor    janitorActionSource
	now        func() time.Time
}

// ReadFresh resolves consent and owner from current canonical stores before
// touching a Janitor journal. Any missing scope, root, permission or journal
// read is an unavailable source, not an authorized empty result.
func (r *janitorKnowledgeReader) ReadFresh(ctx context.Context, userID string) ([]janitorKnowledgeEvidence, error) {
	at := time.Now().UTC()
	if r != nil && r.now != nil {
		at = r.now().UTC()
	}
	var results []janitorKnowledgeEvidence
	err := r.visitJournals(ctx, userID, false, func(workspaceID, ownerID string, settings filejanitor.JanitorSettings, actions []filejanitor.FileAction) {
		results = append(results, projectJanitorKnowledgeSupports(workspaceID, ownerID, settings, actions, at)...)
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].WorkspaceID != results[j].WorkspaceID {
			return results[i].WorkspaceID < results[j].WorkspaceID
		}
		return results[i].Category < results[j].Category
	})
	return results, nil
}

// UndoneSupportIDs reads only authorized, current-root journals. It detects
// durable UndoDone independently of today's proposal threshold: after one of
// three supports is undone, the remaining two cannot form a new proposal but
// the old approval must still be suspended. While paused, this recovery read
// is permitted; context and new proposals remain disabled.
func (r *janitorKnowledgeReader) UndoneSupportIDs(ctx context.Context, userID string) (map[string]bool, error) {
	undone := make(map[string]bool)
	err := r.visitJournals(ctx, userID, true, func(workspaceID, ownerID string, settings filejanitor.JanitorSettings, actions []filejanitor.FileAction) {
		for _, action := range actions {
			if action.WorkspaceID == workspaceID && action.RootID == settings.RootID &&
				action.ApprovedBy == ownerID && !action.ApprovedAt.IsZero() &&
				action.Operation == filejanitor.OperationMove && action.Result == filejanitor.ResultApplied &&
				action.Undo == filejanitor.UndoDone && action.Validate() == nil {
				undone[action.ID] = true
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return undone, nil
}

// visitJournals is the single source authorization gate for suggestions,
// approval revalidation and undo reconciliation. No callback runs before
// resolving the saved brief scope, built-in provenance, owner and read-only
// root permissions. An incomplete scope or unavailable source fails closed.
func (r *janitorKnowledgeReader) visitJournals(ctx context.Context, userID string, allowPaused bool, visit func(string, string, filejanitor.JanitorSettings, []filejanitor.FileAction)) error {
	if r == nil || r.bindings == nil || r.briefs == nil || r.workspaces == nil || r.janitor == nil {
		return errJanitorKnowledgeSourceUnavailable
	}
	binding, err := r.bindings.Resolve(ctx, userID)
	if err != nil {
		return err
	}
	if binding.Paused && !allowPaused {
		return personalassistant.ErrKnowledgePaused
	}
	cfg, err := r.briefs.GetConfig(ctx, binding.HQWorkspaceID)
	if err != nil || cfg == nil || cfg.WorkspaceID != binding.HQWorkspaceID {
		return errJanitorKnowledgeSourceUnavailable
	}
	scope := dailybrief.ResolveWorkspaceScope(r.workspaces, *cfg, binding.UserID)
	if !scope.Complete {
		return errJanitorKnowledgeSourceUnavailable
	}
	verifiedRoots := make(map[string]filejanitor.JanitorSettings)
	originalIDs := make(map[string]bool, len(scope.Workspaces))
	for _, scoped := range scope.Workspaces {
		originalIDs[scoped.ID] = true
		if scoped.Workspace == nil || scoped.Workspace.ID != scoped.ID || !isBuiltInJanitorSource(scoped.Workspace) ||
			!ownedBy(scoped.Workspace, binding.UserID) {
			continue
		}
		status, err := r.janitor.Status(scoped.ID)
		if err != nil {
			return errJanitorKnowledgeSourceUnavailable
		}
		if !janitorKnowledgeReadAllowed(status, scoped.ID) {
			// Only an observed failed required read check is a revoked/missing
			// permission. A malformed status, unknown root or failed read is
			// unavailable, not evidence of an intentional revocation.
			for _, check := range status.Readiness.Checks {
				if (check.Component == filejanitor.ComponentDirectoryAccess || check.Component == filejanitor.ComponentMCPBinding) &&
					check.Status != filejanitor.ComponentOK {
					return errJanitorKnowledgeAccessRevoked
				}
			}
			return errJanitorKnowledgeSourceUnavailable
		}
		actions, err := r.janitor.ListActions(scoped.ID)
		if err != nil || len(actions) > 512 {
			return errJanitorKnowledgeSourceUnavailable
		}
		visit(scoped.ID, binding.UserID, status.Settings, actions)
		verifiedRoots[scoped.ID] = status.Settings
	}
	// Scope, ownership, root or read permissions can change while journals
	// are read. Re-read all of them before admitting any observation. A
	// concurrent action within the journal still fails closed on the next
	// source revalidation before context or a canonical approval.
	latestCfg, err := r.briefs.GetConfig(ctx, binding.HQWorkspaceID)
	if err != nil || latestCfg == nil || !reflect.DeepEqual(*latestCfg, *cfg) {
		return errJanitorKnowledgeSourceUnavailable
	}
	latestScope := dailybrief.ResolveWorkspaceScope(r.workspaces, *latestCfg, binding.UserID)
	if !latestScope.Complete || len(latestScope.Workspaces) != len(scope.Workspaces) {
		return errJanitorKnowledgeSourceUnavailable
	}
	for _, scoped := range latestScope.Workspaces {
		if scoped.Workspace == nil || scoped.Workspace.ID != scoped.ID || !originalIDs[scoped.ID] {
			return errJanitorKnowledgeSourceUnavailable
		}
		if settings, found := verifiedRoots[scoped.ID]; found {
			if !isBuiltInJanitorSource(scoped.Workspace) || !ownedBy(scoped.Workspace, binding.UserID) {
				return errJanitorKnowledgeSourceUnavailable
			}
			status, err := r.janitor.Status(scoped.ID)
			if err != nil || !janitorKnowledgeReadAllowed(status, scoped.ID) || !reflect.DeepEqual(status.Settings, settings) {
				return errJanitorKnowledgeSourceUnavailable
			}
		}
	}
	// Re-resolve the assistant principal last: a changed relationship is not
	// an empty or transferable Janitor source.
	current, err := r.bindings.Resolve(ctx, binding.UserID)
	if err != nil || current != binding {
		return errJanitorKnowledgeSourceUnavailable
	}
	return nil
}

// projectJanitorKnowledgeSupports is pure and returns no file name/path or
// raw action record. Failed undo records (which may have UndoneAt set) still
// count as applied support; only a durable UndoDone removes that support.
func projectJanitorKnowledgeSupports(workspaceID, userID string, settings filejanitor.JanitorSettings, actions []filejanitor.FileAction, now time.Time) []janitorKnowledgeEvidence {
	cutoff := now.Add(-90 * 24 * time.Hour)
	byCategory := make(map[filejanitor.Category][]filejanitor.FileAction)
	seen := make(map[string]bool)
	for _, action := range actions {
		if action.WorkspaceID != workspaceID || action.RootID != settings.RootID ||
			action.ApprovedBy != userID || action.ApprovedAt.IsZero() || action.CompletedAt.IsZero() ||
			action.CompletedAt.Before(cutoff) || action.CompletedAt.After(now) ||
			action.Operation != filejanitor.OperationMove || action.Result != filejanitor.ResultApplied ||
			action.Undo == filejanitor.UndoDone || !filejanitor.ValidCategory(action.DestinationCategory) ||
			action.AfterFingerprint.Zero() || action.Validate() != nil || seen[action.ID] {
			continue
		}
		canonicalDestination, err := filejanitor.DestinationRelativeFor(
			settings.FilingRootName, action.DestinationCategory, path.Base(action.DestinationRelative))
		if err != nil || canonicalDestination != action.DestinationRelative {
			continue
		}
		seen[action.ID] = true
		byCategory[action.DestinationCategory] = append(byCategory[action.DestinationCategory], action)
	}
	var results []janitorKnowledgeEvidence
	for category, supports := range byCategory {
		if len(supports) < 3 {
			continue
		}
		sort.Slice(supports, func(i, j int) bool {
			if supports[i].CompletedAt.Equal(supports[j].CompletedAt) {
				return supports[i].ID < supports[j].ID
			}
			return supports[i].CompletedAt.After(supports[j].CompletedAt)
		})
		item := janitorKnowledgeEvidence{WorkspaceID: workspaceID, RootID: settings.RootID, Category: category}
		for _, support := range supports {
			item.SupportIDs = append(item.SupportIDs, support.ID)
			if len(item.ActionIDs) < 3 {
				item.ActionIDs = append(item.ActionIDs, support.ID)
				item.CompletedAt = append(item.CompletedAt, support.CompletedAt)
			}
		}
		results = append(results, item)
	}
	return results
}

func isBuiltInJanitorSource(ws *workspace.Workspace) bool {
	if ws == nil {
		return false
	}
	p := ws.GetTemplateProvenance()
	return p != nil && p.Builtin && p.PluginOwner == nil &&
		(strings.EqualFold(p.TemplateID, fileJanitorBlueprintID) || strings.EqualFold(p.TemplateID, filejanitor.LegacyTemplateID))
}

func janitorKnowledgeReadAllowed(status filejanitor.Status, workspaceID string) bool {
	s := status.Settings
	if !status.Applies || s.WorkspaceID != workspaceID || !s.IsSetUp() ||
		strings.TrimSpace(s.RootID) == "" || strings.TrimSpace(s.RootConflictWorkspaceID) != "" {
		return false
	}
	required := map[filejanitor.ReadinessComponent]bool{
		filejanitor.ComponentDirectoryAccess: false,
		filejanitor.ComponentMCPBinding:      false,
	}
	for _, check := range status.Readiness.Checks {
		if _, found := required[check.Component]; found {
			if required[check.Component] || check.Status != filejanitor.ComponentOK {
				return false // duplicate or failed permission check
			}
			required[check.Component] = true
		}
	}
	for _, passed := range required {
		if !passed {
			return false
		}
	}
	return true
}
