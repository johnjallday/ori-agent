package settingsreset

import (
	"errors"
	"testing"
)

// Canonical policy.json bytes as the shipped encoder wrote them, each paired
// with the incident receipt's operation id ("compat-operation-id"). The policy
// is a separate, merged record: it says which operation last touched it, never
// that the receipt applied anything.
const (
	// Written by an earlier, completed reset — the reported incident.
	policyPriorOperationBytes = `{"schema_version":1,"operation_id":"earlier-operation-id","suppress_workspace_adoption":true,"suppress_agent_rehydration":true,"suppress_profile_seed":true,"suppress_external_mcp_import":true,"updated_at":"2026-02-01T12:00:00Z"}`
	// Written by this operation's own first recovery launch, which then died
	// before the `applying` marker became durable.
	policyThisOperationBytes = `{"schema_version":1,"operation_id":"compat-operation-id","suppress_workspace_adoption":true,"suppress_agent_rehydration":true,"suppress_profile_seed":true,"suppress_external_mcp_import":true,"updated_at":"2026-03-01T12:30:00Z"}`
)

func TestPolicyProvenanceFromStoredBytes(t *testing.T) {
	cases := []struct {
		name  string
		bytes string
		want  PolicyProvenance
	}{
		{"an earlier operation's policy", policyPriorOperationBytes, PolicyPriorOperation},
		{"this operation's policy", policyThisOperationBytes, PolicyThisOperation},
		{"not canonical: whitespace", `{ "schema_version":1,"operation_id":"earlier-operation-id","suppress_workspace_adoption":true,"updated_at":"2026-02-01T12:00:00Z"}`, PolicyInvalid},
		{"not canonical: unknown field", `{"schema_version":1,"operation_id":"earlier-operation-id","suppress_workspace_adoption":true,"suppress_agent_rehydration":false,"suppress_profile_seed":false,"suppress_external_mcp_import":false,"updated_at":"2026-02-01T12:00:00Z","extra":1}`, PolicyInvalid},
		{"no suppression flag at all", `{"schema_version":1,"operation_id":"earlier-operation-id","suppress_workspace_adoption":false,"suppress_agent_rehydration":false,"suppress_profile_seed":false,"suppress_external_mcp_import":false,"updated_at":"2026-02-01T12:00:00Z"}`, PolicyInvalid},
		{"future schema version", `{"schema_version":2,"operation_id":"earlier-operation-id","suppress_workspace_adoption":true,"suppress_agent_rehydration":false,"suppress_profile_seed":false,"suppress_external_mcp_import":false,"updated_at":"2026-02-01T12:00:00Z"}`, PolicyInvalid},
		{"empty", ``, PolicyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := decodeStartupPolicy([]byte(tc.bytes))
			if tc.want == PolicyInvalid && !errors.Is(err, ErrPolicyInvalid) {
				t.Fatalf("decode = %v, want ErrPolicyInvalid", err)
			}
			if got := policyProvenance(policy, err, "compat-operation-id"); got != tc.want {
				t.Fatalf("provenance = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("no policy record", func(t *testing.T) {
		if got := policyProvenance(StartupPolicy{}, nil, "compat-operation-id"); got != PolicyNone {
			t.Fatalf("provenance = %q, want %q", got, PolicyNone)
		}
	})
}
