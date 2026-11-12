package ci

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The integration and E2E jobs only run provider suites when a provider key is
// available. These contracts keep the early eligibility decision honest: the
// structure is pinned, and the real step scripts are executed against stub
// go/make commands and invented sentinel values. Nothing here contacts a
// provider or reads a real credential.

const (
	eligibleCondition = "steps.eligibility.outputs.eligible == 'true'"

	openAIValue      = "${{ secrets.OPENAI_API_KEY }}"
	anthropicValue   = "${{ secrets.ANTHROPIC_API_KEY }}"
	workspaceValue   = "${{ secrets.ANTHROPIC_WORKSPACE_ID }}"
	openAIPresent    = "${{ secrets.OPENAI_API_KEY != '' }}"
	anthropicPresent = "${{ secrets.ANTHROPIC_API_KEY != '' }}"

	sentinelOpenAI    = "sentinel-openai-is-not-a-real-key"
	sentinelAnthropic = "sentinel-anthropic-is-not-a-real-key"
	sentinelWorkspace = "sentinel-workspace-is-not-a-real-id"
)

type providerStep struct {
	unitStep `yaml:",inline"`
	Shell    string `yaml:"shell"`
}

type providerJob struct {
	Name            string            `yaml:"name"`
	RunsOn          string            `yaml:"runs-on"`
	Needs           string            `yaml:"needs"`
	If              string            `yaml:"if"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Steps           []providerStep    `yaml:"steps"`
}

type providerSpec struct {
	id, name, suite, finalStep, command, skipLine string
	steps                                         []string
}

var providerSpecs = []providerSpec{
	{
		id: "test-integration", name: "Integration Tests", suite: "integration tests",
		finalStep: "Run integration tests",
		command:   "ORI_RUN_PROVIDER_INTEGRATION=1 go test -v -run Integration ./...",
		skipLine:  "Skipping integration tests (no API keys configured)",
		steps: []string{"Check provider eligibility", "Checkout code", "Set up Go", "Download dependencies",
			"Run integration tests"},
	},
	{
		id: "test-e2e", name: "E2E Tests", suite: "E2E tests",
		finalStep: "Run E2E tests",
		command:   "ORI_RUN_PROVIDER_INTEGRATION=1 go test -v ./tests/e2e/...",
		skipLine:  "Skipping E2E tests (no API keys configured)",
		steps: []string{"Check provider eligibility", "Checkout code", "Set up Go", "Download dependencies",
			"Build server", "Run E2E tests"},
	},
}

func loadProviderJob(t *testing.T, id string) providerJob {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]providerJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs[id]
	if !ok {
		t.Fatalf("job %s missing", id)
	}
	return job
}

func TestProviderJobsDecideEligibilityBeforeAnySetup(t *testing.T) {
	for _, spec := range providerSpecs {
		t.Run(spec.id, func(t *testing.T) {
			job := loadProviderJob(t, spec.id)
			if job.Name != spec.name || job.RunsOn != "ubuntu-24.04" || job.Needs != "test-unit" || job.If != "" {
				t.Fatalf("required check identity, runner, dependency or condition changed: %#v", job)
			}
			var names []string
			for _, step := range job.Steps {
				names = append(names, step.Name)
			}
			if strings.Join(names, "|") != strings.Join(spec.steps, "|") {
				t.Fatalf("steps = %q, want %q", names, spec.steps)
			}

			check := job.Steps[0]
			wantEnv := map[string]string{
				"PROVIDER_SUITE":    spec.suite,
				"HAS_OPENAI_KEY":    openAIPresent,
				"HAS_ANTHROPIC_KEY": anthropicPresent,
			}
			if check.ID != "eligibility" || check.Shell != "bash" || check.If != "" || check.Uses != "" {
				t.Fatalf("eligibility step must be the first unconditional bash step: %#v", check)
			}
			for key, want := range wantEnv {
				if check.Env[key] != want {
					t.Fatalf("eligibility env %s = %q, want %q", key, check.Env[key], want)
				}
			}
			if len(check.Env) != len(wantEnv) {
				t.Fatalf("eligibility step may expose only booleans and the suite label: %#v", check.Env)
			}

			// A failure must fail the job: nothing may be marked continue-on-error,
			// and no job-level env or `with:` input may carry a secret to a step
			// that is not the one test step.
			if job.ContinueOnError != nil || len(job.Env) != 0 {
				t.Fatalf("job-level continue-on-error or env would hide failures or widen secret exposure: %#v %#v", job.ContinueOnError, job.Env)
			}
			credentialSteps := 0
			for i, step := range job.Steps {
				if step.ContinueOnError != nil {
					t.Fatalf("step %q must not set continue-on-error: a failed provider step has to fail the job", step.Name)
				}
				for input, value := range step.With {
					if strings.Contains(fmt.Sprint(value), "secrets.") {
						t.Fatalf("step %q passes a secret through its %q input", step.Name, input)
					}
				}
				if strings.Contains(step.If, "secrets.") {
					t.Fatalf("step %q reads secrets in an if condition", step.Name)
				}
				if i > 0 && step.If != eligibleCondition {
					t.Fatalf("step %q must be gated on the eligibility output, got if=%q", step.Name, step.If)
				}
				for _, value := range step.Env {
					if strings.Contains(value, "secrets.") && value != openAIPresent && value != anthropicPresent {
						credentialSteps++
						break
					}
				}
			}
			if credentialSteps != 1 {
				t.Fatalf("raw credentials must reach exactly one step, reached %d", credentialSteps)
			}

			final := job.Steps[len(job.Steps)-1]
			if final.Name != spec.finalStep || final.Env["OPENAI_API_KEY"] != openAIValue ||
				final.Env["ANTHROPIC_API_KEY"] != anthropicValue || final.Env["ANTHROPIC_WORKSPACE_ID"] != workspaceValue {
				t.Fatalf("credentials must stay on the final test step: %#v", final.Env)
			}
			for _, want := range []string{
				`if [ -n "$OPENAI_API_KEY" ] || [ -n "$ANTHROPIC_API_KEY" ]; then`,
				spec.command,
				`echo "` + spec.skipLine + `"`,
			} {
				if !strings.Contains(final.Run, want) {
					t.Fatalf("eligible-path test command changed, missing %q in:\n%s", want, final.Run)
				}
			}
		})
	}
}

// bashCI runs a script the way a GitHub Actions step does. An explicit `shell:
// bash` runs `bash --noprofile --norc -eo pipefail {0}`; a step with no shell
// runs `bash -e {0}`, which has no pipefail, so a failure hidden behind a pipe
// there is hidden in CI too.
func bashCI(t *testing.T, script string, env []string, shell string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", path}
	if shell == "bash" {
		args = []string{"--noprofile", "--norc", "-eo", "pipefail", path}
	} else if shell != "" {
		t.Fatalf("unsupported step shell %q", shell)
	}
	// #nosec G204 -- constant interpreter; the arguments are fixed flags and a file this test just wrote.
	command := exec.CommandContext(t.Context(), "bash", args...)
	command.Env = env
	output, err := command.CombinedOutput()
	status := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	return string(output), status
}

// stubBin returns a PATH directory whose go and make log their arguments and
// fail on request (STUB_FAIL is "make", "go", or "go test").
func stubBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `#!/bin/sh
name=$(basename "$0")
printf '%s %s | ORI_RUN_PROVIDER_INTEGRATION=%s\n' "$name" "$*" "${ORI_RUN_PROVIDER_INTEGRATION:-}" >> "$STUB_LOG"
if [ "$STUB_FAIL" = "$name" ] || [ "$STUB_FAIL" = "$name $1" ]; then exit 1; fi
exit 0
`
	for _, name := range []string{"go", "make"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G302 -- the stub must be executable; it lives in this test's private temp directory.
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type providerScenario struct {
	name           string
	openAI         string // sentinel secret value, "" = not configured
	anthropic      string
	forceDetection string // replaces the HAS_* booleans to simulate an unusable detection result
	forceEmpty     bool   // makes the HAS_* booleans empty, as if the expression yielded nothing
	stubFail       string
	wantEligible   string // "", "true" or "false" as written to the step output
	wantRan        bool   // setup/build/test commands were reached
	wantJobFailed  bool
}

// expand resolves the only expressions the provider jobs use.
func expand(t *testing.T, value string, sc providerScenario) string {
	t.Helper()
	switch value {
	case openAIValue:
		return sc.openAI
	case anthropicValue:
		return sc.anthropic
	case workspaceValue:
		if sc.anthropic != "" || sc.openAI != "" {
			return sentinelWorkspace
		}
		return ""
	case openAIPresent, anthropicPresent:
		if sc.forceEmpty {
			return ""
		}
		if sc.forceDetection != "" {
			return sc.forceDetection
		}
		present := sc.openAI
		if value == anthropicPresent {
			present = sc.anthropic
		}
		if present != "" {
			return "true"
		}
		return "false"
	}
	if strings.Contains(value, "${{") {
		t.Fatalf("unsupported expression in step env: %q", value)
	}
	return value
}

func parseOutputs(text string) map[string]string {
	outputs := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			outputs[key] = value
		}
	}
	return outputs
}

// runProviderJob walks a job's steps as the runner would: `uses:` steps are
// recorded (never executed), `run:` steps execute under bash, and a step
// whose condition is false, or that follows a failure, is skipped.
func runProviderJob(t *testing.T, spec providerSpec, sc providerScenario) (executed []string, stubLog string, log string, failed bool) {
	t.Helper()
	job := loadProviderJob(t, spec.id)
	work := t.TempDir()
	stubLogPath := filepath.Join(work, "stub.log")
	outputPath := filepath.Join(work, "output")
	summaryPath := filepath.Join(work, "summary")
	for _, path := range []string{stubLogPath, outputPath, summaryPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := stubBin(t)
	outputs := map[string]string{}
	var transcript strings.Builder
	for _, step := range job.Steps {
		run := !failed
		switch step.If {
		case "":
		case eligibleCondition:
			run = run && outputs["eligible"] == "true"
		default:
			t.Fatalf("unsupported step condition %q", step.If)
		}
		if !run {
			continue
		}
		executed = append(executed, step.Name)
		if step.Uses != "" {
			continue
		}
		env := []string{
			"PATH=" + bin + ":/usr/bin:/bin",
			"STUB_LOG=" + stubLogPath,
			"STUB_FAIL=" + sc.stubFail,
			"GITHUB_OUTPUT=" + outputPath,
			"GITHUB_STEP_SUMMARY=" + summaryPath,
		}
		for key, value := range step.Env {
			env = append(env, key+"="+expand(t, value, sc))
		}
		before, _ := os.ReadFile(outputPath)
		out, status := bashCI(t, step.Run, env, step.Shell)
		transcript.WriteString(out)
		if status != 0 {
			failed = true
		}
		after, _ := os.ReadFile(outputPath)
		if step.ID != "" {
			maps.Copy(outputs, parseOutputs(strings.TrimPrefix(string(after), string(before))))
		}
		if step.ID == "eligibility" {
			summary, _ := os.ReadFile(summaryPath)
			transcript.Write(summary)
		}
	}
	logged, _ := os.ReadFile(stubLogPath)
	if got := outputs["eligible"]; got != sc.wantEligible {
		t.Fatalf("eligible output = %q, want %q", got, sc.wantEligible)
	}
	return executed, string(logged), transcript.String(), failed
}

func TestProviderEligibilityRoutesWithoutCredentialsOrSetup(t *testing.T) {
	scenarios := []providerScenario{
		// Fork and Dependabot runs receive no secrets, so they are this scenario.
		{name: "neither key", wantEligible: "false"},
		{name: "anthropic only", anthropic: sentinelAnthropic, wantEligible: "true", wantRan: true},
		{name: "openai only", openAI: sentinelOpenAI, wantEligible: "true", wantRan: true},
		{name: "both keys", openAI: sentinelOpenAI, anthropic: sentinelAnthropic, wantEligible: "true", wantRan: true},
		{name: "detection failure", openAI: sentinelOpenAI, forceDetection: "unusable", wantJobFailed: true},
		{name: "empty detection result", anthropic: sentinelAnthropic, forceEmpty: true, wantJobFailed: true},
	}
	for _, spec := range providerSpecs {
		for _, sc := range scenarios {
			t.Run(spec.id+"/"+sc.name, func(t *testing.T) {
				executed, stubLog, transcript, failed := runProviderJob(t, spec, sc)
				t.Logf("steps run: %s | stub commands: [%s] | job failed: %v",
					strings.Join(executed, " > "), strings.ReplaceAll(strings.TrimSpace(stubLog), "\n", " ; "), failed)
				for _, sentinel := range []string{sentinelOpenAI, sentinelAnthropic, sentinelWorkspace, "unusable"} {
					if strings.Contains(transcript, sentinel) || strings.Contains(stubLog, sentinel) {
						t.Fatalf("a credential or detection value was echoed:\n%s", transcript)
					}
				}
				if failed != sc.wantJobFailed {
					t.Fatalf("job failed = %v, want %v\n%s", failed, sc.wantJobFailed, transcript)
				}
				switch {
				case sc.wantJobFailed:
					if strings.Join(executed, "|") != spec.steps[0] || stubLog != "" ||
						!strings.Contains(transcript, "::error title=Provider eligibility could not be determined::") {
						t.Fatalf("a failed detection must stop at the detection step, ran %q, log %q\n%s", executed, stubLog, transcript)
					}
				case !sc.wantRan:
					if strings.Join(executed, "|") != spec.steps[0] || stubLog != "" {
						t.Fatalf("an ineligible job ran setup, build or test: %q log %q", executed, stubLog)
					}
					for _, want := range []string{spec.skipLine, "::notice title=Provider tests not run::", "did NOT run",
						"This green check is not a provider-suite pass."} {
						if !strings.Contains(transcript, want) {
							t.Fatalf("missing %q in the no-key output:\n%s", want, transcript)
						}
					}
				default:
					if strings.Join(executed, "|") != strings.Join(spec.steps, "|") {
						t.Fatalf("eligible job ran %q, want every step %q", executed, spec.steps)
					}
					want := []string{"go mod download | ORI_RUN_PROVIDER_INTEGRATION="}
					if spec.id == "test-e2e" {
						want = append(want, "make build | ORI_RUN_PROVIDER_INTEGRATION=")
					}
					testArgs := "test -v -run Integration ./..."
					if spec.id == "test-e2e" {
						testArgs = "test -v ./tests/e2e/..."
					}
					want = append(want, "go "+testArgs+" | ORI_RUN_PROVIDER_INTEGRATION=1")
					if got := strings.Split(strings.TrimSpace(stubLog), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Fatalf("eligible commands changed:\n got %q\nwant %q", got, want)
					}
					if strings.Contains(transcript, "did NOT run") {
						t.Fatalf("an eligible job must not claim the suite did not run:\n%s", transcript)
					}
				}
			})
		}
	}
}

func TestProviderEligiblePathPropagatesFailures(t *testing.T) {
	for _, spec := range providerSpecs {
		failures := []struct {
			name, stubFail string
			neverReached   string
		}{
			{"dependency download fails", "go mod", "go test"},
			{"test fails", "go test", ""},
		}
		if spec.id == "test-e2e" {
			failures = append(failures, struct{ name, stubFail, neverReached string }{"server build fails", "make", "go test"})
		}
		for _, failure := range failures {
			t.Run(spec.id+"/"+failure.name, func(t *testing.T) {
				sc := providerScenario{anthropic: sentinelAnthropic, wantEligible: "true", wantRan: true,
					stubFail: failure.stubFail, wantJobFailed: true}
				executed, stubLog, transcript, failed := runProviderJob(t, spec, sc)
				t.Logf("steps run: %s | stub commands: [%s] | job failed: %v",
					strings.Join(executed, " > "), strings.ReplaceAll(strings.TrimSpace(stubLog), "\n", " ; "), failed)
				if !failed {
					t.Fatalf("%s did not fail the job\n%s", failure.name, transcript)
				}
				if failure.neverReached != "" && strings.Contains(stubLog, failure.neverReached) {
					t.Fatalf("the test command ran after %s:\n%s", failure.name, stubLog)
				}
			})
		}
	}
}
