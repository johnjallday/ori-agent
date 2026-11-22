//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"golang.org/x/sys/unix"
)

const (
	nativeWatchArg     = "--ori-native-reset-watch"
	nativeBootArg      = "--ori-native-reset-reopen"
	defaultMenubarPort = 8765
)

type nativeResetLaunch struct {
	bundle, executable string
	image              os.FileInfo
}

func identifyNativeResetLaunch(bundle string) (nativeResetLaunch, error) {
	var launch nativeResetLaunch
	exe, err := os.Executable()
	if err != nil {
		return launch, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return launch, err
	}
	// NSRunningApplication has no bundle for raw exec or the current Ori
	// LaunchAgent. A path that merely happens to reside in an .app isn't enough.
	if !filepath.IsAbs(bundle) || filepath.Ext(bundle) != ".app" {
		return launch, errors.New("no registered macOS app bundle")
	}
	bundle, err = filepath.EvalSymlinks(bundle)
	if err != nil {
		return launch, err
	}
	if filepath.Dir(exe) != filepath.Join(bundle, "Contents", "MacOS") || !bundleOwnsExecutable(bundle, exe) {
		return launch, errors.New("macOS app bundle does not own the running executable")
	}
	image, err := os.Stat(exe)
	if err != nil {
		return launch, err
	}
	return nativeResetLaunch{bundle: bundle, executable: exe, image: image}, nil
}

func bundleOwnsExecutable(bundle, exe string) bool {
	declared := nativeBundleExecutable(bundle)
	if !filepath.IsAbs(declared) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(declared)
	return err == nil && resolved == exe
}

func (l nativeResetLaunch) unchanged() bool {
	if l.image == nil {
		return false
	}
	current, err := os.Stat(l.executable)
	return err == nil && os.SameFile(l.image, current) && l.image.ModTime().Equal(current.ModTime()) &&
		l.image.Size() == current.Size() && bundleOwnsExecutable(l.bundle, l.executable)
}

func parseNativeResetArgs(args []string) (string, int, error) {
	if len(args) != 2 || !settingsreset.ValidOperationID(args[0]) {
		return "", 0, errors.New("invalid macOS reset handoff")
	}
	port, err := strconv.Atoi(args[1])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("invalid macOS reset port")
	}
	return args[0], port, nil
}

func reserveNativeResetPort(port int) (net.Listener, error) {
	// Darwin can allow a wildcard bind to coexist with an interface-specific
	// listener. Check both local browser addresses explicitly; do not disable
	// SO_REUSEADDR, since the accepting app's own connections may be in TIME_WAIT.
	ctx := context.Background()
	config := net.ListenConfig{}
	for _, addr := range []string{"127.0.0.1", "::1"} {
		probe, err := config.Listen(ctx, "tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err != nil {
			return nil, err
		}
		_ = probe.Close()
	}
	return config.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
}

// startWatcher arms a private, short-lived helper before the app starts to
// quit. Closing the permit without committing never reopens the application.
func (l nativeResetLaunch) startWatcher(operation string, port int) (finish func(bool) error, err error) {
	if !l.unchanged() {
		return nil, errors.New("the macOS app changed since launch")
	}
	// #nosec G204 G702 -- exact native app image and a fixed helper mode, without a shell. The operation and port are validated by the child.
	cmd := exec.Command(l.executable, nativeWatchArg, strconv.Itoa(os.Getpid()), l.bundle, operation, strconv.Itoa(port))
	cmd.SysProcAttr = &unix.SysProcAttr{Setsid: true}
	// Keep the launch environment (including the activated root) in the
	// processes' environment, never in a journal or command-line credentials.
	cmd.Stderr = os.Stderr
	permit, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	ack, err := cmd.StdoutPipe()
	if err != nil {
		_ = permit.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = permit.Close()
		return nil, err
	}
	armed := make(chan bool, 1)
	go func() {
		var token [1]byte
		_, err := io.ReadFull(ack, token[:])
		armed <- err == nil && token[0] == '!'
	}()
	defer func() { go func() { _ = cmd.Wait() }() }()
	select {
	case ok := <-armed:
		if ok {
			return func(commit bool) error {
				defer func() { _ = permit.Close() }()
				if !commit {
					return nil
				}
				_, err := io.WriteString(permit, "!")
				return err
			}, nil
		}
	case <-time.After(3 * time.Second):
	}
	_ = permit.Close()
	_ = cmd.Process.Kill()
	return nil, errors.New("could not arm macOS reset relaunch")
}

// Run before any app constructors. The helper tracks its actual parent using
// a kernel exit notification, not a recycled PID poll and not a sleep alone.
func runNativeResetWatcher(args []string) error {
	if len(args) != 4 {
		return errors.New("invalid native reset watcher invocation")
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil || pid <= 1 || pid != os.Getppid() {
		return errors.New("macOS reset watcher has no matching live parent")
	}
	launch, err := identifyNativeResetLaunch(args[1])
	if err != nil {
		return err
	}
	id, port, err := parseNativeResetArgs(args[2:])
	if err != nil {
		return err
	}
	watch, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(watch) }()
	unix.CloseOnExec(watch)
	event := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(watch, []unix.Kevent_t{event}, nil, nil); err != nil || os.Getppid() != pid {
		return errors.New("cannot watch the exact macOS app process")
	}
	if _, err := io.WriteString(os.Stdout, "!"); err != nil {
		return err
	}
	var permit [1]byte
	if _, err := io.ReadFull(os.Stdin, permit[:]); err != nil || permit[0] != '!' {
		return nil // The parent intentionally cancelled or exited without a permit.
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("timed out waiting for the prior Ori app to exit")
		}
		timeout := unix.NsecToTimespec(remaining.Nanoseconds())
		var results [1]unix.Kevent_t
		count, err := unix.Kevent(watch, nil, results[:], &timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || count != 1 || results[0].Ident != uint64(pid) || results[0].Fflags&unix.NOTE_EXIT == 0 {
			return errors.New("the prior Ori app exit could not be verified")
		}
		break
	}
	if !launch.unchanged() {
		return errors.New("refusing to reopen an app bundle that changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// #nosec G204 G702 -- fixed OS opener and verified absolute bundle. Only a bounded ID and numeric port follow --args.
	cmd := exec.CommandContext(ctx, "/usr/bin/open", "-g", "-n", "-a", launch.bundle, "--args", nativeBootArg, id, strconv.Itoa(port))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// A manual reopen into recovery cannot construct shell managers, but the
// recovery listener still needs the prior custom origin. Read only the bounded
// port field from this lease-owned directory (without applying dotenv/config).
func menubarRecoveryPort(root string) int {
	// #nosec G304 -- root is the canonical path of the owned reset lease.
	dir, err := os.OpenRoot(root)
	if err == nil {
		defer func() { _ = dir.Close() }()
		file, err := dir.OpenFile("app_state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err == nil {
			defer func() { _ = file.Close() }()
			const limit = 1 << 20
			var state struct {
				MenuBar struct {
					Port int `json:"port"`
				} `json:"menubar"`
			}
			if info, err := file.Stat(); err == nil && info.Mode().IsRegular() && info.Size() <= limit {
				data, err := io.ReadAll(io.LimitReader(file, limit+1))
				if err == nil && len(data) <= limit && json.Unmarshal(data, &state) == nil && state.MenuBar.Port > 0 && state.MenuBar.Port <= 65535 {
					return state.MenuBar.Port
				}
			}
		}
	}
	if port, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PORT"))); err == nil && port > 0 && port <= 65535 {
		return port
	}
	return defaultMenubarPort
}
