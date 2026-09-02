package sessionhttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
)

// CreatePersonalAssistantProfile creates — or takes ownership of — exactly one
// global agent profile for a confirmed hire, using the same canonical
// personal-ops entry specification the eventual Personal HQ entry agent must
// use: the orchestrator role, the trusted Personal Assistant prompt fragment
// layered onto the template's Chief of Staff prompt, the template's model
// defaults, and the validated appearance.
//
// It deliberately creates nothing else. No workspace, no Journal or other
// support profile, no workspace membership, no tool/skill/MCP/Vault binding,
// and no filesystem scope change. Personal HQ is built later, from the guided
// Map quest, around this profile.
//
// Ownership is durable and structural: the created profile is stamped with
// namespaced provenance markers binding it to the stable assistant ID and this
// hire request ID. A retry therefore recognizes its own profile, while an
// unrelated agent that merely shares the name is a conflict rather than
// something to adopt.
func (h *Handler) CreatePersonalAssistantProfile(ctx context.Context, options personalhq.AssistantCreationOptions) (*personalhq.AssistantProfileResult, error) {
	if h == nil || h.agentStore == nil {
		return nil, fmt.Errorf("session handler is not configured for personal assistant creation")
	}
	options.AssistantID = strings.TrimSpace(options.AssistantID)
	options.RequestID = strings.TrimSpace(options.RequestID)
	if options.AssistantID == "" || options.RequestID == "" {
		return nil, fmt.Errorf("personal assistant and hire request ids are required")
	}

	tpl, err := h.personalHQTemplate()
	if err != nil {
		return nil, err
	}
	// Reuse the same validation the HQ path applies: reserved Ori names, support
	// roster collisions, appearance rules, prompt bounds, and the orchestrator
	// role are all rejected here rather than at workspace creation time.
	applied, err := applyPersonalAssistantTemplateOptions(tpl, options)
	if err != nil {
		return nil, err
	}
	entrySpec := applied.Agents[0]
	name := strings.TrimSpace(entrySpec.Name)

	if existing, found := h.agentStore.GetAgent(name); found {
		return resolveOwnedPersonalAssistantProfile(name, existing, options)
	}

	cfg, _ := h.templateAgentCreateConfig(entrySpec)
	if err := h.agentStore.CreateAgent(name, cfg); err != nil {
		return nil, fmt.Errorf("create personal assistant profile: %w", err)
	}
	if err := h.markPersonalAssistantProfile(name, options); err != nil {
		// The marker is what makes the profile recoverable, so an unmarked
		// profile is worse than none: a retry could not tell it from an unrelated
		// agent and would refuse to continue. Remove it and let the caller retry.
		if deleteErr := h.agentStore.DeleteAgent(name); deleteErr != nil {
			return nil, fmt.Errorf("personal assistant profile could not be marked or rolled back: %w", err)
		}
		return nil, fmt.Errorf("mark personal assistant profile: %w", err)
	}
	return &personalhq.AssistantProfileResult{GlobalAgentProfileName: name}, nil
}

// assertProfileOwnedByAssistant checks the one thing that makes an existing
// profile safe to reuse: bounded provenance proving this relationship owns it.
//
// Both creation paths need this. Neither may fall back to a name match: an
// unrelated user-created agent, or another relationship's assistant, is a
// conflict rather than something to adopt.
func assertProfileOwnedByAssistant(name string, existing *agent.Agent, assistantID string) (personalassistant.ProfileProvenance, error) {
	var tags []string
	if existing != nil && existing.Metadata != nil {
		tags = existing.Metadata.Tags
	}
	provenance := personalassistant.ProfileProvenanceFromTags(name, tags)
	if !provenance.OwnedBy(assistantID) {
		return provenance, fmt.Errorf("%w: %q collides with an existing global agent",
			personalhq.ErrAssistantNameConflict, name)
	}
	return provenance, nil
}

// resolveOwnedPersonalAssistantProfile decides whether an existing profile is
// this relationship's own, previously-created profile.
//
// On the hire path the request ID is the hire request, and the profile records
// the hire that created it, so a mismatch means two different hires are fighting
// over one name. (The HQ path carries an HQ request ID instead and therefore
// checks assistant ownership only — see assertProfileOwnedByAssistant.)
func resolveOwnedPersonalAssistantProfile(name string, existing *agent.Agent, options personalhq.AssistantCreationOptions) (*personalhq.AssistantProfileResult, error) {
	provenance, err := assertProfileOwnedByAssistant(name, existing, options.AssistantID)
	if err != nil {
		return nil, err
	}
	if provenance.HireRequestID != "" && provenance.HireRequestID != options.RequestID {
		return nil, fmt.Errorf("%w: %q was created by a different hire request",
			personalhq.ErrAssistantNameConflict, name)
	}
	return &personalhq.AssistantProfileResult{GlobalAgentProfileName: name, Reused: true}, nil
}

// markPersonalAssistantProfile stamps the durable ownership markers. It adds
// only tags: no role, prompt, model, tool, skill, or permission is touched.
func (h *Handler) markPersonalAssistantProfile(name string, options personalhq.AssistantCreationOptions) error {
	return h.agentStore.UpdateAgent(name, func(record *agent.Agent) error {
		if record == nil {
			return fmt.Errorf("agent %q disappeared before it could be marked", name)
		}
		if record.Metadata == nil {
			record.Metadata = &types.AgentMetadata{}
		}
		tags, err := personalassistant.EnsureProfileMarkers(record.Metadata.Tags, options.AssistantID, options.RequestID)
		if err != nil {
			return err
		}
		record.Metadata.Tags = tags
		return nil
	})
}
