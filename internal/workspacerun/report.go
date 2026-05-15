package workspacerun

func NewReport(summary string, artifacts []Artifact, validation *ValidationResult) Report {
	changedFiles := changedFilesFromArtifacts(artifacts)
	status := ValidationStatusPassed
	if validation != nil {
		status = validationStatus(validation)
	}
	return Report{
		Summary:           summary,
		ChangedFiles:      changedFiles,
		ValidationStatus:  status,
		HumanReviewNeeded: status != ValidationStatusPassed,
	}
}

func validationStatus(result *ValidationResult) string {
	if result == nil || len(result.Checks) == 0 {
		return ValidationStatusPassed
	}
	softFailures := 0
	for _, check := range result.Checks {
		if check.Status == CheckStatusFailed {
			if check.Soft {
				softFailures++
				continue
			}
			return ValidationStatusFailed
		}
	}
	if softFailures > 0 {
		return ValidationStatusPartial
	}
	return ValidationStatusPassed
}

func changedFilesFromArtifacts(artifacts []Artifact) []string {
	seen := map[string]bool{}
	var files []string
	for _, artifact := range artifacts {
		if artifact.Kind != ArtifactChangedFiles {
			continue
		}
		if artifact.Metadata != nil {
			if values, ok := artifact.Metadata["files"].([]string); ok {
				for _, value := range values {
					if value != "" && !seen[value] {
						seen[value] = true
						files = append(files, value)
					}
				}
			}
		}
	}
	return files
}
