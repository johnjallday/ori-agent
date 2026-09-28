package trigger

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ProjectImportedTriggers prepares a copied triggers.json for a reviewed
// workspace import. Definitions, enabled intent and fire history stay as the
// user's saved configuration. What only means something on the source machine
// does not: a queued fire is dropped (it is never resumed), and a webhook's
// URL token and secret are replaced by a fresh local token with no secret, so
// the copied URL cannot reach this installation. Nothing here activates a
// trigger; local admission still blocks it until the user enables routines.
func ProjectImportedTriggers(data []byte) ([]byte, int, error) {
	var file triggersFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&file); err != nil {
		return nil, 0, fmt.Errorf("decode triggers: %w", err)
	}
	for _, t := range file.Triggers {
		if t == nil {
			return nil, 0, fmt.Errorf("decode triggers: empty entry")
		}
		t.PendingFire = nil
		if t.Webhook != nil {
			token, err := GenerateToken()
			if err != nil {
				return nil, 0, err
			}
			t.Webhook.Token, t.Webhook.Secret = token, ""
		}
	}
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	return out, len(file.Triggers), nil
}
