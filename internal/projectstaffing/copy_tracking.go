package projectstaffing

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// How one workspace copy of an agent relates to the record a Home's standing
// consent keeps for it. Reported by CopyTracking; read-only.
const (
	// CopyInStep: Ori tracks the copy and it holds the library's carried
	// settings.
	CopyInStep = "in_step"
	// CopyPending: Ori tracks the copy and it is unchanged in its project, but
	// it does not hold the library's current carried settings yet. The next
	// carry would update it.
	CopyPending = "pending"
	// CopyCustomized: the copy was changed in its project since Ori last wrote
	// it. A carry leaves it alone.
	CopyCustomized = "customized"
	// CopyUntracked: no consent tracks this copy. It is the workspace's own,
	// independent copy; a library edit never reaches it.
	CopyUntracked = "untracked"
	// CopyUnknown: the tracking record could not be read or confirmed.
	CopyUnknown = "unknown"
)

// CopyTracking reports how the given workspace copy of agentName relates to
// its consent tracking record, comparing the definitions the caller already
// read (library may be nil when the agent has no library profile).
//
// It mirrors carryRole's decisions without performing them: it never carries,
// never updates digests and never drops a record. Matching values alone are
// not tracking: a copy no consent records is untracked even when it equals the
// library.
func (s *Service) CopyTracking(agentName, workspaceID string, library, copyAgent *agent.Agent) (string, error) {
	if s == nil || s.workspaces == nil || s.consents == nil {
		return CopyUnknown, errors.New("copy tracking is not available")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if copyAgent == nil || workspaceID == "" || agentName == "" {
		return CopyUnknown, errors.New("no workspace copy to compare")
	}
	project, err := s.workspaces.Get(workspaceID)
	if err != nil || project == nil {
		return CopyUnknown, errors.New("the workspace could not be read")
	}
	link := project.GetAssistantProjectLink()
	if link == nil || strings.TrimSpace(link.StationWorkspaceID) == "" {
		// Only a linked project's copy is ever tracked.
		return CopyUntracked, nil
	}
	consent, err := s.consents.Read(link.StationWorkspaceID)
	if err != nil {
		return CopyUnknown, errors.New("the Home's staffing record could not be read")
	}
	if consent == nil {
		return CopyUntracked, nil
	}
	// Every role of this agent that records this project counts: Validate
	// allows one agent in several roles, and Carry visits each of them. A
	// revoked consent is still carried, so revocation is not checked here.
	type record struct{ roleID, digest string }
	var records []record
	for _, role := range consent.Roles {
		if role.AgentName != agentName {
			continue
		}
		for _, entry := range role.Copies {
			if entry.WorkspaceID == workspaceID {
				records = append(records, record{role.RoleID, entry.Digest})
			}
		}
	}
	if len(records) == 0 {
		return CopyUntracked, nil
	}
	if library == nil {
		// Carry does nothing for an agent without a library definition.
		return CopyUnknown, errors.New("the library profile could not be read")
	}
	// carryRole drops a record whose project is no longer a linked song or
	// whose role went to another agent; report that as unconfirmed rather than
	// as a live tracking relationship.
	song, err := s.Song(workspaceID)
	if err != nil {
		return CopyUnknown, errors.New("the project's link to its Home could not be confirmed")
	}
	filled := song.FilledRoles()
	recorded := ""
	for _, rec := range records {
		if instance, ok := filled[rec.roleID]; !ok || instance.Name != agentName {
			return CopyUnknown, errors.New("a tracked role is no longer filled by this agent")
		}
		if recorded != "" && recorded != rec.digest {
			return CopyUnknown, errors.New("the tracking records for this copy disagree")
		}
		recorded = rec.digest
	}
	current, target := CopyDigest(copyAgent), CopyDigest(library)
	switch current {
	case target:
		return CopyInStep, nil
	case recorded:
		return CopyPending, nil
	default:
		return CopyCustomized, nil
	}
}
