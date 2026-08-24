package workspacesurface

import (
	"strings"
	"time"
	"unicode/utf8"
)

// NormalizeStationStatus turns service-authored status into bounded text and a
// closed host vocabulary. Invalid state becomes degraded rather than inventing
// readiness; invalid timestamps become the host check time.
func NormalizeStationStatus(status StationStatus, now time.Time) StationStatus {
	switch status.State {
	case StationChecking, StationReady, StationAttention, StationDegraded, StationUnavailable, StationDisabled:
	default:
		status.State = StationDegraded
	}
	status.Value = boundedText(status.Value, maxStationValue)
	status.Description = boundedText(status.Description, maxDescriptionBytes)
	checked, err := time.Parse(time.RFC3339, strings.TrimSpace(status.CheckedAt))
	if err != nil || checked.After(now.Add(time.Minute)) {
		checked = now
	}
	status.CheckedAt = checked.UTC().Format(time.RFC3339)
	return status
}

func boundedText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	var out []rune
	bytes := 0
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		size := utf8.RuneLen(r)
		if size < 0 || bytes+size > maximum {
			break
		}
		out = append(out, r)
		bytes += size
	}
	return string(out)
}
