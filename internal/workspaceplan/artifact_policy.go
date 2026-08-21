package workspaceplan

import (
	"fmt"
	"path"
	"strings"
)

// ArtifactPolicy is the compiled application decision for repository planning
// outputs. Unlike GuidanceInput.PreferredArtifacts, these values are applied by
// code before a Plan becomes reviewable; a model cannot choose a different
// path or enable a file the workspace disabled.
type ArtifactPolicy struct {
	// Apply distinguishes "planning outputs are disabled" from "this build has
	// no artifact-policy resolver". The latter preserves caller-authored
	// artifacts for backwards compatibility and offline use.
	Apply         bool
	Directory     string
	WritePRD      bool
	WriteTaskList bool
}

// ApplyArtifactPolicy returns a copy of content whose PRD and task-list
// artifacts follow the workspace's configured directory and the Plan's stable
// feature identity. Other artifact kinds remain untouched.
func ApplyArtifactPolicy(plan *Plan, content PlanContent, policy ArtifactPolicy) (PlanContent, error) {
	out := content.Clone()
	if !policy.Apply {
		return out, nil
	}
	if plan == nil {
		return PlanContent{}, fmt.Errorf("%w: artifact policy requires a plan", ErrValidation)
	}

	directory, err := normalizeArtifactDirectory(policy.Directory)
	if err != nil {
		return PlanContent{}, err
	}
	feature := PlanFeatureSlug(plan)

	planning := map[ArtifactKind]ProposedArtifact{}
	for _, artifact := range out.Artifacts {
		if artifact.Kind != ArtifactPRD && artifact.Kind != ArtifactTaskList {
			continue
		}
		// Keep the first proposal's human labels and stable ID. Additional
		// proposals of the same kind are dropped: one approved Plan has one
		// canonical PRD and one canonical implementation checklist.
		if _, exists := planning[artifact.Kind]; !exists {
			planning[artifact.Kind] = artifact
		}
	}

	configure := func(kind ArtifactKind, enabled bool) {
		artifact, exists := planning[kind]
		if !exists && !enabled {
			return
		}
		if strings.TrimSpace(artifact.ID) == "" {
			artifact.ID = canonicalArtifactID(plan.ID, kind)
		}
		artifact.Kind = kind
		artifact.Enabled = enabled
		artifact.Path = canonicalArtifactPath(directory, feature, kind)
		planning[kind] = artifact
	}
	configure(ArtifactPRD, policy.WritePRD)
	configure(ArtifactTaskList, policy.WriteTaskList)

	artifacts := make([]ProposedArtifact, 0, len(out.Artifacts)+2)
	if artifact, exists := planning[ArtifactPRD]; exists {
		artifacts = append(artifacts, artifact)
	}
	if artifact, exists := planning[ArtifactTaskList]; exists {
		artifacts = append(artifacts, artifact)
	}
	for _, artifact := range out.Artifacts {
		if artifact.Kind == ArtifactPRD || artifact.Kind == ArtifactTaskList {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	out.Artifacts = artifacts
	return out, nil
}

// PlanFeatureSlug is the stable repository identity exported with an approved
// Plan. It derives from immutable Plan fields, not the editable display title,
// so renaming a draft cannot rename its artifact files or future worktree.
func PlanFeatureSlug(plan *Plan) string {
	if plan == nil {
		return "plan"
	}
	seed := firstLine(plan.OriginalRequest)
	if seed == "" {
		seed = plan.Title
	}
	base := slugify(seed)
	if base == "" {
		base = "plan"
	}
	const maxBase = 48
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}

	suffix := stableIDFragment(plan.ID)
	if suffix == "" {
		return base
	}
	return base + "-" + suffix
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func stableIDFragment(id string) string {
	value := strings.TrimPrefix(strings.TrimSpace(id), planIDPrefix)
	value = strings.ReplaceAll(value, "-", "")
	if len(value) > 8 {
		value = value[:8]
	}
	return slugify(value)
}

func canonicalArtifactID(planID string, kind ArtifactKind) string {
	return "art_" + string(kind) + "_" + stableIDFragment(planID)
}

func canonicalArtifactPath(directory, feature string, kind ArtifactKind) string {
	prefix := "tasks-"
	if kind == ArtifactPRD {
		prefix = "prd-"
	}
	return path.Join(directory, prefix+feature+".md")
}

func normalizeArtifactDirectory(directory string) (string, error) {
	directory = strings.ReplaceAll(strings.TrimSpace(directory), "\\", "/")
	if directory == "" {
		directory = "tasks"
	}
	cleaned := path.Clean(directory)
	if cleaned == "." {
		return ".", nil
	}
	probe := path.Join(cleaned, "planning-artifact.md")
	if err := ValidateArtifactPath(probe); err != nil {
		return "", fmt.Errorf("%w: invalid planning output directory: %v", ErrUnsafePath, err)
	}
	return cleaned, nil
}
