package settingsreset

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var ErrPolicyInvalid = errors.New("reset import-suppression policy is invalid or unsupported; preserve it and recover before opening stores")

// StartupPolicy prevents retained artifacts from silently recreating reset
// state. It is not a deletion plan and contains no filesystem paths or secrets.
// A later explicit import/reattachment flow may retire individual suppressions.
type StartupPolicy struct {
	SchemaVersion             int       `json:"schema_version"`
	OperationID               string    `json:"operation_id"`
	SuppressWorkspaceAdoption bool      `json:"suppress_workspace_adoption"`
	SuppressAgentRehydration  bool      `json:"suppress_agent_rehydration"`
	SuppressProfileSeed       bool      `json:"suppress_profile_seed"`
	SuppressExternalMCPImport bool      `json:"suppress_external_mcp_import"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

func ReadStartupPolicy(lease *resetstate.Lease) (StartupPolicy, error) {
	if lease == nil {
		return StartupPolicy{}, nil
	}
	data, err := lease.Read(resetstate.PolicyRecord)
	if err != nil {
		return StartupPolicy{}, ErrPolicyInvalid
	}
	if data == nil {
		return StartupPolicy{}, nil
	}
	var policy StartupPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return StartupPolicy{}, ErrPolicyInvalid
	}
	canonical, err := encodeStartupPolicy(policy)
	if err != nil || !bytes.Equal(data, canonical) {
		return StartupPolicy{}, ErrPolicyInvalid
	}
	return policy, nil
}

func writeStartupPolicy(lease *resetstate.Lease, operationID string, selected []CategoryID, now time.Time) error {
	policy, err := ReadStartupPolicy(lease)
	if err != nil {
		return err
	}
	policy.SchemaVersion = SchemaVersion
	policy.OperationID = operationID
	policy.UpdatedAt = now.UTC()
	for _, id := range selected {
		switch id {
		case CategoryAgents:
			policy.SuppressAgentRehydration = true
		case CategoryAppRecords:
			policy.SuppressWorkspaceAdoption = true
			policy.SuppressProfileSeed = true
			policy.SuppressExternalMCPImport = true
		}
	}
	if !policy.SuppressWorkspaceAdoption && !policy.SuppressAgentRehydration && !policy.SuppressProfileSeed && !policy.SuppressExternalMCPImport {
		return nil
	}
	data, err := encodeStartupPolicy(policy)
	if err != nil {
		return err
	}
	return lease.Replace(resetstate.PolicyRecord, data)
}

func encodeStartupPolicy(policy StartupPolicy) ([]byte, error) {
	if policy.SchemaVersion != SchemaVersion || !validID(policy.OperationID) || policy.UpdatedAt.IsZero() ||
		(!policy.SuppressWorkspaceAdoption && !policy.SuppressAgentRehydration && !policy.SuppressProfileSeed && !policy.SuppressExternalMCPImport) {
		return nil, ErrPolicyInvalid
	}
	data, err := json.Marshal(policy)
	if err != nil || len(data) > resetstate.MaxRecordBytes {
		return nil, ErrPolicyInvalid
	}
	return data, nil
}
