package foldermap

import (
	"strings"
)

// ProjectSection splits a saved project folder into its top-level ancestor
// and the intervening folders (not the project leaf). It describes ancestry,
// never a scan target, workspace or user-managed collection.
func ProjectSection(relative string) (section, breadcrumb string) {
	if clean, err := CleanRel(relative); err != nil || clean != relative || clean == "" {
		return "", ""
	}
	first, below, nested := strings.Cut(relative, "/")
	if !nested {
		return "", ""
	}
	if last := strings.LastIndexByte(below, '/'); last >= 0 {
		return first, below[:last]
	}
	return first, ""
}
