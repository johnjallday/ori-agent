package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestRenderLaunchAgentUsesStableArgumentsAndEscapesXML(t *testing.T) {
	contents, err := RenderLaunchAgent(LaunchdConfig{
		HelperPath:  "/stable/bin/herdr-devflow",
		RuntimeRoot: "/stable/runtime & state",
		HerdrBinary: "/Applications/Herdr <beta>/herdr",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<string>/stable/bin/herdr-devflow</string>",
		"<string>--home</string>",
		"<string>/stable/runtime &amp; state</string>",
		"<string>--herdr-bin</string>",
		"<string>/Applications/Herdr &lt;beta&gt;/herdr</string>",
		"<string>dispatch</string>",
		"<key>StartInterval</key><integer>60</integer>",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("RenderLaunchAgent() missing %q:\n%s", want, contents)
		}
	}
	if strings.Contains(contents, "--repo-root") || strings.Contains(contents, "feature/") {
		t.Fatalf("RenderLaunchAgent() points at a feature checkout:\n%s", contents)
	}
}

func TestInstallLaunchAgentWritesOneStablePlistAndRegistersScopedJob(t *testing.T) {
	home := t.TempDir()
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	var calls [][]string
	path, err := InstallLaunchAgent(context.Background(), LaunchdConfig{
		GOOS:        "darwin",
		HomeDir:     home,
		UID:         501,
		HelperPath:  "/stable/bin/herdr-devflow",
		RuntimeRoot: runtimeRoot,
		HerdrBinary: "/usr/local/bin/herdr",
		Launchctl:   "fake-launchctl",
		Run: func(_ context.Context, command string, args ...string) error {
			calls = append(calls, append([]string{command}, args...))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != LaunchAgentPath(home) {
		t.Fatalf("InstallLaunchAgent() path = %q, want %q", path, LaunchAgentPath(home))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "/stable/bin/herdr-devflow") || !strings.Contains(string(contents), runtimeRoot) {
		t.Fatalf("plist did not use stable paths: %s", contents)
	}
	wantCalls := [][]string{
		{"fake-launchctl", "bootout", "gui/501/" + LaunchAgentLabel},
		{"fake-launchctl", "bootstrap", "gui/501", path},
		{"fake-launchctl", "kickstart", "-k", "gui/501/" + LaunchAgentLabel},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("launchctl calls = %#v, want %#v", calls, wantCalls)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("plist mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestInstallLaunchAgentRejectsUnsupportedPlatformsWithoutWriting(t *testing.T) {
	home := t.TempDir()
	_, err := InstallLaunchAgent(context.Background(), LaunchdConfig{GOOS: "linux", HomeDir: home, UID: 1, HelperPath: "/stable/helper", RuntimeRoot: "/stable/runtime"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrSchedulerUnsupported {
		t.Fatalf("InstallLaunchAgent() error = %v, want unsupported stage error", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(statErr) {
		t.Fatalf("unsupported install wrote a LaunchAgents directory: %v", statErr)
	}
}
