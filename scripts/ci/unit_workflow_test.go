package ci

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type unitStep struct {
	workflowStep `yaml:",inline"`
	ID           string `yaml:"id"`
}

type unitJob struct {
	Name        string            `yaml:"name"`
	RunsOn      string            `yaml:"runs-on"`
	Needs       string            `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Strategy    struct {
		Matrix map[string][]string `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []unitStep `yaml:"steps"`
}

func TestUnitWorkflowPreservesLabelsScopeCoverageAndCacheBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On   map[string]any     `yaml:"on"`
		Jobs map[string]unitJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.On) != 2 || workflow.On["push"] == nil || workflow.On["pull_request"] == nil {
		t.Fatal("unit cache must not add privileged triggers/promotion")
	}
	job := workflow.Jobs["test-unit"]
	if job.Name != "Unit Tests" || job.RunsOn != "${{ matrix.os }}" {
		t.Fatal("required-check identity changed")
	}
	wantMatrix := map[string][]string{"os": {"ubuntu-latest", "macos-latest"}, "go-version": {"1.25.12"}}
	if !reflect.DeepEqual(job.Strategy.Matrix, wantMatrix) {
		t.Fatalf("matrix renamed required contexts: %#v", job.Strategy.Matrix)
	}
	if !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
		t.Fatal("unit job permissions expanded")
	}
	steps := map[string]unitStep{}
	order := map[string]int{}
	for i, step := range job.Steps {
		steps[step.Name] = step
		order[step.Name] = i
		if step.Name != "Upload coverage to Codecov" && step.ContinueOnError != nil && step.ContinueOnError != false {
			t.Fatalf("%s hides failure", step.Name)
		}
	}
	setup := steps["Set up Go"]
	if setup.Uses != "actions/setup-go@v6" || setup.With["go-version-file"] != "go.mod" || setup.With["cache"] != false {
		t.Fatal("actual toolchain or exclusive cache ownership changed")
	}
	identity := steps["Resolve unit cache identity"]
	for _, field := range []string{"go env GOVERSION", "go env GOOS", "go env GOARCH", "ImageOS", "go env GOMODCACHE", "go env GOCACHE"} {
		if !strings.Contains(identity.Run, field) {
			t.Fatalf("cache identity missing %s", field)
		}
	}
	modules, build := steps["Restore unit modules"], steps["Restore unit build cache"]
	for _, step := range []unitStep{modules, build} {
		if step.Uses != "actions/cache/restore@v4" {
			t.Fatal("restore/save must be explicit, not a post-action race")
		}
		key, ok := step.With["key"].(string)
		if !ok {
			t.Fatal("missing cache key")
		}
		for _, identity := range []string{"steps.unit_cache_identity.outputs.platform", "steps.unit_cache_identity.outputs.go", "hashFiles('go.mod', 'go.sum')"} {
			if !strings.Contains(key, identity) {
				t.Fatalf("missing key identity %s", identity)
			}
		}
	}
	if modules.With["path"] != "${{ steps.unit_cache_identity.outputs.modules }}" || build.With["path"] != "${{ steps.unit_cache_identity.outputs.build }}" {
		t.Fatal("cache paths overlap or ownership changed")
	}
	if !strings.HasPrefix(modules.With["key"].(string), "ori-unit-mod-v1-") || modules.With["restore-keys"] != nil {
		t.Fatal("unbounded module cache fallback")
	}
	prefix := strings.TrimSpace(build.With["restore-keys"].(string))
	if strings.Contains(prefix, "\n") || !strings.HasPrefix(prefix, "ori-unit-build-v1-") || !strings.HasSuffix(prefix, "-race-cover-atomic-") {
		t.Fatal("unbounded build fallback or instrumentation drift")
	}
	wantKey := prefix + "${{ github.sha }}-${{ github.run_id }}-${{ github.run_attempt }}"
	if build.With["key"] != wantKey {
		t.Fatal("immutable build key cannot refresh each successful source/run attempt")
	}
	for _, pair := range [][2]string{{"Save unit modules", "unit_modules"}, {"Save unit build cache", "unit_build"}} {
		save := steps[pair[0]]
		if save.Uses != "actions/cache/save@v4" || save.If != "success() && steps.unit.outcome == 'success' && steps."+pair[1]+".outputs.cache-hit != 'true'" {
			t.Fatal("failed/cancelled tests could publish cache")
		}
		if save.With["key"] != "${{ steps."+pair[1]+".outputs.cache-primary-key }}" {
			t.Fatal("save key differs from restore primary key")
		}
		if order[pair[0]] <= order["Run unit tests"] {
			t.Fatal("save precedes successful tests")
		}
	}
	unit := steps["Run unit tests"]
	if unit.ID != "unit" || strings.TrimSpace(unit.Run) != "./scripts/run-unit-tests.sh" {
		t.Fatal("unit orchestration drift")
	}
	if order["Restore unit build cache"] >= order["Run unit tests"] || order["Restore unit modules"] >= order["Download dependencies"] {
		t.Fatal("restore occurs after compilation/download")
	}
	coverage := steps["Upload coverage to Codecov"]
	if coverage.If != "runner.os == 'Linux'" || coverage.Uses != "codecov/codecov-action@v4" || coverage.With["flags"] != "unittests" || coverage.With["file"] != "${{ steps.unit.outputs.artifact-dir }}/coverage.txt" || coverage.ContinueOnError != true {
		t.Fatal("Linux coverage upload contract changed")
	}
	artifacts := steps["Preserve unit timing evidence"]
	if artifacts.If != "always() && steps.unit.outputs.artifact-dir != ''" || artifacts.Uses != "actions/upload-artifact@v4" || artifacts.With["retention-days"] != 7 {
		t.Fatal("timing evidence must survive test failures with bounded retention")
	}
	for _, name := range []string{"test-integration", "test-e2e"} {
		if workflow.Jobs[name].Needs != "test-unit" {
			t.Fatalf("%s no longer depends on unit tests", name)
		}
	}
}

// Shell behavior is part of the existing Go unit gate, not a manual-only test.
func TestUnitWrapperContract(t *testing.T) {
	command := exec.CommandContext(t.Context(), "bash", "../run-unit-tests.test.sh")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unit wrapper contract: %v\n%s", err, output)
	}
}
