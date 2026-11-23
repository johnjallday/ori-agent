//go:build !unix

package directoryid

import "os"

// Never substitute a pathname or timestamp for unavailable physical identity.
func Witness(os.FileInfo) string { return "" }
