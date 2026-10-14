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

type unitShard struct {
	OS    string `yaml:"os"`
	Shard string `yaml:"shard"`
}

type unitMatrix struct {
	OS        []string    `yaml:"os"`
	GoVersion []string    `yaml:"go-version"`
	Include   []unitShard `yaml:"include"`
}

type unitJob struct {
	Name            string            `yaml:"name"`
	RunsOn          string            `yaml:"runs-on"`
	Needs           string            `yaml:"needs"`
	If              string            `yaml:"if"`
	Permissions     map[string]string `yaml:"permissions"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Strategy        struct {
		Matrix   unitMatrix `yaml:"matrix"`
		FailFast *bool      `yaml:"fail-fast"`
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
	gate := workflow.Jobs["test-unit"]
	if gate.Name != "Unit Tests" || gate.RunsOn != pinnedUbuntuMatrixRunner {
		t.Fatal("required-check identity changed")
	}
	wantMatrix := unitMatrix{OS: []string{"ubuntu-latest", "macos-latest"}, GoVersion: []string{"go.mod"}}
	if !reflect.DeepEqual(gate.Strategy.Matrix, wantMatrix) {
		t.Fatalf("matrix renamed required contexts: %#v", gate.Strategy.Matrix)
	}
	if gate.Needs != "test-unit-run" || gate.If != "${{ always() }}" {
		t.Fatal("required gate must report failure for failed, cancelled or skipped executions")
	}
	job := workflow.Jobs["test-unit-run"]
	wantExecution := unitMatrix{Include: []unitShard{
		{"ubuntu-latest", "1"}, {"ubuntu-latest", "2"}, {"ubuntu-latest", "3"}, {"ubuntu-latest", "4"}, {"macos-latest", "platform"},
	}}
	if !reflect.DeepEqual(job.Strategy.Matrix, wantExecution) || job.RunsOn != pinnedUbuntuMatrixRunner {
		t.Fatal("must execute four Linux shards and one unchanged Darwin subset")
	}
	if job.Env["ORI_UNIT_SHARD"] != "${{ matrix.os == 'ubuntu-latest' && matrix.shard || '' }}" || job.Env["ORI_UNIT_SHARD_COUNT"] != "${{ matrix.os == 'ubuntu-latest' && '4' || '' }}" || job.Env["ORI_SKIP_CACHE_PRUNE"] != "1" {
		t.Fatal("unit scope or cache ownership drift")
	}
	for _, j := range []unitJob{job, gate} {
		if !reflect.DeepEqual(j.Permissions, map[string]string{"contents": "read"}) || j.ContinueOnError == true {
			t.Fatal("unit job permissions expanded or failures hidden")
		}
		if j.Strategy.FailFast == nil || *j.Strategy.FailFast {
			t.Fatal("unit siblings must finish and preserve failure evidence")
		}
	}
	steps := map[string]unitStep{}
	order := map[string]int{}
	for i, step := range job.Steps {
		steps[step.Name] = step
		order[step.Name] = i
		if step.ContinueOnError == true {
			switch step.Name {
			case "Restore unit modules", "Restore unit build cache", "Save unit modules", "Save unit build cache", "Preserve unit timing evidence":
			default:
				t.Fatalf("%s hides failure", step.Name)
			}
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
		if step.ContinueOnError != true {
			t.Fatal("optional cache restore failure must not break test correctness")
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
	if strings.Contains(prefix, "\n") || !strings.HasPrefix(prefix, "ori-unit-build-v2-") || !strings.HasSuffix(prefix, "-race-cover-atomic-${{ matrix.shard }}-") {
		t.Fatal("unbounded build fallback or instrumentation drift")
	}
	wantKey := prefix + "${{ github.sha }}-${{ github.run_id }}-${{ github.run_attempt }}"
	if build.With["key"] != wantKey {
		t.Fatal("immutable build key cannot refresh each successful source/run attempt")
	}
	for _, pair := range [][2]string{{"Save unit modules", "unit_modules"}, {"Save unit build cache", "unit_build"}} {
		save := steps[pair[0]]
		if save.Uses != "actions/cache/save@v4" || save.If != "success() && steps.unit.outcome == 'success' && steps."+pair[1]+".outcome == 'success' && steps."+pair[1]+".outputs.cache-hit != 'true'" || save.ContinueOnError != true {
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
	publication := steps["Publish successful unit shard"]
	if publication.If != "success() && runner.os == 'Linux'" || publication.Uses != "actions/upload-artifact@v4" || publication.With["name"] != "unit-results-${{ matrix.shard }}" || publication.With["overwrite"] != true || publication.With["if-no-files-found"] != "error" || publication.ContinueOnError == true {
		t.Fatal("successful shard results must be mandatory, isolated and rerunnable")
	}
	for _, file := range []string{"packages.txt", "all-packages.txt", "metadata.txt", "coverage.txt"} {
		if !strings.Contains(publication.With["path"].(string), "/"+file) {
			t.Fatalf("shard results lack %s", file)
		}
	}
	if _, ok := steps["Upload coverage to Codecov"]; ok {
		t.Fatal("partial shard coverage must not be uploaded")
	}
	gateSteps := map[string]unitStep{}
	gateOrder := map[string]int{}
	for i, step := range gate.Steps {
		gateSteps[step.Name], gateOrder[step.Name] = step, i
		if step.ContinueOnError == true && step.Name != "Upload coverage to Codecov" {
			t.Fatalf("required gate %s hides failures", step.Name)
		}
	}
	guard := gateSteps["Require every unit execution"]
	if gateOrder[guard.Name] != 0 || guard.If != "" || guard.Env["UNIT_RESULT"] != "${{ needs.test-unit-run.result }}" || !strings.Contains(guard.Run, `if [[ "$UNIT_RESULT" != success ]]`) || !strings.Contains(guard.Run, "exit 1") {
		t.Fatal("required gate must reject every non-success result before any other work")
	}
	receive := gateSteps["Receive successful unit shards"]
	if receive.Uses != "actions/download-artifact@v4" || receive.If != "runner.os == 'Linux'" || receive.With["pattern"] != "unit-results-*" || receive.With["merge-multiple"] == true {
		t.Fatal("all separate result manifests must reach the Linux gate")
	}
	merge := gateSteps["Verify complete partition and merge coverage"]
	for _, fragment := range []string{"set -euo pipefail", "./scripts/list-unit-packages.sh", "python3 scripts/unit-shards.py merge --count 4", "--packages", "--directory", "--revision", "git rev-parse HEAD"} {
		if !strings.Contains(merge.Run, fragment) {
			t.Fatalf("coverage aggregation missing %s", fragment)
		}
	}
	if merge.If != "runner.os == 'Linux'" || gateOrder[receive.Name] >= gateOrder[merge.Name] {
		t.Fatal("Linux-only coverage verification must follow artifact download")
	}
	coverage := gateSteps["Upload coverage to Codecov"]
	if coverage.If != "runner.os == 'Linux'" || coverage.Uses != "codecov/codecov-action@v4" || coverage.With["flags"] != "unittests" || coverage.With["file"] != "${{ runner.temp }}/unit-coverage.txt" || coverage.ContinueOnError != true || gateOrder[merge.Name] >= gateOrder[coverage.Name] {
		t.Fatal("Linux coverage upload must follow successful full-suite verification")
	}
	artifacts := steps["Preserve unit timing evidence"]
	if artifacts.If != "always() && steps.unit.outputs.artifact-dir != ''" || artifacts.Uses != "actions/upload-artifact@v4" || artifacts.With["retention-days"] != 7 || artifacts.ContinueOnError != true || !strings.Contains(artifacts.With["name"].(string), "matrix.shard") {
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
