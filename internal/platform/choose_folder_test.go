package platform

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

func TestChooseFolderScript_EscapesPrompt(t *testing.T) {
	if got := chooseFolderScript(""); got != `POSIX path of (choose folder)` {
		t.Errorf("empty prompt script = %q", got)
	}
	got := chooseFolderScript(`Which "folder" \ now?`)
	want := `POSIX path of (choose folder with prompt "Which \"folder\" \\ now?")`
	if got != want {
		t.Errorf("script = %q, want %q", got, want)
	}
}

func TestChooseFolder_GateAndCancel(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, _, err := ChooseFolder(context.Background(), "x"); !errors.Is(err, ErrFolderDialogUnavailable) {
			t.Fatalf("non-darwin err = %v", err)
		}
		if ChooseFolderAvailable() {
			t.Fatal("dialog reported available off macOS")
		}
		return
	}

	t.Setenv(NoDesktopOpenEnv, "1")
	if ChooseFolderAvailable() {
		t.Error("dialog reported available under the desktop gate")
	}
	if _, _, err := ChooseFolder(context.Background(), "x"); !errors.Is(err, ErrFolderDialogUnavailable) {
		t.Errorf("gated err = %v", err)
	}

	t.Setenv(NoDesktopOpenEnv, "")
	if !ChooseFolderAvailable() {
		t.Error("dialog reported unavailable with the gate off")
	}
	original := chooseFolderCommand
	t.Cleanup(func() { chooseFolderCommand = original })

	var seenScript string
	chooseFolderCommand = func(_ context.Context, script string) ([]byte, error) {
		seenScript = script
		return []byte("/Users/me/Documents/Thesis/\n"), nil
	}
	path, chosen, err := ChooseFolder(context.Background(), "Pick")
	if err != nil || !chosen || path != "/Users/me/Documents/Thesis/" {
		t.Errorf("chosen = %q %v %v", path, chosen, err)
	}
	if seenScript != chooseFolderScript("Pick") {
		t.Errorf("script = %q", seenScript)
	}

	chooseFolderCommand = func(context.Context, string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("execution error: User canceled. (-128)")}
	}
	path, chosen, err = ChooseFolder(context.Background(), "Pick")
	if err != nil || chosen || path != "" {
		t.Errorf("cancel = %q %v %v, want clean no-choice", path, chosen, err)
	}

	chooseFolderCommand = func(context.Context, string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("execution error: something else (-1700)")}
	}
	if _, _, err = ChooseFolder(context.Background(), "Pick"); err == nil {
		t.Error("a real osascript failure must surface")
	}

	chooseFolderCommand = func(context.Context, string) ([]byte, error) { return []byte("  \n"), nil }
	if _, chosen, err = ChooseFolder(context.Background(), "Pick"); err != nil || chosen {
		t.Errorf("empty output should be no choice: %v %v", chosen, err)
	}
}
