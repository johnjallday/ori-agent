package platform

import (
	"runtime"
	"testing"
)

// The chooser explains a missing dialog from this reason, so the switch and
// the platform must each name themselves.
func TestChooseFolderUnavailableReason(t *testing.T) {
	t.Setenv(NoDesktopOpenEnv, "1")
	if runtime.GOOS == "darwin" {
		if got := ChooseFolderUnavailableReason(); got != FolderDialogUnavailableDesktopOff {
			t.Fatalf("reason under %s = %q, want %q", NoDesktopOpenEnv, got, FolderDialogUnavailableDesktopOff)
		}
		if ChooseFolderAvailable() {
			t.Fatal("available while desktop launches are switched off")
		}
	} else if got := ChooseFolderUnavailableReason(); got != FolderDialogUnavailablePlatform {
		t.Fatalf("reason off macOS = %q, want %q", got, FolderDialogUnavailablePlatform)
	}

	t.Setenv(NoDesktopOpenEnv, "")
	if runtime.GOOS == "darwin" {
		if got := ChooseFolderUnavailableReason(); got != "" || !ChooseFolderAvailable() {
			t.Fatalf("reason on macOS with the switch clear = %q", got)
		}
	} else if ChooseFolderAvailable() {
		t.Fatal("available off macOS")
	}
}
