package skillshttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// SkillSourceFileName records where a skill in the Skills folder came from, so
// any machine can check it for updates.
const SkillSourceFileName = ".ori-skill-source.json"

const skillSourceSchemaVersion = 1

// SkillSource is the content of a skill's .ori-skill-source.json.
type SkillSource struct {
	SchemaVersion int       `json:"schema_version"`
	Package       string    `json:"package"`
	InstalledAt   time.Time `json:"installed_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// TreeDigest is the digest of the skill folder, without this file, when it
	// was installed or last updated. A different digest now means the user
	// edited the skill.
	TreeDigest string `json:"tree_digest"`
}

// readSkillSource reads a skill folder's source file. found is false when the
// folder has none: the skill did not come from the marketplace.
func readSkillSource(skillDir string) (source SkillSource, found bool, err error) {
	data, err := os.ReadFile(filepath.Join(skillDir, SkillSourceFileName)) // #nosec G304 -- the fixed source file inside a skill folder of the Skills folder
	if errors.Is(err, os.ErrNotExist) {
		return SkillSource{}, false, nil
	}
	if err != nil {
		return SkillSource{}, true, err
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return SkillSource{}, true, fmt.Errorf("read %s: %w", SkillSourceFileName, err)
	}
	return source, true, nil
}

// writeSkillSource writes a skill folder's source file in one step: a temp
// file renamed into place.
func writeSkillSource(skillDir string, source SkillSource) error {
	source.SchemaVersion = skillSourceSchemaVersion
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(skillDir, ".ori-skill-source-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, filepath.Join(skillDir, SkillSourceFileName)); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

// skillFolderDigest hashes a skill folder the same way on every machine,
// leaving out its own source file and the .DS_Store files Finder leaves
// behind, so opening a folder does not look like an edit.
func skillFolderDigest(skillDir string) (string, error) {
	return plugin.TreeDigest(skillDir, func(rel string) bool {
		return rel == SkillSourceFileName || path.Base(rel) == ".DS_Store" ||
			(strings.HasPrefix(rel, ".ori-skill-source-") && strings.HasSuffix(rel, ".tmp"))
	})
}
