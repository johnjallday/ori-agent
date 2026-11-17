package chathttp

// FolderWelcome says whether the lead's welcome waits on the folder agent of a
// workspace that was made by feeding a folder. That Commander asks its own first
// question, so the welcome (which would ask a second, unrelated one) is held
// until the folder is set up or kept as it is.
type FolderWelcome string

const (
	// FolderWelcomeNone: not made by feeding a folder; the welcome is unchanged.
	FolderWelcomeNone FolderWelcome = ""
	// FolderWelcomeHold: the Commander is looking, asking, reporting a failure or
	// setting up. No welcome is posted yet.
	FolderWelcomeHold FolderWelcome = "hold"
	// FolderWelcomeAfterSetUp: the folder was set up. The welcome may be posted
	// without its goal question, because the Commander offers a goal itself.
	FolderWelcomeAfterSetUp FolderWelcome = "set_up"
	// FolderWelcomeAfterKept: the folder was kept as it is; the welcome is unchanged.
	FolderWelcomeAfterKept FolderWelcome = "kept"
)

// folderWelcomeReader is the seam to the folder agent. This package, the
// welcome worker and the session store do not import the folder agent: the
// server wires an implementation, as it does for the filing curator's welcome.
type folderWelcomeReader interface {
	FolderWelcome(workspaceID string) FolderWelcome
}

// SetFolderWelcomeReader wires the folder agent's answer. A nil reader (or an
// unwired folder agent) means no workspace is held.
func (h *Handler) SetFolderWelcomeReader(reader folderWelcomeReader) {
	h.folderWelcome = reader
}

// folderWelcomeFor is the hold on this workspace's welcome.
func (h *Handler) folderWelcomeFor(workspaceID string) FolderWelcome {
	if h.folderWelcome == nil {
		return FolderWelcomeNone
	}
	return h.folderWelcome.FolderWelcome(workspaceID)
}
