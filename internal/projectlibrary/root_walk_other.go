//go:build !darwin && !linux && !freebsd

package projectlibrary

import "context"

type DirectoryRow struct {
	Name       string
	IsDir      bool
	IsLink     bool
	Size       int64
	Identity   string
	Unreadable bool
}

func directoryMatches(Root, string, string) bool { return false }

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
