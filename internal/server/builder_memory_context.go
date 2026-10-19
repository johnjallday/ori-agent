package server

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryContextSettingsFile keeps each workspace's "Also write to the folder
// root" switch, installation-local, in the data dir.
const memoryContextSettingsFile = "workspace_memory_context.json"

// initializeMemoryContextFiles builds the writer that renders each
// workspace's memory into AGENTS.md, CLAUDE.md and GEMINI.md for the CLIs a
// user runs by hand (PRD FR23–FR26). The Memory station's switch drives it
// directly; re-rendering after every memory write starts with the server's
// background services (Server.Start), so a constructed but unserved server
// never writes into workspace folders.
func (b *ServerBuilder) initializeMemoryContextFiles() {
	if b.workspaceFileStore == nil {
		return
	}
	writer := &workspace.MemoryContextWriter{
		Folders:    b.workspaceFileStore,
		Workspaces: b.workspaceFileStore,
		Settings:   workspace.NewMemoryContextSettings(filepath.Join(config.DefaultDataDir(), memoryContextSettingsFile)),
		RootFolder: b.memoryContextRootFolder,
		Allowed: func(folder string) error {
			_, err := folderpath.Check(folder, filejanitor.DefaultRootGuards())
			return err
		},
	}
	b.server.memoryContext = writer
	if b.externalMemoryRunner != nil {
		// FR26: files are also refreshed after each external-memory sync.
		b.externalMemoryRunner.OnSynced(func(externalmemory.RunResult) { b.renderAllMemoryContextFiles(writer) })
	}
}

func (b *ServerBuilder) renderAllMemoryContextFiles(writer *workspace.MemoryContextWriter) {
	if b.workspaceStore == nil {
		return
	}
	workspaces, err := b.workspaceStore.ListActive()
	if err != nil {
		logger.Debug("Skipped refreshing workspace memory context files", logger.Fields{"error": err})
		return
	}
	for _, ws := range workspaces {
		if ws != nil {
			writer.Render(ws.ID)
		}
	}
}

// memoryContextRootFolder is the folder the user codes in: a bound software
// project's checkout, else the resolved project path, else the workspace's own
// folder. An unavailable bound checkout gives no folder, so nothing is written.
func (b *ServerBuilder) memoryContextRootFolder(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	if b.workspacePlanPolicy != nil {
		if folder, bound := b.workspacePlanPolicy.BoundCodeFolder(ws.ID); bound {
			return folder
		}
		code := strings.TrimSpace(b.workspacePlanPolicy.CodeFolder(ws.ID))
		files := ""
		if b.workspaceStore != nil {
			files = b.workspaceStore.GetFilesPath(ws.ID)
		}
		if code != "" && filepath.Clean(code) != filepath.Clean(files) {
			return code
		}
	}
	folder, err := b.workspaceFileStore.GetFolderPath(ws.ID)
	if err != nil {
		return ""
	}
	return folder
}
