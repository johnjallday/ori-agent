package reapersetup

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxREAPERConfigBytes   = 2 << 20
	maxREAPERConfigLines   = 8192
	maxRunnerIDBytes       = 256
	maxProbeResponse       = 64 << 10
	maxVerificationInbox   = 1 << 20
	maxWebRemoteInterfaces = 8
)

var runnerCommandIDPattern = regexp.MustCompile(`^(?:_[A-Za-z0-9][A-Za-z0-9_-]{1,63}|[1-9][0-9]{0,9})$`)

// parseWebRemoteConfig accepts the two REAPER Web Remote encodings currently
// written to reaper.ini. In REAPER's HTTP encoding the field before the port is
// not an enabled flag: a live interface is normally persisted as "HTTP 0 2307".
// WEBR uses an explicit enabled field. A malformed configured entry is invalid
// rather than silently falling back to a guessed port.
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
	ports := make([]int, 0, 2)
	seenPorts := make(map[int]struct{})
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
		portField := fields[2]
		if strings.EqualFold(fields[0], "HTTP") {
			// REAPER currently writes 0 here for an active Web browser
			// interface. Validate the bounded format, but do not interpret it
			// as disabled; interface absence is represented by no HTTP row.
			if fields[1] != "0" && fields[1] != "1" {
				return WebRemoteObservation{State: ProbeInvalid}
			}
		} else {
			enabled, validEnabled := parseREAPEREnabled(fields[1])
			if !validEnabled {
				return WebRemoteObservation{State: ProbeInvalid}
			}
			portField = fields[len(fields)-1]
			if !enabled {
				foundDisabled = true
				continue
			}
		}
		port, err := strconv.Atoi(portField)
		if err != nil || port < 1 || port > 65535 {
			return WebRemoteObservation{State: ProbeInvalid}
		}
		if _, exists := seenPorts[port]; exists {
			continue
		}
		if len(ports) >= maxWebRemoteInterfaces {
			return WebRemoteObservation{State: ProbeUnknown}
		}
		seenPorts[port] = struct{}{}
		ports = append(ports, port)
	}
	if scanner.Err() != nil {
		return WebRemoteObservation{State: ProbeUnknown}
	}
	if len(ports) > 0 {
		return WebRemoteObservation{State: ProbeReady, Port: ports[0], Ports: ports}
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
