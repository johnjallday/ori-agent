package authdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const (
	ClaudeCliKeychainService = "Claude Code-credentials"
	ClaudeCliKeychainAccount = "Claude Code"
	ClaudeCliRelativePath    = ".claude/.credentials.json"
)

var ErrNoClaudeLogin = errors.New("no Claude Code login found")

var errClaudeLoginUnreadable = errors.New("claude code login is unreadable")

type ClaudeOauth struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // Unix milliseconds, as written by Claude Code.
}

type ClaudeCredentialsFile struct {
	ClaudeAiOauth ClaudeOauth `json:"claudeAiOauth"`
}

// DiscoverClaudeToken preserves the token-only API for existing callers.
func DiscoverClaudeToken() (string, error) {
	oauth, err := DiscoverClaudeOauth()
	return oauth.AccessToken, err
}

// DiscoverClaudeOauth reads the existing login without refreshing or writing it.
// Only the access token and expiry are returned; refresh tokens are not retained.
func DiscoverClaudeOauth() (ClaudeOauth, error) {
	return DiscoverClaudeOauthContext(context.Background())
}

func DiscoverClaudeOauthContext(ctx context.Context) (ClaudeOauth, error) {
	return discoverClaudeOauth(func() ([]byte, error) {
		if runtime.GOOS != "darwin" {
			return nil, os.ErrNotExist
		}
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		data, err := claudeKeychainCommand(ctx).Output()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			return nil, os.ErrNotExist
		}
		return data, err
	}, func() ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return readClaudeCredentialsFile()
	})
}

func claudeKeychainCommand(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", ClaudeCliKeychainService, "-w")
	// The explicit read-only bridge also restores the login user's Keychain
	// search context. Only this read-only security child gets the native HOME;
	// Ori, Claude settings, sessions and the mirror installer stay sandboxed.
	if strings.TrimSpace(os.Getenv("ORI_CLAUDE_CREDENTIALS_DIR")) != "" {
		if loginUser, err := user.Current(); err == nil && loginUser.HomeDir != "" {
			cmd.Env = append(os.Environ(), "HOME="+loginUser.HomeDir)
		}
	}
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}

// ORI_CLAUDE_CREDENTIALS_DIR is an explicit read-only credential source for an
// isolated demo. Unlike CLAUDE_CONFIG_DIR, it does not redirect CLI settings,
// sessions, or the opt-in status-line installer into the user's real directory.
func readClaudeCredentialsFile() ([]byte, error) {
	dir := strings.TrimSpace(os.Getenv("ORI_CLAUDE_CREDENTIALS_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".claude")
	}
	// #nosec G304 G703 -- fixed credential filename under HOME or an explicit trusted local configuration directory; read-only, never an HTTP-supplied path.
	return os.ReadFile(filepath.Join(dir, ".credentials.json"))
}

func discoverClaudeOauth(keychain, file func() ([]byte, error)) (ClaudeOauth, error) {
	data, keychainErr := keychain()
	if keychainErr == nil {
		if oauth, err := parseClaudeOauth(data); err == nil {
			logClaudeDiscovery("keychain", nil)
			return oauth, nil
		} else {
			keychainErr = err
		}
	}
	logClaudeDiscovery("keychain", keychainErr)
	data, fileErr := file()
	if fileErr == nil {
		oauth, err := parseClaudeOauth(data)
		logClaudeDiscovery("file", err)
		return oauth, err
	}
	logClaudeDiscovery("file", fileErr)
	if (errors.Is(keychainErr, os.ErrNotExist) || errors.Is(keychainErr, ErrNoClaudeLogin)) && errors.Is(fileErr, os.ErrNotExist) {
		return ClaudeOauth{}, ErrNoClaudeLogin
	}
	// Do not propagate command output, credential JSON or file paths into logs.
	return ClaudeOauth{}, errClaudeLoginUnreadable
}

// Diagnostics deliberately exclude paths, command output and credential fields.
func logClaudeDiscovery(source string, err error) {
	if os.Getenv("ORI_USAGE_LIMITS_DEBUG") != "1" {
		return
	}
	result := "found"
	switch {
	case errors.Is(err, os.ErrNotExist):
		result = "missing"
	case errors.Is(err, ErrNoClaudeLogin):
		result = "no_oauth"
	case err != nil:
		result = "unreadable"
	}
	log := logger.New("usage-limits")
	log.SetLevel(logger.DebugLevel)
	log.Debug("Claude login discovery", logger.Fields{"source": source, "result": result})
}

func parseClaudeOauth(data []byte) (ClaudeOauth, error) {
	var envelope ClaudeCredentialsFile
	if json.Unmarshal(data, &envelope) != nil {
		return ClaudeOauth{}, errClaudeLoginUnreadable
	}
	oauth := envelope.ClaudeAiOauth
	if oauth.AccessToken == "" {
		// A bare OAuth object is valid JSON too, so envelope decoding alone does
		// not report an error for it.
		if json.Unmarshal(data, &oauth) != nil {
			return ClaudeOauth{}, errClaudeLoginUnreadable
		}
	}
	oauth.AccessToken = strings.TrimSpace(oauth.AccessToken)
	oauth.RefreshToken = ""
	if oauth.AccessToken == "" {
		return ClaudeOauth{}, ErrNoClaudeLogin
	}
	return oauth, nil
}
