package workspace

import (
	"context"
	"errors"
	"os"
	"path"
	"sort"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// CollectContinuityProfiles captures canonical workspace-local definitions and
// their owned appearance bytes; it never falls back to a same-named global
// profile. The caller supplies an authoritative workspace snapshot, a private
// spool and the same SQL/file capture fence as the other domain collectors.
// Returned fingerprints must be checked again immediately before publication.
func CollectContinuityProfiles(ctx context.Context, folder string, ws *Workspace, spool *workspacecontinuity.Spool) ([]workspacecontinuity.Fingerprint, error) {
	if ws == nil || spool == nil || !workspacecontinuity.ValidID(ws.ID) {
		return nil, workspacecontinuity.ErrInvalid
	}
	expected := map[string]string{}
	for _, instance := range ws.AgentInstances {
		_, slug, err := localAgentPath(instance.Name)
		if err != nil || expected[slug] != "" && expected[slug] != instance.Name {
			return nil, workspacecontinuity.ErrInvalid
		}
		expected[slug] = instance.Name
	}
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, WorkspaceAgentsDir, workspacecontinuity.MaxFiles)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) && len(expected) == 0 {
		if err := spool.SetAvailability(ctx, "agents", workspacecontinuity.Empty, ""); err != nil {
			return nil, err
		}
		return []workspacecontinuity.Fingerprint{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 && len(expected) > 0 {
		return nil, workspacecontinuity.ErrIncomplete
	}
	fingerprints := make([]workspacecontinuity.Fingerprint, 0, len(entries)*2)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, workspacecontinuity.ErrUnsafe
		}
		name := entry.Name()
		if declared, ok := expected[name]; ok {
			name = declared
			delete(expected, entry.Name())
		}
		configPath, slug, err := localAgentPath(name)
		if err != nil || slug != entry.Name() {
			return nil, workspacecontinuity.ErrInvalid
		}
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, configPath, workspacecontinuity.MaxRecordBytes)
		if err != nil {
			return nil, err
		}
		profile, err := decodeLocalAgent(data)
		if err != nil {
			return nil, err
		}
		// An agent-specific key in the native file is local: the typed record
		// excludes it and an import installs only the denied definition.
		record, err := SnapshotContinuityProfile(ws, name, profile)
		if err != nil {
			return nil, err
		}
		if err := checkContinuityProfileFiles(ctx, folder, slug, profile.Appearance.UploadedImage()); err != nil {
			return nil, err
		}
		if err := spool.AddRecord(ctx, "agents", "profiles", record); err != nil {
			return nil, err
		}
		fingerprints = append(fingerprints, workspacecontinuity.Fingerprint{Path: configPath, Digest: workspacecontinuity.Digest(data), Bytes: int64(len(data))})
		var value ContinuityProfile
		if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
			return nil, err
		}
		image, blob, err := SnapshotContinuityAppearance(ctx, folder, value)
		if err != nil {
			return nil, err
		}
		if image == nil {
			continue
		}
		if len(blob) > 0 {
			ref, err := spool.AddBlob(ctx, "agents", blob)
			if err != nil {
				return nil, err
			}
			location, err := profileAppearancePath(name, value.Appearance.UploadedImage())
			if err != nil {
				return nil, err
			}
			fingerprints = append(fingerprints, workspacecontinuity.Fingerprint{Path: location, Digest: ref.Digest, Bytes: ref.Bytes})
		}
		if err := spool.AddRecord(ctx, "agents", "appearances", *image); err != nil {
			return nil, err
		}
	}
	if len(expected) != 0 {
		return nil, workspacecontinuity.ErrIncomplete
	}
	if len(entries) == 0 {
		if err := spool.SetAvailability(ctx, "agents", workspacecontinuity.Empty, ""); err != nil {
			return nil, err
		}
	}
	sort.Slice(fingerprints, func(i, j int) bool { return fingerprints[i].Path < fingerprints[j].Path })
	for i := 1; i < len(fingerprints); i++ {
		if fingerprints[i-1].Path == fingerprints[i].Path {
			return nil, workspacecontinuity.ErrInvalid
		}
	}
	return fingerprints, nil
}

// An unreferenced extra profile file or appearance would travel in the user's
// directory copy without a corresponding typed owner. Refuse publication rather
// than advertise Ready while silently dropping that private content.
func checkContinuityProfileFiles(ctx context.Context, folder, slug, image string) error {
	base := path.Join(WorkspaceAgentsDir, slug)
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, base, workspacecontinuity.MaxFiles)
	if err != nil {
		return err
	}
	config, appearance := false, false
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		switch entry.Name() {
		case WorkspaceAgentConfigFile:
			if entry.IsDir() {
				return workspacecontinuity.ErrUnsafe
			}
			config = true
		case "appearance":
			if !entry.IsDir() {
				return workspacecontinuity.ErrUnsafe
			}
			appearance = true
		default:
			return workspacecontinuity.ErrIncomplete
		}
	}
	if !config {
		return workspacecontinuity.ErrIncomplete
	}
	if !appearance {
		return nil
	} // an uploaded choice may be explicitly missing
	images, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, path.Join(base, "appearance"), workspacecontinuity.MaxFiles)
	if err != nil {
		return err
	}
	for _, entry := range images {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		if entry.Name() != image {
			return workspacecontinuity.ErrIncomplete
		}
	}
	return nil
}
