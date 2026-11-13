package knownfolder

import "golang.org/x/sys/windows"

func platformPath(_ string, id string) (string, error) {
	folder := map[string]*windows.KNOWNFOLDERID{
		"desktop":   windows.FOLDERID_Desktop,
		"documents": windows.FOLDERID_Documents,
		"downloads": windows.FOLDERID_Downloads,
	}[id]
	if folder == nil {
		return "", ErrUnavailable
	}
	// DEFAULT returns the user's current location, including OneDrive and
	// policy redirection. DEFAULT_PATH would incorrectly force the old home.
	return windows.KnownFolderPath(folder, windows.KF_FLAG_DEFAULT)
}
