package settingsreset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// resolvePath preserves the old reset handler's longest-existing-prefix
// confinement semantics, including missing files and symlinked temp roots.
func resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" || strings.ContainsRune(path, 0) || !utf8.ValidString(path) || len(path) > 4096 {
		return "", errors.New("missing or unsupported filesystem location")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("filesystem location unavailable")
	}
	suffix := ""
	for current := filepath.Clean(abs); ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(current) == current {
			return "", errors.New("filesystem location cannot be resolved")
		}
		// A dangling symlink must not be treated as an ordinary missing path.
		info, linkErr := os.Lstat(current)
		if linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("dangling filesystem link")
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
	}
}

func containsPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func pathsOverlap(a, b string) bool {
	if containsPath(a, b) || containsPath(b, a) {
		return true
	}
	// Also catch hard links and casing aliases on case-insensitive volumes.
	// Comparing parent identity catches roots with differently spelled casing.
	physicalAncestor := func(parent, child string) bool {
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return false
		}
		for current := child; ; current = filepath.Dir(current) {
			if info, err := os.Stat(current); err == nil && os.SameFile(parentInfo, info) {
				return true
			}
			if filepath.Dir(current) == current {
				return false
			}
		}
	}
	return physicalAncestor(a, b) || physicalAncestor(b, a)
}

// A directory traversal is display/count-only and never follows symlinks. An
// unknown count remains unknown on any error or inspection limit.
func countFiles(ctx context.Context, root string) CountFact {
	fact := CountFact{Name: "files in owner root (including preserved files)", UnavailableReason: "file inventory unavailable or exceeds inspection limit"}
	var count int64
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		fact.Count, fact.UnavailableReason = &count, ""
		return fact
	}
	visited := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 4096 {
			return errors.New("file inventory limit")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("linked contents not inspected")
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	if err == nil {
		fact.Count, fact.UnavailableReason = &count, ""
	}
	return fact
}

const maxProtectedHashEntries = 100_000
const maxProtectedHashBytes int64 = 1 << 30

type protectedDigestEntry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	Link   string `json:"link,omitempty"`
	Absent bool   `json:"absent,omitempty"`
}

// digestProtectedPath produces one bounded Merkle-like digest for a retained
// file or tree. It never follows links and records relative names, modes,
// lengths, link targets, and regular-file bytes. Only the digest enters reset
// metadata; retained content never does.
func refreshProtectedDigests(ctx context.Context, evidence *resolvedEvidence) error {
	if evidence == nil || len(evidence.ProtectedPaths) != len(evidence.ProtectedDigests) {
		return errors.New("protected digest evidence is incomplete")
	}
	updated := make([]protectedDigest, len(evidence.ProtectedPaths))
	for i, path := range evidence.ProtectedPaths {
		digest, err := digestProtectedPath(ctx, path)
		if err != nil {
			return err
		}
		updated[i] = protectedDigest{Path: path, Digest: digest}
	}
	evidence.ProtectedDigests = updated
	return nil
}

func digestProtectedPath(ctx context.Context, root string) (string, error) {
	hash := sha256.New()
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		if err := json.NewEncoder(hash).Encode(protectedDigestEntry{Path: ".", Absent: true}); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	if err != nil {
		return "", err
	}
	entries := 0
	var totalBytes int64
	hashOne := func(path string, entryInfo os.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > maxProtectedHashEntries {
			return errors.New("protected tree exceeds entry hashing limit")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(rel) && rel != "." {
			return errors.New("protected tree contains non-local entry")
		}
		record := protectedDigestEntry{Path: filepath.ToSlash(rel), Mode: uint32(entryInfo.Mode()), Size: entryInfo.Size()}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			record.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		if err := json.NewEncoder(hash).Encode(record); err != nil {
			return err
		}
		if !entryInfo.Mode().IsRegular() {
			if entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			return errors.New("protected tree contains unsupported special file")
		}
		totalBytes += entryInfo.Size()
		if totalBytes > maxProtectedHashBytes {
			return errors.New("protected tree exceeds content hashing limit")
		}
		file, err := os.Open(path) // #nosec G304 -- canonical retained evidence is read-only and never client supplied.
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	}
	if !info.IsDir() {
		if err := hashOne(root, info); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		return hashOne(path, entryInfo)
	}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
