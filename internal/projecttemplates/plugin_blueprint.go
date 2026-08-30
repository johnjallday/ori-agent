package projecttemplates

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxPluginBlueprintManifestBytes = 256 << 10
	maxPluginBlueprintFiles         = 512
	maxPluginBlueprintFileBytes     = 8 << 20
	maxPluginBlueprintTotalBytes    = 64 << 20
)

// LoadPluginBlueprint strictly validates one trusted installed plugin
// blueprint. The caller supplies canonical contained paths and an injected
// catalog that knows exactly which capabilities/providers this contribution
// may reference.
func LoadPluginBlueprint(manifestPath, skeletonRoot string, catalog RuntimeCatalog) (Template, string, error) {
	manifestInfo, err := os.Lstat(manifestPath) // #nosec G304 -- caller canonicalizes this trusted plugin-relative path
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return Template{}, "", errors.New("plugin blueprint manifest is not a regular contained file")
	}
	if manifestInfo.Size() < 2 || manifestInfo.Size() > maxPluginBlueprintManifestBytes {
		return Template{}, "", errors.New("plugin blueprint manifest exceeds its size limit")
	}
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- exact non-symlink manifest selected above
	if err != nil {
		return Template{}, "", errors.New("plugin blueprint manifest could not be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var declaration manifest
	if err := decoder.Decode(&declaration); err != nil {
		return Template{}, "", fmt.Errorf("plugin blueprint manifest is invalid: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Template{}, "", errors.New("plugin blueprint manifest has trailing data")
	}
	if declaration.Builtin || declaration.BuiltinVersion != 0 {
		return Template{}, "", errors.New("plugin blueprint cannot claim built-in ownership")
	}
	if len(bytes.TrimSpace(declaration.Onboarding)) > 0 && !bytes.Equal(bytes.TrimSpace(declaration.Onboarding), []byte("null")) {
		return Template{}, "", errors.New("plugin blueprint cannot declare legacy onboarding behavior")
	}

	digest, files, total, err := validatePluginSkeleton(skeletonRoot)
	if err != nil {
		return Template{}, "", err
	}
	template := newTemplateWithManifest(skeletonRoot, declaration, catalog)
	template.Builtin = false
	template.BuiltinVersion = 0
	template.HasSkeleton = files > 0
	if template.RuntimeRequirementsError != "" || template.SetupWizardError != "" || template.AssistantProgramError != "" {
		return Template{}, "", errors.New("plugin blueprint setup/runtime/assistant-program references are unavailable")
	}
	for _, warning := range template.Warnings {
		if strings.Contains(warning, "declares capability") || strings.Contains(warning, "project_entry is ignored") {
			return Template{}, "", errors.New("plugin blueprint contains an unavailable reference")
		}
	}
	if strings.TrimSpace(template.Name) == "" || len(template.Name) > 120 || len(template.Description) > 1000 {
		return Template{}, "", errors.New("plugin blueprint display text is outside its limits")
	}
	if len(template.Tags) > 32 || len(template.StarterTasks) > 64 || len(template.Agents) > 16 {
		return Template{}, "", errors.New("plugin blueprint metadata exceeds its item limit")
	}
	for _, task := range template.StarterTasks {
		if len(task.Description) > 500 || len(task.Details) > 4000 {
			return Template{}, "", errors.New("plugin blueprint starter task text exceeds its limit")
		}
	}
	_ = total // retained in validation for a clear aggregate limit
	return template, digest, nil
}

func validatePluginSkeleton(root string) (string, int, int64, error) {
	rootInfo, err := os.Lstat(root) // #nosec G304 -- caller canonicalizes this trusted plugin-relative path
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", 0, 0, errors.New("plugin blueprint skeleton is not a contained directory")
	}
	type entry struct {
		path string
		size int64
	}
	var entries []entry
	var total int64
	err = filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("plugin blueprint skeleton could not be read")
		}
		if path == root {
			return nil
		}
		info, infoErr := item.Info()
		if infoErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("plugin blueprint skeleton contains a symlink or unreadable entry")
		}
		if item.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("plugin blueprint skeleton contains a special file")
		}
		if info.Size() > maxPluginBlueprintFileBytes {
			return errors.New("plugin blueprint skeleton file exceeds its size limit")
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("plugin blueprint skeleton path escaped its root")
		}
		entries = append(entries, entry{path: filepath.ToSlash(relative), size: info.Size()})
		total += info.Size()
		if len(entries) > maxPluginBlueprintFiles || total > maxPluginBlueprintTotalBytes {
			return errors.New("plugin blueprint skeleton exceeds its aggregate limit")
		}
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	for _, item := range entries {
		_, _ = io.WriteString(hash, item.path)
		_, _ = io.WriteString(hash, "\x00")
		file, openErr := os.Open(filepath.Join(root, filepath.FromSlash(item.path))) // #nosec G304 -- validated relative regular file under root
		if openErr != nil {
			return "", 0, 0, errors.New("plugin blueprint skeleton changed during validation")
		}
		_, copyErr := io.CopyN(hash, file, item.size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return "", 0, 0, errors.New("plugin blueprint skeleton changed during validation")
		}
		_, _ = io.WriteString(hash, "\x00")
	}
	return hex.EncodeToString(hash.Sum(nil)), len(entries), total, nil
}
