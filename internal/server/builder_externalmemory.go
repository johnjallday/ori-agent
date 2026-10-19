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
		externalmemory.NewClaudeCodeSource(externalmemory.DefaultClaudeCodeRoot()),
		externalmemory.NewGeminiSource(externalmemory.DefaultGeminiRoot()),
		externalmemory.NewClaudeAccountSource(store),
	)
	b.externalMemoryRunner = externalmemory.NewRunner(store, registry)
	b.externalMemoryHandler = externalmemoryhttp.NewHandler(b.externalMemoryRunner)
	// Promote to global goes through the personal assistant's knowledge
	// service, read when called: it may be unset (no hired assistant).
	b.externalMemoryHandler.SetGlobalMemory(builderGlobalMemory{b: b})
	// Copy for Claude import: the profile and approved Personal HQ facts.
	b.externalMemoryHandler.SetExportSource(externalMemoryExport{b: b})
	// Workspace chat: only the personal assistant answering in its own
	// Personal HQ gets the block and memory_recall; the knowledge reader's
	// binding check is the gate. Task runs are never wired here.
	if b.chatHandler != nil && b.personalAssistantKnowledge != nil {
		b.chatHandler.SetExternalMemory(b.externalMemoryRunner.Reader(), b.personalAssistantKnowledge)
	}
	b.server.externalMemory = b.externalMemoryRunner
}
