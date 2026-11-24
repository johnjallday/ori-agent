package settingsreset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var (
	// ErrCancelStale: the request names an operation or revision that is not the
	// receipt now on disk (an old tab, a replaced receipt). Nothing was written.
	ErrCancelStale = errors.New("this reset receipt changed since you opened this page; reload it to see its current state")
	// ErrCancelNotAllowed: the evidence no longer proves the reset is safe to
	// cancel. Nothing was written. See CancelRefusedError for the reason.
	ErrCancelNotAllowed = errors.New("this reset cannot be canceled because Ori cannot prove it changed nothing")
)

// CancelRefusedError carries the stable reason a cancel was refused. It matches
// ErrCancelNotAllowed with errors.Is.
type CancelRefusedError struct{ Reason CancelReason }

func (e *CancelRefusedError) Error() string {
	return fmt.Sprintf("%s (%s)", ErrCancelNotAllowed.Error(), e.Reason)
}

func (e *CancelRefusedError) Is(target error) bool { return target == ErrCancelNotAllowed }

// CancelRequest names the exact receipt the person was looking at. It carries no
// path, category or reason: the server decides eligibility itself, so a forged
// request can at most ask for a decision the server then makes alone.
type CancelRequest struct {
	OperationID string `json:"operation_id"`
	Revision    uint64 `json:"revision"`
}

// Cancel retires a blocked, provably unapplied Start Fresh by writing one
// strictly validated terminal canceled receipt. It is the only receipt
// transition recovery hosts perform apart from recovery itself, and it:
//
//   - runs under the installation lease and its admission gate, so a second
//     process cannot hold the lease and a second request in this process waits;
//   - reloads the receipt through the strict decoder and requires the exact
//     operation id and revision the caller saw (a stale tab, a replaced receipt
//     or a different revision writes nothing);
//   - recomputes the full eligibility decision against the current host state
//     immediately before writing, rather than trusting any earlier verdict;
//   - changes nothing else: no category, policy, setting, vault, credential or
//     retained folder is written, restored or deleted, and the live process is
//     not un-fenced — only a fresh process start may open stores.
//
// A repeated request for an already canceled receipt is answered from the
// durable evidence without writing. A failed or uncertain write is never
// reported as success.
func (c *Coordinator) Cancel(ctx context.Context, req CancelRequest) (Operation, error) {
	if !validID(req.OperationID) || req.Revision == 0 {
		return Operation{}, ErrInvalidRequest
	}
	if c.lease == nil {
		return Operation{}, ErrLifecycleUnavailable
	}
	var operation Operation
	err := c.lease.WithAdmission(ctx, func() error {
		j, err := c.load()
		if err != nil {
			return err
		}
		if j == nil || j.Operation.ID != req.OperationID {
			return ErrCancelStale
		}
		// Duplicate confirmation: this very block was already retired.
		if j.Operation.State == StateCanceled {
			if j.Operation.Cancellation == nil || j.Operation.Cancellation.FromRevision != req.Revision {
				return ErrCancelStale
			}
			if c.lease.Uncertain() {
				return ErrAdmissionUncertain
			}
			operation = c.publicOperation(j)
			return nil
		}
		if j.Operation.Revision != req.Revision {
			return ErrCancelStale
		}
		if c.lease.Uncertain() {
			return ErrAdmissionUncertain
		}
		receiptBefore, policyBefore, err := readCancelEvidence(c.lease)
		if err != nil {
			return err
		}
		verdict, err := EvaluateCancel(ctx, c.lease, RecoveryOptions{DataDir: c.lease.Path()})
		if err != nil {
			return err
		}
		if !verdict.Eligible {
			return &CancelRefusedError{Reason: verdict.Reason}
		}
		// The host observation can take a while on a large retained tree. The
		// proof rests on the receipt and policy bytes alone, so require that both
		// are still exactly what the decision was made from. Only a writer that
		// ignores the installation lease could change them from here to the rename.
		if c.betweenCancelReads != nil {
			c.betweenCancelReads()
		}
		receiptAfter, policyAfter, err := readCancelEvidence(c.lease)
		if err != nil {
			return err
		}
		if !bytes.Equal(receiptBefore, receiptAfter) || !bytes.Equal(policyBefore, policyAfter) {
			return ErrCancelStale
		}
		canceled, err := cancelReceipt(j, verdict, c.now())
		if err != nil {
			return err
		}
		if err := c.persist(canceled); err != nil {
			operation = uncertainOperation(j.Operation)
			return ErrAdmissionUncertain
		}
		operation = c.publicOperation(canceled)
		return nil
	})
	return operation, err
}

func readCancelEvidence(lease *resetstate.Lease) (receipt, policy []byte, err error) {
	if receipt, err = lease.Read(resetstate.OperationRecord); err != nil || receipt == nil {
		return nil, nil, ErrJournalInvalid
	}
	if policy, err = lease.Read(resetstate.PolicyRecord); err != nil {
		return nil, nil, ErrPolicyInvalid
	}
	return receipt, policy, nil
}

// cancelReceipt builds the canceled receipt from a blocked one. Only the state,
// revision, blocker list, cancellation record and timestamp change: every
// category result stays exactly as it was, still pending, because nothing ran.
func cancelReceipt(blocked *journal, verdict CancelVerdict, now time.Time) (*journal, error) {
	if !verdict.Eligible || verdict.OperationID != blocked.Operation.ID || verdict.Revision != blocked.Operation.Revision {
		return nil, ErrCancelStale
	}
	data, err := encodeJournal(blocked)
	if err != nil {
		return nil, err
	}
	canceled, err := decodeJournal(data) // an independent copy; the loaded receipt stays untouched
	if err != nil {
		return nil, err
	}
	canceled.Operation.Cancellation = &Cancellation{
		FromRevision: blocked.Operation.Revision,
		PriorBlocker: blocked.Operation.Blockers[0].Code,
	}
	canceled.Operation.State = StateCanceled
	canceled.Operation.Revision++
	canceled.Operation.Blockers = []Blocker{}
	canceled.Operation.UpdatedAt = now.UTC()
	return canceled, nil
}
