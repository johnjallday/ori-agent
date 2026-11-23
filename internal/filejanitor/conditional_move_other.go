//go:build !darwin && !linux

package filejanitor

import "context"

// On other platforms there is no verified primitive that refuses to replace a
// name, and no file identity to compare (platformFileID is empty on Windows). File
// and Undo of reviewed destinations are unavailable; there is no fallback.
type unsupportedConditionalMover struct{}

func newPlatformConditionalMover() ConditionalMover { return unsupportedConditionalMover{} }

func (unsupportedConditionalMover) Supported(string, string) error { return ErrMoveUnsupported }

func (unsupportedConditionalMover) Folder(string, string, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}

func (unsupportedConditionalMover) MakeFolder(context.Context, string, string, DirRef, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}

func (unsupportedConditionalMover) MoveIfUnchanged(context.Context, ConditionalMove) (Fingerprint, error) {
	return Fingerprint{}, ErrMoveUnsupported
}
