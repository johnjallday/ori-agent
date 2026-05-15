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
	if run.Cost != nil {
		cost := *run.Cost
		out.Cost = &cost
	}
	if run.Report != nil {
		report := *run.Report
		report.ChangedFiles = cloneStrings(run.Report.ChangedFiles)
		report.FollowUps = cloneStrings(run.Report.FollowUps)
		out.Report = &report
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

func cloneMap(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	out := make(map[string]interface{}, len(values))
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
