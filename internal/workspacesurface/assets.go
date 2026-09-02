package workspacesurface

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

const MaxAssetBytes = 8 << 20

var ErrAssetPathInvalid = errors.New("workspace surface asset path is invalid")

var allowedAssetMIME = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".svg":   "image/svg+xml",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// Asset is one bounded regular file under a trusted installed asset root.
type Asset struct {
	Path        string
	ContentType string
	Data        []byte
}

// ReadAsset rejects absolute/traversal paths, symlinks at the root or any path
// component, directories, special files, unknown MIME types, and oversized
// files. It never follows an owner-controlled symlink even when it points back
// inside the root; this keeps containment review simple and deterministic.
//
// The safety of this function does NOT rest on who authored the asset root. It
// serves user-authored dashboards as well as installed plugins, and in both
// cases the bytes are untrusted. What it guarantees is containment: the root is
// absolute and already clean, the relative path cannot escape it, and every
// component from the root down is stat'd without following symlinks. The
// #nosec G304 annotations below record that invariant — the path is contained
// and symlink-free by construction at each point — not any trust in the author.
func ReadAsset(binding Binding, relativePath string) (Asset, error) {
	if !filepath.IsAbs(binding.AssetRoot) || filepath.Clean(binding.AssetRoot) != binding.AssetRoot || !safeRelativeAssetPath(relativePath) {
		return Asset{}, ErrAssetPathInvalid
	}
	rootInfo, err := os.Lstat(binding.AssetRoot) // #nosec G304 -- root is absolute and already Clean; Lstat does not follow a symlinked root, which is rejected next
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Asset{}, ErrAssetPathInvalid
	}

	parts := strings.Split(filepath.FromSlash(relativePath), string(filepath.Separator))
	current := binding.AssetRoot
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Asset{}, ErrAssetPathInvalid
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current) // #nosec G304 -- built from the checked root plus components that cannot be "..", and each is rejected if it is a symlink, so the walk stays inside the root
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return Asset{}, ErrAssetPathInvalid
		}
	}

	rel, err := filepath.Rel(binding.AssetRoot, current)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Asset{}, ErrAssetPathInvalid
	}
	info, err := os.Lstat(current) // #nosec G304 -- containment re-verified above via filepath.Rel; this is the same path the walk just proved is inside the root
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > MaxAssetBytes {
		return Asset{}, ErrAssetPathInvalid
	}
	extension := strings.ToLower(filepath.Ext(current))
	contentType, allowed := allowedAssetMIME[extension]
	if !allowed {
		return Asset{}, fmt.Errorf("%w: unsupported asset type", ErrAssetPathInvalid)
	}
	file, err := os.Open(current) // #nosec G304 -- opens the exact contained, non-symlink regular file checked immediately above; the read below is bounded by MaxAssetBytes
	if err != nil {
		return Asset{}, ErrAssetPathInvalid
	}
	data, readErr := io.ReadAll(io.LimitReader(file, MaxAssetBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > MaxAssetBytes {
		return Asset{}, ErrAssetPathInvalid
	}
	// Preserve the explicit allowlist while letting the standard library add no
	// executable surprises. The map value is authoritative.
	if parsed, _, parseErr := mime.ParseMediaType(contentType); parseErr != nil || parsed == "" {
		return Asset{}, ErrAssetPathInvalid
	}
	return Asset{Path: relativePath, ContentType: contentType, Data: data}, nil
}
