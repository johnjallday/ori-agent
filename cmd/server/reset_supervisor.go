package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

const (
	supervisorPIDEnv  = "ORI_RESET_SUPERVISOR_PID"
	expectedResetEnv  = "ORI_RESET_EXPECTED_OPERATION"
	restartReplyGrace = 2 * time.Second
)

// The supervisor never opens app stores or serves HTTP. It starts an exact
// pinned binary, and recognizes a request only from that direct child's private
// pipe, after a successful join. No exit code alone requests a replacement.
type managedServerSpec struct {
	binary, root   string
	inode          os.FileInfo
	args, env      []string
	stdin          io.Reader
	stdout, stderr io.Writer
}

func runResetSupervisor(port int) error {
	if !resetstate.Supported() {
		return errors.New("managed reset restart is unavailable on this platform")
	}
	if port < 1 || port > 65535 {
		return errors.New("managed reset restart requires a fixed valid port")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	inode, err := os.Stat(binary)
	if err != nil {
		return err
	}
	root := os.Getenv("ORI_DATA_DIR") // Already canonicalized by activateDataDirectory.
	spec := managedServerSpec{binary: binary, root: root, inode: inode, args: os.Args[1:],
		env: append(os.Environ(), "PORT="+strconv.Itoa(port)), stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return superviseResetServer(ctx, spec)
}

func superviseResetServer(ctx context.Context, spec managedServerSpec) error {
	var expected string
	for ctx.Err() == nil {
		requested, err := spec.runChild(ctx, expected)
		if ctx.Err() != nil {
			return nil // An intentional stop never launches a replacement.
		}
		if err != nil {
			return fmt.Errorf("server stopped: %w; check the error above and relaunch the same Ori installation manually", err)
		}
		if requested == "" {
			return nil // Ordinary child exit.
		}
		if requested == expected {
			return errors.New("refused a repeated restart for the same reset; relaunch the same installation manually")
		}
		// One replacement per operation. Any failure to start/own the correct
		// root or address exits the supervisor instead of respawning.
		expected = requested
	}
	return nil
}

func (s managedServerSpec) runChild(ctx context.Context, expected string) (string, error) {
	current, err := os.Stat(s.binary)
	if err != nil {
		return "", err
	}
	if !os.SameFile(s.inode, current) || !s.inode.ModTime().Equal(current.ModTime()) || s.inode.Size() != current.Size() {
		return "", errors.New("the managed server executable changed")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()
	// #nosec G204 -- canonical path to this exact executable; args are the operator's original CLI.
	cmd := exec.Command(s.binary, s.args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = s.root, s.stdin, s.stdout, s.stderr
	cmd.Env = append(append([]string(nil), s.env...), supervisorPIDEnv+"="+strconv.Itoa(os.Getpid()), expectedResetEnv+"="+expected)
	cmd.ExtraFiles = []*os.File{writer} // FD 3 is a private, one-way restart signal.
	err = cmd.Start()
	_ = writer.Close()
	if err != nil {
		return "", err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err = <-wait:
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(35 * time.Second)
		defer timer.Stop()
		select {
		case <-wait:
		case <-timer.C:
			_ = cmd.Process.Kill()
			<-wait
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// A bounded protocol, read only after our exact writer process exited.
	// Deadline also handles an unexpected inherited descriptor.
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(reader, 130))
	if err != nil || len(data) == 0 {
		return "", err
	}
	id, ok := strings.CutSuffix(string(data), "\n")
	if !ok || !settingsreset.ValidOperationID(id) {
		return "", errors.New("invalid managed reset restart handoff")
	}
	return id, nil
}

type inheritedResetSupervisor struct {
	pipe *os.File
	pid  int
}

func resetSupervisorFromEnvironment() *inheritedResetSupervisor {
	pid, err := strconv.Atoi(os.Getenv(supervisorPIDEnv))
	if err != nil || pid <= 1 || pid != os.Getppid() || !resetstate.Supported() {
		return nil
	}
	pipe := os.NewFile(3, "ori-reset-supervisor")
	if pipe == nil {
		return nil
	}
	info, err := pipe.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil
	}
	markResetPipeCloseOnExec()
	return &inheritedResetSupervisor{pipe: pipe, pid: pid}
}

func (s *inheritedResetSupervisor) ready() bool {
	return s != nil && os.Getppid() == s.pid
}

func (s *inheritedResetSupervisor) releaseOperation(id string) error {
	if !s.ready() || !settingsreset.ValidOperationID(id) {
		return errors.New("managed reset restart supervisor is unavailable")
	}
	_, err := io.WriteString(s.pipe, id+"\n")
	return err
}
