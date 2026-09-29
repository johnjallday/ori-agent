package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// transientContinuityPrefixes are scratch names Ori's own atomic writers use
// beside the file they replace. A leftover from a crash is not user content.
var transientContinuityPrefixes = []string{
	".tmp-", ".ori-config-", ".ori-project-fallback-", ".assistant-learning-",
	".memory-exact-", ".triggers-", ".personal-assistant-knowledge-", ".workspace_allowlist-",
}

// continuityInventoryOwned reports paths that a typed collector declares
// itself (workspace.json and scoped agent profiles) or that are never part of
// this workspace's own files (managed checkpoint, children, locks, OS junk).
func continuityInventoryOwned(rel string, dir bool) (excluded bool) {
	folded := strings.ToLower(rel)
	top := folded
	if i := strings.IndexByte(folded, '/'); i >= 0 {
		top = folded[:i]
	}
	switch {
	case !dir && folded == WorkspaceConfigFile:
		return true
	case top == WorkspaceAgentsDir:
		return true
	}
	return continuityNeverCovered(rel, dir)
}

// continuityNeverCovered reports paths no checkpoint describes: the managed
// checkpoint itself, child workspaces, locks, OS junk and writer scratch.
func continuityNeverCovered(rel string, dir bool) bool {
	folded := strings.ToLower(rel)
	top := folded
	if i := strings.IndexByte(folded, '/'); i >= 0 {
		top = folded[:i]
	}
	switch {
	case folded == strings.ToLower(workspacecontinuity.Directory) || strings.HasPrefix(folded, strings.ToLower(workspacecontinuity.Directory)+"/"):
		return true
	case top == SubWorkspacesDir || top == "sub_workspaces":
		return true
	case folded == ".ori/personal-assistant-knowledge.lock":
		return true
	}
	name := path.Base(rel)
	switch strings.ToLower(name) {
	case ".ds_store", "thumbs.db", "desktop.ini":
		return true
	}
	if strings.HasPrefix(folded, ".ori/.continuity-init-") {
		return true
	}
	if !dir {
		for _, prefix := range transientContinuityPrefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

// walkContinuityFolder visits every regular file the workspace itself owns,
// without following links. Links, devices and sockets are refused: a copy
// cannot faithfully carry them, and silently dropping them would misreport
// what the folder holds.
func walkContinuityFolder(ctx context.Context, folder string, visit func(rel string, info fs.FileInfo, root *os.Root) error) error {
	return walkContinuityFiles(ctx, folder, continuityInventoryOwned, visit)
}

func walkContinuityFiles(ctx context.Context, folder string, excluded func(string, bool) bool, visit func(rel string, info fs.FileInfo, root *os.Root) error) error {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return workspacecontinuity.ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	visited := 0
	stack := []string{"."}
	for len(stack) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		handle, err := root.Open(dir)
		if err != nil {
			return workspacecontinuity.ErrUnsafe
		}
		entries, readErr := handle.ReadDir(-1)
		closeErr := handle.Close()
		if readErr != nil || closeErr != nil {
			return workspacecontinuity.ErrUnsafe
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			visited++
			if visited > 4*workspacecontinuity.MaxFiles {
				return workspacecontinuity.ErrLimit
			}
			rel := entry.Name()
			if dir != "." {
				rel = dir + "/" + entry.Name()
			}
			if excluded(rel, entry.IsDir()) {
				continue
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return workspacecontinuity.ErrUnsafe
			}
			if entry.IsDir() {
				stack = append(stack, rel)
				continue
			}
			if !entry.Type().IsRegular() {
				return workspacecontinuity.ErrUnsafe
			}
			info, err := entry.Info()
			if err != nil {
				return workspacecontinuity.ErrChanged
			}
			if err := visit(rel, info, root); err != nil {
				return err
			}
		}
	}
	return nil
}

// CollectContinuityFolderFiles fingerprints every other workspace-owned file
// (notes, MEMORY.md, files/, outputs/, triggers, sidecars and anything the
// user put in the folder). Every one is declared in the manifest, so a
// reviewed copy either carries all of them or is reported incomplete; none
// is silently dropped. Limits surface as ErrLimit, never truncation.
func CollectContinuityFolderFiles(ctx context.Context, folder string) ([]workspacecontinuity.Fingerprint, error) {
	var result []workspacecontinuity.Fingerprint
	var total int64
	err := walkContinuityFolder(ctx, folder, func(rel string, info fs.FileInfo, root *os.Root) error {
		if len(result) >= workspacecontinuity.MaxFiles {
			return workspacecontinuity.ErrLimit
		}
		if !workspacecontinuity.ValidCanonicalPath(rel) {
			return workspacecontinuity.ErrUnsafe // a name another filesystem cannot hold faithfully
		}
		if info.Size() > workspacecontinuity.MaxBlobBytes || info.Size() > workspacecontinuity.MaxTotalBytes-total {
			return workspacecontinuity.ErrLimit
		}
		file, err := root.Open(rel)
		if err != nil {
			return workspacecontinuity.ErrChanged
		}
		before, statErr := file.Stat()
		hash := sha256.New()
		written, copyErr := io.Copy(hash, io.LimitReader(file, workspacecontinuity.MaxBlobBytes+1))
		after, afterErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(statErr, copyErr, afterErr, closeErr); err != nil {
			return workspacecontinuity.ErrChanged
		}
		if !before.Mode().IsRegular() || written != before.Size() || !os.SameFile(before, after) ||
			after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, info) {
			return workspacecontinuity.ErrChanged
		}
		total += written
		result = append(result, workspacecontinuity.Fingerprint{Path: rel, Digest: hex.EncodeToString(hash.Sum(nil)), Bytes: written})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// ContinuityFolderSignature is a cheap change detector over every file the
// checkpoint covers (including workspace.json and agent profiles): paths,
// sizes and modification times, without reading content. A different value
// means the folder changed since the signature was taken.
func ContinuityFolderSignature(ctx context.Context, folder string) (string, error) {
	hash := sha256.New()
	err := walkContinuityFiles(ctx, folder, continuityNeverCovered, func(rel string, info fs.FileInfo, _ *os.Root) error {
		_, _ = io.WriteString(hash, rel+"\x00"+info.ModTime().UTC().Format(time.RFC3339Nano)+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CheckContinuityFolderCoverage compares the physical folder with the
// manifest without reading file content: every owned file must be declared.
// Inspect separately verifies each declared file's bytes. An undeclared file
// would travel in the copy without a reviewed owner, so it makes the member
// incomplete rather than being skipped on import.
func CheckContinuityFolderCoverage(ctx context.Context, folder string, files []workspacecontinuity.Fingerprint) error {
	declared := make(map[string]int64, len(files))
	for _, file := range files {
		declared[file.Path] = file.Bytes
	}
	return walkContinuityFolder(ctx, folder, func(rel string, info fs.FileInfo, _ *os.Root) error {
		size, ok := declared[rel]
		if !ok || size != info.Size() {
			return workspacecontinuity.ErrIncomplete
		}
		return nil
	})
}
