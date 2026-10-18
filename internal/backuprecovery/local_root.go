package backuprecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Only the composition-owned application data root is inspected here, never a
// selected source/repository. Its canonical location and native file identity
// bind a receipt to this installation across ordinary process restarts.
type localRoot struct {
	path        string
	fingerprint string
	info        os.FileInfo
}

func newLocalRoot(value string) (localRoot, error) {
	if value == "" {
		return localRoot{}, errLocalState
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return localRoot{}, errLocalState
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Dir(canonical) == canonical {
		return localRoot{}, errLocalState
	}
	info, err := os.Lstat(canonical) // #nosec G703 -- composition-owned application root, not a request path; identity is bound below
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return localRoot{}, errLocalState
	}
	device, inode, ok := localRootFileIdentity(info)
	if !ok {
		return localRoot{}, errLocalState
	}
	encoded, err := json.Marshal([]any{canonical, device, inode})
	if err != nil {
		return localRoot{}, errLocalState
	}
	digest := sha256.Sum256(encoded)
	return localRoot{canonical, hex.EncodeToString(digest[:]), info}, nil
}

func (r localRoot) check() bool {
	current, err := newLocalRoot(r.path)
	return err == nil && current.fingerprint == r.fingerprint && os.SameFile(r.info, current.info)
}
