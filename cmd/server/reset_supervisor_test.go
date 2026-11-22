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

func TestManagedResetSupervisorChild(t *testing.T) {
	if os.Getenv("ORI_TEST_MANAGED_CHILD") != "1" {
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
