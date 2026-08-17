package reapersetup

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxREAPERConfigBytes = 2 << 20
	maxREAPERConfigLines = 8192
	maxRunnerIDBytes     = 256
	maxProbeResponse     = 64 << 10
	maxVerificationInbox = 1 << 20
)

var runnerCommandIDPattern = regexp.MustCompile(`^(?:_[A-Za-z0-9][A-Za-z0-9_-]{1,63}|[1-9][0-9]{0,9})$`)

// parseWebRemoteConfig accepts the two REAPER Web Remote encodings currently
// written to reaper.ini. A disabled interface is not configured. A malformed
// enabled entry is invalid rather than silently falling back to a guessed port.
func parseWebRemoteConfig(data []byte) WebRemoteObservation {
	if len(data) == 0 {
		return WebRemoteObservation{State: ProbeMissing}
	}
	if len(data) > maxREAPERConfigBytes {
		return WebRemoteObservation{State: ProbeUnknown}
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	lines := 0
	foundDisabled := false
	for scanner.Scan() {
		lines++
		if lines > maxREAPERConfigLines {
			return WebRemoteObservation{State: ProbeUnknown}
		}
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "csurf_") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(value))
		if len(fields) == 0 || (!strings.EqualFold(fields[0], "HTTP") && !strings.EqualFold(fields[0], "WEBR")) {
			continue
		}
		if len(fields) < 3 {
			return WebRemoteObservation{State: ProbeInvalid}
		}
		enabled, validEnabled := parseREAPEREnabled(fields[1])
		if !validEnabled {
			return WebRemoteObservation{State: ProbeInvalid}
		}
		portField := fields[2]
		if strings.EqualFold(fields[0], "WEBR") {
			portField = fields[len(fields)-1]
		}
		port, err := strconv.Atoi(portField)
		if err != nil || port < 1 || port > 65535 {
			return WebRemoteObservation{State: ProbeInvalid}
		}
		if !enabled {
			foundDisabled = true
			continue
		}
		return WebRemoteObservation{State: ProbeReady, Port: port}
	}
	if scanner.Err() != nil {
		return WebRemoteObservation{State: ProbeUnknown}
	}
	if foundDisabled {
		return WebRemoteObservation{State: ProbeMissing}
	}
	return WebRemoteObservation{State: ProbeMissing}
}

func parseREAPEREnabled(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	default:
		return false, false
	}
}

func validRunnerCommandID(value string) bool {
	return runnerCommandIDPattern.MatchString(strings.TrimSpace(value))
}
