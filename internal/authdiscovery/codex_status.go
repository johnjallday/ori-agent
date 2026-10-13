package authdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// CodexLoginStatus exposes only login mode, never credentials or account identity.
// Unlike DiscoverCodexToken, this path never refreshes, exchanges or writes tokens.
type CodexLoginStatus struct {
	SignedIn bool
	APIKey   bool
}

// ReadCodexLoginStatus checks the CLI's local stores with a caller-owned deadline.
func ReadCodexLoginStatus(ctx context.Context, home string) (CodexLoginStatus, error) {
	return readCodexLoginStatus(home, func() ([]byte, error) {
		if runtime.GOOS != "darwin" {
			return nil, os.ErrNotExist
		}
		account := computeCodexKeychainAccount(home)
		cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", CodexCliKeychainService, "-a", account, "-w")
		data, err := cmd.Output()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			return nil, os.ErrNotExist // errSecItemNotFound
		}
		return data, err
	})
}

func readCodexLoginStatus(home string, keychain func() ([]byte, error)) (CodexLoginStatus, error) {
	// #nosec G304 -- caller supplies the trusted local Codex home; filename is fixed.
	data, fileErr := os.ReadFile(filepath.Join(home, CodexCliAuthFilename))
	if fileErr == nil {
		return parseCodexLoginStatus(data)
	}
	if !errors.Is(fileErr, os.ErrNotExist) {
		return CodexLoginStatus{}, errors.New("Codex login file is unreadable")
	}
	data, err := keychain()
	if errors.Is(err, os.ErrNotExist) {
		return CodexLoginStatus{}, nil
	}
	if err != nil {
		return CodexLoginStatus{}, errors.New("Codex login status is unavailable")
	}
	return parseCodexLoginStatus(data)
}

func parseCodexLoginStatus(data []byte) (CodexLoginStatus, error) {
	var auth struct {
		Mode   string `json:"auth_mode"`
		APIKey string `json:"OPENAI_API_KEY"`
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return CodexLoginStatus{}, errors.New("Codex login data is unreadable")
	}
	apiKey := auth.Mode == "apikey" || auth.APIKey != ""
	return CodexLoginStatus{SignedIn: apiKey || auth.Tokens.AccessToken != "", APIKey: apiKey}, nil
}
