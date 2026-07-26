//go:build unix

package downloadsjanitor

import "syscall"

// openNoFollow refuses to open a symlink, so a link swapped in between the
// fingerprint check and the read cannot redirect it to another file.
const openNoFollow = syscall.O_NOFOLLOW
