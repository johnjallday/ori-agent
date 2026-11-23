package onboarding

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Distinct from the Welcome state: choosing a landing never configures a workspace.
const (
	SearchFirst   = "first"
	SearchDesk    = "search"
	SearchLater   = "later"
	SearchLegacy  = "legacy"
	SearchUnknown = "unavailable"
	searchReceipt = "search_landing.v1"
)

var landingMu sync.Mutex

// CreateInstallRoot records new-install eligibility only for the process that
// creates the final directory. No directory enumeration or user folder lookup.
func CreateInstallRoot(root string) (bool, error) {
	// Preserve the read-only startup path for an already existing installation.
	if st, err := os.Stat(root); err == nil {
		if !st.IsDir() {
			return false, errors.New("installation root is not a directory")
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0750); err != nil {
		return false, err
	}
	err := os.Mkdir(root, 0750)
	if errors.Is(err, os.ErrExist) {
		info, e := os.Stat(root)
		if e != nil {
			return false, e
		}
		if !info.IsDir() {
			return false, errors.New("installation root is not a directory")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if runtime.GOOS == "darwin" && os.Getenv("WORKSPACE_DIR") == "" && os.Getenv("AGENT_STORE_PATH") == "" {
		if e := writeLanding(root, SearchFirst); e != nil {
			return true, e
		}
	}
	return true, nil
}
func SearchLanding(root string) string {
	landingMu.Lock()
	defer landingMu.Unlock()
	return readLanding(root)
}
func readLanding(root string) string {
	path := filepath.Join(root, searchReceipt)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return SearchLegacy
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32 {
		return SearchUnknown
	}
	// #nosec G304 G703 -- host-owned installation root and a fixed receipt filename; only a bounded enum is decoded.
	f, err := os.Open(path)
	if err != nil {
		return SearchUnknown
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return SearchUnknown
	}
	switch string(b) {
	case SearchFirst, SearchDesk, SearchLater:
		return string(b)
	}
	return SearchUnknown
}
func writeLanding(root, value string) error {
	f, err := os.CreateTemp(root, ".landing-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.WriteString(value)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(root, searchReceipt))
}

// Compare-and-set a displayed decision. A stale first-open tab cannot undo
// another tab's decision. Visiting the desk itself never calls this function.
func DecideSearchLanding(root, expected, next string) error {
	landingMu.Lock()
	defer landingMu.Unlock()
	current := readLanding(root)
	if current != expected || current == SearchUnknown || (next != SearchDesk && next != SearchLater) {
		return errors.New("landing decision changed")
	}
	return writeLanding(root, next)
}

// AcceptSearchFolder runs on the server after an actual accepted selection,
// even if the browser disconnects. An independently chosen Home wins over it.
func AcceptSearchFolder(root string) error {
	landingMu.Lock()
	defer landingMu.Unlock()
	if readLanding(root) != SearchFirst {
		return nil
	}
	return writeLanding(root, SearchDesk)
}

// Called only by pre-store recovery's Start Fresh identity category, not by
// routine Welcome replay. Recovery gates ordinary routes until verification.
func ResetSearchLanding(root string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	landingMu.Lock()
	defer landingMu.Unlock()
	return writeLanding(root, SearchFirst)
}
