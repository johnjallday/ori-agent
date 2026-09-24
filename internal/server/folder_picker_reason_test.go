package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// The assistant's chooser reads the picker's reason by the personalassistant
// constants; the native picker reports the platform package's. Keep them one.
func TestNativeFolderPicker_ReasonsMatchTheChooser(t *testing.T) {
	if platform.FolderDialogUnavailablePlatform != personalassistant.FolderDialogUnavailablePlatform ||
		platform.FolderDialogUnavailableDesktopOff != personalassistant.FolderDialogUnavailableDesktopOff {
		t.Fatal("platform and personalassistant name the folder dialog reasons differently")
	}
	t.Setenv(platform.NoDesktopOpenEnv, "1")
	var picker personalassistant.FolderPicker = nativeFolderPicker{}
	if picker.Available() {
		t.Fatal("the native picker is available while desktop launches are switched off")
	}
	if got := picker.UnavailableReason(); got == "" {
		t.Fatal("the native picker gave no reason for being unavailable")
	}
}
