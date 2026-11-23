//go:build linux || freebsd

package fileparser

import "golang.org/x/sys/unix"

// Impose a hard address-space limit on the throwaway parser process only.
// Go reserves address space at startup; mappings already made are unaffected.
func limitReviewPDF() bool {
	return unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: 256 << 20, Max: 256 << 20}) == nil && unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: 2, Max: 2}) == nil
}
