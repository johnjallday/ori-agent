package workspace

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif" // Validate only formats accepted by the appearance upload owner.
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
	_ "golang.org/x/image/webp"
)

const maxContinuityAppearanceBytes = 5 << 20 // Matches the canonical upload owner.

// ContinuityAppearance preserves inactive uploaded state as well as the active
// portrait. Filename is a name, never a path/URL or authority to search a roster.
// A missing image is explicit evidence, not an empty/changed appearance choice.
type ContinuityAppearance struct {
	Version     int                          `json:"version"`
	WorkspaceID string                       `json:"workspace_id"`
	ProfileName string                       `json:"profile_name"`
	Filename    string                       `json:"filename"`
	State       string                       `json:"state"` // present or missing
	Blob        *workspacecontinuity.BlobRef `json:"blob"`
}

func profileAppearancePath(name, filename string) (string, error) {
	_, slug, err := localAgentPath(name)
	if err != nil || !validPortableImageName(filename) {
		return "", workspacecontinuity.ErrUnsafe
	}
	return path.Join(WorkspaceAgentsDir, slug, "appearance", filename), nil
}

func validateContinuityImage(filename string, data []byte) error {
	if !validPortableImageName(filename) {
		return workspacecontinuity.ErrUnsafe
	}
	if len(data) == 0 || len(data) > maxContinuityAppearanceBytes {
		return workspacecontinuity.ErrLimit
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return workspacecontinuity.ErrInvalid
	}
	// Pixel limits prevent a tiny compressed file becoming a decompression bomb.
	if config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32<<20 {
		return workspacecontinuity.ErrLimit
	}
	ext := strings.ToLower(path.Ext(filename))
	if format == "jpeg" && (ext == ".jpg" || ext == ".jpeg") ||
		format == "png" && ext == ".png" || format == "gif" && ext == ".gif" || format == "webp" && ext == ".webp" {
		return nil
	}
	return workspacecontinuity.ErrInvalid
}

// SnapshotContinuityAppearance reads only the scoped owned file. Source-time
// copying from a global agent's owned image must first be done by the canonical
// snapshot owner; this collector never substitutes a same-name global image.
func SnapshotContinuityAppearance(ctx context.Context, directory string, profile ContinuityProfile) (*workspacecontinuity.Record, []byte, error) {
	if profile.Version != 1 || !workspacecontinuity.ValidID(profile.WorkspaceID) || !workspacecontinuity.ValidID(profile.Name) {
		return nil, nil, workspacecontinuity.ErrInvalid
	}
	if err := validatePortableAppearance(profile.Appearance); err != nil {
		return nil, nil, err
	}
	filename := profile.Appearance.UploadedImage()
	if filename == "" {
		return nil, nil, nil
	}
	location, err := profileAppearancePath(profile.Name, filename)
	if err != nil {
		return nil, nil, err
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, location, maxContinuityAppearanceBytes)
	value := ContinuityAppearance{Version: 1, WorkspaceID: profile.WorkspaceID, ProfileName: profile.Name, Filename: filename, State: "present"}
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		value.State, data = "missing", nil
	} else if err != nil {
		return nil, nil, err
	} else {
		if err := validateContinuityImage(filename, data); err != nil {
			return nil, nil, err
		}
		value.Blob = &workspacecontinuity.BlobRef{Digest: workspacecontinuity.Digest(data), Bytes: int64(len(data))}
	}
	_, slug, err := localAgentPath(profile.Name)
	if err != nil {
		return nil, nil, err
	}
	record, err := workspacecontinuity.EncodeRecord(scopedProfileRecordID(profile.WorkspaceID, slug), value)
	return &record, data, err
}

func DecodeContinuityAppearance(record workspacecontinuity.Record, profile ContinuityProfile, data []byte) (ContinuityAppearance, error) {
	var value ContinuityAppearance
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	if err := validatePortableAppearance(profile.Appearance); err != nil {
		return value, err
	}
	_, slug, err := localAgentPath(profile.Name)
	if err != nil || record.ID != scopedProfileRecordID(profile.WorkspaceID, slug) || value.WorkspaceID != profile.WorkspaceID || value.ProfileName != profile.Name ||
		value.Filename != profile.Appearance.UploadedImage() || !validPortableImageName(value.Filename) {
		return value, workspacecontinuity.ErrInvalid
	}
	if value.State == "missing" {
		if value.Blob != nil || len(data) != 0 {
			return value, workspacecontinuity.ErrInvalid
		}
		return value, nil
	}
	if value.State != "present" || value.Blob == nil || value.Blob.Bytes != int64(len(data)) || value.Blob.Digest != workspacecontinuity.Digest(data) {
		return value, workspacecontinuity.ErrDigest
	}
	return value, validateContinuityImage(value.Filename, data)
}

// MaterializeContinuityAppearance writes only into the caller's private reviewed
// staging folder. It is not registration, a global upload or runtime activation.
// A source filename collision in another workspace cannot affect these bytes.
// The coordinator owns durable filesystem receipts and final tree publication.
func MaterializeContinuityAppearance(ctx context.Context, staging string, record workspacecontinuity.Record, profile ContinuityProfile, data []byte) error {
	value, err := DecodeContinuityAppearance(record, profile, data)
	if err != nil || value.State == "missing" {
		return err
	}
	location, err := profileAppearancePath(value.ProfileName, value.Filename)
	if err != nil {
		return err
	}
	// Exclusive creation; even an identical existing file is not automatically
	// claimed. Receipt-owned retries are a coordinator responsibility.
	return workspacecontinuity.ReplaceCanonicalFile(ctx, staging, location, "", data)
}
