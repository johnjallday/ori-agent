// Package secondbrain connects the user's own notes, an Obsidian vault or any
// folder of Markdown files, to the Personal HQ as a source the personal
// assistant reads. It finds a vault from Obsidian's own vault list, reads a
// folder within fixed bounds, and turns the open items in it into follow-ups.
//
// Nothing here writes under the notes folder. See
// docs/architecture/second-brain.md for the whole contract.
package secondbrain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
)

const (
	// DetectionTTL is how long one reading of Obsidian's vault list is reused
	// (FR3). Detection runs when the ask is composed and when the chooser
	// opens, never on a timer.
	DetectionTTL = 5 * time.Minute
	// maxVaultListBytes caps how much of obsidian.json is read. The file holds
	// one short entry per vault; anything larger is not Obsidian's list.
	maxVaultListBytes = 1 << 20
	// ObsidianFolder is the folder Obsidian keeps inside every vault.
	ObsidianFolder = ".obsidian"
)

// Vault is one Obsidian vault the user has opened, as Obsidian lists it.
type Vault struct {
	// ID is Ori's handle for the vault, derived from its canonical path. It is
	// what the page sends back; Obsidian's own key never leaves the server.
	ID string
	// Name is the folder's base name, which is also the name Obsidian uses for
	// the vault in its links.
	Name string
	// Path is the canonical, symlink-resolved absolute path.
	Path string
	// LastUsed is when Obsidian last opened the vault ("ts").
	LastUsed time.Time
	// Warning is "" or folderpath.WarningOutsideHome.
	Warning string
}

// VaultListPath returns where Obsidian keeps its vault list on goos (FR1):
// macOS ~/Library/Application Support/obsidian/obsidian.json, Windows
// %APPDATA%\obsidian\obsidian.json, Linux ~/.config/obsidian/obsidian.json.
// Every location derives from home (and appData on Windows) rather than from
// the process environment, so a sandboxed server finds its sandbox's list and
// never the real one (FR4). It returns "" when home is unknown.
func VaultListPath(goos, home, appData string) string {
	home = strings.TrimSpace(home)
	appData = strings.TrimSpace(appData)
	switch goos {
	case "darwin":
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "obsidian", "obsidian.json")
	case "windows":
		if appData == "" {
			if home == "" {
				return ""
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "obsidian", "obsidian.json")
	default:
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".config", "obsidian", "obsidian.json")
	}
}

// listedVault is one entry of obsidian.json as Obsidian writes it.
type listedVault struct {
	Path string `json:"path"`
	// TS is milliseconds since the epoch. Obsidian writes an integer; a float
	// is accepted rather than dropping the vault.
	TS float64 `json:"ts"`
}

// parseVaultList reads Obsidian's vault list. A file that is not the expected
// shape yields no vaults; one malformed entry drops only that entry.
func parseVaultList(data []byte) []listedVault {
	var file struct {
		Vaults map[string]json.RawMessage `json:"vaults"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	vaults := make([]listedVault, 0, len(file.Vaults))
	for _, raw := range file.Vaults {
		var entry listedVault
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if strings.TrimSpace(entry.Path) == "" {
			continue
		}
		vaults = append(vaults, entry)
	}
	return vaults
}

// readVaultList reads at most maxVaultListBytes of path. A missing, unreadable
// or oversized file is the same as no list (FR1): never an error the user sees.
func readVaultList(path string) []listedVault {
	if path == "" {
		return nil
	}
	file, err := os.Open(path) // #nosec G304 -- Obsidian's own config file under the server's home, read as metadata only
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxVaultListBytes+1))
	if err != nil || len(data) > maxVaultListBytes {
		return nil
	}
	return parseVaultList(data)
}

// IsObsidianVault reports whether dir holds Obsidian's own folder, a real
// directory and not a symbolic link. It reads one entry and opens nothing.
func IsObsidianVault(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ObsidianFolder))
	return err == nil && info.IsDir()
}

// SourceID is Ori's handle for a notes folder at the canonical path: a
// detected vault and the connected source share it, so the open items of one
// folder keep their ids when it is connected again.
func SourceID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "v_" + hex.EncodeToString(sum[:8])
}

// checkVault applies FR2 to one listed path: it must itself be a directory,
// not a symbolic link, pass the typed-path rules, and hold .obsidian.
func checkVault(path string, guards filejanitor.RootGuards) (folderpath.Checked, bool) {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return folderpath.Checked{}, false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return folderpath.Checked{}, false
	}
	checked, err := folderpath.Check(path, guards)
	if err != nil {
		return folderpath.Checked{}, false
	}
	if !IsObsidianVault(checked.Path) {
		return folderpath.Checked{}, false
	}
	return checked, true
}

// Detector finds the user's Obsidian vaults. It is safe for concurrent use.
type Detector struct {
	// Home is the server's home folder (FR4); AppData is %APPDATA% on Windows.
	Home    string
	AppData string
	// GOOS picks the vault-list location; "" means the running system.
	GOOS string
	// Guards are the typed-path rules every vault must pass (FR2).
	Guards filejanitor.RootGuards
	// TTL defaults to DetectionTTL; Now defaults to time.Now.
	TTL time.Duration
	Now func() time.Time

	mu       sync.Mutex
	cached   []Vault
	cachedAt time.Time
	fresh    bool
}

// NewDetector returns a detector for the server's own home and guards.
func NewDetector(home string, guards filejanitor.RootGuards) *Detector {
	return &Detector{Home: home, AppData: os.Getenv("APPDATA"), Guards: guards}
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Detector) ttl() time.Duration {
	if d.TTL > 0 {
		return d.TTL
	}
	return DetectionTTL
}

// Vaults returns the vaults Obsidian lists that pass FR2, most recently used
// first. A result is reused for at most the TTL (FR3).
func (d *Detector) Vaults() []Vault {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.fresh && now.Sub(d.cachedAt) < d.ttl() && !now.Before(d.cachedAt) {
		return append([]Vault(nil), d.cached...)
	}
	d.cached = d.detect()
	d.cachedAt = now
	d.fresh = true
	return append([]Vault(nil), d.cached...)
}

// Lookup returns the detected vault with id, from the same cached result.
func (d *Detector) Lookup(id string) (Vault, bool) {
	for _, vault := range d.Vaults() {
		if vault.ID == id {
			return vault, true
		}
	}
	return Vault{}, false
}

// Forget drops the cached result, so the next call reads the list again.
func (d *Detector) Forget() {
	d.mu.Lock()
	d.fresh = false
	d.cached = nil
	d.mu.Unlock()
}

func (d *Detector) detect() []Vault {
	goos := d.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	listed := readVaultList(VaultListPath(goos, d.Home, d.AppData))
	byPath := make(map[string]Vault, len(listed))
	for _, entry := range listed {
		checked, ok := checkVault(entry.Path, d.Guards)
		if !ok {
			continue
		}
		vault := Vault{
			ID:       SourceID(checked.Path),
			Name:     checked.Name,
			Path:     checked.Path,
			LastUsed: time.UnixMilli(int64(entry.TS)).UTC(),
			Warning:  checked.Warning,
		}
		// Two entries for one folder (a link and its target): keep the newer.
		if seen, ok := byPath[checked.Path]; ok && !vault.LastUsed.After(seen.LastUsed) {
			continue
		}
		byPath[checked.Path] = vault
	}
	vaults := make([]Vault, 0, len(byPath))
	for _, vault := range byPath {
		vaults = append(vaults, vault)
	}
	sort.Slice(vaults, func(i, j int) bool {
		if !vaults[i].LastUsed.Equal(vaults[j].LastUsed) {
			return vaults[i].LastUsed.After(vaults[j].LastUsed)
		}
		if vaults[i].Name != vaults[j].Name {
			return vaults[i].Name < vaults[j].Name
		}
		return vaults[i].Path < vaults[j].Path
	})
	return vaults
}
