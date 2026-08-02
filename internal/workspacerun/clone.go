package workspacerun

func CloneRun(run *Run) *Run {
	if run == nil {
		return nil
	}
	out := *run
	out.ProfileSnapshot = cloneProfile(run.ProfileSnapshot)
	out.Executor.Config = cloneRawMessage(run.Executor.Config)
	out.Scope.FilesystemRoots = cloneStrings(run.Scope.FilesystemRoots)
	out.Scope.NetworkAllowlist = cloneStrings(run.Scope.NetworkAllowlist)
	out.Policy.ToolAllow = cloneStrings(run.Policy.ToolAllow)
	out.Policy.ToolDeny = cloneStrings(run.Policy.ToolDeny)
	out.Environment.EnvVars = cloneStringMap(run.Environment.EnvVars)
	out.TraceTail = CloneTraceEvents(run.TraceTail)
	out.Artifacts = CloneArtifacts(run.Artifacts)
	if run.PreparedContext != nil {
		prepared := *run.PreparedContext
		prepared.Items = clonePreparedContextItems(run.PreparedContext.Items)
		prepared.AvailableTools = cloneStrings(run.PreparedContext.AvailableTools)
		out.PreparedContext = &prepared
	}
	if run.ValidationRequest != nil {
		req := *run.ValidationRequest
		req.Commands = cloneStrings(req.Commands)
		out.ValidationRequest = &req
	}
	if run.ValidationResult != nil {
		result := *run.ValidationResult
		result.Checks = append([]CheckResult(nil), run.ValidationResult.Checks...)
		out.ValidationResult = &result
	}
	if run.TaskOutput != nil {
		output := *run.TaskOutput
		if run.TaskOutput.ValidatedAt != nil {
			validatedAt := *run.TaskOutput.ValidatedAt
			output.ValidatedAt = &validatedAt
		}
		output.Errors = append([]TaskOutputValidationError(nil), run.TaskOutput.Errors...)
		out.TaskOutput = &output
	}
	if run.Cost != nil {
		cost := *run.Cost
		out.Cost = &cost
	}
	if run.Report != nil {
		report := *run.Report
		report.ChangedFiles = cloneStrings(run.Report.ChangedFiles)
		report.FollowUps = cloneStrings(run.Report.FollowUps)
		if run.Report.ReferenceURLInspection != nil {
			inspection := *run.Report.ReferenceURLInspection
			report.ReferenceURLInspection = &inspection
		}
		out.Report = &report
	}
	// The snapshot is immutable by contract; deep-copying is how that survives
	// a caller that does not know it (PRD FR-110).
	out.ToolboxSnapshot = run.ToolboxSnapshot.Clone()
	if run.ToolboxWrapUp != nil {
		wrapUp := *run.ToolboxWrapUp
		wrapUp.UnusedOperations = cloneStrings(run.ToolboxWrapUp.UnusedOperations)
		wrapUp.ConnectionFailures = cloneStrings(run.ToolboxWrapUp.ConnectionFailures)
		wrapUp.Operations = append([]WrapUpOperation(nil), run.ToolboxWrapUp.Operations...)
		wrapUp.SkillObservations = append([]WrapUpSkillObservation(nil), run.ToolboxWrapUp.SkillObservations...)
		wrapUp.Suggestions = append([]WrapUpSuggestion(nil), run.ToolboxWrapUp.Suggestions...)
		out.ToolboxWrapUp = &wrapUp
	}
	return &out
}

func cloneRawMessage(value *RawConfig) *RawConfig {
	if value == nil {
		return nil
	}
	out := make(RawConfig, len(*value))
	copy(out, *value)
	return &out
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func clonePreparedContextItems(values []PreparedContextItem) []PreparedContextItem {
	return append([]PreparedContextItem(nil), values...)
}
