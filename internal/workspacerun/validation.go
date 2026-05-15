package workspacerun

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const (
	ValidationProfileNone = "none"
	ValidationProfileUnit = "unit"
)

type Validator struct {
	AllowedCommands map[string][]string
}

func NewValidator() *Validator {
	return &Validator{
		AllowedCommands: map[string][]string{
			"go test ./...": {"go", "test", "./..."},
		},
	}
}

func (v *Validator) Validate(ctx context.Context, run *Run, artifacts []Artifact) (*ValidationResult, []Artifact, error) {
	if run == nil {
		return nil, nil, fmt.Errorf("run is nil")
	}
	req := run.ValidationRequest
	profile := ValidationProfileNone
	if req != nil && strings.TrimSpace(req.Profile) != "" {
		profile = strings.ToLower(strings.TrimSpace(req.Profile))
	}
	result := &ValidationResult{Profile: profile}
	var produced []Artifact

	switch profile {
	case ValidationProfileNone:
		result.Checks = append(result.Checks, CheckResult{Name: "validation_skipped", Status: CheckStatusSkipped, Soft: true})
	case ValidationProfileUnit:
		output, status := v.runAllowedCommand(ctx, run, "go test ./...")
		result.Checks = append(result.Checks, CheckResult{Name: "unit_validation", Status: status, Evidence: output})
		produced = append(produced, NewArtifact(run.ID, ArtifactTestOutput, ArtifactInline([]byte(output)), ArtifactMetadata(map[string]interface{}{"profile": profile})))
	default:
		result.Checks = append(result.Checks, CheckResult{Name: "validation_profile_supported", Status: CheckStatusFailed, Evidence: fmt.Sprintf("unknown validation profile %q", profile)})
	}

	if req != nil {
		for _, command := range req.Commands {
			command = strings.TrimSpace(command)
			if command == "" {
				continue
			}
			output, status := v.runAllowedCommand(ctx, run, command)
			result.Checks = append(result.Checks, CheckResult{Name: "command:" + command, Status: status, Evidence: output})
			produced = append(produced, NewArtifact(run.ID, ArtifactTestOutput, ArtifactInline([]byte(output)), ArtifactMetadata(map[string]interface{}{"command": command})))
		}
	}

	result.Checks = append(result.Checks, engineeringEvidenceChecks(run, artifacts)...)
	return result, produced, nil
}

func (v *Validator) runAllowedCommand(ctx context.Context, run *Run, command string) (string, string) {
	argv, ok := v.AllowedCommands[command]
	if !ok || len(argv) == 0 {
		return fmt.Sprintf("validation command %q is not allowlisted", command), CheckStatusFailed
	}
	if strings.TrimSpace(run.Scope.RepoPath) == "" {
		return "repo_path is required for command validation", CheckStatusFailed
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = run.Scope.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), CheckStatusFailed
	}
	return string(out), CheckStatusPassed
}

func engineeringEvidenceChecks(run *Run, artifacts []Artifact) []CheckResult {
	if run.ProfileID != ProfileEngineering || run.Policy.Mutation != PolicyMutationAllowed {
		return nil
	}
	for _, artifact := range artifacts {
		if artifact.Kind == ArtifactChangedFiles || artifact.Kind == ArtifactDiff {
			return []CheckResult{{Name: "change_evidence_present", Status: CheckStatusPassed}}
		}
	}
	return []CheckResult{{Name: "change_evidence_present", Status: CheckStatusFailed, Evidence: "engineering runs with mutation allowed require changed files or diff artifact"}}
}

func ValidationAcceptable(result *ValidationResult) bool {
	if result == nil {
		return true
	}
	for _, check := range result.Checks {
		if check.Soft {
			continue
		}
		if check.Status != CheckStatusPassed {
			return false
		}
	}
	return true
}
