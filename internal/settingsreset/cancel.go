package settingsreset

import (
	"context"
	"slices"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Cancellation of an unapplied Start Fresh.
//
// A staged reset that recovery blocked because the reviewed scope changed is
// otherwise permanent: a blocked non-archive receipt is never retried, and the
// installation stays fenced. This file decides — without any side effect —
// whether such a receipt can be PROVEN to have changed nothing, so that a later
// step may retire it. It grants no permission by itself: the decision is
// recomputed under the installation lease immediately before any write.
//
// Why the proof is the revision and nothing else. Recovery writes, in order,
// the scope check, the import-suppression policy, the `applying` marker
// (revision+1) and only then changes a category. `blockRecovery` also adds one.
// A blocked receipt at revision 3 can therefore only have been produced from
// the staged `awaiting_restart` receipt at revision 2, before any `applying`
// marker existed. A crash after the first category changed but before its
// result was recorded leaves `applying` at revision 3 with every result still
// pending; the next failed launch turns that into blocked revision 4 with the
// same blocker code and the same all-pending results. Pending results, the
// blocker code, timestamps and the policy file cannot tell the two apart; the
// revision can. Anything else is "not provable" and stays fenced.

// CancelReason is a stable, secret-free code for a cancellation verdict. It
// never carries a path, a count or a user-supplied value.
type CancelReason string

const (
	CancelEligible CancelReason = "eligible"

	// Receipt-level refusals (decided from the strictly decoded journal alone).
	CancelNotBlocked        CancelReason = "not_blocked"
	CancelOtherBlocker      CancelReason = "other_blocker"
	CancelNotStartFresh     CancelReason = "not_start_fresh"
	CancelArchiveOperation  CancelReason = "hq_archive_operation"
	CancelNotPreApply       CancelReason = "not_provably_pre_apply"
	CancelResultsNotPending CancelReason = "results_not_pending"
)

// Host-level refusals (decided from independent, read-only observation).
const (
	// CancelScopeUnverifiable: an independent derivation could not be completed,
	// so nothing can be attributed. Uncertain is never eligible.
	CancelScopeUnverifiable CancelReason = "scope_unverifiable"
	// CancelOtherScopeChanged: something besides the retained workspace no longer
	// matches the review (targets, database, agents folder, plugins, settings,
	// vault). Unfencing would open stores on state nobody reviewed.
	CancelOtherScopeChanged CancelReason = "other_scope_changed"
	// CancelWorkspaceUnchanged: the host sees no workspace change to explain the
	// block, so the reason stays unknown and the receipt stays fenced.
	CancelWorkspaceUnchanged CancelReason = "workspace_unchanged"
	// CancelRetainedUnreadable: a retained folder exists but cannot be read; that
	// is not the same as it being emptied or removed.
	CancelRetainedUnreadable CancelReason = "retained_unreadable"
	// CancelRetainedOtherChanged: a retained path that is not the workspace root
	// (a vault location) changed as well.
	CancelRetainedOtherChanged CancelReason = "retained_other_changed"
	// CancelPolicyInvalid: the startup policy record is unreadable or malformed.
	CancelPolicyInvalid CancelReason = "policy_invalid"
	// CancelPolicyThisOperation: the policy on disk was written by THIS
	// operation's own recovery launch. Only a receipt that went past the
	// import-suppression write can leave that, so it is ineligible: the same
	// lineage also describes a receipt hand-edited back to awaiting_restart
	// after an apply, and cancellation (which never edits the policy) would keep
	// this operation's suppression flags in force forever.
	CancelPolicyThisOperation CancelReason = "policy_written_by_this_operation"
	// CancelSameLaunch: the receipt was accepted by this very process, which
	// recovery never is.
	CancelSameLaunch CancelReason = "accepted_by_this_launch"
)

// cancelRevision is the revision of a receipt that recovery blocked straight
// from the staged awaiting_restart receipt (revision 2).
const cancelRevision uint64 = 3

// PolicyProvenance says which operation, if any, wrote the startup policy that
// is on disk. It is evidence about HOW the receipt was reached, never a grant:
// the policy file is merged across operations and cancellation never edits it.
type PolicyProvenance string

const (
	// PolicyNone: no policy record exists.
	PolicyNone PolicyProvenance = "none"
	// PolicyPriorOperation: a valid policy written by an earlier operation (the
	// reported incident). Cancellation leaves it exactly as it is.
	PolicyPriorOperation PolicyProvenance = "prior_operation"
	// PolicyThisOperation: a valid policy written by this operation's own
	// recovery launch. Ineligible, see CancelPolicyThisOperation.
	PolicyThisOperation PolicyProvenance = "this_operation"
	// PolicyInvalid: present but unreadable or non-canonical.
	PolicyInvalid PolicyProvenance = "invalid"
)

// classifyPolicy reads the policy record through the lease and compares its
// owner with the receipt. It is read-only.
func classifyPolicy(lease *resetstate.Lease, operationID string) PolicyProvenance {
	policy, err := ReadStartupPolicy(lease)
	return policyProvenance(policy, err, operationID)
}

func policyProvenance(policy StartupPolicy, err error, operationID string) PolicyProvenance {
	switch {
	case err != nil:
		return PolicyInvalid
	case policy.SchemaVersion == 0:
		return PolicyNone
	case policy.OperationID == operationID:
		return PolicyThisOperation
	default:
		return PolicyPriorOperation
	}
}

// CancelCause is how specifically the host can say what changed. Only the
// retained workspace is ever named, and only when the host observed it itself.
type CancelCause string

const (
	CauseUnknown                  CancelCause = ""
	CauseRetainedWorkspaceChanged CancelCause = "retained_workspace_changed"
)

// CancelVerdict is the result of the side-effect-free decision. Eligible is a
// statement about the evidence at one instant, not a grant: a later transition
// must recompute it under the installation lease.
type CancelVerdict struct {
	Eligible         bool
	Reason           CancelReason
	Cause            CancelCause
	OperationID      string
	Revision         uint64
	PolicyProvenance PolicyProvenance
	// Workspace is how the folder looks now. It exists only for wording and is
	// never stored in a receipt: a missing folder may be an unmounted volume, so
	// no copy may say it was deleted.
	Workspace WorkspaceCondition
}

// WorkspaceCondition classifies the changed workspace folder for display only.
type WorkspaceCondition string

const (
	WorkspaceUnknown WorkspaceCondition = ""
	// WorkspaceMissing: Ori cannot find the folder (deleted, renamed or on a
	// volume that is not mounted — the host cannot tell which).
	WorkspaceMissing WorkspaceCondition = "missing"
	// WorkspaceEmpty: the folder exists and holds nothing but Finder metadata.
	WorkspaceEmpty WorkspaceCondition = "empty"
	// WorkspaceChanged: the folder exists and its contents differ from the review.
	WorkspaceChanged WorkspaceCondition = "changed"
)

func refuseCancel(j *journal, reason CancelReason, policy PolicyProvenance) CancelVerdict {
	verdict := CancelVerdict{Reason: reason, PolicyProvenance: policy}
	if j != nil {
		verdict.OperationID, verdict.Revision = j.Operation.ID, j.Operation.Revision
	}
	return verdict
}

// decideCancel is pure: receipt, policy provenance and host facts in, verdict
// out. Every refusal is a specific, testable reason; anything not affirmatively
// proven is a refusal.
func decideCancel(j *journal, policy PolicyProvenance, host hostFacts) CancelVerdict {
	if reason := receiptProvesPreApply(j); reason != CancelEligible {
		return refuseCancel(j, reason, policy)
	}
	switch policy {
	case PolicyNone, PolicyPriorOperation:
	case PolicyThisOperation:
		return refuseCancel(j, CancelPolicyThisOperation, policy)
	default:
		return refuseCancel(j, CancelPolicyInvalid, policy)
	}
	if host.Unverifiable {
		return refuseCancel(j, CancelScopeUnverifiable, policy)
	}
	// Any unreadable retained path is uncertain, never "missing".
	for _, fact := range host.Retained {
		if fact.State != retainedMatches && fact.State != retainedDiffers {
			return refuseCancel(j, CancelRetainedUnreadable, policy)
		}
	}
	var changedWorkspaces []string
	for _, fact := range host.Retained {
		if fact.State == retainedDiffers && fact.Role == roleWorkspace {
			changedWorkspaces = append(changedWorkspaces, fact.Path)
		}
	}
	// A changed path that is not the workspace root is explained only when it
	// lives inside a changed workspace root (a vault nested there goes with it).
	for _, fact := range host.Retained {
		if fact.State != retainedDiffers || fact.Role == roleWorkspace {
			continue
		}
		if !slices.ContainsFunc(changedWorkspaces, func(root string) bool { return containsPath(root, fact.Path) }) {
			return refuseCancel(j, CancelRetainedOtherChanged, policy)
		}
	}
	if !host.OtherScopeIntact {
		return refuseCancel(j, CancelOtherScopeChanged, policy)
	}
	if len(changedWorkspaces) == 0 {
		return refuseCancel(j, CancelWorkspaceUnchanged, policy)
	}
	verdict := refuseCancel(j, CancelEligible, policy)
	verdict.Eligible, verdict.Cause = true, CauseRetainedWorkspaceChanged
	verdict.Workspace = host.Workspace
	return verdict
}

// EvaluateCancel is the side-effect-free read of the current decision. It reads
// the receipt and policy through the lease and observes the host; it never
// writes, opens an application store or touches a credential store.
func EvaluateCancel(ctx context.Context, lease *resetstate.Lease, options RecoveryOptions) (CancelVerdict, error) {
	if lease == nil {
		return CancelVerdict{}, ErrLifecycleUnavailable
	}
	data, err := lease.Read(resetstate.OperationRecord)
	if err != nil || data == nil {
		return CancelVerdict{}, ErrJournalInvalid
	}
	j, err := decodeJournal(data)
	if err != nil || j.Plan.Installation != lease.Path() {
		return CancelVerdict{}, ErrJournalInvalid
	}
	policy := classifyPolicy(lease, j.Operation.ID)
	// Observe the host only for a receipt that already proves it never applied;
	// anything else is refused without hashing a single retained byte.
	if reason := receiptProvesPreApply(j); reason != CancelEligible {
		return refuseCancel(j, reason, policy), nil
	}
	if j.AcceptedBy == lease.Identity() {
		return refuseCancel(j, CancelSameLaunch, policy), nil
	}
	return decideCancel(j, policy, observeCancelHost(ctx, lease, j, options)), nil
}

// receiptProvesPreApply is the receipt-only half of the decision. It is pure:
// it reads nothing but the already strictly decoded journal.
func receiptProvesPreApply(j *journal) CancelReason {
	if j == nil || j.Operation.State != StateBlocked {
		return CancelNotBlocked
	}
	if len(j.Operation.Blockers) != 1 || j.Operation.Blockers[0].Code != "recovery_scope_changed" {
		return CancelOtherBlocker
	}
	if j.Operation.Intent != IntentStartFresh || j.Plan.Preview.Intent != IntentStartFresh {
		return CancelNotStartFresh
	}
	if j.Archive != nil || j.Plan.Evidence.HQArchive != nil || j.Operation.HQArchive != nil {
		return CancelArchiveOperation
	}
	if j.Operation.Revision != cancelRevision {
		return CancelNotPreApply
	}
	if len(j.Operation.Results) == 0 || !allResultsPending(j.Operation.Results) {
		return CancelResultsNotPending
	}
	for _, result := range j.Operation.Results {
		for _, check := range result.Checks {
			if check.Outcome != OutcomePending {
				return CancelResultsNotPending
			}
		}
	}
	return CancelEligible
}
