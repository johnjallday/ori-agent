package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ErrSkillTreeNotPlain refuses to digest a skill folder that is a symlink or
// holds anything but ordinary files and folders.
var ErrSkillTreeNotPlain = errors.New("skill folder is not a plain tree of files")

// SkillTreeDigest hashes a skill folder the way trusted component fingerprints
// and the retired plugin skill receipts did: relative paths, file modes, and
// bytes, without following symlinks, leaving out a receipt file. Its output
// must never change, or every installed plugin would look modified.
func SkillTreeDigest(root string) (string, error) {
	return TreeDigest(root, func(rel string) bool { return rel == legacySkillReceiptFileName })
}

// TreeDigest hashes relative paths, file modes, and bytes under root without
// following symlinks. Entries for which skip reports true (given their
// slash-separated path relative to root) are left out, with their contents.
func TreeDigest(root string, skip func(rel string) bool) (string, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrSkillTreeNotPlain
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
		if skip != nil && skip(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrSkillTreeNotPlain
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
				return "", ErrSkillTreeNotPlain
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
