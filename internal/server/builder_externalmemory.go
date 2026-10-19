package server

import (
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/externalmemoryhttp"
)

// initializeExternalMemory builds the read-only mirror of other agents' memory
// (Codex, Claude Code, Gemini CLI, the Claude account) and its sync runner. The
// store lives in the data dir, so ORI_DATA_DIR scopes it; source roots come
// from CODEX_HOME and friends at construction. Nothing is read until the user
// enables a source on the Profile page, and the schedule starts with the
// server's background services (Server.Start), never during construction.
func (b *ServerBuilder) initializeExternalMemory() {
	store := externalmemory.NewStore(config.DefaultExternalMemoryDir())
	registry := externalmemory.NewRegistry(
		externalmemory.NewCodexSource(externalmemory.DefaultCodexRoot()),
	)
	b.externalMemoryRunner = externalmemory.NewRunner(store, registry)
	b.externalMemoryHandler = externalmemoryhttp.NewHandler(b.externalMemoryRunner)
	b.server.externalMemory = b.externalMemoryRunner
}
