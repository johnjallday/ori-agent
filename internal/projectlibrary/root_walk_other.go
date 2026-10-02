//go:build !darwin && !linux && !freebsd

package projectlibrary

import (
	"context"
	"os"
	"time"
)

type DirectoryRow struct {
	Name       string
	IsDir      bool
	IsLink     bool
	Size       int64
	Identity   string
	ModifiedAt time.Time
	Unreadable bool
}

func directoryMatches(Root, string, string) bool { return false }

// Without a descriptor-anchored no-follow open, the fact pass opens nothing.
func openPinnedProjectFile(Root, string, string, FactsSource) (*os.File, error) {
	return nil, ErrUnavailable
}

func pinnedFileStillMatches(*os.File, FactsSource) bool { return false }

func readPinnedDirectory(context.Context, Root, string, string, int) ([]DirectoryRow, bool, error) {
	return nil, false, ErrUnavailable
}

// Fail closed on platforms without a descriptor-anchored no-follow walker.
func (r *Roots) ReadDirectory(context.Context, Scope, string, string, int) ([]DirectoryRow, bool, error) {
	return nil, false, ErrUnavailable
}

func (r *Roots) readDirectoryWithIdentity(context.Context, Scope, string, string, string, int) ([]DirectoryRow, bool, error) {
	return nil, false, ErrUnavailable
}
