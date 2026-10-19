package filejanitor

import "os"

const CodeInitialReceiptUnavailable = "initial_receipt_unavailable"

func initialReceiptUnavailable() error {
	return setupErr(CodeInitialReceiptUnavailable, "The accepted initial result cannot be confirmed from retained receipts. Nothing was rescanned. Open Review and choose Scan now explicitly if you want another scan.", "", nil)
}

// preparedScanReceipt is stricter than ordinary Scan now's corrupt/missing-state
// fallback. An old confirmation cannot turn loss/pruning of its receipt into
// another scan. The digest is only a refusal fence, never a substitute result.
func (s *Service) preparedScanReceipt(workspaceID string, preparation SetupPreparation, freshAttempt ...bool) (ScanOperationReceipt, bool, error) {
	receipt, found, err := s.ScanOperationReceipt(workspaceID, preparation.ID+":scan")
	if err != nil {
		return receipt, false, err
	}
	if found {
		if receipt.RootID == "" || receipt.PrivacyMode != ContentModeMetadataOnly || receipt.CompletedAt.IsZero() || receipt.EligibleCount < 0 || receipt.IneligibleCount < 0 ||
			(receipt.Outcome != ScanOutcomeBatch && receipt.Outcome != ScanOutcomeNoEligible) ||
			(receipt.Outcome == ScanOutcomeNoEligible && (receipt.BatchID != "" || receipt.EligibleCount != 0)) ||
			(receipt.Outcome == ScanOutcomeBatch && (receipt.BatchID == "" || receipt.EligibleCount == 0)) {
			return ScanOperationReceipt{}, false, initialReceiptUnavailable()
		}
		if receipt.Outcome == ScanOutcomeBatch {
			state, err := s.store.LoadScanState(workspaceID)
			if err != nil {
				return ScanOperationReceipt{}, false, err
			}
			present := false
			for _, batch := range state.Batches {
				if batch.ID == receipt.BatchID && batch.WorkspaceID == workspaceID {
					present = true
					break
				}
			}
			if !present {
				return ScanOperationReceipt{}, false, initialReceiptUnavailable()
			}
		}
		digest, err := setupDigest(receipt)
		if err != nil {
			return receipt, false, err
		}
		if preparation.ScanReceiptDigest != "" && preparation.ScanReceiptDigest != digest {
			return ScanOperationReceipt{}, false, initialReceiptUnavailable()
		}
		return receipt, true, nil
	}
	fresh := len(freshAttempt) == 1 && freshAttempt[0]
	if preparation.ScanReceiptDigest != "" || (!preparation.ScanDispatchedAt.IsZero() && !fresh) {
		return receipt, false, initialReceiptUnavailable()
	}
	path, err := s.store.scanStatePath(workspaceID)
	if err != nil {
		return receipt, false, err
	}
	// A record without this acknowledgment may be a different/manual scan or
	// a corrupt/pruned result. Do not infer a fresh initial attempt from it.
	if _, err := os.Stat(path); err == nil { // #nosec G304 -- resolved workspace folder plus fixed scan-state filename
		return receipt, false, initialReceiptUnavailable()
	} else if !os.IsNotExist(err) {
		return receipt, false, err
	}
	return receipt, false, nil
}
