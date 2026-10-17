package backuprecovery

import "os"

// A nativeDirectory is metadata-only and private to the bounded helper. It does
// not establish physical storage identity or approve a read. O_SEARCH provides
// directory traversal without a read-data open. The helper's kernel sandbox
// must independently deny source data reads, including readdir/openat content.
// O_EVTONLY is deliberately not used: it allowed enumeration in our probe.
type nativeDirectory struct {
	path     string
	file     *os.File
	identity anchoredIdentity
	mount    nativeMountEvidence
	capacity *nativeCapacity
}

type nativeCapacity struct{ total, available uint64 }

// unchanged re-collects native mount evidence from the exact selected name.
// It must run in the same bounded helper. It is not a substitute for re-querying
// the volume/store mapping before publication; capacity may legitimately vary.
func (directory *nativeDirectory) unchanged() bool {
	if directory == nil || !directory.sameSelectedDirectory() {
		return false
	}
	current, err := readNativeDirectory(directory.path)
	if err != nil {
		return false
	}
	defer current.close()
	return current.identity == directory.identity && current.mount == directory.mount
}

func (directory *nativeDirectory) close() {
	if directory != nil && directory.file != nil {
		_ = directory.file.Close()
	}
}
