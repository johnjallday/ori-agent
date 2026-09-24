//go:build !darwin

package folderdigest

import "os"

// isDataless is macOS-only; other platforms have no dataless file flag the
// scan needs to honour.
func isDataless(os.FileInfo) bool { return false }
