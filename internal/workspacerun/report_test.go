package workspacerun

import "testing"

func TestCostAggregationNormalizesTotals(t *testing.T) {
	got := AddCost(
		CostSummary{InputTokens: 2, OutputTokens: 3},
		CostSummary{InputTokens: 5, OutputTokens: 7, TotalTokens: 12, USD: 0.25},
	)
	got = NormalizeCost(got)
	if got.InputTokens != 7 || got.OutputTokens != 10 || got.TotalTokens != 12 || got.USD != 0.25 {
		t.Fatalf("cost = %+v, want summed fields with explicit total preserved", got)
	}

	normalized := NormalizeCost(CostSummary{InputTokens: 4, OutputTokens: 6})
	if normalized.TotalTokens != 10 {
		t.Fatalf("TotalTokens = %d, want 10", normalized.TotalTokens)
	}
}

func TestNewReportSummarizesValidation(t *testing.T) {
	artifact := NewArtifact("run-1", ArtifactChangedFiles, ArtifactMetadata(map[string]interface{}{
		"files": []string{"a.go", "b.go"},
	}))
	validation := &ValidationResult{
		Profile: ValidationProfileUnit,
		Checks:  []CheckResult{{Name: "unit", Status: CheckStatusPassed}},
	}

	report := NewReport("done", []Artifact{artifact}, validation)
	if report.Summary != "done" {
		t.Fatalf("Summary = %q, want done", report.Summary)
	}
	if report.ValidationStatus != ValidationStatusPassed {
		t.Fatalf("ValidationStatus = %q, want passed", report.ValidationStatus)
	}
	if report.HumanReviewNeeded {
		t.Fatal("HumanReviewNeeded = true, want false")
	}
	if len(report.ChangedFiles) != 2 {
		t.Fatalf("ChangedFiles = %v, want two files", report.ChangedFiles)
	}
}

func TestNewReportFlagsHardValidationFailure(t *testing.T) {
	validation := &ValidationResult{
		Checks: []CheckResult{{Name: "unit", Status: CheckStatusFailed}},
	}
	report := NewReport("failed", nil, validation)
	if report.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("ValidationStatus = %q, want failed", report.ValidationStatus)
	}
	if !report.HumanReviewNeeded {
		t.Fatal("HumanReviewNeeded = false, want true")
	}
}
