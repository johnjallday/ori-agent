package port

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type ProcessInfo struct {
	PID  int
	Name string
}

var oriProcessNames = map[string]struct{}{
	"ori-agent":   {},
	"ori-menubar": {},
}

// ownerLookupTimeout bounds each external process lookup; a lookup that runs
// out of time is treated like one that found nothing.
var ownerLookupTimeout = 10 * time.Second

func IsPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func lookupOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ownerLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// A descendant still holding stdout must not extend the deadline.
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

func FindPortProcesses(port int) ([]ProcessInfo, error) {
	pids := findPortPIDs(port)
	processes := make([]ProcessInfo, 0, len(pids))
	for _, pid := range pids {
		name, nameErr := ResolveProcessName(pid)
		if nameErr != nil {
			name = ""
		}
		processes = append(processes, ProcessInfo{PID: pid, Name: name})
	}
	return processes, nil
}

func ResolveProcessName(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid")
	}

	switch runtime.GOOS {
	case "darwin", "linux":
		output, err := lookupOutput("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
		if err != nil {
			return "", err
		}
		name := strings.TrimSpace(string(output))
		if name == "" {
			return "", fmt.Errorf("empty process name")
		}
		return name, nil

	case "windows":
		output, err := lookupOutput("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
		if err != nil {
			return "", err
		}
		name := parseTasklistName(output)
		if name == "" {
			return "", fmt.Errorf("empty process name")
		}
		return name, nil
	default:
		return "", fmt.Errorf("unsupported platform")
	}
}

func IsOriProcessName(name string) bool {
	normalized := normalizeProcessName(name)
	if normalized == "" {
		return false
	}
	_, ok := oriProcessNames[normalized]
	return ok
}

func TerminateProcesses(processes []ProcessInfo) error {
	var firstErr error
	for _, process := range processes {
		if process.PID <= 0 {
			continue
		}
		if err := terminateProcess(process.PID); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if len(processes) > 0 {
		time.Sleep(100 * time.Millisecond)
	}

	return firstErr
}

func FormatProcessSummary(processes []ProcessInfo) string {
	seen := make(map[string]struct{})
	parts := make([]string, 0, len(processes))
	for _, process := range processes {
		name := strings.TrimSpace(process.Name)
		if name == "" {
			name = "unknown"
		}
		if process.PID > 0 {
			name = fmt.Sprintf("%s (pid %d)", name, process.PID)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "unknown process"
	}
	return strings.Join(parts, ", ")
}

func normalizeProcessName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	base := filepath.Base(trimmed)
	lower := strings.ToLower(base)
	return strings.TrimSuffix(lower, ".exe")
}

func findPortPIDs(port int) []int {
	pidSet := make(map[int]struct{})

	switch runtime.GOOS {
	case "darwin", "linux":
		output, err := lookupOutput("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN")
		if err != nil {
			return nil
		}
		parsePIDs(output, pidSet)

	case "windows":
		// netstat and tasklist are plain executables. PowerShell's
		// Get-NetTCPConnection loads modules into the user profile first, which
		// stalled installed startups past a 45s health deadline.
		output, err := lookupOutput("netstat", "-ano")
		if err != nil {
			return nil
		}
		for _, pid := range parseNetstatListeners(output, port) {
			pidSet[pid] = struct{}{}
		}
	default:
		return nil
	}

	pids := make([]int, 0, len(pidSet))
	for pid := range pidSet {
		pids = append(pids, pid)
	}
	return pids
}

func parsePIDs(output []byte, pidSet map[int]struct{}) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		pidStr := strings.TrimSpace(scanner.Text())
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			pidSet[pid] = struct{}{}
		}
	}
}

// parseNetstatListeners returns the PIDs of TCP sockets listening on port in
// `netstat -ano` output. The state column is localized and may span words, so
// a listener is recognized by its unbound foreign address, and the PID is the
// last field.
func parseNetstatListeners(output []byte, port int) []int {
	suffix := ":" + strconv.Itoa(port)
	var pids []int
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) || (fields[2] != "0.0.0.0:0" && fields[2] != "[::]:0") {
			continue
		}
		if pid, err := strconv.Atoi(fields[len(fields)-1]); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// parseTasklistName returns the image name from `tasklist /FO CSV /NH` output,
// or "" when no process matched (tasklist then prints a localized notice).
func parseTasklistName(output []byte) string {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		record, err := csv.NewReader(strings.NewReader(line)).Read()
		if err == nil && len(record) > 0 {
			return strings.TrimSpace(record[0])
		}
	}
	return ""
}

func terminateProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}

	pidStr := strconv.Itoa(pid)
	switch runtime.GOOS {
	case "darwin", "linux":
		termCmd := exec.Command("kill", "-15", pidStr)
		if err := termCmd.Run(); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
		checkCmd := exec.Command("kill", "-0", pidStr)
		if checkCmd.Run() == nil {
			forceCmd := exec.Command("kill", "-9", pidStr)
			return forceCmd.Run()
		}
		return nil
	case "windows":
		killCmd := exec.Command("taskkill", "/F", "/PID", pidStr)
		return killCmd.Run()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
