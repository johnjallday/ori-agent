package workspacerun

import (
	"fmt"
	"strings"
)

const referenceURLInspectionRole = "reference_url_inspection"

// BuildRunExecutionPrompt assembles the execution contract for a native-CLI
// run. memorySection, when non-empty, is a pre-rendered `## Workspace Memory`
// block (see workspace.RenderMemoryPromptSection) injected as read-only context
// before the user prompt — CLI backends don't have Ori's memory tools, so it
// carries no tool guidance.
func BuildRunExecutionPrompt(run *Run, memorySection string) string {
	if run == nil {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("# Workspace Run Execution Contract\n\n")
	fmt.Fprintf(&prompt, "- Run ID: %s\n", strings.TrimSpace(run.ID))
	fmt.Fprintf(&prompt, "- Profile: %s", strings.TrimSpace(run.ProfileID))
	if strings.TrimSpace(run.ProfileVersion) != "" {
		fmt.Fprintf(&prompt, " v%s", strings.TrimSpace(run.ProfileVersion))
	}
	prompt.WriteString("\n")
	fmt.Fprintf(&prompt, "- Executor: %s", strings.TrimSpace(string(run.Executor.Kind)))
	if strings.TrimSpace(run.Executor.Ref) != "" {
		fmt.Fprintf(&prompt, " (%s)", strings.TrimSpace(run.Executor.Ref))
	}
	prompt.WriteString("\n")
	fmt.Fprintf(&prompt, "- Mutation policy: %s\n", strings.TrimSpace(run.Policy.Mutation))
	fmt.Fprintf(&prompt, "- Approval policy: %s\n", strings.TrimSpace(run.Policy.Approval))
	if strings.TrimSpace(run.Policy.ExternalEffects) != "" {
		fmt.Fprintf(&prompt, "- External effects policy: %s\n", strings.TrimSpace(run.Policy.ExternalEffects))
	}
	if len(run.Scope.NetworkAllowlist) > 0 {
		fmt.Fprintf(&prompt, "- Network allowlist: %s\n", strings.Join(run.Scope.NetworkAllowlist, ", "))
	}

	if referenceURL := strings.TrimSpace(run.ReferenceURL); referenceURL != "" {
		prompt.WriteString("\n## Reference URL\n\n")
		fmt.Fprintf(&prompt, "%s\n\n", referenceURL)
		prompt.WriteString("Treat this URL as authoritative source material for the run. ")
		prompt.WriteString("Inspect it with available fetch, browser, or web tools before implementation or factual reporting that depends on its contents. ")
		prompt.WriteString("Do not claim facts about the URL from memory or assumptions. ")
		prompt.WriteString("If URL access tools are unavailable, the host is not reachable, authentication is required, or access is blocked, state that limitation in the final response.\n\n")
		prompt.WriteString("In your final response, include `Reference URL inspection: inspected`, `Reference URL inspection: blocked`, or `Reference URL inspection: unknown` with a short reason.\n")
	}

	if section := strings.TrimSpace(memorySection); section != "" {
		prompt.WriteString("\n")
		prompt.WriteString(section)
		prompt.WriteString("\n")
	}

	prompt.WriteString("\n## User Prompt\n\n")
	prompt.WriteString(strings.TrimSpace(run.Prompt))
	prompt.WriteString("\n")
	return prompt.String()
}

func ReferenceURLInspectionEvidenceForOutput(run *Run, output, source string) *ReferenceURLInspectionEvidence {
	if run == nil || strings.TrimSpace(run.ReferenceURL) == "" {
		return nil
	}
	status := ReferenceURLInspectionUnknown
	normalized := strings.ToLower(output)
	switch {
	case strings.Contains(normalized, "reference url inspection: inspected"):
		status = ReferenceURLInspectionInspected
	case strings.Contains(normalized, "reference url inspection: blocked"):
		status = ReferenceURLInspectionBlocked
	case strings.Contains(normalized, "reference url inspection: unknown"):
		status = ReferenceURLInspectionUnknown
	}
	detail := "No explicit reference URL inspection status was reported."
	switch status {
	case ReferenceURLInspectionInspected:
		detail = "Agent reported that it inspected the reference URL."
	case ReferenceURLInspectionBlocked:
		detail = "Agent reported that reference URL inspection was blocked."
	}
	return &ReferenceURLInspectionEvidence{
		URL:    strings.TrimSpace(run.ReferenceURL),
		Status: status,
		Source: strings.TrimSpace(source),
		Detail: detail,
	}
}

func ReferenceURLInspectionArtifact(runID string, evidence *ReferenceURLInspectionEvidence) Artifact {
	metadata := map[string]any{
		"role":   referenceURLInspectionRole,
		"status": string(ReferenceURLInspectionUnknown),
	}
	if evidence != nil {
		metadata["url"] = evidence.URL
		metadata["status"] = string(NormalizeReferenceURLInspectionStatus(string(evidence.Status)))
		metadata["source"] = evidence.Source
		metadata["detail"] = evidence.Detail
	}
	return NewArtifact(runID, ArtifactTrace, ArtifactMetadata(metadata))
}
