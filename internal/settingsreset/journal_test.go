package settingsreset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestJournalRefusesCorruptUnsupportedOrEditedEvidenceWithoutEffects(t *testing.T) {
	f, _, c, life := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategorySetupSteps)
	_, err := c.Stage(t.Context(), req)
	mustPreview(t, err)
	valid, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	var pretty bytes.Buffer
	mustPreview(t, json.Indent(&pretty, valid, "", "  "))
	cases := [][]byte{
		[]byte(""), []byte(`{"version":99}`), append(bytes.Clone(valid), []byte(` {}`)...), pretty.Bytes(),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"Version":1`), 1),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"extra":"PRIVATE"`), 1),
	}
	for _, mutate := range []func(*journal){
		func(j *journal) { j.Version++ },
		func(j *journal) { j.Operation.State = StateCompleted },
		func(j *journal) { j.Plan.Targets[0].Path = filepath.Join(f.Paths().Workspaces, "retained") },
		func(j *journal) { j.Plan.Targets[0].Kind = "remove_root" },
		func(j *journal) { j.Operation.Results[0].Outcome = OutcomeCompleted },
		func(j *journal) { j.Plan.Preview.Selected = append(j.Plan.Preview.Selected, CategoryAgents) },
	} {
		j, err := decodeJournal(valid)
		mustPreview(t, err)
		mutate(j)
		data, err := json.Marshal(j)
		mustPreview(t, err)
		cases = append(cases, data)
	}
	path := filepath.Join(f.Paths().DataDir, resetstate.Directory, "operation.json")
	for i, data := range cases {
		mustPreview(t, os.WriteFile(path, data, 0o600))
		if _, err := c.Status(t.Context(), v.OperationID); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("case %d accepted: %v", i, err)
		}
		if _, err := c.Stage(t.Context(), req); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("case %d admitted: %v", i, err)
		}
		after, err := os.ReadFile(path)
		mustPreview(t, err)
		if !bytes.Equal(data, after) {
			t.Fatal("reading repaired/rewrote corrupt evidence")
		}
	}
	if life.drains != 1 {
		t.Fatal("corrupt receipt retried draining")
	}
	f.AssertPreserved(t)
}

func TestJournalMissingAfterAdmissionCannotBecomeAnotherOperation(t *testing.T) {
	f, _, c, _ := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategorySetupSteps)
	_, err := c.Stage(t.Context(), req)
	mustPreview(t, err)
	mustPreview(t, os.Remove(filepath.Join(f.Paths().DataDir, resetstate.Directory, "operation.json")))
	other := NewCoordinator(c.lease, c.planner, &fixtureLifecycle{})
	if _, err := other.Status(t.Context(), v.OperationID); !errors.Is(err, ErrJournalInvalid) {
		t.Fatal("lost admitted receipt reported ordinary absence:", err)
	}
	if _, err := other.Stage(t.Context(), req); !errors.Is(err, ErrJournalInvalid) {
		t.Fatal("lost admitted receipt became a fresh wipe:", err)
	}
}

func TestJournalPreparingReopenedUnderNewLeaseIsInterruptedReadOnly(t *testing.T) {
	_, _, c, _ := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategorySetupSteps)
	plan, err := c.planner.validatedPlan(t.Context(), req.PreviewID)
	mustPreview(t, err)
	j := newJournal(plan, req.RequestID, c.lease.Identity(), c.now().UTC())
	data, err := encodeJournal(j)
	mustPreview(t, err)
	mustPreview(t, c.lease.Replace(resetstate.OperationRecord, data))
	// Metadata reopen only: no applier or application constructors run, and no
	// claim is made that releasing an unpinned test lease is a host relaunch.
	path := c.lease.Path()
	mustPreview(t, c.lease.Close())
	lease, err := resetstate.Acquire(path)
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, lease.Close()) })
	op, err := NewCoordinator(lease, nil, nil).Status(t.Context(), v.OperationID)
	mustPreview(t, err)
	if op.State != StateInterrupted {
		t.Fatal("old preparing worker looked alive after reopening")
	}
	after, err := lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	if !bytes.Equal(data, after) {
		t.Fatal("status retried or rewrote interrupted admission")
	}
}

func TestJournalUsesRevalidatedPublicEvidence(t *testing.T) {
	_, _, c, life := coordinatorFixture(t)
	_, req := coordinatorRequest(t, c, CategorySetupSteps)
	// Inflate only immutable reviewed presentation evidence (not binding paths)
	// to test the journal envelope limit after preview's own bound passed.
	c.planner.mu.Lock()
	plan := c.planner.plans[req.PreviewID]
	plan.Preview.Categories[0].Description = string(bytes.Repeat([]byte{'x'}, resetstate.MaxRecordBytes))
	c.planner.plans[req.PreviewID] = plan
	c.planner.mu.Unlock()
	// validatedPlan regenerates public evidence rather than trusting the cached
	// mutable display copy; this corruption must not enter the journal.
	_, err := c.Stage(t.Context(), req)
	mustPreview(t, err)
	if life.drains != 1 {
		t.Fatal("normal scope failed to stage")
	}
	data, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	if len(data) > resetstate.MaxRecordBytes {
		t.Fatal("oversized cached presentation escaped revalidation")
	}
}

func TestJournalEnvelopeLimitRefusesBeforeFence(t *testing.T) {
	f, owners, c, life := coordinatorFixture(t)
	for i := range 45 {
		path := filepath.Join(f.Paths().Vaults, string(bytes.Repeat([]byte{'v'}, 200))+fmt.Sprintf("-%03d.orivault", i))
		_, err := owners.Database.Exec(`INSERT INTO vaults (id, name, description, file_path, created_at, updated_at) VALUES (?, ?, '', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, fmt.Sprintf("fixture-%d", i), fmt.Sprintf("fixture-%d", i), path)
		mustPreview(t, err)
	}
	_, req := coordinatorRequest(t, c, CategorySetupSteps)
	if _, err := c.Stage(t.Context(), req); !errors.Is(err, resetstate.ErrRecordLimit) {
		t.Fatal("oversized journal did not fail before admission:", err)
	}
	if life.fenced || life.drains != 0 {
		t.Fatal("oversized receipt froze the runtime")
	}
}
