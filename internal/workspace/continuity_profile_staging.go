package workspace

import (
	"context"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// StageContinuityProfile writes one exact verified scoped profile's denied
// definition into an owned private staging directory. It does not install an
// agent, decrypt a source slot, copy an uploaded image, or register a roster
// entry. The coordinator owns the folder and must validate all profiles and
// assets, hold reset ownership, and recheck the whole tree before publication.
func StageContinuityProfile(ctx context.Context, sourceDir, stagingDir string, inspected workspacecontinuity.Inspection, name string) (ContinuityProfile, error) {
	var value ContinuityProfile
	if !workspacecontinuity.ValidID(name) || inspected.Manifest.WorkspaceID == "" {
		return value, workspacecontinuity.ErrInvalid
	}
	stage, stageIdentity, err := openPrivateContinuityStage(sourceDir, stagingDir)
	if err != nil {
		return value, err
	}
	defer func() { _ = stage.Close() }()
	value, profile, err := readVerifiedContinuityProfile(ctx, sourceDir, inspected, name)
	if err != nil {
		return value, err
	}
	filePath, _, err := localAgentPath(name)
	if err != nil {
		return value, err
	}
	data, err := json.Marshal(profile)
	if err != nil || len(data) > workspacecontinuity.MaxRecordBytes {
		return value, workspacecontinuity.ErrLimit
	}
	if err := stageIdentity(); err != nil {
		return value, err
	}
	// The name is verified by the profile's scoped record and matching owner
	// file; an unrelated existing stage definition is never adopted by retry.
	if err := workspacecontinuity.ReplaceCanonicalFileInRoot(ctx, stage, filePath, "", data); err != nil {
		return value, err
	}
	fresh, err := workspacecontinuity.Inspect(ctx, sourceDir)
	if err != nil || fresh.Pointer != inspected.Pointer || fresh.Manifest.SourceFingerprint != inspected.Manifest.SourceFingerprint {
		return value, workspacecontinuity.ErrChanged
	}
	return value, stageIdentity()
}

// The typed profile, canonical workspace definition and exact fingerprint are
// all independently checked from the inspected generation before any use.
func readVerifiedContinuityProfile(ctx context.Context, sourceDir string, inspected workspacecontinuity.Inspection, name string) (ContinuityProfile, *agent.Agent, error) {
	var value ContinuityProfile
	if !workspacecontinuity.ValidID(name) {
		return value, nil, workspacecontinuity.ErrInvalid
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return value, nil, err
	}
	var ws *Workspace
	err = workspacecontinuity.ReadComponentRecords(ctx, sourceDir, inspected, "workspace", func(chunk workspacecontinuity.Chunk) error {
		if ws != nil || chunk.Family != "workspaces" || len(chunk.Records) != 1 || chunk.Records[0].ID != inspected.Manifest.WorkspaceID {
			return workspacecontinuity.ErrInvalid
		}
		var err error
		ws, err = DecodeContinuityWorkspace(chunk.Records[0], canonical)
		return err
	})
	if err != nil {
		return value, nil, err
	}
	if ws == nil {
		return value, nil, workspacecontinuity.ErrIncomplete
	}
	filePath, _, err := localAgentPath(name)
	if err != nil {
		return value, nil, err
	}
	var fingerprint workspacecontinuity.Fingerprint
	for _, file := range inspected.Manifest.Files {
		if file.Path == filePath {
			fingerprint = file
			break
		}
	}
	if fingerprint.Path == "" {
		return value, nil, workspacecontinuity.ErrIncomplete
	}
	var profile *agent.Agent
	err = workspacecontinuity.ReadComponentRecords(ctx, sourceDir, inspected, "agents", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "profiles" {
			return nil
		}
		for _, record := range chunk.Records {
			var hint struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(record.Data, &hint); err != nil {
				return workspacecontinuity.ErrInvalid
			}
			if hint.Name != name {
				continue
			}
			if profile != nil {
				return workspacecontinuity.ErrInvalid
			}
			var err error
			value, profile, err = VerifyContinuityProfileEvidence(ctx, sourceDir, ws, record, fingerprint)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return value, nil, err
	}
	if profile == nil {
		return value, nil, workspacecontinuity.ErrIncomplete
	}
	return value, profile, nil
}
