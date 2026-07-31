package wakeservice

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	maxPMSetOutput  = 128 * 1024
	pmsetTimeLayout = "01/02/06 15:04:05"
)

var scheduledEventPattern = regexp.MustCompile(
	`^\s*\[[0-9]+\]\s+([A-Za-z]+)\s+at\s+([0-9]{2}/[0-9]{2}/(?:[0-9]{2}|[0-9]{4})\s+[0-9]{2}:[0-9]{2}:[0-9]{2})\s+by\s+'([^']+)'\s*$`,
)

// PowerEvent is the bounded portion of one scheduled macOS power event that
// the daemon needs in order to distinguish its event from every other owner.
type PowerEvent struct {
	Type  string
	At    time.Time
	Owner string
}

// PowerScheduler is the entire privileged process boundary. Implementations
// may only list scheduled events, schedule the fixed Herdr event, or cancel the
// exact fixed Herdr event identified by its timestamp.
type PowerScheduler interface {
	Events(context.Context) ([]PowerEvent, error)
	Schedule(context.Context, time.Time) error
	Cancel(context.Context, time.Time) error
}

// PMSetRunner receives only the argument vector after /usr/bin/pmset.
type PMSetRunner func(context.Context, []string) ([]byte, error)

// PMSet invokes pmset through a fixed executable and fixed argument shapes.
type PMSet struct {
	run      PMSetRunner
	timeout  time.Duration
	location *time.Location
}

// NewPMSet creates a scheduler with explicit test seams but no configurable
// executable, event type, or owner.
func NewPMSet(run PMSetRunner, timeout time.Duration, location *time.Location) (*PMSet, error) {
	if run == nil {
		return nil, fmt.Errorf("pmset runner is required")
	}
	if timeout <= 0 {
		timeout = DefaultIOTimeout
	}
	if location == nil {
		location = time.Local
	}
	return &PMSet{run: run, timeout: timeout, location: location}, nil
}

func newDefaultPowerScheduler(timeout time.Duration) (PowerScheduler, error) {
	return NewPMSet(defaultPMSetRunner, timeout, time.Local)
}

// Events reads and strictly parses the one-time scheduled-event section.
func (p *PMSet) Events(ctx context.Context) ([]PowerEvent, error) {
	output, err := p.invoke(ctx, []string{"-g", "sched"})
	if err != nil {
		return nil, err
	}
	return parsePMSetSchedule(output, p.location)
}

// Schedule creates only the fixed Herdr wakeorpoweron event.
func (p *PMSet) Schedule(ctx context.Context, wakeAt time.Time) error {
	_, err := p.invoke(ctx, []string{
		"schedule",
		PMSetEventType,
		formatPMSetTime(wakeAt, p.location),
		PMSetOwner,
	})
	return err
}

// Cancel removes only the fixed Herdr wakeorpoweron event at the exact second.
func (p *PMSet) Cancel(ctx context.Context, wakeAt time.Time) error {
	_, err := p.invoke(ctx, []string{
		"schedule",
		"cancel",
		PMSetEventType,
		formatPMSetTime(wakeAt, p.location),
		PMSetOwner,
	})
	return err
}

func (p *PMSet) invoke(ctx context.Context, arguments []string) ([]byte, error) {
	callContext, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	output, err := p.run(callContext, append([]string(nil), arguments...))
	if len(output) > maxPMSetOutput {
		return nil, fmt.Errorf("pmset output exceeds %d bytes", maxPMSetOutput)
	}
	if err != nil {
		detail := boundedMessage(strings.TrimSpace(string(output)))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("pmset failed: %s", boundedMessage(detail))
	}
	return output, nil
}

func formatPMSetTime(value time.Time, location *time.Location) string {
	return value.In(location).Format(pmsetTimeLayout)
}

func parsePMSetSchedule(output []byte, location *time.Location) ([]PowerEvent, error) {
	if len(output) > maxPMSetOutput {
		return nil, fmt.Errorf("pmset schedule output exceeds %d bytes", maxPMSetOutput)
	}
	if location == nil {
		location = time.Local
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), maxPMSetOutput)
	inScheduledSection := false
	sawScheduledSection := false
	events := make([]PowerEvent, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "":
			continue
		case "Scheduled power events:":
			inScheduledSection = true
			sawScheduledSection = true
			continue
		case "Repeating power events:":
			inScheduledSection = false
			continue
		}
		if !inScheduledSection {
			// Repeating-event descriptions are intentionally opaque. The
			// daemon never changes them, and only one-time schedule entries
			// can match the fixed Herdr owner.
			continue
		}
		match := scheduledEventPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("unsupported pmset scheduled-event line")
		}
		parsed, err := parsePMSetTime(match[2], location)
		if err != nil {
			return nil, fmt.Errorf("unsupported pmset scheduled-event timestamp")
		}
		events = append(events, PowerEvent{
			Type:  strings.ToLower(match[1]),
			At:    parsed.UTC(),
			Owner: match[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read pmset schedule output: %w", err)
	}
	if !sawScheduledSection {
		return nil, fmt.Errorf("pmset output did not contain the scheduled-event section")
	}
	return events, nil
}

func parsePMSetTime(value string, location *time.Location) (time.Time, error) {
	layout := pmsetTimeLayout
	datePart := strings.SplitN(value, " ", 2)[0]
	year := strings.Split(datePart, "/")
	if len(year) == 3 && len(year[2]) == 4 {
		layout = "01/02/2006 15:04:05"
	}
	return time.ParseInLocation(layout, value, location)
}
