package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementRequest_ExplicitModeCannotFallThroughToOrdinaryTools(t *testing.T) {
	for _, raw := range []string{`{"arrangement":null}`, `{"Arrangement":null}`, `{"Arrangement":{"share_held_metadata":true}}`, `{"arrangement":{},"arrangement":null}`, `{"arrangement":null,"arrangement":{"share_held_metadata":true}}`} {
		var request HomeAssistantAskRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatalf("ambiguous mode accepted: %s", raw)
		}
	}
	var ordinary HomeAssistantAskRequest
	if err := json.Unmarshal([]byte(`{"Prompt":"ordinary\ndiscussion","legacy_unknown":true}`), &ordinary); err != nil || ordinary.Arrangement != nil || ordinary.Prompt != "ordinary\ndiscussion" {
		t.Fatal("ordinary Ask compatibility changed", err)
	}
	var selected HomeAssistantAskRequest
	if err := json.Unmarshal([]byte(`{"arrangement":{"share_held_metadata":true}}`), &selected); err != nil || selected.Arrangement == nil || !selected.Arrangement.ShareHeldMetadata {
		t.Fatal("explicit mode lost", err)
	}
}

func TestArrangementRequest_OnlyExplicitBooleanSharingField(t *testing.T) {
	for _, raw := range []string{`{}`, `{"share_held_metadata":false}`, `{"share_held_metadata":true}`, `{"share_held_metadata":true,"expected_provider":"ollama","expected_model":"model"}`} {
		var request HomeAssistantArrangementRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(raw, err)
		}
		if request.ShareHeldMetadata != strings.Contains(raw, "true") {
			t.Fatal("sharing choice changed")
		}
	}
	for _, raw := range []string{`{"expected_model":"model"}`, `{"expected_provider":"ollama"}`, `{"expected_provider":null,"expected_model":"model"}`, `{"expected_provider":"ollama","expected_model":""}`, `{"expected_provider":"ollama","expected_model":"a","expected_model":"b"}`, `null`, `[]`, `true`, `{"Share_Held_Metadata":true}`, `{"share_held_metadata":null}`, `{"share_held_metadata":"true"}`, `{"share_held_metadata":true,"share_held_metadata":false}`, `{"share_held_metadata":true,"approved":true}`, `{"share_held_metadata":true} {}`} {
		var request HomeAssistantArrangementRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatalf("invalid consent accepted: %s", raw)
		}
	}
}

func TestArrangementFailure_ClassifiesRecoveryWithoutLeakingErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		stage, code string
	}{
		{"consent", nil, "sharing_required", "sharing_required"},
		{"transport", nil, "unsupported_transport", "unsupported_transport"},
		{"source", workspacearrangement.ErrUnavailable, "source_unavailable", "source_unavailable"},
		{"cancel", fmt.Errorf("secret-path: %w", context.Canceled), "save_failed", "cancelled"},
		{"deadline", context.DeadlineExceeded, "planning_failed", "timed_out"},
		{"busy", workspacearrangement.ErrBusy, "planning_failed", "busy"},
		{"stale", workspacearrangement.ErrStale, "save_failed", "stale_context"},
		{"folder", ErrPersonalAssistantFolderConflict, "source_unavailable", "stale_context"},
		{"owner", ErrPersonalAssistantConversationOwnerChanged, "save_failed", "stale_context"},
		{"workspace", errAssistantWorkspaceScopeChanged, "planning_failed", "stale_context"},
		{"budget", workspacearrangement.ErrLimit, "planning_failed", "limit_reached"},
		{"storage", workspacearrangement.ErrLimit, "save_failed", "storage_limit"},
		{"model-shape", workspacearrangement.ErrInvalid, "planning_failed", "invalid_reply"},
		{"save-shape", workspacearrangement.ErrInvalid, "save_failed", "save_failed"},
		{"private-model-error", errors.New("secret-path API-key=private"), "planning_failed", "planning_failed"},
		{"private-save-error", errors.New("secret-path sqlite SQL failed"), "save_failed", "save_failed"},
		{"unknown-stage", errors.New("private"), "secret-path", "planning_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := arrangementFailure(tc.err, tc.stage)
			raw, err := json.Marshal(failure)
			if err != nil || failure.Code != tc.code || failure.Message == "" || strings.Contains(string(raw), "secret-path") || strings.Contains(string(raw), "private") {
				t.Fatalf("unsafe classification: %+v %v", failure, err)
			}
		})
	}
}
