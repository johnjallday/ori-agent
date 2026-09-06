package ci

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowStep struct {
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	With            map[string]any    `yaml:"with"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
}

type workflowJob struct {
	If              string            `yaml:"if"`
	Needs           string            `yaml:"needs"`
	Permissions     map[string]string `yaml:"permissions"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Steps           []workflowStep    `yaml:"steps"`
}

func TestSecurityWorkflowKeepsComparableBaselineAndFailsOnUploadErrors(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	base, baseOK := workflow.Jobs["security-baseline"]
	head, headOK := workflow.Jobs["security"]
	if !baseOK || !headOK {
		t.Fatal("baseline and head must upload in separate jobs: upload-sarif forbids duplicate tool/categories in one job")
	}
	baseSteps := securitySteps(t, base, []string{"Checkout PR base", "Set up Go", "Scan PR base", "Upload PR base SARIF"})
	headSteps := securitySteps(t, head, []string{"Require baseline success", "Checkout code", "Set up Go", "Run Gosec Security Scanner", "Upload SARIF file"})
	if base.If != "github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository" {
		t.Fatal("base uploads must be guarded against forks and non-PR events")
	}
	if baseSteps["Checkout PR base"].With["ref"] != "${{ github.event.pull_request.base.sha }}" {
		t.Fatal("base checkout must use the exact immutable event SHA")
	}
	if head.Needs != "security-baseline" || head.If != "${{ !cancelled() }}" {
		t.Fatal("head must wait for the baseline, including when it is skipped or fails")
	}
	guard := headSteps["Require baseline success"]
	if guard.If != "needs.security-baseline.result != 'success' && needs.security-baseline.result != 'skipped'" || !strings.Contains(guard.Run, "exit 1") {
		t.Fatal("the required Security Scan check must fail when baseline infrastructure fails")
	}
	for _, name := range []string{"Checkout code", "Run Gosec Security Scanner", "Upload SARIF file"} {
		step := headSteps[name]
		if step.If != "" || step.With["ref"] != nil || step.With["sha"] != nil {
			t.Fatalf("%s must run for the original event ref/SHA, including pushes and forks", name)
		}
	}

	baseScan, headScan := baseSteps["Scan PR base"], headSteps["Run Gosec Security Scanner"]
	if !regexp.MustCompile(`^securego/gosec@[a-f0-9]{40}$`).MatchString(headScan.Uses) || baseScan.Uses != headScan.Uses {
		t.Fatal("base and head must use the same immutable scanner action/image")
	}
	for _, scan := range []workflowStep{baseScan, headScan} {
		if scan.With["args"] != "-no-fail -fmt sarif -out results.sarif ./..." {
			t.Fatal("scan all packages without filtering rules; code scanning owns the findings gate")
		}
	}
	baseUpload, headUpload := baseSteps["Upload PR base SARIF"], headSteps["Upload SARIF file"]
	for _, upload := range []workflowStep{baseUpload, headUpload} {
		if upload.Env["CODEQL_ACTION_ANALYSIS_KEY"] != ".github/workflows/ci.yml:security" {
			t.Fatal("baseline and head need the same logical analysis key, not separate job-derived configurations")
		}
		if upload.Uses != "github/codeql-action/upload-sarif@v3" || upload.With["category"] != ".github/workflows/ci.yml:security" || upload.With["sarif_file"] != "results.sarif" {
			t.Fatal("both uploads must retain the existing analysis category and complete scan results")
		}
	}
	if baseUpload.With["ref"] != "refs/heads/${{ github.event.pull_request.base.ref }}" || baseUpload.With["sha"] != "${{ github.event.pull_request.base.sha }}" {
		t.Fatal("base SARIF must be attributed to the exact scanned base ref/SHA")
	}
}

func securitySteps(t *testing.T, job workflowJob, order []string) map[string]workflowStep {
	t.Helper()
	wantPermissions := map[string]string{"contents": "read", "security-events": "write"}
	if !reflect.DeepEqual(job.Permissions, wantPermissions) {
		t.Fatalf("security permissions = %#v, want %#v", job.Permissions, wantPermissions)
	}
	if job.ContinueOnError != nil && job.ContinueOnError != false {
		t.Fatal("security jobs must not hide failures")
	}
	if len(job.Steps) != len(order) {
		t.Fatalf("security step count = %d, want %d", len(job.Steps), len(order))
	}
	steps := make(map[string]workflowStep)
	for index, step := range job.Steps {
		if step.Name != order[index] {
			t.Fatalf("security step %d = %s, want %s", index, step.Name, order[index])
		}
		if step.ContinueOnError != nil && step.ContinueOnError != false {
			t.Fatalf("%s must not hide scanner/upload errors", step.Name)
		}
		steps[step.Name] = step
	}
	return steps
}
