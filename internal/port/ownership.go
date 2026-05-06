package port

import (
	"bufio"
	"bytes"
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

func IsPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
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
		cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
		output, err := cmd.Output()
		if err != nil {
			return "", err
		}
		name := strings.TrimSpace(string(output))
		if name == "" {
			return "", fmt.Errorf("empty process name")
		}
		return name, nil

	case "windows":
		psCmd := fmt.Sprintf(`(Get-Process -Id %d -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ProcessName)`, pid)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			return "", err
		}
		name := strings.TrimSpace(string(output))
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
		cmd := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port))
		output, err := cmd.Output()
		if err != nil {
			return nil
		}
		parsePIDs(output, pidSet)

	case "windows":
		psCmd := fmt.Sprintf(`Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess`, port)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			return nil
		}
		parsePIDs(output, pidSet)
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
