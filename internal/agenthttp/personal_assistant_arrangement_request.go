package agenthttp

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// UnmarshalJSON preserves ordinary Ask decoding but does not let an explicitly
// supplied null, duplicate or case-aliased arrangement mode disappear into the
// broader ordinary discussion/tool loop. A missing mode remains ordinary Ask.
func (r *HomeAssistantAskRequest) UnmarshalJSON(raw []byte) error {
	type wireRequest HomeAssistantAskRequest
	var decoded wireRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		if _, err := decoder.Token(); err != nil {
			return err
		}
		seen := false
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return workspacearrangement.ErrInvalid
			}
			if strings.EqualFold(name, "arrangement") {
				if name != "arrangement" || seen || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return workspacearrangement.ErrInvalid
				}
				seen = true
			}
		}
	}
	*r = HomeAssistantAskRequest(decoded)
	return nil
}
