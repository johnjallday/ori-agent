package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestManagedResetSupervisorPinsChildContractAndBoundsRelaunch(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("managed installation lease unsupported")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, secondHandoff, wantError string }{
		{"runs one replacement", "", ""},
		{"refuses repeated operation", "fixture-operation", "refused a repeated restart"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			spec := managedServerSpec{binary: binary, root: root, inode: info,
				args: []string{"-test.run=^TestManagedResetSupervisorChild$"},
				env: []string{"ORI_TEST_MANAGED_CHILD=1", "ORI_TEST_SECOND_HANDOFF=" + tc.secondHandoff,
					"ORI_DATA_DIR=" + root, "PORT=43123", "ORI_TEST_SENTINEL=unchanged"}, stdout: &output, stderr: &output}
			err := superviseResetServer(ctx, spec)
			if ctx.Err() != nil || (err == nil) != (tc.wantError == "") || (err != nil && !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("supervisor result = %v (%v)\n%s", err, ctx.Err(), &output)
			}
			var ids []string
			for line := range strings.SplitSeq(output.String(), "\n") {
				if strings.HasPrefix(line, "managed child ") {
					ids = append(ids, line)
				}
			}
			if len(ids) != 2 || !strings.HasSuffix(ids[0], "initial") || !strings.HasSuffix(ids[1], "fixture-operation") ||
				strings.Fields(ids[0])[2] == strings.Fields(ids[1])[2] {
				t.Fatalf("expected exactly one distinct replacement, got %q\n%s", ids, &output)
			}
		})
	}
}

// A staged reset's recovery child may ask for exactly one more replacement once
// that operation is durably canceled, and never a repeat of either handoff.
func TestManagedResetSupervisorAllowsOneCanceledHandoffAfterTheStagedOne(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("managed installation lease unsupported")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, repeat, wantError string }{
		{"one canceled replacement then a normal exit", "", ""},
		{"a repeated canceled handoff is refused", "1", "refused a repeated restart"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			spec := managedServerSpec{binary: binary, root: root, inode: info,
				args: []string{"-test.run=^TestManagedResetSupervisorChild$"},
				env: []string{"ORI_TEST_MANAGED_CHILD=1", "ORI_TEST_CANCELED_FLOW=1", "ORI_TEST_REPEAT_CANCELED=" + tc.repeat,
					"ORI_DATA_DIR=" + root, "PORT=43123", "ORI_TEST_SENTINEL=unchanged"}, stdout: &output, stderr: &output}
			err := superviseResetServer(ctx, spec)
			if ctx.Err() != nil || (err == nil) != (tc.wantError == "") || (err != nil && !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("supervisor result = %v (%v)\n%s", err, ctx.Err(), &output)
			}
			var lines []string
			for line := range strings.SplitSeq(output.String(), "\n") {
				if strings.HasPrefix(line, "managed child ") {
					lines = append(lines, line)
				}
			}
			if len(lines) != 3 || !strings.HasSuffix(lines[0], " initial") || !strings.HasSuffix(lines[1], " fixture-operation staged") ||
				!strings.HasSuffix(lines[2], " fixture-operation canceled") {
				t.Fatalf("expected initial, one staged and one canceled child, got %q\n%s", lines, &output)
			}
		})
	}
}

func TestManagedResetSupervisorChild(t *testing.T) {
	if os.Getenv("ORI_TEST_MANAGED_CHILD") != "1" {
		return
	}
	if os.Getenv("ORI_TEST_CANCELED_FLOW") == "1" {
		supervisor := resetSupervisorFromEnvironment()
		expected, state := os.Getenv(expectedResetEnv), os.Getenv(expectedStateEnv)
		switch {
		case expected == "":
			fmt.Printf("managed child %d initial\n", os.Getpid())
			if err := supervisor.releaseOperation("fixture-operation"); err != nil {
				t.Fatal(err)
			}
		case state == "":
			fmt.Printf("managed child %d %s staged\n", os.Getpid(), expected)
			if err := supervisor.releaseCanceled(expected); err != nil {
				t.Fatal(err)
			}
		default:
			fmt.Printf("managed child %d %s %s\n", os.Getpid(), expected, state)
			if os.Getenv("ORI_TEST_REPEAT_CANCELED") == "1" {
				if err := supervisor.releaseCanceled(expected); err != nil {
					t.Fatal(err)
				}
			}
		}
		return
	}
	supervisor := resetSupervisorFromEnvironment()
	cwd, err := os.Getwd()
	if err != nil || cwd != os.Getenv("ORI_DATA_DIR") || os.Getenv("PORT") != "43123" ||
		os.Getenv("ORI_TEST_SENTINEL") != "unchanged" || !supervisor.ready() {
		t.Fatal("managed child did not inherit its launch identity")
	}
	expected := os.Getenv(expectedResetEnv)
	if expected == "" {
		fmt.Printf("managed child %d initial\n", os.Getpid())
		if err := supervisor.releaseOperation("fixture-operation"); err != nil {
			t.Fatal(err)
		}
		return
	}
	fmt.Printf("managed child %d %s\n", os.Getpid(), expected)
	if second := os.Getenv("ORI_TEST_SECOND_HANDOFF"); second != "" {
		if err := supervisor.releaseOperation(second); err != nil {
			t.Fatal(err)
		}
	}
}
