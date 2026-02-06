package authdiscovery

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ClaudeCliKeychainService = "Claude Code-credentials"
	ClaudeCliKeychainAccount = "Claude Code"
	ClaudeCliRelativePath    = ".claude/.credentials.json"
)

type ClaudeOauth struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"`
}

type ClaudeCredentialsFile struct {
	ClaudeAiOauth ClaudeOauth `json:"claudeAiOauth"`
}

// DiscoverClaudeToken attempts to find an Anthropic API token from Claude CLI
func DiscoverClaudeToken() (string, error) {
	// 1. Try macOS keychain if on Darwin
	if runtime.GOOS == "darwin" {
		token, err := readClaudeFromKeychain()
		if err == nil && token != "" {
			return token, nil
		}
	}

	// 2. Try fallback file
	return readClaudeFromFile()
}

func readClaudeFromKeychain() (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", ClaudeCliKeychainService, "-w")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var file ClaudeCredentialsFile
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &file); err != nil {
		// Try unmarshaling as just the Oauth object in case it's different
		var oauth ClaudeOauth
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &oauth); err == nil {
			return strings.TrimSpace(oauth.AccessToken), nil
		}
		return "", err
	}

	return strings.TrimSpace(file.ClaudeAiOauth.AccessToken), nil
}

func readClaudeFromFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(home, ClaudeCliRelativePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var file ClaudeCredentialsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", err
	}

	token := strings.TrimSpace(file.ClaudeAiOauth.AccessToken)
	if token == "" {
		return "", fmt.Errorf("no access token found in %s", path)
	}

	return token, nil
}
