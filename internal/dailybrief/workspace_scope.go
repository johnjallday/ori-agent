package dailybrief

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxWorkspaceScopeGaps bounds workspace-resolution failures carried into a
// snapshot or Today projection. The final slot summarizes any additional
// failures so a damaged selected scope cannot grow an API response without
// limit.
const maxWorkspaceScopeGaps = 20

// ScopedWorkspace is one hydrated workspace that passed the saved Daily Brief
// scope and current-user access checks. ID is the canonical record identity;
// Slug is populated only when it is safe for workspace navigation.
type ScopedWorkspace struct {
	ID        string
	Slug      string
	Name      string
	Workspace *workspace.Workspace
}

// WorkspaceScopeResult is the reusable, observational result of resolving a
// saved Daily Brief workspace scope. Workspaces are hydrated and ordered by
// stable ID. Complete is false when a configured source could not be read;
// exclusions such as groups, inactive records, foreign owners, and post-cutoff
// workspaces do not make an otherwise successful read incomplete.
type WorkspaceScopeResult struct {
	Scope      Scope
	Workspaces []ScopedWorkspace
	Gaps       []string
	Complete   bool
}

// FollowUpOwner is an authorized operational owner whose canonical follow-up
// rows may be projected into Personal HQ. It carries only stable workspace
// identity and safe display/navigation fields; it grants no mutation access.
type FollowUpOwner struct {
	WorkspaceID   string
	WorkspaceSlug string
	Name          string
}

// FollowUpOwnerScope is the bounded HQ-plus-Email-Ops inclusion result. Gaps
// include the underlying saved-scope resolution gaps so every consumer reports
// the same degraded source state.
type FollowUpOwnerScope struct {
	Owners []FollowUpOwner
	Gaps   []string
}

// ResolveWorkspaceScope resolves cfg without changing it or any workspace.
// Selected IDs are normalized and deduplicated before Get. All-scope listings
// are treated only as candidate identities and every candidate is hydrated
// through Get before access, status, provenance, or payload decisions.
func ResolveWorkspaceScope(src WorkspaceSource, cfg Config, userID string) WorkspaceScopeResult {
	resolvedScope := cfg.Scope
	if resolvedScope != ScopeSelected {
		resolvedScope = ScopeAll
	}
	result := WorkspaceScopeResult{Scope: resolvedScope, Complete: true}
	gaps := newWorkspaceScopeGaps()
	if src == nil {
		result.Complete = false
		gaps.add("workspace data is unavailable")
		result.Gaps = gaps.values()
		return result
	}

	type candidate struct {
		id   string
		name string
	}
	candidates := make([]candidate, 0)
	if cfg.Scope == ScopeSelected {
		for _, id := range cfg.SelectedWorkspaceIDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			candidates = append(candidates, candidate{id: trimmed, name: trimmed})
		}
	} else {
		listed, err := src.ListActive()
		if err != nil {
			result.Complete = false
			gaps.add("workspace list is unavailable")
			result.Gaps = gaps.values()
			return result
		}
		for _, lean := range listed {
			if lean == nil {
				continue
			}
			id := strings.TrimSpace(lean.ID)
			if id == "" || id != lean.ID {
				// All-scope malformed identities are exclusions, not selected
				// sources that can be safely named or fetched.
				continue
			}
			name := strings.TrimSpace(lean.Name)
			if name == "" {
				name = id
			}
			candidates = append(candidates, candidate{id: id, name: name})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].id != candidates[j].id {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].name < candidates[j].name
	})
	seen := make(map[string]struct{}, len(candidates))
	userID = strings.TrimSpace(userID)
	for _, candidate := range candidates {
		if _, duplicate := seen[candidate.id]; duplicate {
			continue
		}
		seen[candidate.id] = struct{}{}
		ws, err := src.Get(candidate.id)
		if err != nil || ws == nil || strings.TrimSpace(ws.ID) != candidate.id || ws.ID != candidate.id {
			result.Complete = false
			gaps.add(fmt.Sprintf("workspace %s is unavailable", boundedWorkspaceLabel(candidate.name)))
			continue
		}
		if !workspaceEligibleForScope(ws, userID) {
			if result.Scope == ScopeSelected {
				result.Complete = false
				gaps.add(fmt.Sprintf("workspace %s is unavailable", boundedWorkspaceLabel(candidate.name)))
			}
			continue
		}
		if cfg.Scope != ScopeSelected && !cfg.IncludeFutureWorkspaces && !cfg.UpdatedAt.IsZero() && ws.CreatedAt.After(cfg.UpdatedAt) {
			continue
		}
		slug := ""
		if workspace.IsCanonicalWorkspaceSlug(ws.FolderSlug) {
			slug = ws.FolderSlug
		}
		name := strings.TrimSpace(ws.Name)
		if name == "" {
			name = ws.ID
		}
		result.Workspaces = append(result.Workspaces, ScopedWorkspace{
			ID: ws.ID, Slug: slug, Name: name, Workspace: ws,
		})
	}
	result.Gaps = gaps.values()
	return result
}

// ResolveFollowUpOwnerScope layers the bounded feature policy over a resolved
// Daily Brief scope. The designated HQ remains eligible independently of the
// saved workspace selection, preserving existing HQ-owned commitments. The
// only additional owners are scope-eligible workspaces with canonical
// email-ops template provenance. Every owner must have exact stable identity
// and a canonical route slug.
func ResolveFollowUpOwnerScope(src WorkspaceSource, scope WorkspaceScopeResult, hqWorkspaceID, userID string) FollowUpOwnerScope {
	gaps := newWorkspaceScopeGaps()
	for _, gap := range scope.Gaps {
		gaps.add(gap)
	}
	out := FollowUpOwnerScope{}
	hqWorkspaceID = strings.TrimSpace(hqWorkspaceID)
	userID = strings.TrimSpace(userID)
	byID := make(map[string]ScopedWorkspace, len(scope.Workspaces))
	for _, candidate := range scope.Workspaces {
		byID[candidate.ID] = candidate
	}

	seen := make(map[string]struct{})
	if hqWorkspaceID != "" {
		hq, ok := byID[hqWorkspaceID]
		if !ok && src != nil {
			ws, err := src.Get(hqWorkspaceID)
			if err == nil && ws != nil && ws.ID == hqWorkspaceID && workspaceEligibleForScope(ws, userID) {
				slug := ""
				if workspace.IsCanonicalWorkspaceSlug(ws.FolderSlug) {
					slug = ws.FolderSlug
				}
				name := strings.TrimSpace(ws.Name)
				if name == "" {
					name = ws.ID
				}
				hq = ScopedWorkspace{ID: ws.ID, Slug: slug, Name: name, Workspace: ws}
				ok = true
			}
		}
		if !ok || hq.Workspace == nil || hq.ID != hqWorkspaceID || hq.Slug == "" {
			gaps.add("Personal HQ workspace is unavailable")
		} else {
			out.Owners = append(out.Owners, FollowUpOwner{
				WorkspaceID: hq.ID, WorkspaceSlug: hq.Slug, Name: hq.Name,
			})
			seen[hq.ID] = struct{}{}
		}
	}

	for _, candidate := range scope.Workspaces {
		if candidate.Workspace == nil || !candidate.Workspace.IsFromTemplate(workspace.EmailOpsTemplateID) {
			continue
		}
		if _, duplicate := seen[candidate.ID]; duplicate {
			continue
		}
		if candidate.Slug == "" {
			if scope.Scope == ScopeSelected {
				gaps.add(fmt.Sprintf("workspace %s has invalid navigation identity", boundedWorkspaceLabel(candidate.Name)))
			}
			continue
		}
		out.Owners = append(out.Owners, FollowUpOwner{
			WorkspaceID: candidate.ID, WorkspaceSlug: candidate.Slug, Name: candidate.Name,
		})
		seen[candidate.ID] = struct{}{}
	}
	sort.SliceStable(out.Owners, func(i, j int) bool {
		return out.Owners[i].WorkspaceID < out.Owners[j].WorkspaceID
	})
	out.Gaps = gaps.values()
	return out
}

func workspaceEligibleForScope(ws *workspace.Workspace, userID string) bool {
	if ws == nil || isGroupWorkspace(ws) || ws.Status != workspace.StatusActive {
		return false
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	return owner == "" || strings.EqualFold(owner, userID)
}

type workspaceScopeGaps struct {
	items     []string
	seen      map[string]struct{}
	truncated bool
}

func newWorkspaceScopeGaps() *workspaceScopeGaps {
	return &workspaceScopeGaps{seen: make(map[string]struct{})}
}

func (g *workspaceScopeGaps) add(message string) {
	message = strings.TrimSpace(message)
	if message == "" || g.truncated {
		return
	}
	if _, exists := g.seen[message]; exists {
		return
	}
	g.seen[message] = struct{}{}
	if len(g.items) < maxWorkspaceScopeGaps-1 {
		g.items = append(g.items, message)
		return
	}
	g.items = append(g.items, "additional workspace sources are unavailable")
	g.truncated = true
}

func (g *workspaceScopeGaps) values() []string {
	return append([]string(nil), g.items...)
}

func boundedWorkspaceLabel(label string) string {
	label = strings.TrimSpace(label)
	runes := []rune(label)
	if len(runes) > 80 {
		label = string(runes[:80])
	}
	if label == "" {
		return "unknown"
	}
	return label
}
