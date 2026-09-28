package sessionfiles

import (
	"bytes"
	"context"
	"os"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// StageContinuityUploadBytes stages one exact reviewed upload under a caller-
// owned private Root, never into the live session-files Store. It returns false
// for a linked or missing entry: those are references, not bytes to install.
// The coordinator must validate session ownership and the complete component,
// hold its operation/reset lease, pin the private stage and later install the
// manifest and files together under the receipt. No normal attachment API
// reads this directory.
func StageContinuityUploadBytes(ctx context.Context, sourceDir string, inspected workspacecontinuity.Inspection, record workspacecontinuity.Record, stage *os.Root) (bool, error) {
	if stage == nil || inspected.Manifest.Validate() != nil {
		return false, workspacecontinuity.ErrInvalid
	}
	owner := inspected.Manifest.WorkspaceID
	var value ContinuityUpload
	found := false
	err := workspacecontinuity.ReadComponentRecords(ctx, sourceDir, inspected, "uploads", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "uploads" {
			return workspacecontinuity.ErrInvalid
		}
		for _, current := range chunk.Records {
			if current.ID != record.ID {
				continue
			}
			if found || !bytes.Equal(current.Data, record.Data) {
				return workspacecontinuity.ErrChanged
			}
			var decodeErr error
			value, decodeErr = DecodeContinuityUpload(current, owner)
			if decodeErr != nil {
				return decodeErr
			}
			found = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if !found {
		return false, workspacecontinuity.ErrIncomplete
	}
	if value.State != "copied" {
		return false, nil
	}
	// All components of this path were validated as opaque identities or one
	// simple, nontraversing manifest filename by DecodeContinuityUpload. The
	// source record's OriginalPath is intentionally never used here.
	target := "session_files/" + value.SessionID + "/files/" + value.Entry.Path
	if err := workspacecontinuity.StageInspectedBlobToRoot(ctx, sourceDir, inspected, "uploads", *value.Blob, stage, target); err != nil {
		return false, err
	}
	return true, nil
}
