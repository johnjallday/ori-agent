// Package resetfixture provides test-owned installations for destructive reset
// tests. It is not a production sandbox. Tests must inject its memory secrets
// and construct only the services they exercise, not boot cmd/server implicitly.
package resetfixture

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
)

// Paths is a copy of the fixture's locations. None can be supplied by a caller.
// WorkDir deliberately differs from DataDir to expose CWD-dependent stores.
type Paths struct {
	Root, DataDir, WorkDir, Home, Workspaces, Vaults, Templates string
}

// Fixture owns a newly allocated directory, fake secrets, and retained-file
// assertions. Use in sequential tests only: New changes process environment/CWD
// using testing's restoring helpers, which reject parallel tests.
type Fixture struct {
	paths     Paths
	root      *os.Root
	secrets   *vault.MemorySecretStore
	other     *vault.MemorySecretStore
	sentinels map[string][sha256.Size]byte
}

// New never accepts an existing directory, environment, secret backend, or URL.
// Cleanup restores CWD/environment after subsequently registered service cleanup
// runs, then removes only the newly allocated temporary directory.
func New(t testing.TB) *Fixture {
	t.Helper()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	root, err := os.OpenRoot(rootPath)
	must(t, err)
	t.Cleanup(func() { must(t, root.Close()) })
	f := &Fixture{
		paths: Paths{
			Root: rootPath, DataDir: filepath.Join(rootPath, "data"),
			WorkDir: filepath.Join(rootPath, "cwd"), Home: filepath.Join(rootPath, "home"),
			Workspaces: filepath.Join(rootPath, "projects"), Vaults: filepath.Join(rootPath, "vaults"),
			Templates: filepath.Join(rootPath, "templates"),
		},
		root: root, secrets: vault.NewMemorySecretStore(), other: vault.NewMemorySecretStore(),
		sentinels: make(map[string][sha256.Size]byte),
	}
	for _, dir := range []string{"data", "cwd", "home", "projects", "vaults", "templates", "bin", "tmp", "workflow-templates"} {
		must(t, root.MkdirAll(dir, 0o750))
	}
	f.isolateProcess(t)
	for _, store := range []*vault.MemorySecretStore{f.secrets, f.other} {
		for _, key := range []vault.SecretKey{
			vault.SecretKeyOpenAIAPIKey, vault.SecretKeyAnthropicAPIKey,
			vault.SecretKeyGeminiAPIKey, vault.SecretKeyBraveAPIKey, vault.SecretKeyVaultDEK,
		} {
			// Synthetic values are generated locally, never copied from credentials
			// or printed in a fixture manifest, assertion, or log.
			var value [32]byte
			_, err := rand.Read(value[:])
			must(t, err)
			must(t, store.Set(key, hex.EncodeToString(value[:])))
		}
	}
	for _, name := range []string{
		"external/settings.json", "data-backup/settings.json",
		"home/.config/other-app/auth.json", "projects/retained/source.txt", "vaults/retained.bin",
		"data/workspaces/retained-project/source.txt", "data/retained-vaults/retained.bin",
	} {
		content := []byte("reset fixture: preserve " + name + "\n")
		must(t, f.WriteFile(name, content))
		f.sentinels[name] = sha256.Sum256(content)
	}
	t.Cleanup(func() { f.AssertPreserved(t) })
	return f
}

func (f *Fixture) Paths() Paths { return f.paths }

// Secrets is the only credential backend to inject into tested Ori services.
func (f *Fixture) Secrets() *vault.MemorySecretStore { return f.secrets }

// OtherSecrets represents an unrelated installation/namespace, not an OS store.
func (f *Fixture) OtherSecrets() *vault.MemorySecretStore { return f.other }

func (f *Fixture) isolateProcess(t testing.TB) {
	t.Helper()
	// An allowlist avoids overlooking a new provider key/root/base-URL variable.
	// Empty PATH also prevents accidental OS Keychain/CLI discovery via exec.
	// Retain only Windows' OS runtime paths, not application config or secrets.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "SYSTEMROOT") || strings.EqualFold(key, "WINDIR") {
			continue
		}
		t.Setenv(key, "")
	}
	for key, value := range map[string]string{
		"HOME": f.paths.Home, "USERPROFILE": f.paths.Home,
		"XDG_CONFIG_HOME":   filepath.Join(f.paths.Home, ".config"),
		"XDG_DATA_HOME":     filepath.Join(f.paths.Home, ".local", "share"),
		"XDG_CACHE_HOME":    filepath.Join(f.paths.Home, ".cache"),
		"APPDATA":           filepath.Join(f.paths.Home, "AppData", "Roaming"),
		"LOCALAPPDATA":      filepath.Join(f.paths.Home, "AppData", "Local"),
		"CODEX_HOME":        filepath.Join(f.paths.Home, ".codex"),
		"CLAUDE_CONFIG_DIR": filepath.Join(f.paths.Home, ".claude"),
		"PATH":              filepath.Join(f.paths.Root, "bin"),
		"TMPDIR":            filepath.Join(f.paths.Root, "tmp"),
		"TMP":               filepath.Join(f.paths.Root, "tmp"), "TEMP": filepath.Join(f.paths.Root, "tmp"),
		"ORI_DATA_DIR":                    f.paths.DataDir,
		"AGENT_STORE_PATH":                filepath.Join(f.paths.DataDir, "agents.json"),
		"ORI_TEMPLATES_DIR":               f.paths.Templates,
		"WORKFLOW_TEMPLATES_DIR":          filepath.Join(f.paths.Root, "workflow-templates"),
		"ORI_DISABLE_EXTERNAL_MCP_IMPORT": "true", "NO_BROWSER": "1",
		// Config owns these roots by default. Tests of operator roots must
		// explicitly set them to fixture paths, never inherited directories.
		"WORKSPACE_DIR": "", "ORI_VAULT_DIR": "",
	} {
		t.Setenv(key, value)
	}
	t.Chdir(f.paths.WorkDir)
}

// WriteFile confines fixture setup/writers to the owned root, including through
// symlinks. Absolute paths and parent traversal are never accepted.
func (f *Fixture) WriteFile(name string, data []byte) error {
	if !filepath.IsLocal(name) || name == "." {
		return fmt.Errorf("fixture file must have a local relative name")
	}
	if err := f.root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return err
	}
	return f.root.WriteFile(name, data, 0o600)
}

// AssertPreserved compares contents without printing them. It also runs at test
// cleanup, after services/writers registered later have been stopped and joined.
func (f *Fixture) AssertPreserved(t testing.TB) {
	t.Helper()
	for name, expected := range f.sentinels {
		data, err := f.root.ReadFile(name)
		if err != nil {
			t.Errorf("retained fixture file %s is unreadable: %v", name, err)
		} else if sha256.Sum256(data) != expected {
			t.Errorf("retained fixture file %s changed", name)
		}
	}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
