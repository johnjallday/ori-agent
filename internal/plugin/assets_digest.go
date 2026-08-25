package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxPluginUIFiles      = 1024
	maxPluginUIFileBytes  = 8 << 20
	maxPluginUITotalBytes = 32 << 20
)

func resolvePluginAssetDigest(descriptor PluginDescriptor) (string, error) {
	if descriptor.WorkspaceSurfaces == nil {
		return "", nil
	}
	roots := make(map[string]struct{})
	for _, capability := range descriptor.WorkspaceSurfaces.Capabilities {
		for _, surface := range capability.Surfaces {
			if !safeContributionPath(surface.EntryAsset) {
				return "", errors.New("plugin UI entry asset path is invalid")
			}
			parts := strings.Split(surface.EntryAsset, "/")
			roots[parts[0]] = struct{}{}
		}
	}
	type asset struct {
		path string
		size int64
	}
	var assets []asset
	var total int64
	for relativeRoot := range roots {
		root, err := containedPluginComponent(descriptor.InstallDir, relativeRoot, true)
		if err != nil {
			return "", errors.New("plugin UI asset root is unsafe")
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return errors.New("plugin UI asset tree is unreadable")
			}
			if path == root || entry.IsDir() {
				return nil
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxPluginUIFileBytes {
				return errors.New("plugin UI asset is unsafe")
			}
			relative, relErr := filepath.Rel(descriptor.InstallDir, path)
			if relErr != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.New("plugin UI asset escaped its root")
			}
			assets = append(assets, asset{path: filepath.ToSlash(relative), size: info.Size()})
			total += info.Size()
			if len(assets) > maxPluginUIFiles || total > maxPluginUITotalBytes {
				return errors.New("plugin UI assets exceed their aggregate limit")
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].path < assets[j].path })
	hash := sha256.New()
	for _, asset := range assets {
		_, _ = io.WriteString(hash, asset.path)
		_, _ = io.WriteString(hash, "\x00")
		file, err := os.Open(filepath.Join(descriptor.InstallDir, filepath.FromSlash(asset.path))) // #nosec G304 -- validated regular contained asset
		if err != nil {
			return "", errors.New("plugin UI asset changed during validation")
		}
		_, copyErr := io.CopyN(hash, file, asset.size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return "", errors.New("plugin UI asset changed during validation")
		}
		_, _ = io.WriteString(hash, "\x00")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
