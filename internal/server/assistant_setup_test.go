package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/store"
)

// Only the reviewed creator's typed pre-write refusals are classified. An
// error that merely mentions a plan or a root is an unproven outcome and must
// pass through unchanged.
func TestAssistantSetupSessionErrorsClassifyOnlyTypedRefusals(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"root unavailable", fmt.Errorf("review: %w", store.ErrAgentRootUnavailable), assistantsetup.ErrAgentRootUnavailable},
		{"plan changed", sessionhttp.ErrReviewedPlanChanged, assistantsetup.ErrTeamConflict},
		{"plan unsupported", fmt.Errorf("%w: unsupported role action", sessionhttp.ErrReviewedPlanUnsupported), assistantsetup.ErrTeamConflict},
	}
	for _, tc := range cases {
		if got := mapAssistantSetupSessionError(tc.err); !errors.Is(got, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, untyped := range []error{
		errors.New("file janitor team plan changed"),
		errors.New("agent root unavailable"),
		errors.New("reviewed workspace creation failed (500)"),
	} {
		got := mapAssistantSetupSessionError(untyped)
		if errors.Is(got, assistantsetup.ErrTeamConflict) || errors.Is(got, assistantsetup.ErrAgentRootUnavailable) || got != untyped {
			t.Fatalf("untyped %q was classified as %v", untyped, got)
		}
	}
	if mapAssistantSetupSessionError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
