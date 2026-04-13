package authdiscovery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	CodexCliKeychainService = "Codex Auth"
	CodexCliAuthFilename    = "auth.json"

	codexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexTokenEndpoint = "https://auth.openai.com/oauth/token"
)

type CodexTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
}

type CodexCredentialsSource string

const (
	CodexSourceUnknown  CodexCredentialsSource = ""
	CodexSourceKeychain CodexCredentialsSource = "keychain"
	CodexSourceFile     CodexCredentialsSource = "file"
)

// ExchangeForAPIKey exchanges the stored Codex OAuth credentials for an OpenAI
// API key using RFC 8693 token exchange.  If the id_token is missing or expired
// it refreshes first using the refresh_token.  Returns the raw API key (sk-…).
func (t *CodexTokens) ExchangeForAPIKey() (string, error) {
	if t.IDToken == "" || isJWTExpired(t.IDToken) {
		if t.RefreshToken == "" {
			return "", fmt.Errorf("codex: id_token expired and no refresh_token available")
		}
		refreshed, err := refreshCodexTokens(t.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("codex: token refresh failed: %w", err)
		}
		*t = *refreshed
	}
	return exchangeIDTokenForAPIKey(t.IDToken)
}

// RefreshIfNeeded refreshes Codex OAuth credentials when the access token looks
// expired or missing. Returns true if a refresh occurred.
func (t *CodexTokens) RefreshIfNeeded() (bool, error) {
	now := time.Now().Unix()

	if t.AccessToken != "" {
		if exp, ok := parseJWTExp(t.AccessToken); ok {
			if now < exp {
				return false, nil
			}
		} else {
			// If we can't parse access token expiry, rely on id_token if present.
			if t.IDToken == "" || !isJWTExpired(t.IDToken) {
				return false, nil
			}
		}
	}

	if t.AccessToken == "" && t.RefreshToken == "" {
		return false, fmt.Errorf("codex: access_token missing and no refresh_token available")
	}
	if t.AccessToken != "" && t.RefreshToken == "" {
		return false, fmt.Errorf("codex: access_token expired and no refresh_token available")
	}

	refreshed, err := refreshCodexTokens(t.RefreshToken)
	if err != nil {
		return false, fmt.Errorf("codex: token refresh failed: %w", err)
	}
	*t = *refreshed
	return true, nil
}

// parseJWTExp extracts the exp claim from a JWT. Returns (exp, true) if parsed,
// otherwise (0, false).
func parseJWTExp(token string) (int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return 0, false
	}
	return claims.Exp, true
}

// isJWTExpired decodes the exp claim from a JWT without verifying the signature.
// Returns true if the token is expired or if the payload cannot be parsed.
func isJWTExpired(token string) bool {
	exp, ok := parseJWTExp(token)
	if !ok {
		return true
	}
	return time.Now().Unix() >= exp
}

type CodexAuthFile struct {
	Tokens      CodexTokens `json:"tokens"`
	LastRefresh interface{} `json:"last_refresh"`
}

// DiscoverCodexToken attempts to find an OpenAI API token from Codex CLI.
// It returns the access_token directly — useful when that token is already an
// API key (sk-… prefix).
func DiscoverCodexToken() (string, error) {
	creds, source, err := DiscoverCodexCredentialsWithSource()
	if err != nil {
		return "", err
	}
	if refreshed, err := creds.RefreshIfNeeded(); err != nil {
		return "", err
	} else if refreshed {
		_ = PersistCodexCredentials(source, creds)
	}
	if creds.AccessToken == "" {
		return "", fmt.Errorf("codex: no access token available")
	}
	return creds.AccessToken, nil
}

// DiscoverCodexCredentials reads the full set of OAuth credentials stored by
// the Codex CLI (keychain on macOS, fallback to auth.json).
func DiscoverCodexCredentials() (*CodexTokens, error) {
	creds, _, err := DiscoverCodexCredentialsWithSource()
	return creds, err
}

// DiscoverCodexCredentialsWithSource reads Codex credentials and returns where they came from.
func DiscoverCodexCredentialsWithSource() (*CodexTokens, CodexCredentialsSource, error) {
	codexHome := getCodexHome()

	if runtime.GOOS == "darwin" {
		creds, err := readCodexCredsFromKeychain(codexHome)
		if err == nil && creds.AccessToken != "" {
			return creds, CodexSourceKeychain, nil
		}
	}

	creds, err := readCodexCredsFromFile(codexHome)
	if err != nil {
		return nil, CodexSourceUnknown, err
	}
	return creds, CodexSourceFile, nil
}

func getCodexHome() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home
	}
	userHome, _ := os.UserHomeDir()
	return filepath.Join(userHome, ".codex")
}

func computeCodexKeychainAccount(codexHome string) string {
	// Need to match openclaw's implementation:
	// const hash = createHash("sha256").update(codexHome).digest("hex");
	// return `cli|${hash.slice(0, 16)}`;

	// Ensure we use the absolute path if possible, openclaw uses fs.realpathSync.native
	absHome, err := filepath.Abs(codexHome)
	if err == nil {
		codexHome = absHome
	}

	hash := sha256.Sum256([]byte(codexHome))
	hashHex := hex.EncodeToString(hash[:])
	return fmt.Sprintf("cli|%s", hashHex[:16])
}

func readCodexCredsFromKeychain(codexHome string) (*CodexTokens, error) {
	account := computeCodexKeychainAccount(codexHome)
	cmd := exec.Command("security", "find-generic-password", "-s", CodexCliKeychainService, "-a", account, "-w")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &data); err != nil {
		return nil, err
	}

	tokens, ok := data["tokens"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no tokens found in keychain data")
	}

	creds := &CodexTokens{}
	if v, _ := tokens["access_token"].(string); v != "" {
		creds.AccessToken = v
	}
	if v, _ := tokens["refresh_token"].(string); v != "" {
		creds.RefreshToken = v
	}
	if v, _ := tokens["id_token"].(string); v != "" {
		creds.IDToken = v
	}
	if v, _ := tokens["account_id"].(string); v != "" {
		creds.AccountID = v
	}
	return creds, nil
}

func readCodexCredsFromFile(codexHome string) (*CodexTokens, error) {
	path := filepath.Join(codexHome, CodexCliAuthFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var authFile CodexAuthFile
	if err := json.Unmarshal(data, &authFile); err != nil {
		return nil, err
	}

	if authFile.Tokens.AccessToken == "" {
		return nil, fmt.Errorf("no access token found in %s", path)
	}

	return &authFile.Tokens, nil
}

// PersistCodexCredentials writes the refreshed credentials back to the original store.
func PersistCodexCredentials(source CodexCredentialsSource, creds *CodexTokens) error {
	codexHome := getCodexHome()
	authFile := CodexAuthFile{
		Tokens:      *creds,
		LastRefresh: time.Now().Format(time.RFC3339),
	}
	payload, err := json.Marshal(authFile)
	if err != nil {
		return err
	}
	if source == CodexSourceKeychain && runtime.GOOS == "darwin" {
		return writeCodexCredsToKeychain(codexHome, payload)
	}
	return writeCodexCredsToFile(codexHome, payload)
}

func writeCodexCredsToFile(codexHome string, payload []byte) error {
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		return err
	}
	path := filepath.Join(codexHome, CodexCliAuthFilename)
	return os.WriteFile(path, payload, 0600)
}

func writeCodexCredsToKeychain(codexHome string, payload []byte) error {
	account := computeCodexKeychainAccount(codexHome)
	cmd := exec.Command(
		"security",
		"add-generic-password",
		"-U",
		"-s",
		CodexCliKeychainService,
		"-a",
		account,
		"-w",
		string(payload),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex keychain write failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// refreshCodexTokens obtains a fresh token set (including id_token) from the
// Codex OAuth server using the refresh_token grant.
func refreshCodexTokens(refreshToken string) (*CodexTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {codexClientID},
		"refresh_token": {refreshToken},
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.PostForm(codexTokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("codex refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex refresh read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex refresh HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("codex refresh parse: %w", err)
	}
	if result.IDToken == "" {
		return nil, fmt.Errorf("codex refresh: no id_token in response")
	}

	return &CodexTokens{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		IDToken:      result.IDToken,
	}, nil
}

// exchangeIDTokenForAPIKey performs RFC 8693 token exchange to convert a Codex
// id_token into an OpenAI API key.
func exchangeIDTokenForAPIKey(idToken string) (string, error) {
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"client_id":          {codexClientID},
		"requested_token":    {"openai-api-key"},
		"subject_token":      {idToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.PostForm(codexTokenEndpoint, form)
	if err != nil {
		return "", fmt.Errorf("codex exchange request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("codex exchange read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("codex exchange HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("codex exchange parse: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("codex exchange: empty access_token in response")
	}

	return result.AccessToken, nil
}
