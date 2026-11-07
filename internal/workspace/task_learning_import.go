package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// StripLearningImportAuthority removes installation-local correlations and
// single-use execution approvals, not the restrictive recipe or ordinary work.
// Use at both modern projection and legacy import boundaries, never on normal
// native reopen (which is not an import or fresh grant).
func StripLearningImportAuthority(ws *Workspace) bool {
	changed := false
	if ws == nil {
		return false
	}
	for n := range ws.Tasks {
		for _, key := range []string{TaskContextLearning, TaskContextReviewedExecutor, TaskContextLearningStart, TaskContextLearningObservation} {
			if _, exists := ws.Tasks[n].Context[key]; exists {
				delete(ws.Tasks[n].Context, key)
				changed = true
			}
		}
	}
	return changed
}

// The destination is a folder explicitly selected for legacy import. Sanitize
// its whole tree before registration; the source copy outside it is untouched.
func stripImportedLearningTree(root string) error {
	path := filepath.Join(root, WorkspaceConfigFile)
	data, err := os.ReadFile(path) // #nosec G304 G703 -- fixed filename in the explicitly selected destination import tree
	if err != nil {
		return fmt.Errorf("read imported learning metadata: %w", err)
	}
	ws, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("decode imported learning metadata: %w", err)
	}
	if StripLearningImportAuthority(ws) {
		projected, err := ws.ToJSON()
		if err != nil {
			return err
		}
		if err := atomicWriteFile(path, projected); err != nil {
			return err
		}
	}
	children, err := os.ReadDir(filepath.Join(root, SubWorkspacesDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.IsDir() {
			if err := stripImportedLearningTree(filepath.Join(root, SubWorkspacesDir, child.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
