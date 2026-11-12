package ci

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Before this contract, every lighter Linux job restored the same setup-go key
// and whichever saved first (a ~24 MB archive from a job that compiled almost
// nothing) owned it, so a job with an expensive compile could never publish its
// own work. The unit jobs avoid that with explicit, namespaced restore and save
// steps. This test keeps that ownership one-sided: the unit namespaces are
// written by exactly one job, no other job publishes a cache with an explicit
// step, and nothing else borrows the unit namespaces. A new cache namespace
// needs its own single writer and a measured case (the Linux application-build
// cache was measured, about 250 MiB per archive against no critical-path
// benefit, and deliberately not added).
func TestCacheNamespacesKeepOneExplicitWriter(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]unitJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	const owner = "test-unit-run"
	var ownerSaves []string
	for id, job := range workflow.Jobs {
		for _, step := range job.Steps {
			// actions/cache@ both restores and saves; actions/cache/save@ only saves.
			// actions/cache/restore@ never publishes.
			publishes := strings.HasPrefix(step.Uses, "actions/cache/save@") || strings.HasPrefix(step.Uses, "actions/cache@")
			// A restore with a restore-keys prefix borrows a namespace as surely as a key does.
			key := fmt.Sprint(step.With["key"]) + " " + fmt.Sprint(step.With["restore-keys"])
			if id != owner && strings.Contains(key, "ori-unit-") {
				t.Fatalf("job %s (step %q) uses a unit cache namespace owned by %s", id, step.Name, owner)
			}
			if !publishes {
				continue
			}
			if id != owner {
				t.Fatalf("job %s (step %q) publishes a cache with an explicit step; a namespace needs exactly one writer, and a new one needs a measured case first", id, step.Name)
			}
			ownerSaves = append(ownerSaves, step.Name)
		}
	}
	if strings.Join(ownerSaves, "|") != "Save unit modules|Save unit build cache" {
		t.Fatalf("%s must publish exactly its two unit namespaces, got %q", owner, ownerSaves)
	}
}
