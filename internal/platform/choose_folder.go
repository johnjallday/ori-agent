package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrFolderDialogUnavailable reports that no native folder dialog can be
// shown: the platform has none wired up, or desktop launches are switched
// off with ORI_NO_DESKTOP_OPEN.
var ErrFolderDialogUnavailable = errors.New("folder dialog is unavailable")

// chooseFolderCommand runs the AppleScript and returns its stdout. Tests
// replace it to exercise cancellation and parsing without a dialog.
var chooseFolderCommand = func(ctx context.Context, script string) ([]byte, error) {
	// The program is the constant osascript; the only variable is the script,
	// which chooseFolderScript builds from a prompt escaped for an AppleScript
	// string literal, never from a path or anything the browser sent.
	return exec.CommandContext(ctx, "osascript", "-e", script).Output() // #nosec G204 -- constant binary, escaped prompt
}

// Why ChooseFolder cannot show a dialog, for a chooser that has to explain
// itself rather than offer a list that may be empty.
const (
	// FolderDialogUnavailablePlatform: no native dialog is wired up on this
	// operating system (macOS only for now).
	FolderDialogUnavailablePlatform = "platform"
	// FolderDialogUnavailableDesktopOff: desktop launches are switched off with
	// ORI_NO_DESKTOP_OPEN, as every sandboxed demo server does.
	FolderDialogUnavailableDesktopOff = "desktop_off"
)

// ChooseFolderAvailable reports whether ChooseFolder can show a dialog here.
// It is quiet: a chooser may ask on every render, and the skipped launch is
// logged once when a choice is actually attempted.
func ChooseFolderAvailable() bool {
	return ChooseFolderUnavailableReason() == ""
}

// ChooseFolderUnavailableReason names why ChooseFolder cannot show a dialog
// here, or returns "" when it can. Quiet, like ChooseFolderAvailable.
func ChooseFolderUnavailableReason() string {
	if runtime.GOOS != "darwin" {
		return FolderDialogUnavailablePlatform
	}
	if desktopOpenSwitchedOff() {
		return FolderDialogUnavailableDesktopOff
	}
	return ""
}

// ChooseFolder shows the native Finder folder dialog and returns the chosen
// folder's POSIX path. A cancelled dialog returns chosen=false with no error,
// so callers can treat it as "no folder chosen" rather than a failure.
//
// Platform support: macOS only (osascript "choose folder"). Elsewhere, and
// under ORI_NO_DESKTOP_OPEN, it returns ErrFolderDialogUnavailable.
func ChooseFolder(ctx context.Context, prompt string) (path string, chosen bool, err error) {
	if runtime.GOOS != "darwin" {
		return "", false, ErrFolderDialogUnavailable
	}
	if desktopOpenDisabled("choose_folder", prompt) {
		return "", false, ErrFolderDialogUnavailable
	}
	out, err := chooseFolderCommand(ctx, chooseFolderScript(prompt))
	if err != nil {
		if chooseFolderCancelled(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("choose folder: %w", err)
	}
	path = strings.TrimSpace(string(out))
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}

// chooseFolderScript builds the AppleScript. The prompt is the only variable
// part and is escaped the way the menu bar app escapes its dialog strings.
func chooseFolderScript(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return `POSIX path of (choose folder)`
	}
	return fmt.Sprintf(`POSIX path of (choose folder with prompt "%s")`, escapeAppleScriptString(prompt))
}

// ChooseFile uses the same gated native chooser as ChooseFolder, without an
// extension filter. The caller validates the selected file and its parent.
func ChooseFile(ctx context.Context, prompt string) (path string, chosen bool, err error) {
	if runtime.GOOS != "darwin" {
		return "", false, ErrFolderDialogUnavailable
	}
	if desktopOpenDisabled("choose_file", prompt) {
		return "", false, ErrFolderDialogUnavailable
	}
	out, err := chooseFolderCommand(ctx, chooseFileScript(prompt))
	if err != nil {
		if chooseFolderCancelled(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("choose file: %w", err)
	}
	path = strings.TrimSpace(string(out))
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}

func chooseFileScript(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return `POSIX path of (choose file)`
	}
	return fmt.Sprintf(`POSIX path of (choose file with prompt "%s")`, escapeAppleScriptString(prompt))
}

// chooseFolderCancelled recognises the dialog's Cancel button: osascript
// exits non-zero with AppleScript error -128 ("User canceled") on stderr.
func chooseFolderCancelled(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	return strings.Contains(string(exit.Stderr), "-128")
}

// escapeAppleScriptString quotes a value for use inside an AppleScript
// string literal. Backslashes are escaped before quotes, or the quote
// escaping itself gets double-escaped.
func escapeAppleScriptString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}
