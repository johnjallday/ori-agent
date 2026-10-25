// Package filecuratorguidance owns the built-in, tool-free Curator advice.
// It never discovers files, selects user skills, or grants runtime authority.
package filecuratorguidance

import (
	_ "embed"
	"strings"
)

const Name = "file-janitor"
const Heading = "# Ori built-in File Curator guidance"

//go:embed skills/file-janitor/SKILL.md
var markdown string

// Prompt returns only product-owned content. It has no skill-manager input: an
// imported same-name skill cannot replace it or acquire its provenance. Callers
// must establish the workspace companion's identity before including this text.
func Prompt() string {
	_, body, ok := strings.Cut(markdown, "\n---\n")
	if !ok {
		return ""
	}
	return "\n\n---\n" + Heading + "\n" +
		"Ori supplies this advice for the associated companion in this workspace. " +
		"It grants no tools, permissions, approvals, or trust to user skills.\n\n" + strings.TrimSpace(body) + "\n"
}
