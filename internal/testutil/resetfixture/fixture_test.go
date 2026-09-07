package resetfixture_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFixtureReplacesInheritedInstallationAndRestoresProcess(t *testing.T) {
	// Poison values point only at another test-owned directory, never real state.
	outside := t.TempDir()
	for _, key := range []string{
		"HOME", "USERPROFILE", "ORI_DATA_DIR", "AGENT_STORE_PATH", "WORKSPACE_DIR", "ORI_VAULT_DIR",
		"ORI_TEMPLATES_DIR", "WORKFLOW_TEMPLATES_DIR", "CODEX_HOME", "CLAUDE_CONFIG_DIR",
	} {
		t.Setenv(key, outside)
	}
	for _, key := range []string{"OPENAI_API_KEY", "ORI_VAULT_PASSPHRASE", "NEW_UNKNOWN_PROVIDER_KEY", "HTTPS_PROXY", "PLAYWRIGHT_BASE_URL"} {
		t.Setenv(key, "synthetic-inherited-value")
	}
	beforeEnv := os.Environ()
	sort.Strings(beforeEnv)
	beforeWD, err := os.Getwd()
	check(t, err)
	var owned string
	t.Run("isolated", func(t *testing.T) {
		f := resetfixture.New(t)
		p := f.Paths()
		owned = p.Root
		if p.WorkDir == p.DataDir || p.Home == p.DataDir {
			t.Fatal("CWD, HOME and data directory must be distinct")
		}
		for _, path := range []string{p.DataDir, p.Home, p.WorkDir, p.Workspaces, p.Vaults, p.Templates} {
			rel, err := filepath.Rel(p.Root, path)
			check(t, err)
			if !filepath.IsLocal(rel) || path == outside || rel == "." {
				t.Fatal("fixture root escaped ownership")
			}
		}
		wd, err := os.Getwd()
		check(t, err)
		home, err := os.UserHomeDir()
		check(t, err)
		if wd != p.WorkDir || home != p.Home || config.DefaultDataDir() != p.DataDir {
			t.Fatal("runtime resolved a non-fixture installation")
		}
		for _, key := range []string{
			"WORKSPACE_DIR", "ORI_VAULT_DIR", "OPENAI_API_KEY", "ORI_VAULT_PASSPHRASE",
			"NEW_UNKNOWN_PROVIDER_KEY", "HTTPS_PROXY", "PLAYWRIGHT_BASE_URL",
		} {
			if os.Getenv(key) != "" {
				t.Errorf("inherited %s was not disabled", key)
			}
		}
		if os.Getenv("ORI_DISABLE_EXTERNAL_MCP_IMPORT") != "true" {
			t.Fatal("external MCP import is not disabled")
		}
		for _, command := range []string{"security", "secret-tool", "claude", "codex"} {
			if _, err := exec.LookPath(command); err == nil {
				t.Errorf("fixture can discover native command %s", command)
			}
		}
		// Default construction must not find an OS backend through PATH either.
		if vault.NewDefaultSecretStore().Status().Available {
			t.Fatal("default secret discovery unexpectedly reached a real backend")
		}
	})
	afterEnv := os.Environ()
	sort.Strings(afterEnv)
	afterWD, err := os.Getwd()
	check(t, err)
	if !reflect.DeepEqual(beforeEnv, afterEnv) || beforeWD != afterWD {
		t.Fatal("fixture failed to restore environment/CWD")
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture did not clean its owned directory")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("fixture cleanup touched an unowned directory")
	}
}

func TestFixtureFilesCannotEscapeOwnedRoot(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep.txt")
	check(t, os.WriteFile(sentinel, []byte("unchanged"), 0o600))
	f := resetfixture.New(t)
	for _, path := range []string{"", ".", "../keep.txt", "data/../../keep.txt", sentinel} {
		if err := f.WriteFile(path, []byte("bad")); err == nil {
			t.Errorf("accepted non-local path %q", path)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks requires Windows privileges; path checks still run")
		}
		check(t, os.Symlink(outside, filepath.Join(f.Paths().Root, "escape")))
		if err := f.WriteFile("escape/keep.txt", []byte("bad")); err == nil {
			t.Fatal("write through escaping symlink succeeded")
		}
	})
	data, err := os.ReadFile(sentinel)
	check(t, err)
	if string(data) != "unchanged" {
		t.Fatal("external sentinel changed")
	}
	f.AssertPreserved(t)
}

func TestFixtureSeedsRealStoresAndReopensSameInstallation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	cfg := config.NewManagerWithSecretStore(filepath.Join(p.DataDir, "settings.json"), f.Secrets())
	check(t, cfg.Load())
	if cfg.GetWorkspaceRoot() != p.Workspaces || cfg.GetVaultRoot() != p.Vaults {
		t.Fatal("seeded roots are not fixture-owned")
	}
	if cfg.GetAPIKey() == "" || cfg.GetAnthropicAPIKey() == "" || cfg.GetGeminiAPIKey() == "" || cfg.GetBraveAPIKey() == "" {
		t.Fatal("seeded config did not read fake provider/search credentials")
	}
	settingsBytes, err := os.ReadFile(filepath.Join(p.DataDir, "settings.json"))
	check(t, err)
	if bytes.Contains(settingsBytes, []byte(cfg.GetAPIKey())) {
		t.Fatal("fixture persisted a fake secret into plaintext settings")
	}
	mgr := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	user, assistant := mgr.GetNames()
	if !mgr.IsOnboardingComplete() || user != "Fixture User" || assistant != resetfixture.AgentName {
		t.Fatal("identity/setup seed did not survive reopening")
	}
	agents, err := store.NewFileStore(filepath.Join(p.DataDir, "agents.json"), types.Settings{})
	check(t, err)
	if names := agents.ListAgents(); len(names) != 1 || names[0] != resetfixture.AgentName {
		t.Fatal("agent seed did not survive reopening")
	}
	folders, err := workspace.NewFileStore(p.Workspaces)
	check(t, err)
	t.Cleanup(func() { check(t, folders.Close()) })
	if _, err := folders.Get(resetfixture.WorkspaceID); err != nil {
		t.Fatal("workspace seed did not survive reopening:", err)
	}
	for range 2 {
		db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
		check(t, err)
		saved, readErr := session.NewSQLiteStore(db).GetSession(t.Context(), resetfixture.SessionID)
		closeErr := db.Close()
		check(t, readErr)
		check(t, closeErr)
		if saved.AgentName != resetfixture.AgentName || len(saved.Messages) != 1 {
			t.Fatal("history seed did not survive same-path reopen")
		}
	}
	// CWD writes are observable, but not in the repository or real installation.
	if _, err := os.Stat(filepath.Join(p.WorkDir, "agents.json")); err != nil {
		t.Fatal("expected real agent store's CWD projection:", err)
	}
}

func TestFixtureSecretsAreIndependentMemoryNamespaces(t *testing.T) {
	f := resetfixture.New(t)
	dek, err := f.Secrets().Get(vault.SecretKeyVaultDEK)
	check(t, err)
	otherKey, err := f.OtherSecrets().Get(vault.SecretKeyOpenAIAPIKey)
	check(t, err)
	check(t, f.Secrets().Delete(vault.SecretKeyOpenAIAPIKey))
	if _, err := f.Secrets().Get(vault.SecretKeyOpenAIAPIKey); !errors.Is(err, vault.ErrSecretNotFound) {
		t.Fatal("fake store did not report missing credential")
	}
	if got, err := f.Secrets().Get(vault.SecretKeyVaultDEK); err != nil || got != dek {
		t.Fatal("provider deletion affected vault encryption material")
	}
	if got, err := f.OtherSecrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil || got != otherKey {
		t.Fatal("provider deletion affected another namespace")
	}
}

func TestFixtureHTTPDoesNotReuseBaseURLOrFollowRedirects(t *testing.T) {
	var outsideCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		outsideCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer outside.Close()
	t.Setenv("PLAYWRIGHT_BASE_URL", outside.URL)
	t.Setenv("HTTP_PROXY", outside.URL)
	f := resetfixture.New(t)
	s := f.StartHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, outside.URL, http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Error("missing reset request header")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if s.URL() == outside.URL {
		t.Fatal("fixture reused an unowned server")
	}
	for _, path := range []string{outside.URL, "//" + strings.TrimPrefix(outside.URL, "http://"), "relative", "/\\escape", "/bad#fragment"} {
		if _, err := s.Do(t.Context(), http.MethodPost, path, nil); err == nil {
			t.Errorf("accepted alternate URL/path %q", path)
		}
	}
	for path, status := range map[string]int{"/api/reset": http.StatusNoContent, "/redirect": http.StatusTemporaryRedirect} {
		resp, err := s.Do(t.Context(), http.MethodPost, path, strings.NewReader(`{}`))
		check(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		check(t, resp.Body.Close())
		check(t, err)
		if resp.StatusCode != status {
			t.Errorf("%s: status %d, want %d", path, resp.StatusCode, status)
		}
	}
	if outsideCalls.Load() != 0 {
		t.Fatal("fixture sent requests to an unowned server")
	}
	s.Close()
	if _, err := s.Do(t.Context(), http.MethodPost, "/api/reset", nil); err == nil {
		t.Fatal("closed fixture permitted a request to a potentially reused port")
	}
}

func TestFixtureWriterReproducesCachedSaveAfterFileRemoval(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	path := filepath.Join(f.Paths().DataDir, "settings.json")
	cfg := config.NewManagerWithSecretStore(path, f.Secrets())
	check(t, cfg.Load())
	w := resetfixture.NewWriter(t, cfg.Save)
	pending, err := w.Begin(t.Context())
	check(t, err)
	check(t, os.Remove(path))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("writer did not pause before its save")
	}
	pending.Release()
	check(t, pending.Wait(t.Context()))
	if _, err := os.Stat(path); err != nil {
		t.Fatal("expected cached save to recreate removed settings:", err)
	}
	w.Stop()
	if _, err := w.Begin(t.Context()); !errors.Is(err, resetfixture.ErrWriterStopped) {
		t.Fatal("stopped writer accepted more work")
	}
}

func TestFixtureWriterCancelsPendingWorkAndPublishesErrors(t *testing.T) {
	var writes atomic.Int32
	writeErr := errors.New("synthetic save failure")
	w := resetfixture.NewWriter(t, func() error { writes.Add(1); return writeErr })
	cancelled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	if _, err := w.Begin(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("writer admitted an already-cancelled request")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pending, err := w.Begin(ctx)
	check(t, err)
	pending.Release()
	for range 2 {
		if err := pending.Wait(ctx); !errors.Is(err, writeErr) {
			t.Fatal("save failure was not retained")
		}
	}
	pending, err = w.Begin(ctx)
	check(t, err)
	w.Stop()
	pending.Release()
	if err := pending.Wait(ctx); !errors.Is(err, resetfixture.ErrWriterStopped) {
		t.Fatal("unreleased write was not cancelled by stop")
	}
	if writes.Load() != 1 {
		t.Fatal("stopping a paused writer ran its callback")
	}
	w.Stop()
}
