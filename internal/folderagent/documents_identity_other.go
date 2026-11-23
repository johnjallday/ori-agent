//go:build !unix

package folderagent

import "os"

// Never substitute a pathname or timestamp for unavailable physical identity.
func directoryWitness(os.FileInfo) string { return "" }
