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

// ownerFieldsPatch is the owner's fields patch as review and commit store it:
// plugin-field ids are checked against the Home's words and stored ids the
// words no longer declare are carried over (PRD #792 FR29, FR30). It reads the
// entry's current fields; a missing entry leaves the patch to the store's own
// conflict.
func (h *Handler) ownerFieldsPatch(scope projectlibrary.Scope, home *workspace.Workspace, entryID string,
	patch projectlibrary.FieldsPatch) (projectlibrary.FieldsPatch, projectlibrary.Words, error) {
	words := h.projectCardWords(home)
	if patch.Extra == nil {
		return patch, words, nil
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return projectlibrary.FieldsPatch{}, words, err
	}
	var stored projectlibrary.Fields
	for _, entry := range doc.Entries {
		if entry.ID == entryID {
			stored = entry.Fields
			break
		}
	}
	merged, err := projectlibrary.OwnerExtraPatch(patch, stored, words)
	return merged, words, err
}
