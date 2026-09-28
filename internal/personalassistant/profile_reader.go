package personalassistant

import (
	"context"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// AgentStoreProfileReader adapts the global agent store to the narrow
// ProfileReader seam. It reads exactly one thing — the durable ownership tags
// on the named profile — and deliberately drops every other field of the agent
// record so a read projection can never leak a prompt, model, or credential.
type AgentStoreProfileReader struct {
	agents      store.Store
	attachments ContinuityAttachmentReader
}

// NewAgentStoreProfileReader wraps the global agent store.
func NewAgentStoreProfileReader(agents store.Store) *AgentStoreProfileReader {
	return &AgentStoreProfileReader{agents: agents}
}

// NewContinuityAgentStoreProfileReader opts in to receipt-owned exact profile
// reads. Do not compose this with a legacy store that cannot resolve workspace
// identity; absence then fails closed for imported assistant relationships.
func NewContinuityAgentStoreProfileReader(agents store.Store, attachments ContinuityAttachmentReader) *AgentStoreProfileReader {
	return &AgentStoreProfileReader{agents: agents, attachments: attachments}
}

func (r *AgentStoreProfileReader) ImportedProfileProvenance(ctx context.Context, workspaceID, name string) (ProfileProvenance, bool, bool) {
	if r == nil || r.attachments == nil || !workspacecontinuity.ValidID(workspaceID) {
		return ProfileProvenance{}, false, false
	}
	attachment, err := r.attachments.Attachment(ctx, workspaceID)
	if err != nil {
		return ProfileProvenance{}, false, true
	}
	// Attachment rows are written only by imports, discovery and preparation.
	// A relationship whose HQ has none (or a native one) predates continuity
	// and keeps its original roster provenance lookup.
	if attachment.Version == 0 || attachment.State == workspacecontinuity.Native {
		return ProfileProvenance{}, false, false
	}
	if attachment.State != workspacecontinuity.ImportedInactive && attachment.State != workspacecontinuity.ImportedActive || attachment.Disposition != workspacecontinuity.AdoptedHQ {
		return ProfileProvenance{}, false, true
	}
	scoped, ok := r.agents.(interface {
		WorkspaceAgentExact(string, string) (*agent.Agent, bool)
	})
	if !ok || strings.TrimSpace(name) == "" {
		return ProfileProvenance{}, false, true
	}
	entry, found := scoped.WorkspaceAgentExact(workspaceID, name)
	if !found || entry == nil {
		return ProfileProvenance{}, false, true
	}
	var tags []string
	if entry.Metadata != nil {
		tags = entry.Metadata.Tags
	}
	return ProfileProvenanceFromTags(name, tags), true, true
}

var (
	_ ProfileReader         = (*AgentStoreProfileReader)(nil)
	_ RecoveryProfileLister = (*AgentStoreProfileReader)(nil)
)

// PersonalAssistantProfileProvenance returns bounded ownership for the profile
// stored under name.
func (r *AgentStoreProfileReader) PersonalAssistantProfileProvenance(name string) (ProfileProvenance, bool) {
	name = strings.TrimSpace(name)
	if r == nil || r.agents == nil || name == "" {
		return ProfileProvenance{}, false
	}
	record, found := r.agents.GetAgent(name)
	if !found || record == nil {
		return ProfileProvenance{}, false
	}
	var tags []string
	if record.Metadata != nil {
		tags = record.Metadata.Tags
	}
	return ProfileProvenanceFromTags(name, tags), true
}

// PersonalAssistantRecoveryProfiles lists only profiles carrying at least one
// PAF provenance marker. A partial marker remains visible to the recovery
// coordinator so it can fail closed instead of treating corrupt evidence as a
// fresh install.
func (r *AgentStoreProfileReader) PersonalAssistantRecoveryProfiles() []RecoveryProfile {
	if r == nil || r.agents == nil {
		return nil
	}
	names := append([]string(nil), r.agents.ListAgents()...)
	sort.Strings(names)
	profiles := make([]RecoveryProfile, 0, 1)
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		record, found := r.agents.GetAgent(name)
		if name == "" || !found || record == nil {
			continue
		}
		var tags []string
		if record.Metadata != nil {
			tags = record.Metadata.Tags
		}
		provenance, marked := recoveryProfileProvenance(name, tags)
		if !marked {
			continue
		}
		profile := RecoveryProfile{
			Name: name, AssistantID: provenance.AssistantID,
			HireRequestID: provenance.HireRequestID, Role: record.Role,
			Appearance: record.Appearance.Clone(),
		}
		if record.Statistics != nil {
			profile.CreatedAt = record.Statistics.CreatedAt
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

// recoveryProfileProvenance preserves malformed and duplicate ownership
// markers as blocked evidence. The ordinary provenance reader is intentionally
// tolerant for legacy read paths, but recovery must never let tag order choose
// between multiple durable identities.
func recoveryProfileProvenance(name string, tags []string) (ProfileProvenance, bool) {
	assistantMarkers := 0
	hireMarkers := 0
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		switch {
		case strings.HasPrefix(trimmed, ProfileAssistantMarkerPrefix):
			assistantMarkers++
		case strings.HasPrefix(trimmed, ProfileHireMarkerPrefix):
			hireMarkers++
		}
	}
	if assistantMarkers == 0 && hireMarkers == 0 {
		return ProfileProvenance{Name: strings.TrimSpace(name)}, false
	}
	provenance := ProfileProvenanceFromTags(name, tags)
	if assistantMarkers != 1 || hireMarkers != 1 ||
		strings.TrimSpace(provenance.AssistantID) == "" || strings.TrimSpace(provenance.HireRequestID) == "" {
		return ProfileProvenance{Name: strings.TrimSpace(name)}, true
	}
	return provenance, true
}
