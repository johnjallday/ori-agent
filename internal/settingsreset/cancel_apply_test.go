package settingsreset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// blockedIncident is the reported installation, built with production code:
// Start Fresh accepted, the workspace emptied outside Ori, the first recovery
// launch blocked at revision 3 and still serving its recovery page.
type blockedIncident struct {
	*stagedStartFresh
	lease *resetstate.Lease
}

func newBlockedIncident(t *testing.T, priorPolicy bool) *blockedIncident {
	t.Helper()
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	s := stageStartFresh(t)
	if priorPolicy {
		lease, err := resetstate.Acquire(s.DataDir)
		mustPreview(t, err)
		mustPreview(t, writeStartupPolicy(lease, "earlier-operation-id", []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now().Add(-time.Hour)))
		mustPreview(t, lease.Close())
	}
	s.Fixture.EmptyRetainedFolder(t, "projects", true)
	return &blockedIncident{stagedStartFresh: s, lease: s.blockOnWorkspaceChange(t)}
}

func (b *blockedIncident) coordinator() *Coordinator { return NewCoordinator(b.lease, nil, nil) }

func (b *blockedIncident) request() CancelRequest {
	return CancelRequest{OperationID: b.OperationID, Revision: 3}
}

func (b *blockedIncident) bytes(t *testing.T, record resetstate.Record) []byte {
	t.Helper()
	data, err := b.lease.Read(record)
	mustPreview(t, err)
	return data
}

// treeExcept returns every file under the fixture root except the named
// root-relative paths, so a test can say "nothing but the receipt changed".
func treeExcept(t *testing.T, root string, skip ...string) map[string][32]byte {
	t.Helper()
	all := treeBytes(t, root)
	for _, name := range skip {
		delete(all, filepath.Join(root, name))
	}
	return all
}

func TestCancelRetiresTheBlockedReceiptAndChangesNothingElse(t *testing.T) {
	for _, prior := range []bool{false, true} {
		name := "no earlier policy"
		if prior {
			name = "an earlier operation's policy"
		}
		t.Run(name, func(t *testing.T) {
			b := newBlockedIncident(t, prior)
			root := b.Fixture.Paths().Root
			receiptRel := filepath.Join("data", resetstate.Directory, string(resetstate.OperationRecord))
			before := treeExcept(t, root, receiptRel)
			policyBefore := b.bytes(t, resetstate.PolicyRecord)
			keyBefore, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
			mustPreview(t, err)

			op, err := b.coordinator().Cancel(t.Context(), b.request())
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if op.State != StateCanceled || op.Revision != 4 || op.Cancellation == nil || op.Cancellation.FromRevision != 3 || op.Cancellation.PriorBlocker != "recovery_scope_changed" {
				t.Fatalf("canceled operation = %+v", op)
			}
			if op.VerifiedComplete() || len(op.CompletedCategories()) != 0 {
				t.Fatal("a canceled receipt must never read as a completed reset")
			}
			for _, result := range op.Results {
				if result.Outcome != OutcomePending {
					t.Fatalf("a category result changed: %+v", result)
				}
			}
			if len(op.Blockers) != 0 {
				t.Fatalf("a canceled receipt keeps no blocker: %+v", op.Blockers)
			}

			// The durable receipt is the strictly validated terminal one.
			j := b.receipt(t, b.lease)
			if j.Operation.State != StateCanceled || j.Operation.Revision != 4 || !allResultsPending(j.Operation.Results) {
				t.Fatalf("durable receipt = %s rev %d", j.Operation.State, j.Operation.Revision)
			}
			// Nothing but the receipt moved: policy bytes, credentials and every
			// file in the installation, the vaults and the retained folders.
			if !bytes.Equal(policyBefore, b.bytes(t, resetstate.PolicyRecord)) {
				t.Fatal("cancellation edited the startup policy")
			}
			if key, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil || key != keyBefore {
				t.Fatalf("cancellation changed a credential: %v", err)
			}
			after := treeExcept(t, root, receiptRel)
			if len(before) != len(after) {
				t.Fatalf("cancellation changed the number of files: %d → %d", len(before), len(after))
			}
			for path, digest := range before {
				if after[path] != digest {
					t.Fatalf("cancellation changed %s", path)
				}
			}
			// The live process stays fenced: only a new process start may open stores.
			if release, err := b.lease.EnterRuntime(b.DataDir, b.DataDir); err == nil {
				release()
				t.Fatal("cancellation un-fenced the live process")
			} else if !errors.Is(err, resetstate.ErrRecoveryRequired) {
				t.Fatalf("EnterRuntime = %v, want ErrRecoveryRequired", err)
			}
		})
	}
}

func TestCancelDuplicateConfirmationIsHarmless(t *testing.T) {
	b := newBlockedIncident(t, true)
	first, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)
	receipt := b.bytes(t, resetstate.OperationRecord)
	for range 3 {
		again, err := NewCoordinator(b.lease, nil, nil).Cancel(t.Context(), b.request())
		if err != nil {
			t.Fatalf("a repeated confirmation failed: %v", err)
		}
		if again.State != StateCanceled || again.Revision != first.Revision || !again.UpdatedAt.Equal(first.UpdatedAt) {
			t.Fatalf("repeat = %+v, first = %+v", again, first)
		}
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("a repeated confirmation rewrote the receipt")
	}
}

func TestCancelRefusesStaleOrForeignIdentityWithoutWriting(t *testing.T) {
	b := newBlockedIncident(t, false)
	receipt := b.bytes(t, resetstate.OperationRecord)
	cases := []struct {
		name string
		req  CancelRequest
		want error
	}{
		{"an older revision", CancelRequest{OperationID: b.OperationID, Revision: 2}, ErrCancelStale},
		{"a newer revision", CancelRequest{OperationID: b.OperationID, Revision: 4}, ErrCancelStale},
		{"another operation's id", CancelRequest{OperationID: "another-operation-id", Revision: 3}, ErrCancelStale},
		{"no revision", CancelRequest{OperationID: b.OperationID}, ErrInvalidRequest},
		{"a malformed id", CancelRequest{OperationID: "../escape", Revision: 3}, ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := b.coordinator().Cancel(t.Context(), tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("Cancel = %v, want %v", err, tc.want)
			}
			if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
				t.Fatal("a refused cancel wrote the receipt")
			}
		})
	}
	t.Run("a canceled receipt named by a different revision", func(t *testing.T) {
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		mustPreview(t, err)
		if _, err := b.coordinator().Cancel(t.Context(), CancelRequest{OperationID: b.OperationID, Revision: 4}); !errors.Is(err, ErrCancelStale) {
			t.Fatalf("Cancel = %v, want ErrCancelStale", err)
		}
	})
}

func TestCancelRefusesWhenTheEvidenceNoLongerProvesAnything(t *testing.T) {
	t.Run("the interrupted-apply look-alike at revision 4", func(t *testing.T) {
		s := stageStartFresh(t)
		s.crashDuringFirstCategory(t, true)
		s.Fixture.EmptyRetainedFolder(t, "projects", true)
		lease := s.blockOnWorkspaceChange(t)
		receipt, err := lease.Read(resetstate.OperationRecord)
		mustPreview(t, err)
		_, err = NewCoordinator(lease, nil, nil).Cancel(t.Context(), CancelRequest{OperationID: s.OperationID, Revision: 4})
		var refused *CancelRefusedError
		if !errors.As(err, &refused) || refused.Reason != CancelNotPreApply || !errors.Is(err, ErrCancelNotAllowed) {
			t.Fatalf("Cancel = %v, want a not_provably_pre_apply refusal", err)
		}
		if after, _ := lease.Read(resetstate.OperationRecord); !bytes.Equal(receipt, after) {
			t.Fatal("a refused cancel wrote the receipt")
		}
	})
	t.Run("the policy names this operation", func(t *testing.T) {
		b := newBlockedIncident(t, false)
		mustPreview(t, writeStartupPolicy(b.lease, b.OperationID, []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now()))
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		var refused *CancelRefusedError
		if !errors.As(err, &refused) || refused.Reason != CancelPolicyThisOperation {
			t.Fatalf("Cancel = %v", err)
		}
	})
	t.Run("the workspace was put back, so nothing explains the block", func(t *testing.T) {
		b := newBlockedIncident(t, false)
		j := b.receipt(t, b.lease)
		// Same receipt, but the host now sees every reviewed folder as reviewed.
		j.Plan.Evidence.ProtectedDigests = freshDigests(t, j)
		persistForTest(t, b.lease, j)
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		var refused *CancelRefusedError
		if !errors.As(err, &refused) || refused.Reason != CancelWorkspaceUnchanged {
			t.Fatalf("Cancel = %v", err)
		}
	})
}

func freshDigests(t *testing.T, j *journal) []protectedDigest {
	t.Helper()
	out := make([]protectedDigest, len(j.Plan.Evidence.ProtectedDigests))
	for i, recorded := range j.Plan.Evidence.ProtectedDigests {
		digest, err := j.Plan.Evidence.digestRetained(t.Context(), recorded.Path)
		mustPreview(t, err)
		out[i] = protectedDigest{Path: recorded.Path, Digest: digest}
	}
	return out
}

// Many requests at once — separate coordinators over one lease, as separate
// handlers would be — produce exactly one transition. Run with -race.
func TestCancelConcurrentAttemptsWriteOnce(t *testing.T) {
	b := newBlockedIncident(t, true)
	const attempts = 12
	results := make([]Operation, attempts)
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = NewCoordinator(b.lease, nil, nil).Cancel(context.Background(), b.request())
		}()
	}
	close(start)
	wg.Wait()
	for i := range attempts {
		if errs[i] != nil {
			t.Fatalf("attempt %d: %v", i, errs[i])
		}
		if results[i].State != StateCanceled || results[i].Revision != 4 || !results[i].UpdatedAt.Equal(results[0].UpdatedAt) {
			t.Fatalf("attempt %d saw a different transition: %+v vs %+v", i, results[i], results[0])
		}
	}
	if j := b.receipt(t, b.lease); j.Operation.Revision != 4 {
		t.Fatalf("revision = %d: more than one transition was written", j.Operation.Revision)
	}
}

// A write that fails must never be reported as success, must latch uncertainty
// so nothing else in this process proceeds, and must leave the previous receipt
// for the next process to find intact.
func TestCancelWriteFailureIsNeverSuccess(t *testing.T) {
	b := newBlockedIncident(t, false)
	receipt := b.bytes(t, resetstate.OperationRecord)
	path := filepath.Join(b.DataDir, resetstate.Directory, string(resetstate.OperationRecord))
	c := b.coordinator()
	c.now = func() time.Time {
		// A deterministic non-regular target right before the write; no reliance
		// on chmod or privileges.
		mustPreview(t, os.Remove(path))
		mustPreview(t, os.Mkdir(path, 0o700))
		return time.Now()
	}
	op, err := c.Cancel(t.Context(), b.request())
	if !errors.Is(err, ErrAdmissionUncertain) || op.State == StateCanceled {
		t.Fatalf("a failed write claimed success: %v, %+v", err, op)
	}
	if !b.lease.Uncertain() {
		t.Fatal("a failed write did not latch uncertainty")
	}
	// Nothing in this process may proceed, even once the target is repaired.
	mustPreview(t, os.Remove(path))
	mustPreview(t, os.WriteFile(path, receipt, 0o600))
	if _, err := b.coordinator().Cancel(t.Context(), b.request()); !errors.Is(err, ErrAdmissionUncertain) {
		t.Fatalf("an uncertain process kept going: %v", err)
	}
	// A new process trusts only validated durable evidence: the blocked receipt.
	mustPreview(t, b.lease.Close())
	next, err := resetstate.Acquire(b.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = next.Close() })
	if err := RecoverBeforeStores(t.Context(), next, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("a blocked receipt must keep the next process in recovery: %v", err)
	}
	if got, _ := next.Read(resetstate.OperationRecord); !bytes.Equal(got, receipt) {
		t.Fatal("the failed cancel altered the durable receipt")
	}
	if _, err := NewCoordinator(next, nil, nil).Cancel(t.Context(), b.request()); err != nil {
		t.Fatalf("a clean process could not cancel after the failure: %v", err)
	}
}

// The receipt moved while the host was hashing: the decision was made from other
// bytes, so nothing is written.
func TestCancelRefusesWhenTheEvidenceMovesDuringTheDecision(t *testing.T) {
	for _, record := range []resetstate.Record{resetstate.PolicyRecord, resetstate.OperationRecord} {
		t.Run(string(record), func(t *testing.T) {
			b := newBlockedIncident(t, true)
			c := b.coordinator()
			c.now = func() time.Time {
				t.Error("the decision's evidence moved, yet a write was prepared")
				return time.Now()
			}
			// Between the two reads, a writer that ignores the lease changes a
			// byte of the evidence the decision was made from.
			c.betweenCancelReads = func() {
				if record == resetstate.PolicyRecord {
					mustPreview(t, b.lease.Replace(record, []byte(policyPriorOperationBytes)))
					return
				}
				j := b.receipt(t, b.lease)
				j.Operation.UpdatedAt = j.Operation.UpdatedAt.Add(time.Second)
				persistForTest(t, b.lease, j)
			}
			before := b.bytes(t, resetstate.OperationRecord)
			if _, err := c.Cancel(t.Context(), b.request()); !errors.Is(err, ErrCancelStale) {
				t.Fatalf("Cancel = %v, want ErrCancelStale", err)
			}
			if record == resetstate.PolicyRecord && !bytes.Equal(before, b.bytes(t, resetstate.OperationRecord)) {
				t.Fatal("a stale cancel rewrote the receipt")
			}
			if j := b.receipt(t, b.lease); j.Operation.State != StateBlocked {
				t.Fatalf("a stale cancel produced %s", j.Operation.State)
			}
		})
	}
}

// ---- startup after a cancellation ---------------------------------------------

func TestCanceledReceiptOpensTheNextTwoStartsWithoutApplyingAnything(t *testing.T) {
	b := newBlockedIncident(t, true)
	_, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)
	mustPreview(t, b.lease.Close())
	root := b.Fixture.Paths().Root
	keyBefore, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
	mustPreview(t, err)

	// Start 1: a fresh process takes the lease and runs the real recovery. The
	// canceled receipt is recognised, never re-run, and never rewritten.
	first, err := resetstate.Acquire(b.DataDir)
	mustPreview(t, err)
	if err := RecoverBeforeStores(t.Context(), first, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}); err != nil {
		t.Fatalf("start 1: recovery of a canceled receipt = %v, want nil", err)
	}
	receipt, policy := mustRead(t, first, resetstate.OperationRecord), mustRead(t, first, resetstate.PolicyRecord)
	snapshot := treeBytes(t, root)
	mustPreview(t, first.Close())

	// Start 2: the production host entry point. EnterRuntime pins the lease for
	// the life of this test process, so it is the last launch.
	second, err := BeforeStores(t.Context(), b.DataDir)
	if err != nil {
		t.Fatalf("start 2: BeforeStores = %v", err)
	}
	release, err := second.EnterRuntime(b.DataDir, b.DataDir)
	if err != nil {
		t.Fatalf("start 2: a canceled receipt must hand the runtime over: %v", err)
	}
	release()
	if !bytes.Equal(receipt, mustRead(t, second, resetstate.OperationRecord)) || !bytes.Equal(policy, mustRead(t, second, resetstate.PolicyRecord)) {
		t.Fatal("a later start rewrote the receipt or the policy")
	}
	now := treeBytes(t, root)
	if len(now) != len(snapshot) {
		t.Fatalf("a later start changed the number of files: %d → %d", len(snapshot), len(now))
	}
	for path, digest := range snapshot {
		if now[path] != digest {
			t.Fatalf("a later start changed %s", path)
		}
	}
	if key, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil || key != keyBefore {
		t.Fatalf("a start applied the old plan: %v", err)
	}
}

func mustRead(t *testing.T, lease *resetstate.Lease, record resetstate.Record) []byte {
	t.Helper()
	data, err := lease.Read(record)
	mustPreview(t, err)
	return data
}

// Cancellation widens nothing: the existing refusals around a retained receipt
// still hold for a canceled one.
func TestCanceledReceiptKeepsTheExistingFailClosedRules(t *testing.T) {
	t.Run("a policy that is no longer canonical", func(t *testing.T) {
		b := newBlockedIncident(t, true)
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		mustPreview(t, err)
		mustPreview(t, b.lease.Replace(resetstate.PolicyRecord, []byte(`{"schema_version":1}`)))
		mustPreview(t, b.lease.Close())
		next, err := resetstate.Acquire(b.DataDir)
		mustPreview(t, err)
		t.Cleanup(func() { _ = next.Close() })
		if err := RecoverBeforeStores(t.Context(), next, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}); !errors.Is(err, ErrPolicyInvalid) {
			t.Fatalf("recovery = %v, want ErrPolicyInvalid", err)
		}
	})
	t.Run("a policy with the receipt removed is never an empty installation", func(t *testing.T) {
		b := newBlockedIncident(t, true)
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		mustPreview(t, err)
		mustPreview(t, b.lease.Close())
		mustPreview(t, os.Remove(filepath.Join(b.DataDir, resetstate.Directory, string(resetstate.OperationRecord))))
		next, err := resetstate.Acquire(b.DataDir)
		mustPreview(t, err)
		t.Cleanup(func() { _ = next.Close() })
		if err := RecoverBeforeStores(t.Context(), next, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("recovery = %v, want ErrJournalInvalid", err)
		}
	})
	t.Run("a canceled receipt that no longer decodes", func(t *testing.T) {
		b := newBlockedIncident(t, false)
		_, err := b.coordinator().Cancel(t.Context(), b.request())
		mustPreview(t, err)
		j := b.receipt(t, b.lease)
		j.Operation.Revision = 9 // not a state the transition can produce
		data, err := json.Marshal(j)
		mustPreview(t, err)
		mustPreview(t, b.lease.Replace(resetstate.OperationRecord, data))
		mustPreview(t, b.lease.Close())
		next, err := resetstate.Acquire(b.DataDir)
		mustPreview(t, err)
		t.Cleanup(func() { _ = next.Close() })
		if err := RecoverBeforeStores(t.Context(), next, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("recovery = %v, want ErrJournalInvalid", err)
		}
	})
}

func TestCanceledReceiptCannotBeRevivedByTheOldRequest(t *testing.T) {
	b := newBlockedIncident(t, false)
	_, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)
	receipt := b.bytes(t, resetstate.OperationRecord)

	// Replaying the exact preview and request that staged the old plan reads the
	// durable receipt back; it is answered without a write and without re-staging.
	j := b.receipt(t, b.lease)
	op, err := NewCoordinator(b.lease, nil, &fixtureLifecycle{}).Stage(t.Context(), ExecuteRequest{
		PreviewID: j.Plan.Preview.ID, RequestID: b.RequestID, Confirmation: "RESET", ConfirmAgentsFolder: j.Plan.Evidence.AgentsFolder,
	})
	if err != nil || op.State != StateCanceled {
		t.Fatalf("replay = %+v, %v", op, err)
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("replaying the old request rewrote the receipt")
	}
	// A different request for the old preview is a new reviewed operation the
	// coordinator cannot validate without an owner, so it never becomes the old plan.
	if _, err := NewCoordinator(b.lease, nil, &fixtureLifecycle{}).Stage(t.Context(), ExecuteRequest{
		PreviewID: j.Plan.Preview.ID, RequestID: "another-request", Confirmation: "RESET", ConfirmAgentsFolder: j.Plan.Evidence.AgentsFolder,
	}); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("a new request against the old preview = %v", err)
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("a refused request rewrote the receipt")
	}
}

// Only a newly reviewed, explicitly confirmed plan may replace a canceled receipt.
func TestANewReviewedResetReplacesACanceledReceipt(t *testing.T) {
	b := newBlockedIncident(t, true)
	_, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)
	mustPreview(t, b.lease.Close())

	relaunched, err := resetstate.Acquire(b.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = relaunched.Close() })
	mustPreview(t, RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()}))

	paths := b.Fixture.Paths()
	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), b.Fixture.Secrets())
	mustPreview(t, cfg.Load())
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	mustPreview(t, err)
	folders, err := workspace.NewFileStore(paths.Workspaces)
	mustPreview(t, err)
	owners := &Owners{
		DataDir: paths.DataDir, Config: cfg, Database: db, Workspaces: folders,
		Setup:          onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")),
		Vaults:         vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
		CheckLifecycle: func(context.Context) []Blocker { return nil },
	}
	planner := NewPlanner(func() Owners { return *owners })
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
	mustPreview(t, err)
	life := &fixtureLifecycle{drain: func(context.Context) error { return errors.Join(folders.Close(), db.Close()) }}
	staged, err := NewCoordinator(relaunched, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "new-reviewed-request", Confirmation: "RESET",
	})
	if err != nil || staged.ID == b.OperationID || staged.State != StateAwaitingRestart {
		t.Fatalf("a new reviewed reset did not replace the canceled receipt: %+v, %v", staged, err)
	}
	if _, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), b.OperationID); !errors.Is(err, ErrOperationNotFound) {
		t.Fatal("the canceled operation remained ambiguous after replacement:", err)
	}
}

// Receipts that are not the one terminal canceled shape are refused by the
// strict decoder, so nothing can mint a canceled receipt by hand.
func TestCanceledReceiptValidationIsStrict(t *testing.T) {
	canceled := func(t *testing.T) *journal {
		j := incidentReceipt(t)
		j.Operation.State, j.Operation.Revision, j.Operation.Blockers = StateCanceled, 4, []Blocker{}
		j.Operation.Cancellation = &Cancellation{FromRevision: 3, PriorBlocker: "recovery_scope_changed"}
		return j
	}
	if _, err := encodeJournal(canceled(t)); err != nil {
		t.Fatalf("the canceled shape must validate: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*journal)
	}{
		{"no cancellation record", func(j *journal) { j.Operation.Cancellation = nil }},
		{"revision 5", func(j *journal) { j.Operation.Revision = 5 }},
		{"revision 3", func(j *journal) { j.Operation.Revision = 3 }},
		{"retired from revision 4", func(j *journal) { j.Operation.Cancellation.FromRevision = 4 }},
		{"another prior blocker", func(j *journal) { j.Operation.Cancellation.PriorBlocker = "recovery_unavailable" }},
		{"a blocker still present", func(j *journal) {
			j.Operation.Blockers = []Blocker{{Code: "recovery_scope_changed", Message: "m", Recovery: "r"}}
		}},
		{"a category applied", func(j *journal) {
			j.Operation.Results[0].Outcome = OutcomeCompleted
			j.Operation.Results[0].Checks[0].Outcome = OutcomeCompleted
		}},
		{"selected-data intent", func(j *journal) { j.Operation.Intent, j.Plan.Preview.Intent = IntentSelectedData, IntentSelectedData }},
		{"an archive journal", func(j *journal) { j.Archive = &archiveJournal{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := canceled(t)
			tc.mutate(j)
			if _, err := encodeJournal(j); !errors.Is(err, ErrJournalInvalid) {
				t.Fatalf("encode = %v, want ErrJournalInvalid", err)
			}
		})
	}
	t.Run("a cancellation record on a blocked receipt", func(t *testing.T) {
		j := incidentReceipt(t)
		j.Operation.Cancellation = &Cancellation{FromRevision: 3, PriorBlocker: "recovery_scope_changed"}
		if _, err := encodeJournal(j); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("encode = %v, want ErrJournalInvalid", err)
		}
	})
}
