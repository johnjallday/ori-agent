package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// wireFolderDigest builds "show me a folder" over the resolved HQ knowledge
// store, File Janitor's root rules, and the native folder dialog.
func (b *ServerBuilder) wireFolderDigest(knowledge *personalassistant.KnowledgeStore) {
	if b == nil || knowledge == nil || b.personalAssistantHandler == nil {
		return
	}
	store := personalassistant.NewFolderDigestStore(knowledge)
	service := personalassistant.NewFolderDigestService(store, personalassistant.FolderDigestDeps{
		ValidateRoot: validateShownFolder,
		Picker:       nativeFolderPicker{},
		// Resolved lazily: the template resolver is wired in a later phase,
		// and the answer must reflect plugins enabled after startup.
		BlueprintAvailable: func(id string) bool {
			return b.sessionHandler != nil && b.sessionHandler.BlueprintInstalled(id)
		},
		Linker: folderWorkspaceLinker{files: b.workspaceFileStore, sessions: b.sessionStore, tasks: b.sessionHandler},
		Tidier: b.newFolderTidyRunner(),
	})
	b.personalAssistantFolderDigest = service
	b.personalAssistantHandler.SetFolderDigest(service)
}

// validateShownFolder applies File Janitor's root rules (FR7) and turns the
// refusal into the user-facing message the chooser shows.
func validateShownFolder(raw string) (string, error) {
	root, err := filejanitor.ValidateRoot(raw, filejanitor.DefaultRootGuards())
	if err != nil {
		var setupErr *filejanitor.SetupError
		if errors.As(err, &setupErr) && setupErr.Message != "" {
			return "", &personalassistant.FolderRootError{Message: setupErr.Message}
		}
		return "", &personalassistant.FolderRootError{Message: "Ori could not open that folder. Choose a different folder."}
	}
	return root, nil
}

// nativeFolderPicker runs the macOS folder dialog in the server process,
// skipped under the desktop launch gate like every other desktop launch.
type nativeFolderPicker struct{}

func (nativeFolderPicker) Available() bool { return platform.ChooseFolderAvailable() }

func (nativeFolderPicker) UnavailableReason() string { return platform.ChooseFolderUnavailableReason() }

func (nativeFolderPicker) Choose(ctx context.Context, prompt string) (string, bool, error) {
	path, chosen, err := platform.ChooseFolder(ctx, prompt)
	if errors.Is(err, platform.ErrFolderDialogUnavailable) {
		return "", false, personalassistant.ErrFolderPickerUnavailable
	}
	return path, chosen, err
}

// folderWorkspaceLinker attaches a shown folder to the workspace the user
// created for it (FR28): the folder becomes a linked directory (kept where
// it is) and the workspace's primary project directory, in both the
// workspace folder and the session row, and the shape's first task is
// seeded (FR30).
type folderWorkspaceLinker struct {
	files    *workspace.FileStore
	sessions session.HybridStore
	tasks    *sessionhttp.Handler
}

func (l folderWorkspaceLinker) LinkFolder(ctx context.Context, req personalassistant.FolderLinkRequest) (personalassistant.FolderLinkResult, error) {
	if l.files == nil || l.sessions == nil {
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	ws, err := l.sessions.GetWorkspace(ctx, req.WorkspaceID)
	if err != nil || ws == nil {
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderWorkspaceNotFound
	}
	// The workspace must be the user's own, an ordinary workspace, and the
	// one the modal created for this very offer (FR28).
	if (ws.OwnerUserID != "" && ws.OwnerUserID != req.UserID) || ws.IsGroup() {
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderWorkspaceRefused
	}
	if recorded, _ := ws.SharedData[projecttemplates.FolderOfferIDKey].(string); strings.TrimSpace(recorded) != req.OfferID {
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderWorkspaceRefused
	}
	folderWS, err := l.files.Get(req.WorkspaceID)
	if err != nil || folderWS == nil {
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderWorkspaceNotFound
	}
	route := "/workspaces/" + ws.FolderSlug

	// A workspace that already has a primary directory is linked only if
	// that directory is this same folder (a replayed resolve) or a scaffold
	// the blueprint just made inside the workspace folder, which the shown
	// folder supersedes: the user already has the manuscript. A primary that
	// points at some other outside folder means this workspace was linked to
	// something else, and is refused.
	if existing, _ := folderWS.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); strings.TrimSpace(existing) != "" {
		ref, err := folderWS.GetDirectoryReference(existing)
		switch {
		case err == nil && ref != nil && filepath.Clean(ref.Path) == filepath.Clean(req.Path):
			return personalassistant.FolderLinkResult{Route: route, DirectoryID: existing}, nil
		case err != nil || ref == nil:
			// A dangling primary id; the shown folder takes over.
		case l.insideWorkspaceFolder(req.WorkspaceID, ref.Path):
			// A blueprint scaffold; the shown folder takes over.
		default:
			return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderWorkspaceRefused
		}
	}

	dirID, err := projecttemplates.AttachLinkedDirectory(folderWS, req.Name, req.Path)
	if err != nil {
		logger.Warn("Failed to link shown folder", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	now := time.Now()
	folderWS.UpdatedAt = now
	if err := l.files.Save(folderWS); err != nil {
		logger.Warn("Failed to save linked folder", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
		return personalassistant.FolderLinkResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	// Mirror into the session row; workspace.json stays canonical and reads
	// hydrate from it.
	if ws.SharedData == nil {
		ws.SharedData = make(map[string]any)
	}
	projecttemplates.SetPrimaryDirectoryID(ws.SharedData, dirID)
	if refs, err := json.Marshal(folderWS.DirectoryReferences); err == nil {
		ws.DirectoryReferencesJSON = refs
	}
	ws.UpdatedAt = now
	if err := l.sessions.UpdateWorkspace(ctx, ws); err != nil {
		logger.Warn("Failed to sync workspace metadata after linking a folder", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
	}

	result := personalassistant.FolderLinkResult{Route: route, DirectoryID: dirID}
	if l.tasks != nil {
		description, details := personalassistant.FolderFirstTask(req.Shape)
		seeded, err := l.tasks.SeedStarterTasks(req.WorkspaceID, projecttemplates.Template{
			ID:           "folder-digest",
			StarterTasks: []projecttemplates.StarterTask{{Description: description, Details: details}},
		})
		if err != nil {
			logger.Warn("Failed to seed the shown folder's first task", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
		}
		result.FirstTaskSeeded = seeded > 0
	}
	logger.Debug("Linked shown folder to workspace", logger.Fields{"workspace_id": req.WorkspaceID, "directory_id": dirID, "shape": string(req.Shape)})
	return result, nil
}

// insideWorkspaceFolder reports whether path sits under the workspace's own
// folder, which is where a blueprint scaffolds its starter project.
func (l folderWorkspaceLinker) insideWorkspaceFolder(workspaceID, path string) bool {
	folder, err := l.files.GetFolderPath(workspaceID)
	if err != nil || strings.TrimSpace(folder) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(folder), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
