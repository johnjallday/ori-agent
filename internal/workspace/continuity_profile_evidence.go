package workspace

import (
	"bytes"
	"context"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// VerifyContinuityProfileEvidence ties a portable scoped profile to the exact
// manifest-declared denied canonical definition. A source-side collector must
// have separated local settings before publishing that file; a copied local
// slot never authorizes decryption on the destination. This is read-only
// evidence, not roster trust, local configuration hydration, or a restore.
func VerifyContinuityProfileEvidence(ctx context.Context, directory string, ws *Workspace, record workspacecontinuity.Record, fingerprint workspacecontinuity.Fingerprint) (ContinuityProfile, *agent.Agent, error) {
	value, portable, err := DecodeContinuityProfile(record, ws)
	if err != nil {
		return value, nil, err
	}
	path, slug, err := localAgentPath(value.Name)
	if err != nil || fingerprint.Path != path || fingerprint.Bytes > workspacecontinuity.MaxRecordBytes {
		return value, nil, workspacecontinuity.ErrInvalid
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, path, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return value, nil, err
	}
	if fingerprint.Bytes != int64(len(data)) || fingerprint.Digest != workspacecontinuity.Digest(data) {
		return value, nil, workspacecontinuity.ErrChanged
	}
	if err := checkContinuityProfileFiles(ctx, directory, slug, value.Appearance.UploadedImage()); err != nil {
		return value, nil, err
	}
	source, err := decodeLocalAgent(data)
	if err != nil {
		return value, nil, err
	}
	// A native definition may still hold an agent-specific key or permission
	// defaults entered on the source machine. The typed record never includes
	// them and the returned definition is the denied projection, so a copied
	// file can be read here without any of it reaching this installation.
	observed, err := SnapshotContinuityProfile(ws, value.Name, source)
	if err != nil {
		return value, nil, err
	}
	if observed.ID != record.ID || !bytes.Equal(observed.Data, record.Data) {
		return value, nil, workspacecontinuity.ErrChanged
	}
	return value, portable, ctx.Err()
}
