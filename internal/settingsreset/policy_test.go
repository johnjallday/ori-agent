package settingsreset

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestStartupPolicyIsBoundedCanonicalAndMergesSuppressions(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	lease, err := resetstate.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	now := time.Now().UTC()
	if err := writeStartupPolicy(lease, "operation-1", []CategoryID{CategoryAgents}, now); err != nil {
		t.Fatal(err)
	}
	if err := writeStartupPolicy(lease, "operation-2", []CategoryID{CategoryAppRecords}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	policy, err := ReadStartupPolicy(lease)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.SuppressAgentRehydration || !policy.SuppressWorkspaceAdoption || !policy.SuppressProfileSeed || !policy.SuppressExternalMCPImport || policy.OperationID != "operation-2" {
		t.Fatalf("merged policy = %#v", policy)
	}
	data, err := lease.Read(resetstate.PolicyRecord)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) > resetstate.MaxRecordBytes {
		t.Fatalf("policy size = %d", len(data))
	}
}

func TestStartupPolicyRefusesNonCanonicalOrUnknownEvidence(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	for name, payload := range map[string][]byte{
		"reformatted": []byte(" {\"schema_version\":1}\n"),
		"unknown":     []byte(`{"schema_version":1,"operation_id":"op","suppress_workspace_adoption":true,"suppress_agent_rehydration":false,"suppress_profile_seed":false,"suppress_external_mcp_import":false,"updated_at":"2025-01-01T00:00:00Z","extra":true}`),
	} {
		t.Run(name, func(t *testing.T) {
			lease, err := resetstate.Acquire(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.Close() }()
			if err := lease.Replace(resetstate.PolicyRecord, payload); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadStartupPolicy(lease); !errors.Is(err, ErrPolicyInvalid) {
				t.Fatalf("ReadStartupPolicy = %v", err)
			}
			before, _ := lease.Read(resetstate.PolicyRecord)
			if !bytes.Equal(before, payload) {
				t.Fatal("invalid policy was rewritten")
			}
		})
	}
}
