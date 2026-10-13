package personalassistant

import "errors"

// FolderDestinationBlocker names what the user can set up to make a reviewed
// folder's destination available: the reviewed integration whose guided setup
// provides the Home the project belongs in, and the Home provider that is
// missing. It is provenance by key, never a plan, route or execution grant; the
// host resolves the keys against its own registry when it speaks, so no name
// is stored here.
type FolderDestinationBlocker struct {
	IntegrationKey  string `json:"integration_key,omitempty"`
	HomeProviderKey string `json:"home_provider_key,omitempty"`
}

func (b *FolderDestinationBlocker) clone() *FolderDestinationBlocker {
	if b == nil {
		return nil
	}
	copy := *b
	return &copy
}

// FolderSetupBlockedError is a FolderSetupDestinationReader's answer when the
// destination is unavailable for a reason the user can clear. It wraps the
// host's own unavailable error, so every caller that only asks whether setup
// is possible keeps its answer; only the review's disclosure gains the reason.
type FolderSetupBlockedError struct {
	Blocker FolderDestinationBlocker
	Err     error
}

func (e *FolderSetupBlockedError) Error() string {
	text := "personal assistant: folder setup is blocked until its integration is set up"
	if e != nil && e.Err != nil {
		text += ": " + e.Err.Error()
	}
	return text
}

func (e *FolderSetupBlockedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// folderDestinationBlocker is the blocker a reader's error names, or nil for a
// failure that names nothing: a plain error, or a blocked error with no keys.
func folderDestinationBlocker(err error) *FolderDestinationBlocker {
	var blocked *FolderSetupBlockedError
	if !errors.As(err, &blocked) || blocked == nil || blocked.Blocker == (FolderDestinationBlocker{}) {
		return nil
	}
	return blocked.Blocker.clone()
}
