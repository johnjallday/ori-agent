package projectstaffing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// One definition, edited in one place (D10). Every project the shared agent
// works on runs its own copy of it (the runtime resolver reads the project's
// copy first), so an edit to the user's agent reaches a project only when it is
// carried into that copy. The consent records, per project, the digest of the
// copy as Ori last wrote it; a copy that no longer has that digest was changed
// in its project and keeps the change.

// carried is the part of an agent definition an edit carries: the model and
// the prompt, and the settings edited with them. Keys, web search, native tools
// and cloud fallbacks are not carried: a project keeps the access it was given.
type carried struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	ReasoningEffort string  `json:"reasoning_effort"`
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	SystemPrompt    string  `json:"system_prompt"`
}

func carriedOf(ag *agent.Agent) carried {
	return carried{
		Provider: ag.Settings.Provider, Model: ag.Settings.Model, ReasoningEffort: ag.Settings.ReasoningEffort,
		Temperature: ag.Settings.Temperature, MaxOutputTokens: ag.Settings.MaxOutputTokens, SystemPrompt: ag.Settings.SystemPrompt,
	}
}

// CopyDigest fingerprints the carried part of an agent definition.
func CopyDigest(ag *agent.Agent) string {
	if ag == nil {
		return ""
	}
	encoded, _ := json.Marshal(carriedOf(ag))
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// carryInto writes the carried part of from into the copy; nothing else in the
// copy changes.
func carryInto(copyAgent, from *agent.Agent) {
	c := carriedOf(from)
	copyAgent.Settings.Provider, copyAgent.Settings.Model = c.Provider, c.Model
	copyAgent.Settings.ReasoningEffort, copyAgent.Settings.Temperature = c.ReasoningEffort, c.Temperature
	copyAgent.Settings.MaxOutputTokens, copyAgent.Settings.SystemPrompt = c.MaxOutputTokens, c.SystemPrompt
}

// trackCopies records, for each consent role whose agent fills that role in the
// song, the song's copy as it is now: Settle runs right after the staffing that
// wrote it.
func (s *Service) trackCopies(song Song) error {
	consent, err := s.consents.Read(song.Home.ID)
	if err != nil || consent == nil {
		return nil
	}
	filled := song.FilledRoles()
	for _, role := range consent.Roles {
		instance, found := filled[role.RoleID]
		if role.AgentName == "" || !found || instance.Name != role.AgentName || tracked(role, song.Project.ID) {
			continue
		}
		copyAgent, ok, err := s.workspaces.GetWorkspaceAgent(song.Project.ID, role.AgentName)
		if err != nil || !ok || copyAgent == nil {
			continue
		}
		if err := s.consents.TrackCopy(song.Home.ID, role.RoleID, role.AgentName, song.Project.ID, CopyDigest(copyAgent)); err != nil {
			return err
		}
	}
	return nil
}

func tracked(role workspace.ProjectStaffingConsentRole, projectID string) bool {
	for _, entry := range role.Copies {
		if entry.WorkspaceID == projectID {
			return true
		}
	}
	return false
}

// CarryReport says where an edit to a shared agent went.
type CarryReport struct {
	// Updated are the projects whose copy now has the edit.
	Updated []workspace.WorkspaceRef
	// Customised are the projects whose copy was changed there; they keep it.
	Customised []workspace.WorkspaceRef
}

// Carry brings the user's agent's current model and prompt into every project
// copy a consent tracks for it, except a copy changed in its project. It is
// idempotent: a copy that already matches is left alone, so it is safe to run
// after any save.
func (s *Service) Carry(agentName string) (CarryReport, error) {
	var report CarryReport
	if s == nil || s.workspaces == nil || !s.agentExists(agentName) {
		return report, nil
	}
	shared, found := s.agents.GetAgent(agentName)
	if !found || shared == nil {
		return report, nil
	}
	target := CopyDigest(shared)
	var failures []error
	for _, homeID := range s.homesSharing(agentName) {
		consent, err := s.consents.Read(homeID)
		if err != nil || consent == nil {
			continue
		}
		for _, role := range consent.Roles {
			if role.AgentName != agentName || len(role.Copies) == 0 {
				continue
			}
			digests, drop, err := s.carryRole(role, shared, target, &report)
			if err != nil {
				failures = append(failures, err)
			}
			if err := s.consents.SetCopyDigests(homeID, role.RoleID, agentName, digests, drop); err != nil {
				failures = append(failures, err)
			}
		}
	}
	sortRefs(report.Updated)
	sortRefs(report.Customised)
	return report, errors.Join(failures...)
}

// carryRole carries the edit into one role's tracked copies and returns the new
// digests and the projects that no longer have the agent.
func (s *Service) carryRole(role workspace.ProjectStaffingConsentRole, shared *agent.Agent, target string, report *CarryReport) (map[string]string, []string, error) {
	digests := map[string]string{}
	var drop []string
	var failures []error
	for _, entry := range role.Copies {
		song, err := s.Song(entry.WorkspaceID)
		if err != nil {
			drop = append(drop, entry.WorkspaceID) // deleted, or no longer a linked project
			continue
		}
		if instance, found := song.FilledRoles()[role.RoleID]; !found || instance.Name != role.AgentName {
			drop = append(drop, entry.WorkspaceID) // the role was cleared or given to another agent
			continue
		}
		copyAgent, ok, err := s.workspaces.GetWorkspaceAgent(song.Project.ID, role.AgentName)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !ok || copyAgent == nil {
			drop = append(drop, entry.WorkspaceID)
			continue
		}
		ref := workspace.WorkspaceRef{ID: song.Project.ID, FolderSlug: song.Project.FolderSlug, Name: song.Project.Name}
		switch current := CopyDigest(copyAgent); {
		case current == target:
			digests[entry.WorkspaceID] = target // already current (or a record that missed a write)
		case current != entry.Digest:
			report.Customised = append(report.Customised, ref)
		default:
			carryInto(copyAgent, shared)
			if err := s.workspaces.SaveWorkspaceAgent(song.Project.ID, role.AgentName, copyAgent); err != nil {
				failures = append(failures, err)
				continue
			}
			digests[entry.WorkspaceID] = target
			report.Updated = append(report.Updated, ref)
		}
	}
	return digests, drop, errors.Join(failures...)
}

// InStep lists the projects whose tracked copy of the agent is still as Ori
// wrote it: those are the agent itself, not a customisation of it.
func (s *Service) InStep(agentName string) map[string]bool {
	inStep := map[string]bool{}
	if s == nil || s.workspaces == nil {
		return inStep
	}
	for _, homeID := range s.homesSharing(agentName) {
		consent, err := s.consents.Read(homeID)
		if err != nil || consent == nil {
			continue
		}
		for _, role := range consent.Roles {
			if role.AgentName != agentName {
				continue
			}
			for _, entry := range role.Copies {
				copyAgent, ok, err := s.workspaces.GetWorkspaceAgent(entry.WorkspaceID, agentName)
				if err == nil && ok && CopyDigest(copyAgent) == entry.Digest {
					inStep[entry.WorkspaceID] = true
				}
			}
		}
	}
	return inStep
}

// homesSharing lists the Homes of the projects the agent works in. Only those
// can hold a consent that tracks a copy of it.
func (s *Service) homesSharing(agentName string) []string {
	seen := map[string]bool{}
	var homes []string
	for _, ref := range workspace.WorkspaceMembershipFor(s.workspaces, agentName).Workspaces {
		project, err := s.workspaces.Get(ref.ID)
		if err != nil || project == nil {
			continue
		}
		link := project.GetAssistantProjectLink()
		if link == nil || strings.TrimSpace(link.StationWorkspaceID) == "" || seen[link.StationWorkspaceID] {
			continue
		}
		seen[link.StationWorkspaceID] = true
		homes = append(homes, link.StationWorkspaceID)
	}
	sort.Strings(homes)
	return homes
}

func sortRefs(refs []workspace.WorkspaceRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].ID < refs[j].ID
	})
}
