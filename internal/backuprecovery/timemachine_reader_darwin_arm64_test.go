//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FR25: no source path appears in any child process argument. The helper's
// only children are tmutil's two fixed read verbs; sources reach it on stdin
// and appear in the sandbox profile only as content denials.
func TestTimeMachineHelperChildrenNeverReceiveASourcePath(t *testing.T) {
	scratch := t.TempDir()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "Night Drive Manuscript")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	var specs []readProcessSpec
	original := timeMachineRunProcess
	timeMachineRunProcess = func(_ context.Context, spec readProcessSpec) ([]byte, error) {
		specs = append(specs, spec)
		if spec.arguments[0] == "status" {
			return timeMachineFixture(t, "status_idle.plist"), nil
		}
		return timeMachineFixture(t, "destinationinfo_none.plist"), nil
	}
	t.Cleanup(func() { timeMachineRunProcess = original })

	result, err := runTimeMachineHelper(t.Context(), timeMachineHelperRequest{Scratch: scratch, Sources: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || strings.Join(specs[0].arguments, " ") != "destinationinfo -X" || strings.Join(specs[1].arguments, " ") != "status -X" {
		t.Fatal("the helper ran something other than the two fixed verbs", specs)
	}
	for _, spec := range specs {
		if spec.executable != "/usr/bin/tmutil" || !spec.pipelineMember || spec.directory != "/" || len(spec.input) != 0 || spec.profile == "" {
			t.Fatal("verb not run as a contained fixed system command", spec)
		}
		for _, value := range append(append([]string{spec.executable, spec.directory}, spec.arguments...), spec.environment...) {
			if strings.Contains(value, source) || strings.Contains(value, "Night Drive") {
				t.Fatal("a source path reached a child process", value)
			}
		}
		if !strings.Contains(spec.profile, `(deny file-read-data (subpath "`+source+`"))`) || strings.Contains(spec.profile, "(allow network") || strings.Contains(spec.profile, "file-write") {
			t.Fatal("the profile does not deny the source's contents or widens access")
		}
	}
	if len(result.Sticky) != 1 || result.Sticky[0] != timeMachineIncluded {
		t.Fatal("an unexcluded scratch folder read as", result.Sticky)
	}
	if result.PreferencesDenied == (len(result.Preferences) != 0) {
		t.Fatal("the preferences read must be either data or a Full Disk Access refusal")
	}
	if _, err := runTimeMachineHelper(t.Context(), timeMachineHelperRequest{Scratch: scratch, Sources: []string{"relative/path"}}); err == nil {
		t.Fatal("a relative source was accepted")
	}
}

// Opt-in live acceptance on the validated Mac: the real verbs under the real
// profile. Read-only; it never runs a mutating verb. Run with
// ORI_BACKUP_TEST_TIME_MACHINE=1.
func TestTimeMachineHelperLiveReadOnValidatedHost(t *testing.T) {
	if os.Getenv("ORI_BACKUP_TEST_TIME_MACHINE") != "1" {
		t.Skip("set ORI_BACKUP_TEST_TIME_MACHINE=1 on the validated Mac")
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The real helper leads its own process group; a test process does not,
	// so only that flag changes. Executable, verbs and profile stay real.
	original := timeMachineRunProcess
	timeMachineRunProcess = func(ctx context.Context, spec readProcessSpec) ([]byte, error) {
		spec.pipelineMember = false
		return original(ctx, spec)
	}
	t.Cleanup(func() { timeMachineRunProcess = original })
	result, err := runTimeMachineHelper(t.Context(), timeMachineHelperRequest{Scratch: t.TempDir(), Sources: []string{folder}})
	if err != nil {
		t.Fatal("live read failed", err)
	}
	destinations, err := parseTimeMachineDestinations(result.DestinationInfo)
	if err != nil {
		t.Fatal("destinationinfo under the profile did not parse", err)
	}
	running, err := parseTimeMachineStatus(result.Status)
	if err != nil {
		t.Fatal("status under the profile lacked Percent: the backupd XPC rule is not effective", err)
	}
	mapped, err := timeMachineReadResult(localConfiguration{Provider: providerTimeMachine, Sources: []localSource{{Path: folder}}}, result, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("destinations=%d running=%v preferences_denied=%v sticky=%v completed=%d", len(destinations), running, result.PreferencesDenied, result.Sticky, len(mapped.snapshots))
}

func TestTimeMachineUtilityIsAPinnedSystemExecutable(t *testing.T) {
	if systemExecutablePath(systemTimeMachineUtility) != "/usr/bin/tmutil" {
		t.Fatal("tmutil path changed")
	}
	utility, err := verifySystemExecutable(systemTimeMachineUtility)
	if err != nil {
		t.Skip("this host's /usr/bin/tmutil is not on a sealed system volume:", err)
	}
	defer utility.close()
	if !utility.unchanged() {
		t.Fatal("the pinned executable changed under its anchor")
	}
}
