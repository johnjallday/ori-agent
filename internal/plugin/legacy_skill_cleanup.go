package plugin

// One-time cleanup of the plugin skill copies Ori used to make in
// ~/.agents/skills. Plugin skills are now read in place from each plugin's
// install folder, so those copies only duplicate what the plugin already
// provides. A copy is removed only when it is provably Ori's own and
// untouched; everything else in that folder belongs to the user or to other
// tools and is left exactly as it is.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// legacySkillReceiptFileName marked a plugin skill copy in ~/.agents/skills.
	legacySkillReceiptFileName = ".ori-plugin-skill.json"
	legacySkillReceiptSchema   = 1

	// LegacySkillCleanupMarkerName is the data-dir file recording that the
	// one-time cleanup ran for this data directory.
	LegacySkillCleanupMarkerName = "legacy_plugin_skills_cleaned.json"
)

// LegacySkillReceiptFileName is the ownership file a retired plugin skill copy
// holds. A folder with one belongs to a plugin, not to the user.
const LegacySkillReceiptFileName = legacySkillReceiptFileName

// legacySkillReceipt is the ownership file a plugin skill copy carried.
type legacySkillReceipt struct {
	SchemaVersion int    `json:"schema_version"`
	PluginName    string `json:"plugin_name"`
	SkillName     string `json:"skill_name"`
	TreeDigest    string `json:"tree_digest"`
}

// LegacySkillsRoot is ~/.agents/skills, where plugin skills used to be copied.
// Only the one-time cleanup reads it.
func LegacySkillsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// LegacySkillCleanupResult is what the cleanup did, as logged and as written
// to the marker file.
type LegacySkillCleanupResult struct {
	SkillsRoot string    `json:"skills_root"`
	Removed    []string  `json:"removed"`
	Kept       []string  `json:"kept"`
	RanAt      time.Time `json:"ran_at"`
}

// CleanLegacyPluginSkillsOnce runs the cleanup unless this data directory's
// marker says it already ran. ran is false when it was skipped. A folder that
// cannot be read leaves no marker, so the next start tries again.
func CleanLegacyPluginSkillsOnce(dataDir, skillsRoot string, installed []InstalledPlugin) (LegacySkillCleanupResult, bool, error) {
	marker := filepath.Join(dataDir, LegacySkillCleanupMarkerName)
	if _, err := os.Stat(marker); err == nil {
		return LegacySkillCleanupResult{}, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return LegacySkillCleanupResult{}, false, err
	}
	result, err := CleanLegacyPluginSkills(skillsRoot, installed)
	if err != nil {
		return result, true, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, true, err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return result, true, err
	}
	return result, true, os.WriteFile(marker, append(encoded, '\n'), 0o600)
}

// CleanLegacyPluginSkills removes a folder in skillsRoot only when all of
// these hold: it holds a valid plugin skill receipt naming that folder; the
// plugin the receipt names is installed here; and its contents still match the
// digest recorded at install, so the user has not edited it. Every other
// entry is left alone. Kept lists each receipt-bearing folder that stayed, and
// why.
func CleanLegacyPluginSkills(skillsRoot string, installed []InstalledPlugin) (LegacySkillCleanupResult, error) {
	result := LegacySkillCleanupResult{SkillsRoot: skillsRoot, Removed: []string{}, Kept: []string{}, RanAt: time.Now().UTC()}
	if strings.TrimSpace(skillsRoot) == "" {
		return result, nil
	}
	owned, err := os.OpenRoot(skillsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer func() { _ = owned.Close() }()
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return result, err
	}
	installedHere := make(map[string]bool, len(installed))
	for _, record := range installed {
		installedHere[record.Name] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		folder := filepath.Join(skillsRoot, name)
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		receipt, managed, err := readLegacySkillReceipt(folder)
		if !managed {
			continue // not a plugin copy: the user's own, or another tool's
		}
		reason := ""
		switch {
		case err != nil || receipt.SkillName != name:
			reason = "its plugin receipt is not valid"
		case !installedHere[receipt.PluginName]:
			reason = fmt.Sprintf("the plugin %q is not installed here", receipt.PluginName)
		default:
			digest, digestErr := SkillTreeDigest(folder)
			if digestErr != nil || digest != receipt.TreeDigest {
				reason = "it was changed after it was installed"
			}
		}
		if reason != "" {
			result.Kept = append(result.Kept, name+": "+reason)
			continue
		}
		if err := owned.RemoveAll(name); err != nil {
			result.Kept = append(result.Kept, name+": it could not be removed")
			continue
		}
		result.Removed = append(result.Removed, name)
	}
	sort.Strings(result.Removed)
	sort.Strings(result.Kept)
	return result, nil
}

// readLegacySkillReceipt reports whether folder holds a plugin skill receipt
// (managed) and decodes it strictly. A malformed receipt is managed with an
// error: the folder is a plugin's, but not provably unedited.
func readLegacySkillReceipt(folder string) (legacySkillReceipt, bool, error) {
	marker := filepath.Join(folder, legacySkillReceiptFileName)
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return legacySkillReceipt{}, false, nil
	}
	if err != nil {
		return legacySkillReceipt{}, true, err
	}
	if !info.Mode().IsRegular() {
		return legacySkillReceipt{}, true, ErrSkillTreeNotPlain
	}
	encoded, err := os.ReadFile(marker) // #nosec G304 -- the fixed receipt name inside a folder listed from the skills root
	if err != nil {
		return legacySkillReceipt{}, true, err
	}
	var receipt legacySkillReceipt
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return legacySkillReceipt{}, true, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return legacySkillReceipt{}, true, errors.New("plugin skill receipt has trailing data")
	}
	if receipt.SchemaVersion != legacySkillReceiptSchema || !validResetSegment(receipt.PluginName) ||
		!validResetSegment(receipt.SkillName) || len(receipt.TreeDigest) != 64 {
		return legacySkillReceipt{}, true, errors.New("plugin skill receipt is not valid")
	}
	return receipt, true, nil
}
