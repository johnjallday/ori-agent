//go:build darwin

package reapersetup

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const reaperBundleName = "REAPER.app"

type platformProbe struct {
	roots   RunnerRootResolver
	homeDir func() (string, error)
	client  *http.Client
	mu      sync.Mutex
}

func newPlatformProbe(roots RunnerRootResolver) platformProber {
	transport := &http.Transport{Proxy: nil}
	return &platformProbe{
		roots:   roots,
		homeDir: os.UserHomeDir,
		client: &http.Client{
			Transport: transport,
			Timeout:   2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects are not allowed for REAPER loopback probes")
			},
		},
	}
}

func (p *platformProbe) DetectApplication(context.Context) ApplicationObservation {
	candidates := []string{filepath.Join(string(filepath.Separator), "Applications", reaperBundleName)}
	home, homeErr := p.homeDir()
	if homeErr == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", reaperBundleName))
	}
	unknown := homeErr != nil
	for _, candidate := range candidates {
		// Candidate paths are compiled trusted locations, not request data.
		info, err := os.Lstat(candidate) // #nosec G304 -- fixed macOS application locations
		switch {
		case err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir():
			return ApplicationObservation{State: ProbeReady}
		case err == nil:
			unknown = true
		case !os.IsNotExist(err):
			unknown = true
		}
	}
	if unknown {
		return ApplicationObservation{State: ProbeUnknown}
	}
	return ApplicationObservation{State: ProbeMissing}
}

func (p *platformProbe) DetectWebRemote(context.Context) WebRemoteObservation {
	home, err := p.homeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return WebRemoteObservation{State: ProbeUnknown}
	}

	// REAPER uses the standard per-user path unless a portable reaper.ini sits
	// beside a trusted application bundle. Both locations are fixed platform
	// paths; no browser, workspace, or blueprint value can add a candidate.
	configPaths := []string{filepath.Join(home, "Library", "Application Support", "REAPER", "reaper.ini")}
	appCandidates := []string{
		filepath.Join(string(filepath.Separator), "Applications", reaperBundleName),
		filepath.Join(home, "Applications", reaperBundleName),
	}
	for _, appPath := range appCandidates {
		info, appErr := os.Lstat(appPath) // #nosec G304 -- fixed trusted macOS application locations
		if appErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			configPaths = append(configPaths, filepath.Join(filepath.Dir(appPath), "reaper.ini"))
		}
	}
	return detectWebRemoteConfigs(configPaths)
}

func detectWebRemoteConfigs(configPaths []string) WebRemoteObservation {
	ports := make([]int, 0, 2)
	seenPorts := make(map[int]struct{})
	foundInvalid := false
	foundUnknown := false

	for _, configPath := range configPaths {
		// Paths are assembled only from fixed platform locations above or direct
		// test fixtures. Every candidate must still be a bounded regular file.
		info, err := os.Lstat(configPath) // #nosec G304 -- trusted REAPER config candidates
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxREAPERConfigBytes {
			foundUnknown = true
			continue
		}
		data, readErr := os.ReadFile(configPath) // #nosec G304 -- trusted bounded REAPER config candidate
		if readErr != nil {
			foundUnknown = true
			continue
		}
		observation := parseWebRemoteConfig(data)
		switch observation.State {
		case ProbeReady:
			for _, port := range configuredWebRemotePorts(observation) {
				if _, exists := seenPorts[port]; exists {
					continue
				}
				if len(ports) >= maxWebRemoteInterfaces {
					return WebRemoteObservation{State: ProbeUnknown}
				}
				seenPorts[port] = struct{}{}
				ports = append(ports, port)
			}
		case ProbeInvalid:
			foundInvalid = true
		case ProbeUnknown:
			foundUnknown = true
		}
	}

	if len(ports) > 0 {
		return WebRemoteObservation{State: ProbeReady, Port: ports[0], Ports: ports}
	}
	if foundInvalid {
		return WebRemoteObservation{State: ProbeInvalid}
	}
	if foundUnknown {
		return WebRemoteObservation{State: ProbeUnknown}
	}
	return WebRemoteObservation{State: ProbeMissing}
}

func (p *platformProbe) DetectRunner(context.Context) RunnerObservation {
	if p == nil || p.roots == nil {
		return RunnerObservation{State: ProbeMissing}
	}
	root, err := p.roots.Resolve()
	if err != nil {
		if errors.Is(err, ErrRunnerRootUnsafe) {
			return RunnerObservation{State: ProbeInvalid}
		}
		return RunnerObservation{State: ProbeMissing}
	}
	idPath := filepath.Join(root, "runner.id")
	// root was canonicalized and symlink-checked by RunnerRootResolver.
	info, err := os.Lstat(idPath) // #nosec G304 -- trusted canonical runner root
	if os.IsNotExist(err) {
		return RunnerObservation{State: ProbeMissing, Root: root}
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxRunnerIDBytes {
		return RunnerObservation{State: ProbeInvalid, Root: root}
	}
	data, err := os.ReadFile(idPath) // #nosec G304 -- trusted canonical runner root, bounded file checked above
	if err != nil {
		return RunnerObservation{State: ProbeInvalid, Root: root}
	}
	commandID := strings.TrimSpace(string(data))
	if !validRunnerCommandID(commandID) {
		return RunnerObservation{State: ProbeInvalid, Root: root}
	}
	return RunnerObservation{State: ProbeReady, Root: root, CommandID: commandID}
}

func (p *platformProbe) CheckTransport(ctx context.Context, web WebRemoteObservation) LiveTransportObservation {
	if p == nil || p.client == nil || web.State != ProbeReady {
		return LiveTransportObservation{State: TransportUnavailable}
	}
	ports := configuredWebRemotePorts(web)
	if len(ports) == 0 {
		return LiveTransportObservation{State: TransportUnavailable}
	}

	// Probe bounded configured interfaces concurrently. A stale interface may
	// accept TCP and never answer; it must not hide another configured interface
	// that is serving the current REAPER process.
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	results := make(chan LiveTransportObservation, len(ports))
	for _, port := range ports {
		go func(candidate int) {
			results <- p.checkTransportPort(probeCtx, candidate)
		}(port)
	}

	best := LiveTransportObservation{State: TransportOffline}
	for range ports {
		select {
		case result := <-results:
			if result.State == TransportAvailable {
				cancel()
				return result
			}
			if transportObservationRank(result.State) > transportObservationRank(best.State) {
				best = result
			}
		case <-probeCtx.Done():
			return best
		}
	}
	return best
}

func (p *platformProbe) checkTransportPort(ctx context.Context, port int) LiveTransportObservation {
	if port < 1 || port > 65535 {
		return LiveTransportObservation{State: TransportUnavailable}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, loopbackURL(port, "TRANSPORT"), nil)
	if err != nil {
		return LiveTransportObservation{State: TransportCheckFailed}
	}
	response, err := p.client.Do(request)
	if err != nil {
		return LiveTransportObservation{State: TransportOffline}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return LiveTransportObservation{State: TransportUnavailable}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxProbeResponse+1))
	if readErr != nil {
		return LiveTransportObservation{State: TransportCheckFailed}
	}
	if len(body) > maxProbeResponse || !validTransportResponse(body) {
		return LiveTransportObservation{State: TransportMalformed}
	}
	return LiveTransportObservation{State: TransportAvailable, Port: port}
}

func transportObservationRank(state string) int {
	switch state {
	case TransportMalformed:
		return 4
	case TransportUnavailable:
		return 3
	case TransportCheckFailed:
		return 2
	case TransportOffline:
		return 1
	default:
		return 0
	}
}

func validTransportResponse(body []byte) bool {
	line := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0])
	return line == "TRANSPORT" || strings.HasPrefix(line, "TRANSPORT\t")
}

func loopbackURL(port int, command string) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/_/" + command
}

func (p *platformProbe) VerifyProject(ctx context.Context, target VerificationTarget) VerificationObservation {
	return p.verifyProject(ctx, target)
}
