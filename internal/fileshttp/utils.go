package fileshttp

import (
	"io"
	"os"
)

// openFile opens a file for reading
func openFile(path string) (io.ReadCloser, error) {
	return os.Open(path)
}
