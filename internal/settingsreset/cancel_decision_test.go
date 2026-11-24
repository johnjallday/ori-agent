package settingsreset

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// ---- pure decision table (no filesystem) -------------------------------------

func intactHost() hostFacts {
	return hostFacts{OtherScopeIntact: true, Workspace: WorkspaceEmpty, Retained: []retainedFact{
		{Path: "/fixture/projects", Role: roleWorkspace, State: retainedDiffers},
		{Path: "/fixture/vaults", Role: roleVault, State: retainedMatches},
	}}
}

func TestDecideCancelTable(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*journal, *PolicyProvenance, *hostFacts)
		want   CancelReason
	}{
		{"workspace changed, everything else intact, no policy", func(*journal, *PolicyProvenance, *hostFacts) {}, CancelEligible},
		{"workspace changed, an earlier operation's policy (the incident)", func(_ *journal, p *PolicyProvenance, _ *hostFacts) { *p = PolicyPriorOperation }, CancelEligible},
		{"rev 4 look-alike is refused even when the host sees the same change", func(j *journal, _ *PolicyProvenance, _ *hostFacts) { j.Operation.Revision = 4 }, CancelNotPreApply},
		{"policy written by this very operation (a hand-edited rev 3 looks the same)", func(_ *journal, p *PolicyProvenance, _ *hostFacts) { *p = PolicyThisOperation }, CancelPolicyThisOperation},
		{"a vault nested inside the changed workspace goes with it", func(_ *journal, _ *PolicyProvenance, h *hostFacts) {
			h.Retained = append(h.Retained, retainedFact{Path: "/fixture/projects/nested-vault", Role: roleVault, State: retainedDiffers})
		}, CancelEligible},
		{"a vault nested in the workspace that did NOT change with it is still fine", func(_ *journal, _ *PolicyProvenance, h *hostFacts) {
			h.Retained = append(h.Retained, retainedFact{Path: "/fixture/projects/nested-vault", Role: roleVault, State: retainedMatches})
		}, CancelEligible},
		{"a path that only shares a name prefix with the workspace is not inside it", func(_ *journal, _ *PolicyProvenance, h *hostFacts) {
			h.Retained = append(h.Retained, retainedFact{Path: "/fixture/projects-backup", Role: roleOther, State: retainedDiffers})
		}, CancelRetainedOtherChanged},
		{"invalid policy", func(_ *journal, p *PolicyProvenance, _ *hostFacts) { *p = PolicyInvalid }, CancelPolicyInvalid},
		{"unknown provenance value is not trusted", func(_ *journal, p *PolicyProvenance, _ *hostFacts) { *p = "bogus" }, CancelPolicyInvalid},
		{"host could not verify", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.Unverifiable = true }, CancelScopeUnverifiable},
		{"workspace root unreadable (inaccessible, not missing)", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.Retained[0].State = retainedUnreadable }, CancelRetainedUnreadable},
		{"a vault location changed too", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.Retained[1].State = retainedDiffers }, CancelRetainedOtherChanged},
		{"an unlabelled retained path changed", func(_ *journal, _ *PolicyProvenance, h *hostFacts) {
			h.Retained[1].Role = roleOther
			h.Retained[1].State = retainedDiffers
		}, CancelRetainedOtherChanged},
		{"the workspace did not change: block reason unknown", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.Retained[0].State = retainedMatches }, CancelWorkspaceUnchanged},
		{"another part of the reviewed scope changed", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.OtherScopeIntact = false }, CancelOtherScopeChanged},
		{"another part changed and the workspace did not", func(_ *journal, _ *PolicyProvenance, h *hostFacts) {
			h.OtherScopeIntact = false
			h.Retained[0].State = retainedMatches
		}, CancelOtherScopeChanged},
		{"no retained facts at all", func(_ *journal, _ *PolicyProvenance, h *hostFacts) { h.Retained = nil }, CancelWorkspaceUnchanged},
		{"a corrupt or non-canonical receipt shape is refused before the host is consulted", func(j *journal, _ *PolicyProvenance, h *hostFacts) {
			j.Operation.State = StatePartialFailure
			h.Unverifiable = true
		}, CancelNotBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j, policy, host := incidentReceipt(t), PolicyNone, intactHost()
			tc.mutate(j, &policy, &host)
			verdict := decideCancel(j, policy, host)
			if verdict.Reason != tc.want || verdict.Eligible != (tc.want == CancelEligible) {
				t.Fatalf("verdict = %+v, want reason %q", verdict, tc.want)
			}
			if verdict.Eligible && (verdict.Cause != CauseRetainedWorkspaceChanged || verdict.Workspace != WorkspaceEmpty) {
				t.Fatalf("an eligible verdict names its cause and the folder's condition: %+v", verdict)
			}
			if !verdict.Eligible && verdict.Cause != CauseUnknown {
				t.Fatalf("a refusal must not name a cause: %+v", verdict)
			}
		})
	}
}

// ---- real host observation ---------------------------------------------------

func evaluate(t *testing.T, s *stagedStartFresh, lease *resetstate.Lease) CancelVerdict {
	t.Helper()
	verdict, err := EvaluateCancel(t.Context(), lease, RecoveryOptions{
		DataDir: s.DataDir,
		// Evaluation must never reach a credential store; make any attempt loud.
		OpenSecretStore: func() (vault.SecretStore, error) {
			t.Error("cancellation evaluation opened a credential store")
			return nil, errors.New("must not be opened")
		},
	})
	if err != nil {
		t.Fatalf("EvaluateCancel: %v", err)
	}
	return verdict
}

func TestEvaluateCancelEligibleWhenTheWorkspaceWasEmptiedOrRemoved(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	cases := []struct {
		name      string
		change    func(*testing.T, *stagedStartFresh)
		policy    bool // an earlier operation's policy was on disk, as in the incident
		condition WorkspaceCondition
	}{
		{"emptied folder", func(t *testing.T, s *stagedStartFresh) { s.Fixture.EmptyRetainedFolder(t, "projects", true) }, false, WorkspaceEmpty},
		{"removed folder", func(t *testing.T, s *stagedStartFresh) { s.Fixture.EmptyRetainedFolder(t, "projects", false) }, false, WorkspaceMissing},
		{"emptied but for Finder metadata", func(t *testing.T, s *stagedStartFresh) {
			s.Fixture.EmptyRetainedFolder(t, "projects", true)
			mustPreview(t, s.Fixture.WriteFile("projects/.DS_Store", []byte("finder")))
		}, false, WorkspaceEmpty},
		{"emptied folder with an earlier operation's policy", func(t *testing.T, s *stagedStartFresh) { s.Fixture.EmptyRetainedFolder(t, "projects", true) }, true, WorkspaceEmpty},
		{"any other change to the folder counts (a sync client or editor touched it)", func(t *testing.T, s *stagedStartFresh) {
			mustPreview(t, os.WriteFile(filepath.Join(s.Fixture.Paths().Workspaces, "retained", "edited-elsewhere.txt"), []byte("edited"), 0o600))
		}, false, WorkspaceChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := stageStartFresh(t)
			if tc.policy {
				lease, err := resetstate.Acquire(s.DataDir)
				mustPreview(t, err)
				mustPreview(t, writeStartupPolicy(lease, "earlier-operation-id", []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now().Add(-time.Hour)))
				mustPreview(t, lease.Close())
			}
			tc.change(t, s)
			lease := s.blockOnWorkspaceChange(t)
			verdict := evaluate(t, s, lease)
			if !verdict.Eligible || verdict.Cause != CauseRetainedWorkspaceChanged || verdict.OperationID != s.OperationID || verdict.Revision != 3 {
				t.Fatalf("verdict = %+v", verdict)
			}
			if verdict.Workspace != tc.condition {
				t.Fatalf("workspace condition = %q, want %q", verdict.Workspace, tc.condition)
			}
			want := PolicyNone
			if tc.policy {
				want = PolicyPriorOperation
			}
			if verdict.PolicyProvenance != want {
				t.Fatalf("policy provenance = %q, want %q", verdict.PolicyProvenance, want)
			}
		})
	}
}

func TestEvaluateCancelRefusesTheInterruptedApplyLookAlike(t *testing.T) {
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
			lease := s.blockOnWorkspaceChange(t)
			// Same blocker, same all-pending results, same empty workspace as the
			// incident: only the revision differs, and that must be enough.
			if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelNotPreApply || verdict.Revision != 4 {
				t.Fatalf("verdict = %+v", verdict)
			}
		})
	}
}

func TestEvaluateCancelRefusesWhenSomethingBesideTheWorkspaceChanged(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	t.Run("a vault location is emptied as well", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "vaults", true)
		lease := s.blockOnWorkspaceChange(t)
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelRetainedOtherChanged {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
	t.Run("the templates root moved in settings", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		mustPreview(t, s.Owners.Config.SetTemplatesRoot(filepath.Join(s.DataDir, "moved-templates")))
		mustPreview(t, s.Owners.Config.Save())
		lease := s.blockOnWorkspaceChange(t)
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelOtherScopeChanged {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
	t.Run("the database schema no longer matches", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		j := s.receipt(t, lease)
		j.Plan.Evidence.DatabaseSchema = compatDigest(5) // as if the schema had moved on
		persistForTest(t, lease, j)
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelOtherScopeChanged {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
}

func TestEvaluateCancelRefusesWithoutAnObservedWorkspaceChange(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	s := stageStartFresh(t)
	// A rev-3 block exists but the host sees every reviewed folder as reviewed:
	// the reason for the block is unknown, so nothing is attributed or unfenced.
	lease, err := resetstate.Acquire(s.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	j := s.receipt(t, lease)
	blockRecovery(j, "recovery_scope_changed", time.Now(), "Reset scope changed before pre-start application; no pending category was applied.")
	persistForTest(t, lease, j)
	if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelWorkspaceUnchanged {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestEvaluateCancelTreatsAnUnreadableWorkspaceAsUncertainNotMissing(t *testing.T) {
	if !resetstate.Supported() || os.Geteuid() == 0 {
		t.Skip("needs a lease and a non-root user to make a folder unreadable")
	}
	s := stageStartFresh(t)
	s.Fixture.EmptyRetainedFolder(t, "projects", true)
	lease := s.blockOnWorkspaceChange(t)
	projects := s.Fixture.Paths().Workspaces
	mustPreview(t, os.WriteFile(filepath.Join(projects, "later.txt"), []byte("x"), 0o600))
	mustPreview(t, os.Chmod(projects, 0o000))
	t.Cleanup(func() { _ = os.Chmod(projects, 0o750) })
	if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelRetainedUnreadable {
		t.Fatalf("verdict = %+v", verdict)
	}
}

// A policy the receipt's own operation wrote means recovery got past the
// import-suppression write. The honest crash window (W5) and a receipt that was
// hand-edited back to awaiting_restart after an apply share that lineage, so
// neither can be offered cancellation; the second signal must agree with the first.
func TestEvaluateCancelRefusesAPolicyWrittenByTheSameOperation(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	t.Run("crash after the policy write, before the applying marker (W5)", func(t *testing.T) {
		s := stageStartFresh(t)
		s.crashAfterPolicyBeforeApplyingMarker(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		requireBlockedAllPending(t, s.receipt(t, lease), 3)
		verdict := evaluate(t, s, lease)
		if verdict.Eligible || verdict.Reason != CancelPolicyThisOperation || verdict.PolicyProvenance != PolicyThisOperation {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
	t.Run("a canonical rev 3 receipt whose policy names the same operation", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		if verdict := evaluate(t, s, lease); !verdict.Eligible {
			t.Fatalf("control: the incident shape must be eligible: %+v", verdict)
		}
		mustPreview(t, writeStartupPolicy(lease, s.OperationID, []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now()))
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelPolicyThisOperation {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
	t.Run("a receipt accepted by the launch evaluating it", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		j := s.receipt(t, lease)
		j.AcceptedBy = lease.Identity()
		persistForTest(t, lease, j)
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelSameLaunch {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
}

func TestEvaluateCancelRefusesInvalidEvidence(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	t.Run("a startup policy that is not canonical", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		mustPreview(t, lease.Replace(resetstate.PolicyRecord, []byte(`{"schema_version": 1}`)))
		if verdict := evaluate(t, s, lease); verdict.Eligible || verdict.Reason != CancelPolicyInvalid {
			t.Fatalf("verdict = %+v", verdict)
		}
	})
	t.Run("a corrupt receipt is an error, never a verdict", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		mustPreview(t, lease.Replace(resetstate.OperationRecord, []byte(`{"version":1}`)))
		if _, err := EvaluateCancel(t.Context(), lease, RecoveryOptions{DataDir: s.DataDir}); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("EvaluateCancel = %v, want ErrJournalInvalid", err)
		}
	})
	t.Run("an absent receipt is an error", func(t *testing.T) {
		s := stageStartFresh(t)
		lease, err := resetstate.Acquire(s.DataDir)
		mustPreview(t, err)
		t.Cleanup(func() { _ = lease.Close() })
		mustPreview(t, os.Remove(filepath.Join(s.DataDir, resetstate.Directory, string(resetstate.OperationRecord))))
		if _, err := EvaluateCancel(t.Context(), lease, RecoveryOptions{DataDir: s.DataDir}); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("EvaluateCancel = %v, want ErrJournalInvalid", err)
		}
	})
	t.Run("a receipt for a different installation", func(t *testing.T) {
		s := stageStartFresh(t)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		j := s.receipt(t, lease)
		j.Plan.Installation = filepath.Join(filepath.Dir(s.DataDir), "other-installation")
		data, err := json.Marshal(j)
		mustPreview(t, err)
		mustPreview(t, lease.Replace(resetstate.OperationRecord, data))
		if _, err := EvaluateCancel(t.Context(), lease, RecoveryOptions{DataDir: s.DataDir}); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("EvaluateCancel = %v, want ErrJournalInvalid", err)
		}
	})
}

// A read-only decision must leave every byte where it was: the receipt, the
// policy, the data directory, the retained folders and the credentials.
func TestEvaluateCancelChangesNothing(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	s := stageStartFresh(t)
	s.Fixture.EmptyRetainedFolder(t, "projects", true)
	lease := s.blockOnWorkspaceChange(t)
	before := treeBytes(t, s.Fixture.Paths().Root)
	keyBefore, err := s.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
	mustPreview(t, err)
	for range 3 {
		if verdict := evaluate(t, s, lease); !verdict.Eligible {
			t.Fatalf("verdict = %+v", verdict)
		}
	}
	after := treeBytes(t, s.Fixture.Paths().Root)
	if len(before) != len(after) {
		t.Fatalf("evaluation changed the number of files: %d → %d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("evaluation changed %s", path)
		}
	}
	if key, err := s.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil || key != keyBefore {
		t.Fatalf("evaluation changed a credential: %v", err)
	}
}

// The observation re-implements no reviewed check: on a receipt nothing has
// changed under, it must agree with the real recovery scope check.
func TestObservationAgreesWithTheRealScopeCheckWhenNothingChanged(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	s := stageStartFresh(t)
	lease, err := resetstate.Acquire(s.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	j := s.receipt(t, lease)
	if _, _, _, err := validateRecoveryScope(t.Context(), lease, j, RecoveryOptions{DataDir: s.DataDir, SecretStore: s.Fixture.Secrets()}); err != nil {
		t.Fatalf("the staged receipt must validate before any change: %v", err)
	}
	host := observeCancelHost(t.Context(), lease, j, RecoveryOptions{DataDir: s.DataDir})
	if host.Unverifiable || !host.OtherScopeIntact || len(host.Retained) == 0 {
		t.Fatalf("host = %+v", host)
	}
	for _, fact := range host.Retained {
		if fact.State != retainedMatches {
			t.Fatalf("retained %s = %s on an untouched installation", fact.Path, fact.State)
		}
	}
	roles := map[retainedRole]int{}
	for _, fact := range host.Retained {
		roles[fact.Role]++
	}
	if roles[roleWorkspace] != 1 || roles[roleVault] != 1 {
		t.Fatalf("roles were not derived independently from settings: %v", roles)
	}
}
