package workspace

import (
	"context"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// StageContinuityOwnedFiles streams manifest-declared files/ payloads into an
// inert private staging directory without following source links or replacing
// an occupied destination. Neither these bytes nor this folder are registered
// by a production importer. A coordinator must keep its reset/operation lease,
// validate source and destination ownership, and install all domains together.
func StageContinuityOwnedFiles(ctx context.Context, sourceDir, stagingDir string, inspected workspacecontinuity.Inspection) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stage, stageIdentity, err := openPrivateContinuityStage(sourceDir, stagingDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stage.Close() }()
	verify := func() error {
		fresh, err := workspacecontinuity.Inspect(ctx, sourceDir)
		if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
			return workspacecontinuity.ErrChanged
		}
		return CheckContinuityFilesCoverage(ctx, sourceDir, fresh.Manifest.Files)
	}
	if err := verify(); err != nil {
		return 0, err
	}
	count := 0
	for _, file := range inspected.Manifest.Files {
		if !strings.HasPrefix(file.Path, FilesDir+"/") {
			continue
		}
		if err := stageIdentity(); err != nil {
			return count, err
		}
		if err := workspacecontinuity.StageVerifiedCanonicalFile(ctx, sourceDir, stage, file); err != nil {
			return count, err
		}
		count++
	}
	if err := verify(); err != nil {
		return count, err
	}
	if err := stageIdentity(); err != nil {
		return count, err
	}
	return count, nil
}
