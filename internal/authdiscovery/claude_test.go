package authdiscovery

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClaudeReadOnlyDiscoveryUsesLoginKeychainHome(t *testing.T) {
	loginUser, err := user.Current()
	if err != nil {
		t.Fatal("cannot resolve login user's home")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ORI_CLAUDE_CREDENTIALS_DIR", "")
	if cmd := claudeKeychainCommand(context.Background()); cmd.Env != nil {
		t.Fatal("default discovery must inherit its HOME scope")
	}
	t.Setenv("ORI_CLAUDE_CREDENTIALS_DIR", filepath.Join(home, ".claude"))
	cmd := claudeKeychainCommand(context.Background())
	// Inspect the real command construction without starting a process. A fake
	// child's startup can exceed the production one-second deadline under race
	// suite load; scheduler speed is not the behavior this test verifies.
	want := []string{"security", "find-generic-password", "-s", ClaudeCliKeychainService, "-w"}
	if !reflect.DeepEqual(cmd.Args, want) || cmd.Cancel == nil || cmd.WaitDelay != 100*time.Millisecond {
		t.Fatal("read-only command or bounded cancellation changed")
	}
	found := false
	for _, value := range cmd.Environ() {
		if strings.HasPrefix(value, "HOME=") {
			found = true
			if value != "HOME="+loginUser.HomeDir {
				t.Fatal("opt-in command did not use the native Keychain HOME")
			}
		}
	}
	if !found || os.Getenv("HOME") != home {
		t.Fatal("discovery changed Ori's HOME or omitted the child HOME")
	}
}

func TestClaudeCredentialsReadOnlySource(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := []byte(`{"claudeAiOauth":{"accessToken":"invented"}}`)
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, credentials, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, home, override string
		missing              bool
	}{
		{"normal HOME", home, "", false},
		{"sandbox with explicit read-only source", t.TempDir(), dir, false},
		{"sandbox without override", t.TempDir(), "", true},
		{"missing explicit source does not fall back", home, t.TempDir(), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", tt.home)
			t.Setenv("ORI_CLAUDE_CREDENTIALS_DIR", tt.override)
			data, err := readClaudeCredentialsFile()
			if tt.missing {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("expected missing credential file")
				}
			} else if err != nil || !bytes.Equal(data, credentials) {
				t.Fatal("wrong credential source")
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, credentials) {
				t.Fatal("credential file was changed")
			}
		})
	}
}

func TestClaudeDiscoveryDiagnosticsAreSanitized(t *testing.T) {
	t.Setenv("ORI_USAGE_LIMITS_DEBUG", "1")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, _ = discoverClaudeOauth(func() ([]byte, error) {
		return nil, errors.New("private command output and credential path")
	}, func() ([]byte, error) {
		return []byte(`{"accessToken":"invented-secret"}`), nil
	})
	text := output.String()
	if !strings.Contains(text, "result=unreadable") || !strings.Contains(text, "result=found") {
		t.Fatal("missing discovery diagnostics")
	}
	if strings.Contains(text, "private") || strings.Contains(text, "invented-secret") {
		t.Fatal("diagnostics exposed credentials or raw errors")
	}
}

func TestClaudeOauthDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name, keychain, file string
		wantToken            string
		wantExpiry           int64
		wantErr              bool
	}{
		{"envelope", `{"claudeAiOauth":{"accessToken":" invented ","refreshToken":"do-not-retain","expiresAt":1791874000000}}`, "", "invented", 1791874000000, false},
		{"bare", `{"accessToken":"invented","expiresAt":1791874000000}`, "", "invented", 1791874000000, false},
		{"file fallback", "", `{"claudeAiOauth":{"accessToken":"invented"}}`, "invented", 0, false},
		{"empty keychain falls back", `{}`, `{"accessToken":"invented"}`, "invented", 0, false},
		{"missing", "", "", "", 0, true},
		{"malformed", `{"secret":"invented"`, "", "", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			read := func(data string) func() ([]byte, error) {
				return func() ([]byte, error) {
					if data == "" {
						return nil, os.ErrNotExist
					}
					return []byte(data), nil
				}
			}
			got, err := discoverClaudeOauth(read(tt.keychain), read(tt.file))
			if (err != nil) != tt.wantErr || got.AccessToken != tt.wantToken || got.ExpiresAt != tt.wantExpiry || got.RefreshToken != "" {
				t.Fatalf("unexpected discovery result; error=%v expiry=%d", err, got.ExpiresAt)
			}
			if tt.name == "missing" && !errors.Is(err, ErrNoClaudeLogin) {
				t.Fatal("missing login must be distinguishable")
			}
		})
	}
}
