//go:build !unix

package downloadsjanitor

import "os"

// platformFileID returns no file identity on platforms that do not expose one
// through os.FileInfo. Fingerprint matching then relies on name, size, and
// modification time — weaker, but never wrong in the unsafe direction: a
// mismatch still marks the candidate stale.
func platformFileID(os.FileInfo) string { return "" }
