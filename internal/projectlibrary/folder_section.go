package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// FolderSection is owner-only display metadata. Its ID is not a scan scope,
// collection or workspace ID and no mutation accepts it.
type FolderSection struct {
	ID         string `json:"id"`
	RootLabel  string `json:"root_label"`
	Label      string `json:"label"`
	Breadcrumb string `json:"breadcrumb,omitempty"`
}

func uniqueFolderSource(entry Entry, roots map[string]Root, inactive map[string]bool, inRoot string) *Observation {
	var source *Observation
	for i := range entry.Observations {
		o := &entry.Observations[i]
		root, ok := roots[o.RootID]
		if !ok || (inRoot != "" && inRoot != o.RootID) || inactive[o.RootID] || root.RevokedAt != nil || (o.Availability != "available" && o.Availability != "ambiguous") {
			continue
		}
		if source != nil {
			return nil
		}
		source = o
	}
	return source
}

func observedSection(entry Entry, roots map[string]Root, inactive map[string]bool, inRoot string) *FolderSection {
	return sourceSection(uniqueFolderSource(entry, roots, inactive, inRoot), roots)
}

func sourceSection(source *Observation, roots map[string]Root) *FolderSection {
	if source == nil {
		return nil
	}
	label, crumb := foldermap.ProjectSection(filepath.ToSlash(source.RelativeFolder))
	if label == "" {
		return nil
	}
	id := sha256.Sum256([]byte(source.RootID + "\x00" + label))
	return &FolderSection{ID: hex.EncodeToString(id[:]), RootLabel: folderRootLabel(roots[source.RootID]), Label: label, Breadcrumb: strings.ReplaceAll(crumb, "/", " / ")}
}

func folderRootLabel(root Root) string {
	rootName := filepath.Base(root.Path)
	if !validText(rootName, 255) || rootName == "." || rootName == string(filepath.Separator) {
		rootName = "Folder"
	}
	return rootName
}
