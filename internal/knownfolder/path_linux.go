package knownfolder

import (
	"io"
	"os"
	"path/filepath"
)

func platformPath(home, id string) (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	file, err := os.Open(filepath.Join(config, "user-dirs.dirs")) // #nosec G304 G703 -- host XDG configuration path, never a client path
	if err != nil {
		if os.IsNotExist(err) {
			return englishPath(home, id)
		}
		return "", ErrUnavailable
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(content) > 64*1024 {
		return "", ErrUnavailable
	}
	return linuxUserFolder(home, id, string(content))
}
