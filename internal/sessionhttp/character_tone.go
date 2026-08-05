package sessionhttp

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// characterToneFor returns the bounded tone hint for an agent, plus a short
// description of where it came from, or ("", "") when no tone applies.
//
// Every gate has to pass: the agent must have an explicit character identity,
// be displaying it, have opted in, and reference a character that still exists
// and is assignable. A withdrawn catalog entry therefore silently drops the tone
// rather than leaving an agent speaking as something it no longer shows
// (PRD FR-60/FR-64/FR-74).
func characterToneFor(md *types.AgentMetadata) (hint string, source string) {
	if !md.IsCharacterVoiceEnabled() {
		return "", ""
	}
	id := charactercatalog.CharacterID(md.CharacterCatalogID())
	if id == "" {
		return "", ""
	}

	cat, err := charactercatalog.Load()
	if err != nil {
		return "", ""
	}
	// IsAssignable also excludes Ori's reserved identity, so the guide's voice
	// can never end up on a working agent.
	if !cat.IsAssignable(id) {
		return "", ""
	}

	ch, ok := cat.Get(id)
	if !ok {
		return "", ""
	}
	h := ch.ToneHint()
	if h == "" {
		return "", ""
	}
	return h, fmt.Sprintf("character %q (catalog %s)", ch.Name, cat.Version)
}
