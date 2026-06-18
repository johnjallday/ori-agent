package plugin

import "errors"

var (
	// ErrNoManifest means neither a Claude nor a Codex plugin manifest was found.
	ErrNoManifest = errors.New("plugin: no .claude-plugin/plugin.json or .codex-plugin/plugin.json found")
	// ErrNoName means the manifest is missing the required name field.
	ErrNoName = errors.New(`plugin: manifest missing required "name" field`)
	// ErrEmptySource means no plugin source was provided.
	ErrEmptySource = errors.New("plugin: empty source")
)
