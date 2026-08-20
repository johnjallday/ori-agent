// Package reaper provides the trusted server-side client for REAPER's loopback
// Web Remote. It reuses reapersetup's platform probes for port discovery; no
// browser request, workspace metadata, or caller can provide an endpoint.
package reaper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

const (
	maxRemoteResponse = 256 << 10
	remoteTimeout     = 2 * time.Second
)

var (
	ErrClientUnavailable = errors.New("REAPER client is unavailable")
	ErrMalformedResponse = errors.New("REAPER Web Remote returned a malformed response")
	ErrProjectUnreadable = errors.New("REAPER project metadata is unreadable")
)

// State is the live REAPER state returned to workspace callers. Reason is a
// stable non-sensitive code used only while disconnected. No endpoint or port
// is present in this public shape.
type State struct {
	Applies       bool      `json:"applies"`
	Connected     bool      `json:"connected"`
	Reason        string    `json:"reason,omitempty"`
	Project       string    `json:"project"`
	Tempo         float64   `json:"tempo"`
	TimeSignature string    `json:"time_signature"`
	PlayState     string    `json:"play_state"`
	Position      string    `json:"position"`
	TrackCount    int       `json:"track_count"`
	Tracks        []Track   `json:"tracks"`
	CheckedAt     time.Time `json:"checked_at"`
}

// Track is one non-master track. Peak values are parsed for protocol coverage,
// but callers must not use them to decide whether station state meaningfully
// changed: live meters move continuously.
type Track struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Muted       bool    `json:"muted"`
	Soloed      bool    `json:"soloed"`
	Armed       bool    `json:"armed"`
	PeakLeftDB  float64 `json:"peak_left_db"`
	PeakRightDB float64 `json:"peak_right_db"`
}

// ProjectSource is resolved from trusted persisted workspace metadata by the
// HTTP layer. Path is the canonical .rpp used to read tempo; EntryPath supplies
// the stable project display name because Web Remote exposes no project query.
type ProjectSource struct {
	Path      string
	EntryPath string
}

// Client resolves the current loopback listener through the existing setup
// probes, then reads bounded Web Remote responses.
type Client struct {
	web       reapersetup.WebRemoteProbe
	transport reapersetup.LiveTransportProbe
	http      *http.Client
	now       func() time.Time
}

func NewClient(probes reapersetup.ProbeSet) *Client {
	transport := &http.Transport{Proxy: nil}
	return &Client{
		web:       probes.WebRemote,
		transport: probes.Transport,
		http: &http.Client{
			Transport: transport,
			Timeout:   remoteTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects are not allowed for REAPER loopback requests")
			},
		},
		now: time.Now,
	}
}

// Connected performs only the bounded TRANSPORT probe. It is used by the thin
// first delivery slice and by tests that do not need project metadata.
func (c *Client) Connected(ctx context.Context) State {
	state := c.emptyState()
	_, reason := c.resolve(ctx)
	state.Connected = reason == ""
	state.Reason = reason
	if state.Connected {
		state.PlayState = "stopped"
	}
	return state
}

// ReadState returns the current transport, tracks, beat signature, and trusted
// project-file metadata. An unreachable REAPER is normal and returns a
// disconnected state with nil error.
func (c *Client) ReadState(ctx context.Context, project ProjectSource) (State, error) {
	state := c.emptyState()
	port, reason := c.resolve(ctx)
	if reason != "" {
		state.Reason = reason
		return state, nil
	}

	transportBody, err := c.get(ctx, port, "TRANSPORT")
	if err != nil {
		state.Reason = "reaper_unreachable"
		return state, nil
	}
	playState, position, err := parseTransport(transportBody)
	if err != nil {
		return state, err
	}
	trackBody, err := c.get(ctx, port, "TRACK")
	if err != nil {
		return state, err
	}
	tracks, err := parseTracks(trackBody)
	if err != nil {
		return state, err
	}
	beatBody, err := c.get(ctx, port, "BEATPOS")
	if err != nil {
		return state, err
	}
	timeSignature, err := parseTimeSignature(beatBody)
	if err != nil {
		return state, err
	}
	tempo, err := readProjectTempo(project.Path)
	if err != nil {
		return state, err
	}

	state.Connected = true
	state.Project = projectDisplayName(project.EntryPath)
	state.Tempo = tempo
	state.TimeSignature = timeSignature
	state.PlayState = playState
	state.Position = position
	state.Tracks = tracks
	state.TrackCount = len(tracks)
	return state, nil
}

func (c *Client) emptyState() State {
	now := time.Now
	if c != nil && c.now != nil {
		now = c.now
	}
	return State{
		PlayState: "unknown",
		Tracks:    make([]Track, 0),
		CheckedAt: now().UTC(),
	}
}

func (c *Client) resolve(ctx context.Context) (int, string) {
	if c == nil || c.web == nil || c.transport == nil {
		return 0, "unavailable"
	}
	web := c.web.DetectWebRemote(ctx)
	switch web.State {
	case reapersetup.ProbeReady:
		// Continue to the live check. The persisted configuration is only a
		// candidate; the platform probe owns the stale-ini fallback.
	case reapersetup.ProbeMissing:
		return 0, "web_remote_off"
	case reapersetup.ProbeUnsupported:
		return 0, "unsupported"
	default:
		return 0, "web_remote_unavailable"
	}
	live := c.transport.CheckTransport(ctx, web)
	if live.State != reapersetup.TransportAvailable || live.Port < 1 || live.Port > 65535 {
		return 0, "reaper_unreachable"
	}
	return live.Port, ""
}

func (c *Client) get(ctx context.Context, port int, command string) ([]byte, error) {
	if c == nil || c.http == nil || port < 1 || port > 65535 {
		return nil, ErrClientUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, loopbackURL(port, command), nil)
	if err != nil {
		return nil, ErrClientUnavailable
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: unexpected status", ErrMalformedResponse)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRemoteResponse {
		return nil, ErrMalformedResponse
	}
	return body, nil
}

func loopbackURL(port int, command string) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/_/" + command
}

func parseTransport(body []byte) (string, string, error) {
	fields := firstResponseFields(body)
	if len(fields) < 5 || fields[0] != "TRANSPORT" {
		return "", "", ErrMalformedResponse
	}
	flags, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", "", ErrMalformedResponse
	}
	state := "stopped"
	switch {
	case flags&4 != 0:
		state = "recording"
	case flags&2 != 0:
		state = "paused"
	case flags&1 != 0:
		state = "playing"
	}
	return state, fields[4], nil
}

func parseTracks(body []byte) ([]Track, error) {
	tracks := make([]Track, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 4096), maxRemoteResponse)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := responseFields(line)
		if len(fields) < 8 || fields[0] != "TRACK" {
			return nil, ErrMalformedResponse
		}
		index, indexErr := strconv.Atoi(fields[1])
		flags, flagsErr := strconv.Atoi(fields[3])
		peakLeft, leftErr := strconv.ParseFloat(fields[6], 64)
		peakRight, rightErr := strconv.ParseFloat(fields[7], 64)
		if indexErr != nil || flagsErr != nil || leftErr != nil || rightErr != nil {
			return nil, ErrMalformedResponse
		}
		if index == 0 {
			continue
		}
		tracks = append(tracks, Track{
			Index:       index,
			Name:        fields[2],
			Muted:       flags&8 != 0,
			Soloed:      flags&16 != 0,
			Armed:       flags&64 != 0,
			PeakLeftDB:  peakLeft / 100,
			PeakRightDB: peakRight / 100,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, ErrMalformedResponse
	}
	return tracks, nil
}

func parseTimeSignature(body []byte) (string, error) {
	fields := firstResponseFields(body)
	if len(fields) < 3 || fields[0] != "BEATPOS" {
		return "", ErrMalformedResponse
	}
	numerator := fields[len(fields)-2]
	denominator := fields[len(fields)-1]
	if _, err := strconv.Atoi(numerator); err != nil {
		return "", ErrMalformedResponse
	}
	if _, err := strconv.Atoi(denominator); err != nil {
		return "", ErrMalformedResponse
	}
	return numerator + "/" + denominator, nil
}

func firstResponseFields(body []byte) []string {
	line := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0])
	return responseFields(line)
}

func responseFields(line string) []string {
	if strings.Contains(line, "\t") {
		return strings.Split(line, "\t")
	}
	return strings.Fields(line)
}

func readProjectTempo(path string) (float64, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !strings.EqualFold(filepath.Ext(path), ".rpp") {
		return 0, ErrProjectUnreadable
	}
	info, err := os.Lstat(path) // #nosec G304 -- canonical workspace project path resolved by reapersetup.AuthoritativeProject
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, ErrProjectUnreadable
	}
	file, err := os.Open(path) // #nosec G304 -- canonical workspace project path resolved by reapersetup.AuthoritativeProject
	if err != nil {
		return 0, ErrProjectUnreadable
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(io.LimitReader(file, 256<<10))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) < 2 || fields[0] != "TEMPO" {
			continue
		}
		tempo, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr != nil || tempo <= 0 {
			return 0, ErrProjectUnreadable
		}
		return tempo, nil
	}
	return 0, ErrProjectUnreadable
}

func projectDisplayName(entryPath string) string {
	name := filepath.Base(filepath.Clean(strings.TrimSpace(entryPath)))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}
