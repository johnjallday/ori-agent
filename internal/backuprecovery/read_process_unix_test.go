//go:build darwin || linux

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackupReadProcessHelper(t *testing.T) {
	mode := os.Getenv("ORI_BACKUP_TEST_PROCESS")
	if mode == "" {
		return
	}
	switch mode {
	case "metadata_broker":
		var request struct {
			Path          string
			Device, Inode uint64
		}
		if json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&request) != nil {
			os.Exit(6)
		}
		anchor, err := newMetadataAnchor(request.Path, anchoredIdentity{device: request.Device, inode: request.Inode})
		if err != nil {
			os.Exit(6)
		}
		bundle, err := anchor.collect(t.Context())
		anchor.close()
		if err != nil {
			os.Exit(6)
		}
		encoded, err := encodeMetadataBundle(bundle)
		if err != nil {
			os.Exit(6)
		}
		if _, err := os.Stdout.Write(encoded); err != nil {
			os.Exit(6)
		}
	case "pipeline":
		if syscall.Getpgrp() != os.Getpid() {
			os.Exit(7)
		}
		executable, err := os.Executable()
		if err != nil {
			os.Exit(7)
		}
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(7)
		}
		out, err := runReadProcess(t.Context(), readProcessSpec{executable: executable, arguments: []string{"-test.run=^TestBackupReadProcessHelper$"}, directory: directory, environment: []string{"ORI_BACKUP_TEST_PROCESS=pipeline_child"}, stdoutLimit: 1024, timeout: 5 * time.Second, pipelineMember: true})
		if err != nil || string(out) != "[]" {
			os.Exit(7)
		}
		_, _ = os.Stdout.Write(out)
	case "pipeline_blocked":
		if syscall.Getpgrp() != os.Getpid() {
			os.Exit(7)
		}
		executable, err := os.Executable()
		if err != nil {
			os.Exit(7)
		}
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(7)
		}
		_, _ = runReadProcess(t.Context(), readProcessSpec{executable: executable, arguments: []string{"-test.run=^TestBackupReadProcessHelper$"}, directory: directory, environment: []string{"ORI_BACKUP_TEST_PROCESS=heartbeat", "ORI_BACKUP_TEST_HEARTBEAT=" + os.Getenv("ORI_BACKUP_TEST_HEARTBEAT")}, stdoutLimit: 1024, timeout: providerReadLimit, pipelineMember: true})
		os.Exit(7)
	case "pipeline_child":
		if syscall.Getpgrp() != os.Getppid() || syscall.Getpgrp() == os.Getpid() {
			os.Exit(7)
		}
		if configurePipelineReadProcess(exec.Command("must-not-run")) {
			os.Exit(7)
		}
		_, _ = os.Stdout.WriteString("[]")
	case "success":
		_, _ = os.Stdout.WriteString("[]")
	case "input":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "stderr":
		_, _ = os.Stdout.WriteString("[]")
		_, _ = os.Stderr.WriteString("invented private provider path and diagnostic")
	case "failure":
		_, _ = os.Stdout.WriteString("[]")
		os.Exit(3)
	case "stdout_limit":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, 4096))
	case "stderr_limit":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'x'}, maxReadStderrBytes+1))
	case "wait":
		time.Sleep(time.Minute)
	case "heartbeat":
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if err := os.WriteFile(os.Getenv("ORI_BACKUP_TEST_HEARTBEAT"), []byte("owned child alive"), 0o600); err != nil {
				os.Exit(6)
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "descendant":
		executable, err := os.Executable()
		if err != nil {
			os.Exit(6)
		}
		// Test-only child deliberately outlives this parent and retains its pipes.
		child := exec.Command(executable, "-test.run=^TestBackupReadProcessHelper$")
		child.Env = []string{"ORI_BACKUP_TEST_PROCESS=heartbeat", "ORI_BACKUP_TEST_HEARTBEAT=" + os.Getenv("ORI_BACKUP_TEST_HEARTBEAT")}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(6)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := os.Stat(os.Getenv("ORI_BACKUP_TEST_HEARTBEAT")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(6)
			}
			time.Sleep(10 * time.Millisecond)
		}
	case "environment":
		if os.Getenv("RESTIC_PASSWORD_COMMAND") != "" || os.Getenv("RESTIC_REPOSITORY") != "" {
			os.Exit(4)
		}
		_, _ = os.Stdout.WriteString("[]")
	default:
		os.Exit(5)
	}
	os.Exit(0)
}

func readProcessFixture(t *testing.T, mode string) readProcessSpec {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return readProcessSpec{executable: executable, arguments: []string{"-test.run=^TestBackupReadProcessHelper$"},
		environment: []string{"ORI_BACKUP_TEST_PROCESS=" + mode}, directory: directory,
		stdoutLimit: 1024, timeout: 5 * time.Second}
}

func TestReadProcessNestedChildRetainsOwnedPipelineGroup(t *testing.T) {
	out, err := runReadProcess(t.Context(), readProcessFixture(t, "pipeline"))
	if err != nil || string(out) != "[]" {
		t.Fatal("nested child escaped/lost pipeline ownership")
	}
}

func TestReadProcessDeadlineStopsBlockedHelpersNestedPipelineChild(t *testing.T) {
	spec := readProcessFixture(t, "pipeline_blocked")
	heartbeat := filepath.Join(spec.directory, "invented-heartbeat")
	spec.environment = append(spec.environment, "ORI_BACKUP_TEST_HEARTBEAT="+heartbeat)
	spec.timeout = 750 * time.Millisecond
	if out, err := runReadProcess(t.Context(), spec); err != errReadProcessDeadline || len(out) != 0 {
		t.Fatal("blocked nested pipeline escaped its outer deadline")
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal("nested writer never started; control is inconclusive")
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil || before.ModTime() != after.ModTime() {
		t.Fatal("nested pipeline child continued writing after its owner was reaped")
	}
}

func TestReadProcessRejectsExitZeroWithStderrAndReapsBoundedFailures(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want error
	}{
		{"success", nil}, {"stderr", errReadProcess}, {"failure", errReadProcess},
		{"stdout_limit", errReadProcessLimit}, {"stderr_limit", errReadProcessLimit},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			data, err := runReadProcess(t.Context(), readProcessFixture(t, tt.mode))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v; want %v", err, tt.want)
			}
			if tt.want != nil && len(data) != 0 {
				t.Fatal("partial output escaped failure")
			}
			if tt.want == nil && string(data) != "[]" {
				t.Fatal("unexpected successful output")
			}
		})
	}
}

func TestReadProcessDeadlineAndCancellation(t *testing.T) {
	spec := readProcessFixture(t, "wait")
	spec.timeout = 100 * time.Millisecond
	start := time.Now()
	if data, err := runReadProcess(t.Context(), spec); len(data) != 0 || err != errReadProcessDeadline {
		t.Fatalf("deadline result: %v", err)
	}
	if time.Since(start) > time.Second+childDrainLimit {
		t.Fatal("deadline did not bound child cleanup")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runReadProcess(ctx, spec); err != errReadProcessCancelled {
		t.Fatal("cancelled parent started a child")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	spec.timeout = 5 * time.Second
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	if _, err := runReadProcess(ctx, spec); err != errReadProcessCancelled {
		t.Fatal("in-flight cancellation failed")
	}
}

func TestReadProcessCarriesOnlyAnchoredEncryptedMetadata(t *testing.T) {
	root, anchor := metadataAnchorFixture(t)
	spec := readProcessFixture(t, "metadata_broker")
	spec.stdoutLimit = maxBrokerOutputBytes
	var err error
	spec.input, err = json.Marshal(struct {
		Path          string
		Device, Inode uint64
	}{root, anchor.identity.device, anchor.identity.inode})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := runReadProcess(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := decodeMetadataBundle(wire)
	if err != nil || len(bundle.entries) != 3 {
		t.Fatal("bounded broker transport failed")
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := runReadProcess(t.Context(), spec); err != errReadProcess || len(output) != 0 {
		t.Fatal("broker accepted same-name root replacement")
	}
}

func TestReadProcessStopsDescendantAfterParentExits(t *testing.T) {
	spec := readProcessFixture(t, "descendant")
	heartbeat := filepath.Join(spec.directory, "owned-heartbeat")
	spec.environment = append(spec.environment, "ORI_BACKUP_TEST_HEARTBEAT="+heartbeat)
	spec.timeout = 8 * time.Second
	if _, err := runReadProcess(t.Context(), spec); err != errReadProcess {
		t.Fatalf("expected bounded pipe-drain failure: %v", err)
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal("descendant never started")
	}
	time.Sleep(120 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("descendant continued writing after process cleanup")
	}
}

func TestReadProcessDoesNotInheritProviderEnvironment(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD_COMMAND", "must never execute")
	t.Setenv("RESTIC_REPOSITORY", "must never select")
	if data, err := runReadProcess(t.Context(), readProcessFixture(t, "environment")); err != nil || string(data) != "[]" {
		t.Fatal("ambient environment inherited")
	}
	spec := readProcessFixture(t, "input")
	spec.input = []byte("invented pipe input\n")
	if data, err := runReadProcess(t.Context(), spec); err != nil || !bytes.Equal(data, spec.input) {
		t.Fatal("bounded pipe input failed")
	}
}

func TestReadProcessInvalidSpecAndErrorsAreRedacted(t *testing.T) {
	for _, mutate := range []func(*readProcessSpec){
		func(s *readProcessSpec) { s.environment = nil },
		func(s *readProcessSpec) { s.stdoutLimit = 0 },
		func(s *readProcessSpec) { s.stdoutLimit = maxBrokerOutputBytes + 1 },
		func(s *readProcessSpec) { s.timeout = 0 },
		func(s *readProcessSpec) { s.timeout = completeReadLimit + time.Second },
		func(s *readProcessSpec) { s.executable = "/invented-private-path/does-not-exist" },
	} {
		spec := readProcessFixture(t, "success")
		mutate(&spec)
		if data, err := runReadProcess(t.Context(), spec); len(data) != 0 || err != errReadProcess {
			t.Fatal("invalid process specification accepted")
		}
	}
	for _, err := range []error{errReadProcess, errReadProcessLimit, errReadProcessDeadline, errReadProcessCancelled} {
		if strings.Contains(err.Error(), "/") {
			t.Fatal("path in process error")
		}
	}
}
