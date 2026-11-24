package settingsreset

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// These tests pin today's write order and the receipts each crash window leaves,
// using production staging and the production recovery helpers. They are the
// evidence behind the cancellation predicate: a receipt may be offered for
// cancellation only when it can be told apart from the windows that already
// changed data. They assert existing behavior; none of them grants anything.

// crashAfterPolicyBeforeApplyingMarker is window W5: the first recovery launch
// validated the scope and made the import-suppression policy durable, then the
// process died before the `applying` marker reached disk.
func (s *stagedStartFresh) crashAfterPolicyBeforeApplyingMarker(t *testing.T) {
	t.Helper()
	lease, err := resetstate.Acquire(s.DataDir)
	mustPreview(t, err)
	j := s.receipt(t, lease)
	if _, _, _, err := validateRecoveryScope(t.Context(), lease, j, RecoveryOptions{DataDir: s.DataDir, SecretStore: s.Fixture.Secrets()}); err != nil {
		t.Fatalf("first launch scope check: %v", err)
	}
	mustPreview(t, writeStartupPolicy(lease, j.Operation.ID, j.Plan.Preview.Selected, time.Now()))
	mustPreview(t, lease.Close()) // crash: the journal is still awaiting_restart
}

// crashDuringFirstCategory is window W7 (and, with applyFirst=false, W6): the
// `applying` marker is durable and the first category has been changed, but the
// process died before that category's result was persisted.
func (s *stagedStartFresh) crashDuringFirstCategory(t *testing.T, applyFirst bool) {
	t.Helper()
	lease, err := resetstate.Acquire(s.DataDir)
	mustPreview(t, err)
	j := s.receipt(t, lease)
	options := RecoveryOptions{DataDir: s.DataDir, SecretStore: s.Fixture.Secrets()}
	targets, manager, pluginPaths, err := validateRecoveryScope(t.Context(), lease, j, options)
	mustPreview(t, err)
	mustPreview(t, writeStartupPolicy(lease, j.Operation.ID, j.Plan.Preview.Selected, time.Now()))
	j.Operation.State = StateApplying
	j.Operation.Revision++
	j.Operation.UpdatedAt = time.Now().UTC()
	mustPreview(t, persistRecoveryJournal(lease, j))
	if applyFirst {
		_ = applyRecoveryCategory(t.Context(), j.Operation.Results[0], j.Plan.Preview.Selected, j.Plan.Evidence, targets, manager, pluginPaths, lease)
	}
	mustPreview(t, lease.Close()) // crash before the result is persisted
}

func requireBlockedAllPending(t *testing.T, j *journal, revision uint64) {
	t.Helper()
	if j.Operation.State != StateBlocked || j.Operation.Revision != revision {
		t.Fatalf("receipt = %s rev %d, want blocked rev %d", j.Operation.State, j.Operation.Revision, revision)
	}
	if len(j.Operation.Blockers) != 1 || j.Operation.Blockers[0].Code != "recovery_scope_changed" {
		t.Fatalf("blockers = %+v", j.Operation.Blockers)
	}
	if !allResultsPending(j.Operation.Results) {
		t.Fatal("results are not all pending")
	}
	for _, result := range j.Operation.Results {
		for _, check := range result.Checks {
			if check.Outcome != OutcomePending {
				t.Fatalf("check %s is %s", check.Name, check.Outcome)
			}
		}
	}
}

func TestCharacterizeEmptiedWorkspaceBlocksStartFreshAtRevisionThree(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	for _, keepRoot := range []bool{true, false} {
		name := "workspace folder removed"
		if keepRoot {
			name = "workspace folder emptied"
		}
		t.Run(name, func(t *testing.T) {
			s := stageStartFresh(t)
			s.Fixture.EmptyRetainedFolder(t, "projects", keepRoot)
			lease, err := s.launch(t)
			if !errors.Is(err, ErrRecoveryIncomplete) {
				t.Fatalf("recovery = %v, want ErrRecoveryIncomplete", err)
			}
			requireBlockedAllPending(t, s.receipt(t, lease), 3)
			if _, err := s.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil {
				t.Fatalf("a provider credential changed although nothing was applied: %v", err)
			}
		})
	}
}

func TestCharacterizePolicyPersistedBeforeApplyingMarkerStillBlocksAtRevisionThree(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	s := stageStartFresh(t)
	s.crashAfterPolicyBeforeApplyingMarker(t)
	s.Fixture.EmptyRetainedFolder(t, "projects", true)
	lease, err := s.launch(t)
	if !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("recovery = %v, want ErrRecoveryIncomplete", err)
	}
	j := s.receipt(t, lease)
	requireBlockedAllPending(t, j, 3)
	policy, err := ReadStartupPolicy(lease)
	mustPreview(t, err)
	if policy.OperationID != s.OperationID {
		t.Fatalf("policy belongs to %q, want the staged operation %q", policy.OperationID, s.OperationID)
	}
	if _, err := s.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil {
		t.Fatalf("a provider credential changed although the applying marker never became durable: %v", err)
	}
}

func TestCharacterizeInterruptedApplyBlocksAtRevisionFourNotThree(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	for _, applyFirst := range []bool{false, true} {
		name := "crash before any category changed"
		if applyFirst {
			name = "crash after the first category changed"
		}
		t.Run(name, func(t *testing.T) {
			s := stageStartFresh(t)
			s.crashDuringFirstCategory(t, applyFirst)
			s.Fixture.EmptyRetainedFolder(t, "projects", true)
			lease, err := s.launch(t)
			if !errors.Is(err, ErrRecoveryIncomplete) {
				t.Fatalf("recovery = %v, want ErrRecoveryIncomplete", err)
			}
			// The look-alike: same code, same all-pending results. Only the
			// revision tells it from the pre-apply block, and the first-category
			// crash really did change data.
			requireBlockedAllPending(t, s.receipt(t, lease), 4)
			_, keyErr := s.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
			if applyFirst && !errors.Is(keyErr, vault.ErrSecretNotFound) {
				t.Fatalf("first category did not change anything (key err=%v); the look-alike is not a real apply", keyErr)
			}
			if !applyFirst && keyErr != nil {
				t.Fatalf("no category ran yet a credential changed: %v", keyErr)
			}
		})
	}
}
