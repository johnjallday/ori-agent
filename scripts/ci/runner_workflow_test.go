package ci

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// matrix.os remains a check-name label, not the selected Ubuntu image.
const pinnedUbuntuMatrixRunner = "${{ matrix.os == 'ubuntu-latest' && 'ubuntu-24.04' || matrix.os }}"

func TestWorkflowsPinUbuntuRunners(t *testing.T) {
	paths, err := filepath.Glob("../../.github/workflows/*.y*ml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find workflows: %v (%d files)", err, len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				Jobs map[string]struct {
					RunsOn   string `yaml:"runs-on"`
					Uses     string `yaml:"uses"`
					Strategy struct {
						Matrix struct {
							OS      []string `yaml:"os"`
							Include []struct {
								OS string `yaml:"os"`
							} `yaml:"include"`
						} `yaml:"matrix"`
					} `yaml:"strategy"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(data, &workflow); err != nil {
				t.Fatal(err)
			}
			for name, job := range workflow.Jobs {
				switch job.RunsOn {
				case "ubuntu-24.04", "macos-latest", "windows-latest":
				case pinnedUbuntuMatrixRunner, "${{ matrix.os }}":
					// Check both the CI matrix and the installer's include rows.
					// Only the pinned selector may consume legacy Ubuntu labels.
					images := append([]string(nil), job.Strategy.Matrix.OS...)
					for _, entry := range job.Strategy.Matrix.Include {
						images = append(images, entry.OS)
					}
					if len(images) == 0 {
						t.Errorf("%s: no matrix runner images", name)
					}
					for _, image := range images {
						if job.RunsOn == pinnedUbuntuMatrixRunner && image == "ubuntu-latest" {
							image = "ubuntu-24.04"
						}
						if strings.HasPrefix(image, "ubuntu") && image != "ubuntu-24.04" {
							t.Errorf("%s: unpinned Ubuntu matrix image %q", name, image)
						}
					}
				case "":
					if job.Uses == "" {
						t.Errorf("%s: missing runner or reusable workflow", name)
					}
				default:
					t.Errorf("%s: unreviewed runner %q; keep Linux on ubuntu-24.04 until migration is tested", name, job.RunsOn)
				}
			}
		})
	}
}

func TestCIMatrixRunnerPinsPreserveCheckNames(t *testing.T) {
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
	for _, test := range []struct {
		id     string
		name   string
		matrix map[string][]string
	}{
		{"devtools-contract", "Ori workflow adapter contract (${{ matrix.os }})", map[string][]string{
			"os": {"ubuntu-latest", "macos-latest"},
		}},
		{"test-unit", "Unit Tests", map[string][]string{
			"os": {"ubuntu-latest", "macos-latest"}, "go-version": {"1.25.12"},
		}},
		{"build", "Build", map[string][]string{
			"os": {"ubuntu-latest", "macos-latest", "windows-latest"}, "go-version": {"1.25.12"},
		}},
	} {
		t.Run(test.id, func(t *testing.T) {
			job := workflow.Jobs[test.id]
			if job.Name != test.name || !reflect.DeepEqual(job.Strategy.Matrix, test.matrix) {
				t.Fatalf("check identity changed: name=%q matrix=%#v", job.Name, job.Strategy.Matrix)
			}
			if job.RunsOn != pinnedUbuntuMatrixRunner {
				t.Fatalf("runner = %q; pin Linux without changing macOS/Windows runners or check labels", job.RunsOn)
			}
		})
	}
}
