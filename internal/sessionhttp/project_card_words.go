package sessionhttp

import (
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// projectCardWords are the words the Home's pinned package declares for its
// project card, roster and facts line (PRD FR5). They come from the installed
// release the Home's provider pin names, read like the Home profile card: a
// disabled package still answers with the words the Home was set up under; a
// package that is not installed, a release without the section, or no Home
// at all gets the neutral words.
func (h *Handler) projectCardWords(home *workspace.Workspace) projectlibrary.Words {
	if home == nil {
		return projectlibrary.NeutralWords()
	}
	_, declaration, ok := h.homeProfilePinned(home)
	if !ok {
		return projectlibrary.NeutralWords()
	}
	return projectlibrary.WordsFromDeclaration(declaration.ProjectCard)
}
