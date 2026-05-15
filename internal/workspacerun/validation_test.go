package workspacerun

import (
	"context"
	"testing"
)

func TestValidatorNoneSkips(t *testing.T) {
	validator := NewValidator()
	result, artifacts, err := validator.Validate(context.Background(), &Run{
		ID:                "run-1",
		ValidationRequest: &ValidationRequest{Profile: ValidationProfileNone},
	}, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %v, want none", artifacts)
	}
	if result == nil || len(result.Checks) != 1 || result.Checks[0].Status != CheckStatusSkipped {
		t.Fatalf("result = %+v, want skipped check", result)
	}
}

func TestValidatorEngineeringRequiresChangeEvidence(t *testing.T) {
	validator := NewValidator()
	run := &Run{
		ID:        "run-1",
		ProfileID: ProfileEngineering,
		Policy:    Policy{Mutation: PolicyMutationAllowed},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	}

	result, _, err := validator.Validate(context.Background(), run, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !hasCheck(result, "change_evidence_present", CheckStatusFailed) {
		t.Fatalf("result = %+v, want failed change evidence check", result)
	}

	result, _, err = validator.Validate(context.Background(), run, []Artifact{NewArtifact("run-1", ArtifactChangedFiles)})
	if err != nil {
		t.Fatalf("validate with artifact: %v", err)
	}
	if !hasCheck(result, "change_evidence_present", CheckStatusPassed) {
		t.Fatalf("result = %+v, want passed change evidence check", result)
	}
}

func TestValidatorCommandsExtendSelectedProfile(t *testing.T) {
	validator := NewValidator()
	validator.AllowedCommands = map[string][]string{
		"go version": {"go", "version"},
	}
	run := &Run{
		ID:        "run-1",
		Scope:     Scope{RepoPath: t.TempDir()},
		ProfileID: ProfileGeneral,
		ValidationRequest: &ValidationRequest{
			Profile:  ValidationProfileNone,
			Commands: []string{"go version"},
		},
	}

	result, artifacts, err := validator.Validate(context.Background(), run, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !hasCheck(result, "validation_skipped", CheckStatusSkipped) {
		t.Fatalf("result = %+v, want selected profile check preserved", result)
	}
	if !hasCheck(result, "command:go version", CheckStatusPassed) {
		t.Fatalf("result = %+v, want ad-hoc command check", result)
	}
	if !ValidationAcceptable(result) {
		t.Fatalf("result = %+v, want acceptable validation", result)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactTestOutput {
		t.Fatalf("artifacts = %+v, want command output artifact", artifacts)
	}
}

func TestValidatorCommandsFailWhenNotAllowlistedOrCommandFails(t *testing.T) {
	validator := NewValidator()
	run := &Run{
		ID:    "run-1",
		Scope: Scope{RepoPath: t.TempDir()},
		ValidationRequest: &ValidationRequest{
			Profile:  ValidationProfileNone,
			Commands: []string{"rm -rf /"},
		},
	}

	result, artifacts, err := validator.Validate(context.Background(), run, nil)
	if err != nil {
		t.Fatalf("validate blocked command: %v", err)
	}
	if !hasCheck(result, "command:rm -rf /", CheckStatusFailed) {
		t.Fatalf("result = %+v, want blocked command failure", result)
	}
	if ValidationAcceptable(result) {
		t.Fatalf("result = %+v, want unacceptable validation", result)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactTestOutput {
		t.Fatalf("artifacts = %+v, want blocked command evidence artifact", artifacts)
	}

	validator.AllowedCommands = map[string][]string{
		"go badsubcommand": {"go", "badsubcommand"},
	}
	run.ValidationRequest.Commands = []string{"go badsubcommand"}
	result, _, err = validator.Validate(context.Background(), run, nil)
	if err != nil {
		t.Fatalf("validate failing command: %v", err)
	}
	if !hasCheck(result, "command:go badsubcommand", CheckStatusFailed) {
		t.Fatalf("result = %+v, want failing command check", result)
	}
}

func TestValidationAcceptableIgnoresSoftFailuresOnly(t *testing.T) {
	if !ValidationAcceptable(&ValidationResult{Checks: []CheckResult{{Name: "soft", Status: CheckStatusFailed, Soft: true}}}) {
		t.Fatal("soft failure should be acceptable")
	}
	if ValidationAcceptable(&ValidationResult{Checks: []CheckResult{{Name: "hard", Status: CheckStatusFailed}}}) {
		t.Fatal("hard failure should be unacceptable")
	}
}

func hasCheck(result *ValidationResult, name, status string) bool {
	if result == nil {
		return false
	}
	for _, check := range result.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
