package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// VerifyContinuityAppearanceEvidence checks a present/missing scoped uploaded
// image against the exact reviewed profile, fingerprint and immutable blob.
// It returns no private bytes, performs no write and grants no runtime access.
func VerifyContinuityAppearanceEvidence(ctx context.Context, sourceDir string, inspected workspacecontinuity.Inspection, profile ContinuityProfile) (string, error) {
	status, _, _, err := readContinuityAppearanceEvidence(ctx, sourceDir, inspected, profile)
	return status, err
}

// StageContinuityAppearance validates the same read-only evidence, then writes
// only verified owned bytes into a private stage. Missing remains explicit. It
// does not register an agent or activate the saved portrait.
func StageContinuityAppearance(ctx context.Context, sourceDir, stagingDir string, inspected workspacecontinuity.Inspection, profile ContinuityProfile) (string, error) {
	stage, stageIdentity, err := openPrivateContinuityStage(sourceDir, stagingDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = stage.Close() }()
	status, record, data, err := readContinuityAppearanceEvidence(ctx, sourceDir, inspected, profile)
	if err != nil || status != "present" {
		return status, err
	}
	if err := stageIdentity(); err != nil {
		return "", err
	}
	value, err := DecodeContinuityAppearance(record, profile, data)
	if err != nil || value.State != "present" {
		return "", workspacecontinuity.ErrInvalid
	}
	location, err := profileAppearancePath(value.ProfileName, value.Filename)
	if err != nil {
		return "", err
	}
	if err := workspacecontinuity.ReplaceCanonicalFileInRoot(ctx, stage, location, "", data); err != nil {
		return "", err
	}
	fresh, err := workspacecontinuity.Inspect(ctx, sourceDir)
	if err != nil || fresh.Pointer != inspected.Pointer || fresh.Manifest.SourceFingerprint != inspected.Manifest.SourceFingerprint {
		return "", workspacecontinuity.ErrChanged
	}
	return status, stageIdentity()
}

func readContinuityAppearanceEvidence(ctx context.Context, sourceDir string, inspected workspacecontinuity.Inspection, profile ContinuityProfile) (string, workspacecontinuity.Record, []byte, error) {
	var empty workspacecontinuity.Record
	if profile.Version != 1 || profile.WorkspaceID != inspected.Manifest.WorkspaceID || !workspacecontinuity.ValidID(profile.Name) || validatePortableAppearance(profile.Appearance) != nil {
		return "", empty, nil, workspacecontinuity.ErrInvalid
	}
	exact, _, err := readVerifiedContinuityProfile(ctx, sourceDir, inspected, profile.Name)
	if err != nil {
		return "", empty, nil, err
	}
	if !reflect.DeepEqual(exact, profile) {
		return "", empty, nil, workspacecontinuity.ErrConflict
	}
	filename := profile.Appearance.UploadedImage()
	if filename == "" {
		return "no_upload", empty, nil, nil
	}
	location, err := profileAppearancePath(profile.Name, filename)
	if err != nil {
		return "", empty, nil, err
	}
	var appearance workspacecontinuity.Record
	found := false
	err = workspacecontinuity.ReadComponentRecords(ctx, sourceDir, inspected, "agents", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "appearances" {
			return nil
		}
		for _, record := range chunk.Records {
			if record.ID != scopedProfileRecordID(profile.WorkspaceID, Slugify(profile.Name)) {
				continue
			}
			if found {
				return workspacecontinuity.ErrInvalid
			}
			appearance, found = record, true
		}
		return nil
	})
	if err != nil {
		return "", empty, nil, err
	}
	if !found {
		return "", empty, nil, workspacecontinuity.ErrIncomplete
	}
	var value ContinuityAppearance
	if err := workspacecontinuity.DecodeRecord(appearance, &value); err != nil {
		return "", empty, nil, err
	}
	if value.State == "missing" {
		if file, err := workspacecontinuity.OpenCanonicalFile(sourceDir, location); err == nil {
			_ = file.Close()
			return "", empty, nil, workspacecontinuity.ErrChanged
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", empty, nil, err
		}
		if _, err := DecodeContinuityAppearance(appearance, profile, nil); err != nil {
			return "", empty, nil, err
		}
		return "missing", appearance, nil, nil
	}
	if value.Blob == nil {
		return "", empty, nil, workspacecontinuity.ErrIncomplete
	}
	declared, fingerprint := false, false
	for _, component := range inspected.Manifest.Components {
		if component.Domain != "agents" {
			continue
		}
		for _, blob := range component.Blobs {
			if blob == *value.Blob {
				declared = true
			}
		}
	}
	for _, file := range inspected.Manifest.Files {
		if file.Path == location && file.Digest == value.Blob.Digest && file.Bytes == value.Blob.Bytes {
			fingerprint = true
		}
	}
	if !declared || !fingerprint || value.Blob.Bytes > maxContinuityAppearanceBytes {
		return "", empty, nil, workspacecontinuity.ErrIncomplete
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, location, maxContinuityAppearanceBytes)
	if err != nil {
		return "", empty, nil, err
	}
	object, err := workspacecontinuity.ReadInspectedBlob(ctx, sourceDir, inspected, "agents", *value.Blob, maxContinuityAppearanceBytes)
	if err != nil {
		return "", empty, nil, err
	}
	if !bytes.Equal(data, object) {
		return "", empty, nil, workspacecontinuity.ErrChanged
	}
	if _, err := DecodeContinuityAppearance(appearance, profile, data); err != nil {
		return "", empty, nil, err
	}
	return "present", appearance, data, nil
}
