package knownfolder

import "path/filepath"

// macOS stores these names in English regardless of its display language.
func platformPath(home, id string) (string, error) {
	return englishPath(home, id)
}

func englishPath(home, id string) (string, error) {
	name := map[string]string{"desktop": "Desktop", "documents": "Documents", "downloads": "Downloads"}[id]
	if name == "" {
		return "", ErrUnavailable
	}
	return filepath.Join(home, name), nil
}
