package processfixture

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestExactOutputProcessHelper(t *testing.T) {
	if os.Getenv("ORI_PROCESS_FIXTURE_CHILD") != "1" {
		return
	}
	if testing.CoverMode() != "" || os.Getenv("ORI_PROCESS_FIXTURE_RACE") != strconv.FormatBool(raceEnabled) {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString("exact-fixture-output\n")
	os.Exit(0)
}

func TestExecutablePreservesExactChildOutputWithoutCoverageWrites(t *testing.T) {
	executable := Executable(t)
	if Executable(t) != executable {
		t.Fatal("one test received different helper images")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- only this package's compiled helper and a fixed test selector.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestExactOutputProcessHelper$")
	// Deliberately no GOCOVERDIR or inherited environment.
	cmd.Env = []string{"ORI_PROCESS_FIXTURE_CHILD=1", "ORI_PROCESS_FIXTURE_RACE=" + strconv.FormatBool(raceEnabled)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stdout.String() != "exact-fixture-output\n" || stderr.Len() != 0 {
		t.Fatalf("fixture output changed: exit=%v, stdout bytes=%d, stderr bytes=%d", err, stdout.Len(), stderr.Len())
	}
}
