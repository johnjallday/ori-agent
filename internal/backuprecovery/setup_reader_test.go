//go:build darwin || linux

package backuprecovery

import (
	"bytes"
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
)

type setupActorFixture string

func (actor setupActorFixture) CurrentUserID(context.Context) (string, error) {
	return string(actor), nil
}
func TestBackupStoredWizardFollowsConnectionWithoutCheckingOrGranting(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	adapter := NewSetupAdapter(NewSetupReader(s, nil))
	request := backupSetupFixture()
	request.WorkspaceID = localFixtureWorkspace
	result, err := adapter.Evaluate(t.Context(), request)
	if err != nil || result.Ready || result.ErrorCategory != setupwizard.ErrorCategoryNotConfigured || provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("opening setup checked/activated an unconfigured backup", result, err)
	}
	connectBackupServiceFixture(t, s)
	beforeState, beforeReceipt := localCiphertexts(t, s.store)
	for range 2 {
		result, err = adapter.Confirm(t.Context(), request, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
		if err != nil || !result.Ready {
			t.Fatal("resume/lost-response confirmation ignored stored evidence", result, err)
		}
	}
	afterState, afterReceipt := localCiphertexts(t, s.store)
	if !bytes.Equal(beforeState, afterState) || !bytes.Equal(beforeReceipt, afterReceipt) || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("wizard confirmation refreshed or changed access")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.Evaluate(ctx, request); err == nil {
		t.Fatal("cancelled setup evaluation continued")
	}
}
func TestBackupStoredWizardChecksCanonicalActorAndDegradedRuntime(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	request := backupSetupFixture()
	request.WorkspaceID = localFixtureWorkspace
	adapter := NewSetupAdapter(NewSetupReader(s, setupActorFixture("LOCAL")))
	result, err := adapter.Evaluate(t.Context(), request)
	if err != nil || result.Ready || result.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatal("generic case-folded ownership exposed readiness", result, err)
	}
	// Stored success does not turn a missing runtime into live readiness.
	s.provider = nil
	adapter = NewSetupAdapter(NewSetupReader(s, nil))
	result, err = adapter.Evaluate(t.Context(), request)
	if err != nil || result.Ready || result.ErrorCategory != setupwizard.ErrorCategoryUnavailable || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("unavailable tool claimed readiness or was probed", result, err)
	}
	s.provider = provider // retain fixture-owned close/drain accounting
}
