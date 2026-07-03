package workspacerun

func NewReport(summary string, artifacts []Artifact, validation *ValidationResult) Report {
	changedFiles := changedFilesFromArtifacts(artifacts)
	status := ValidationStatusPassed
	if validation != nil {
		status = validationStatus(validation)
	}
	return Report{
		Summary:                summary,
		ChangedFiles:           changedFiles,
		ValidationStatus:       status,
		ReferenceURLInspection: referenceURLInspectionFromArtifacts(artifacts),
		HumanReviewNeeded:      status != ValidationStatusPassed,
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

func referenceURLInspectionFromArtifacts(artifacts []Artifact) *ReferenceURLInspectionEvidence {
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if artifact.Metadata == nil {
			continue
		}
		role, _ := artifact.Metadata["role"].(string)
		if role != referenceURLInspectionRole {
			continue
		}
		statusText, _ := artifact.Metadata["status"].(string)
		status := NormalizeReferenceURLInspectionStatus(statusText)
		url, _ := artifact.Metadata["url"].(string)
		source, _ := artifact.Metadata["source"].(string)
		detail, _ := artifact.Metadata["detail"].(string)
		return &ReferenceURLInspectionEvidence{
			URL:    url,
			Status: status,
			Source: source,
			Detail: detail,
		}
	}
	return nil
}

func NormalizeReferenceURLInspectionStatus(value string) ReferenceURLInspectionStatus {
	switch ReferenceURLInspectionStatus(value) {
	case ReferenceURLInspectionInspected, ReferenceURLInspectionBlocked:
		return ReferenceURLInspectionStatus(value)
	default:
		return ReferenceURLInspectionUnknown
	}
}

func changedFilesFromArtifacts(artifacts []Artifact) []string {
	seen := map[string]bool{}
	var files []string
	for _, artifact := range artifacts {
		if artifact.Kind != ArtifactChangedFiles {
			continue
		}
		if artifact.Metadata != nil {
			// In-memory artifacts carry []string, but after a JSON round-trip
			// through SQLite the same field decodes as []any — handle both or
			// persisted runs silently lose their changed-files list.
			switch values := artifact.Metadata["files"].(type) {
			case []string:
				for _, value := range values {
					if value != "" && !seen[value] {
						seen[value] = true
						files = append(files, value)
					}
				}
			case []any:
				for _, raw := range values {
					value, ok := raw.(string)
					if ok && value != "" && !seen[value] {
						seen[value] = true
						files = append(files, value)
					}
				}
			}
		}
	}
	return files
}
