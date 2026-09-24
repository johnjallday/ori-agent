package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/platform"
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

func (nativeFolderPicker) Choose(ctx context.Context, prompt string) (string, bool, error) {
	path, chosen, err := platform.ChooseFolder(ctx, prompt)
	if errors.Is(err, platform.ErrFolderDialogUnavailable) {
		return "", false, personalassistant.ErrFolderPickerUnavailable
	}
	return path, chosen, err
}
