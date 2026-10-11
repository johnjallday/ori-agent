package skills

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Only the inspected CLI protocol is accepted. This is a compatibility pin,
// not a signature: installed local runtime code must still be trusted by its
// owner. Discovery never installs, updates or executes npm/npx to resolve it.
const marketplaceRuntimeVersion = "1.7.2"

type marketplaceRuntime struct {
	node    string
	entry   string
	version string
}

func resolveMarketplaceRuntime() (marketplaceRuntime, string) {
	home, _ := os.UserHomeDir()
	return findMarketplaceRuntime(exec.LookPath, home)
}

func findMarketplaceRuntime(lookPath func(string) (string, error), home string) (marketplaceRuntime, string) {
	node, err := lookPath("node")
	if err != nil || !filepath.IsAbs(node) {
		return marketplaceRuntime{}, "node_required"
	}
	// First use an existing global skills executable only when it resolves to
	// the recognized package entry. Shell shims and arbitrary binaries are not
	// executable search backends.
	if bin, err := lookPath("skills"); err == nil {
		if entry, err := filepath.EvalSymlinks(bin); err == nil && filepath.Base(entry) == "cli.mjs" && filepath.Base(filepath.Dir(entry)) == "bin" {
			if runtime, ok := installedMarketplaceRuntime(node, filepath.Dir(filepath.Dir(entry))); ok {
				return runtime, ""
			}
		}
	}
	// Read only the conventional pre-existing npx cache, at most 64 roots.
	// Do not use npm config, workspace node_modules, or ambient endpoint overrides.
	if filepath.IsAbs(home) {
		cache := filepath.Join(home, ".npm", "_npx")
		file, err := os.Open(cache) // #nosec G304 -- fixed npm cache suffix under the OS user's home; read-only runtime discovery
		if err == nil {
			entries, _ := file.ReadDir(64)
			_ = file.Close()
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				root := filepath.Join(cache, entry.Name(), "node_modules", "skills")
				if runtime, ok := installedMarketplaceRuntime(node, root); ok {
					return runtime, ""
				}
			}
		}
	}
	return marketplaceRuntime{}, "skills_runtime_required"
}

func installedMarketplaceRuntime(node, root string) (marketplaceRuntime, bool) {
	data, err := readMetadataFile(filepath.Join(root, "package.json"), 16<<10)
	if err != nil {
		return marketplaceRuntime{}, false
	}
	var metadata struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Bin     map[string]string `json:"bin"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Name != "skills" || metadata.Version != marketplaceRuntimeVersion || metadata.Bin["skills"] != "./bin/cli.mjs" {
		return marketplaceRuntime{}, false
	}
	entry := filepath.Join(root, "bin", "cli.mjs")
	for _, path := range []string{entry, filepath.Join(root, "dist", "cli.mjs")} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return marketplaceRuntime{}, false
		}
	}
	return marketplaceRuntime{node: node, entry: entry, version: metadata.Version}, true
}

var errMarketplaceOutputLimit = errors.New("marketplace output limit exceeded")

// cappedMarketplaceOutput is shared by stdout/stderr, safe for concurrent writes.
// Reaching the cap cancels the process immediately instead of continuing to
// drain unbounded output. Raw output/errors are never returned to the assistant.
type cappedMarketplaceOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (w *cappedMarketplaceOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > MarketplaceOutputBytes-len(w.data) {
		w.overflow = true
		w.cancel()
		return 0, errMarketplaceOutputLimit
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func runMarketplaceSearch(parent context.Context, runtime marketplaceRuntime, query string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, MarketplaceSearchTimeout)
	defer cancel()
	// No real HOME, application cwd, npmrc, proxy, NODE_OPTIONS, tokens or
	// application environment is inherited. The known find command cannot
	// request install and receives one exact nonempty validated query argument.
	home, err := os.MkdirTemp("", "ori-skill-search-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(home) }()
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	home = canonicalHome
	cmd := exec.CommandContext(ctx, runtime.node, runtime.entry, "find", query) // #nosec G204 -- host-resolved installed node and pinned CLI entry; fixed search argv, no shell or model-selected executable
	cmd.Dir = home
	cmd.Env = []string{
		"PATH=" + filepath.Dir(runtime.node) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"HOME=" + home, "TMPDIR=" + home, "USERPROFILE=" + home,
		"CI=1", "NO_COLOR=1", "FORCE_COLOR=0", "DISABLE_TELEMETRY=1", "DO_NOT_TRACK=1",
		"SKILLS_API_URL=" + MarketplaceAPI, "NODE_DISABLE_COMPILE_CACHE=1",
	}
	output := &cappedMarketplaceOutput{cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if output.overflow {
		return "", errMarketplaceOutputLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return string(output.data), nil
}

// readMetadataFile is the shared bounded read used by summary/inventory and
// runtime metadata owners. No prompt parser or package source cache is added.
func readMetadataFile(path string, limit int64) ([]byte, error) {
	data, err := readRegularPrefix(path, limit)
	if err == nil && int64(len(data)) > limit {
		err = errors.New("metadata exceeds the byte limit")
	}
	return data, err
}

func readSkillSummaryFile(path string) ([]byte, error) {
	data, err := readRegularPrefix(path, metadataFileBytes)
	if err != nil {
		return nil, err
	}
	if len(data) > metadataFileBytes {
		data = data[:metadataFileBytes]
		text := strings.TrimLeft(string(data), "\n\r\t ")
		if strings.HasPrefix(text, "---") && !strings.Contains(text[3:], "\n---") {
			return nil, errors.New("frontmatter exceeds the byte limit")
		}
	}
	return data, nil // prompt tail is not needed to parse metadata
}

func readRegularPrefix(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("metadata exceeds the regular-file limit")
	}
	file, err := os.Open(path) // #nosec G304 -- caller composes a metadata leaf under an owner-resolved skills/runtime root
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, limit+1))
}
