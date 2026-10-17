package backuprecovery

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type storedSetupReader struct {
	service *Service
	users   userprofile.UserProvider
}

// NewSetupReader resolves the canonical request actor itself rather than
// trusting a generic catalog's possibly case-folded workspace authorization.
// It has no provider/credential/mutation interface. A nil service is unavailable.
func NewSetupReader(service *Service, users userprofile.UserProvider) SetupReader {
	if users == nil {
		users = userprofile.LocalUserProvider{}
	}
	return &storedSetupReader{service, users}
}
func (reader *storedSetupReader) ReadBackupSetup(ctx context.Context, workspace string) (SetupConnectionState, error) {
	if ctx.Err() != nil {
		return SetupUnavailable, ctx.Err()
	}
	if reader == nil || reader.service == nil || reader.users == nil {
		return SetupUnavailable, nil
	}
	owner, err := reader.users.CurrentUserID(ctx)
	if err != nil {
		return SetupUnavailable, ErrUnavailable
	}
	status, err := reader.service.GetStatus(ctx, owner, workspace)
	if err != nil {
		return SetupUnavailable, err
	}
	if !status.RuntimeAvailable {
		return SetupUnavailable, nil
	}
	if status.Connection == "not_configured" {
		return SetupNotConfigured, nil
	}
	if status.Connection != "configured" || !status.EvidenceCurrent || status.LastSuccessfulRead == nil {
		return SetupReviewRequired, nil
	}
	if status.LastAttempt != nil && status.LastAttempt.Generation == status.Generation {
		switch status.LastAttempt.Outcome {
		case "provider_unavailable", "state_unavailable":
			return SetupUnavailable, nil
		case "credential_unavailable", "identity_mismatch":
			return SetupReviewRequired, nil
		}
	}
	// This says only that a local metadata connection has been observed; an
	// unplugged drive or empty repository is not a failed/completed backup.
	return SetupConnected, nil
}
