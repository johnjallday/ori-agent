package settingsreset

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// TestRecordCancelRoundTripEvidence is the backend demo for the cancellation
// transition. It only runs when ORI_RESET_EVIDENCE_DIR names a directory, and
// then writes one sanitized JSON document: the receipt at each step, the two
// startup decisions after cancellation, and the digest of every file in a
// synthetic installation before and after. Nothing here copies a user's data.
//
//	ORI_RESET_EVIDENCE_DIR=$PWD/tasks/evidence-reset-workspace-change-recovery \
//	  go test ./internal/settingsreset -run TestRecordCancelRoundTripEvidence -count=1
func TestRecordCancelRoundTripEvidence(t *testing.T) {
	dir := os.Getenv("ORI_RESET_EVIDENCE_DIR") // read before the fixture clears the environment
	if dir == "" {
		t.Skip("set ORI_RESET_EVIDENCE_DIR to record the round trip")
	}
	b := newBlockedIncident(t, true)
	root := b.Fixture.Paths().Root

	type receipt struct {
		State    OperationState `json:"state"`
		Revision uint64         `json:"revision"`
		Blockers []string       `json:"blocker_codes"`
		Pending  int            `json:"categories_pending"`
		Total    int            `json:"categories_total"`
	}
	summarize := func(lease *resetstate.Lease) receipt {
		j := b.receipt(t, lease)
		summary := receipt{State: j.Operation.State, Revision: j.Operation.Revision, Total: len(j.Operation.Results), Blockers: []string{}}
		for _, blocker := range j.Operation.Blockers {
			summary.Blockers = append(summary.Blockers, blocker.Code)
		}
		for _, result := range j.Operation.Results {
			if result.Outcome == OutcomePending {
				summary.Pending++
			}
		}
		return summary
	}
	digests := func() map[string]string {
		out := map[string]string{}
		for path, digest := range treeBytes(t, root) {
			rel, err := filepath.Rel(root, path)
			mustPreview(t, err)
			if strings.Contains(rel, resetstate.Directory) || strings.HasSuffix(rel, "-wal") {
				continue // the receipt is shown separately; WAL is checkpoint noise
			}
			out[filepath.ToSlash(rel)] = hex.EncodeToString(digest[:])[:12]
		}
		return out
	}

	evidence := map[string]any{"installation": "synthetic fixture; no user data"}
	evidence["1_blocked_incident"] = summarize(b.lease)
	verdict := evaluate(t, b.stagedStartFresh, b.lease)
	evidence["2_verdict_before_cancel"] = map[string]any{
		"eligible": verdict.Eligible, "reason": verdict.Reason, "workspace": verdict.Workspace,
		"policy_provenance": verdict.PolicyProvenance, "revision": verdict.Revision,
	}
	filesBefore := digests()
	policyBefore := string(b.bytes(t, resetstate.PolicyRecord))
	keyBefore, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
	mustPreview(t, err)

	op, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)
	evidence["3_after_cancel"] = summarize(b.lease)
	evidence["3_cancel_result"] = map[string]any{
		"state": op.State, "revision": op.Revision, "verified_complete": op.VerifiedComplete(),
		"completed_categories": len(op.CompletedCategories()),
	}
	mustPreview(t, b.lease.Close())

	first, err := resetstate.Acquire(b.DataDir)
	mustPreview(t, err)
	startOne := RecoverBeforeStores(t.Context(), first, RecoveryOptions{DataDir: b.DataDir, SecretStore: b.Fixture.Secrets()})
	evidence["4_startup_1"] = map[string]any{"recovery_error": errString(startOne), "receipt": summarize(first)}
	mustPreview(t, first.Close())

	second, err := BeforeStores(t.Context(), b.DataDir)
	mustPreview(t, err)
	release, enterErr := second.EnterRuntime(b.DataDir, b.DataDir)
	if enterErr == nil {
		release()
	}
	evidence["5_startup_2"] = map[string]any{"runtime_admitted": enterErr == nil, "receipt": summarize(second)}

	filesAfter := digests()
	var changed []string
	for path, before := range filesBefore {
		if filesAfter[path] != before {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	keyAfter, err := b.Fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey)
	mustPreview(t, err)
	evidence["6_untouched"] = map[string]any{
		"files_compared":        len(filesBefore),
		"files_changed":         changed,
		"policy_bytes_equal":    policyBefore == string(mustRead(t, second, resetstate.PolicyRecord)),
		"provider_key_equal":    keyBefore == keyAfter,
		"file_digests_after":    filesAfter,
		"emptied_by_the_person": "projects/ (outside Ori, before the first restart)",
	}

	mustPreview(t, os.MkdirAll(dir, 0o750))
	data, err := json.MarshalIndent(evidence, "", "  ")
	mustPreview(t, err)
	mustPreview(t, os.WriteFile(filepath.Join(dir, "02-cancel-round-trip.json"), append(data, '\n'), 0o600))
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
