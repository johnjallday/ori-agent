package folderdigest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAlbumsFixture_ExactAndUnmarkedVariants(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			sandbox := t.TempDir()
			args := []string{"../../scripts/folder-collection-seed.sh"}
			if exact {
				args = append(args, "--exact-albums")
			}
			args = append(args, sandbox, "Albums")
			cmd := exec.Command("bash", args...) // #nosec G204 -- fixed repository fixture helper and test-owned paths
			cmd.Env = append(os.Environ(), "TMPDIR="+filepath.Dir(sandbox))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("seed: %s %v", out, err)
			}
			result, err := Scan(filepath.Join(sandbox, "Albums"), Options{})
			if err != nil {
				t.Fatal(err)
			}
			wantFiles, wantEntries, wantProjects := 15, 26, 5
			if exact {
				wantFiles, wantEntries, wantProjects = 8, 15, 6
			}
			v := Decide(result, now)
			if result.RootCandidate().FileCount != wantFiles || result.Entries != wantEntries || result.Partial ||
				!IsCollection(result, now) || v.Portfolio == nil || v.Portfolio.Projects != wantProjects {
				t.Fatalf("counts/recognition: %+v / %+v", result, v.Portfolio)
			}
			key, supported := IntegrationProjects(result, ShapeAudio, now)
			if key != "ori_reaper" || supported != 5 {
				t.Fatalf("six RPP files must remain five supported folders: %s %d", key, supported)
			}
			for _, candidate := range result.Subfolders() {
				if candidate.Name == "Logic Sketch" && ((candidate.Marker != nil) != exact) {
					t.Fatalf("Logic variant recognition: %+v", candidate)
				}
				if candidate.Name == "Album-5" && candidate.FileCount != 2+boolInt(!exact) {
					t.Fatal("multiple project-file choice was lost")
				}
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestAlbumsPolicy_ThresholdsAndPartialEvidenceRemainUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                           string
		audio, other                   int
		partial, collection, portfolio bool
	}{
		{"empty", 0, 0, false, false, false},
		{"single", 1, 0, false, false, false},
		{"collection", 2, 0, false, true, false},
		{"below_portfolio", 4, 0, false, true, false},
		{"mixed_below_portfolio", 4, 2, false, true, false},
		{"portfolio", 5, 0, false, true, true},
		{"partial_below_portfolio", 4, 0, true, true, false},
		{"partial_observed_portfolio", 5, 0, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Result{Partial: tc.partial, Candidates: []Candidate{{IsRoot: true, Name: "Albums"}}}
			for i := range tc.audio + tc.other {
				marker := Marker{Name: "*.rpp", Shape: ShapeAudio, Label: "REAPER session"}
				if i >= tc.audio {
					marker = Marker{Name: "*.tex", Shape: ShapeManuscript, Label: "Manuscript"}
				}
				r.Candidates = append(r.Candidates, Candidate{Name: fmt.Sprint(i), Marker: &marker, FileCount: 1})
			}
			v := Decide(r, now)
			if IsCollection(r, now) != tc.collection || (v.Portfolio != nil) != tc.portfolio || v.Partial != tc.partial {
				t.Fatalf("threshold or coverage changed: %+v", v)
			}
		})
	}
}
