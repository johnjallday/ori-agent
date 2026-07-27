//go:build !unix

package downloadsjanitor

// openNoFollow has no equivalent on this platform. The fingerprint check
// immediately before the read is the protection that remains: a swapped file
// fails it, because a symlink is not a regular file and its identity differs.
const openNoFollow = 0
