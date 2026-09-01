package personalassistant

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
)

// AgentStoreProfileReader adapts the global agent store to the narrow
// ProfileReader seam. It reads exactly one thing — the durable ownership tags
// on the named profile — and deliberately drops every other field of the agent
// record so a read projection can never leak a prompt, model, or credential.
type AgentStoreProfileReader struct {
	agents store.Store
}

// NewAgentStoreProfileReader wraps the global agent store.
func NewAgentStoreProfileReader(agents store.Store) *AgentStoreProfileReader {
	return &AgentStoreProfileReader{agents: agents}
}

var _ ProfileReader = (*AgentStoreProfileReader)(nil)

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
