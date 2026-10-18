// Package processfixture supplies test-only subprocess images with exact output.
package processfixture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type testExecutable struct {
	once sync.Once
	path string
}

var coveredExecutables sync.Map // *testing.T -> *testExecutable; removed by that test's cleanup

// Executable returns the current package's test binary without coverage
// instrumentation. Coverage-enabled self-exec children otherwise emit runtime
// diagnostics when GOCOVERDIR is absent, or require writes forbidden by a
// read-only sandbox. Never strip stderr or grant coverage-output writes instead.
// The parent remains covered, and the child preserves its race instrumentation.
// Calls for the same test return the same image until that test's cleanup.
func Executable(t *testing.T) string {
	t.Helper()
	path := ""
	if testing.CoverMode() == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		entry, loaded := coveredExecutables.LoadOrStore(t, &testExecutable{})
		if !loaded {
			t.Cleanup(func() { coveredExecutables.Delete(t) })
		}
		image := entry.(*testExecutable)
		image.once.Do(func() {
			image.path = buildExecutable(t)
		})
		path = image.path
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func buildExecutable(t *testing.T) string {
	t.Helper()
	name := "process-helper.test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"test", "-c", "-cover=false", "-o", path}
	if raceEnabled {
		arguments = append(arguments, "-race")
	}
	arguments = append(arguments, ".")
	// #nosec G204 -- CI's pinned Go compiles this trusted package's tests;
	// no application-supplied executable, package, flags or shell is accepted.
	cmd := exec.CommandContext(ctx, goTool, arguments...)
	// Even an empty -covermode/-coverpkg flag enables Go instrumentation.
	// Keep inherited coverage flags out of this test-only fixture build too.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build uninstrumented process fixture: %v\n%s", err, output)
	}
	return path
}
