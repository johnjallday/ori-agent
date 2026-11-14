//go:build !windows && !linux

package knownfolder

// macOS stores these names in English regardless of its display language.
func platformPath(home, id string) (string, error) {
	return englishPath(home, id)
}
