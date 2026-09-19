package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SkillOwnershipSchemaVersion = 1
	SkillOwnershipFileName      = ".ori-plugin-skill.json"
)

var (
	ErrSkillDestinationConflict = errors.New("plugin skill destination is independently owned")
	ErrSkillOwnershipChanged    = errors.New("plugin skill copy was changed after installation")
)

// SkillOwnershipReceipt is written inside an atomically published plugin skill
// copy. The installed-plugin registry claims the name; this receipt and digest
// prove that the files at that shared destination are still the same owned copy.
type SkillOwnershipReceipt struct {
	SchemaVersion int    `json:"schema_version"`
	PluginName    string `json:"plugin_name"`
	SkillName     string `json:"skill_name"`
	TreeDigest    string `json:"tree_digest"`
}

func NewSkillOwnershipReceipt(pluginName, skillName, digest string) SkillOwnershipReceipt {
	return SkillOwnershipReceipt{SchemaVersion: SkillOwnershipSchemaVersion, PluginName: pluginName, SkillName: skillName, TreeDigest: digest}
}

func WriteSkillOwnershipReceipt(directory string, receipt SkillOwnershipReceipt) error {
	if !validSkillOwnershipReceipt(receipt) {
		return ErrSkillDestinationConflict
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, SkillOwnershipFileName), append(encoded, '\n'), 0o600)
}

// ReadSkillOwnershipReceipt identifies a managed personal-skill copy. A
// missing marker means the skill is independently owned, not malformed.
func ReadSkillOwnershipReceipt(directory string) (SkillOwnershipReceipt, bool, error) {
	marker := filepath.Join(directory, SkillOwnershipFileName)
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return SkillOwnershipReceipt{}, false, nil
	}
	if err != nil {
		return SkillOwnershipReceipt{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return SkillOwnershipReceipt{}, true, ErrSkillDestinationConflict
	}
	encoded, err := os.ReadFile(marker) // #nosec G304 -- exact regular marker in a discovered personal skill directory
	if err != nil {
		return SkillOwnershipReceipt{}, true, err
	}
	var receipt SkillOwnershipReceipt
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || !validSkillOwnershipReceipt(receipt) {
		return SkillOwnershipReceipt{}, true, ErrSkillDestinationConflict
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SkillOwnershipReceipt{}, true, ErrSkillDestinationConflict
	}
	return receipt, true, nil
}

func VerifySkillOwnership(directory, pluginName, skillName string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrSkillDestinationConflict
	}
	receipt, managed, err := ReadSkillOwnershipReceipt(directory)
	if err != nil || !managed || receipt.PluginName != pluginName || receipt.SkillName != skillName {
		return ErrSkillDestinationConflict
	}
	digest, err := SkillTreeDigest(directory)
	if err != nil || digest != receipt.TreeDigest {
		return ErrSkillOwnershipChanged
	}
	return nil
}

func validSkillOwnershipReceipt(receipt SkillOwnershipReceipt) bool {
	return receipt.SchemaVersion == SkillOwnershipSchemaVersion && safeSkillSegment(receipt.PluginName) &&
		safeSkillSegment(receipt.SkillName) && len(receipt.TreeDigest) == 64 && strings.ToLower(receipt.TreeDigest) == receipt.TreeDigest
}

func ValidSkillOwnershipSegment(value string) bool { return safeSkillSegment(value) }

func safeSkillSegment(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, `/\\\x00`) && strings.TrimSpace(value) == value
}

// SkillTreeDigest hashes relative paths, file modes and bytes without following
// symlinks. The ownership receipt itself is excluded.
func SkillTreeDigest(root string) (string, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrSkillDestinationConflict
	}
	type entry struct {
		path string
		mode os.FileMode
	}
	var entries []entry
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == SkillOwnershipFileName {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrSkillDestinationConflict
		}
		entries = append(entries, entry{path: rel, mode: info.Mode()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	tree, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = tree.Close() }()
	hash := sha256.New()
	for _, entry := range entries {
		mode := "dir"
		if entry.mode.IsRegular() {
			mode = entry.mode.String()
		}
		_, _ = io.WriteString(hash, entry.path+"\x00"+mode+"\x00")
		if entry.mode.IsRegular() {
			file, openErr := tree.Open(filepath.FromSlash(entry.path))
			if openErr != nil {
				return "", openErr
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !openedInfo.Mode().IsRegular() {
				_ = file.Close()
				return "", ErrSkillDestinationConflict
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		_, _ = io.WriteString(hash, "\x00")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
