//go:build !unix

package samplelibrary

import "os"

func openWriteNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
} // #nosec G304 -- path is component-revalidated beneath a reviewed child root.

func openReadNoFollow(path string) (*os.File, error) { return os.Open(path) } // #nosec G304 -- path is component-revalidated beneath an exact reviewed root.
