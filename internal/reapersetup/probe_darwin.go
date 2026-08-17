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
	configPath := filepath.Join(home, "Library", "Application Support", "REAPER", "reaper.ini")
	// The path is the fixed REAPER configuration location under the current
	// user's trusted home directory.
	info, err := os.Lstat(configPath) // #nosec G304 -- fixed trusted REAPER config path
	if os.IsNotExist(err) {
		return WebRemoteObservation{State: ProbeMissing}
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxREAPERConfigBytes {
		return WebRemoteObservation{State: ProbeUnknown}
	}
	data, err := os.ReadFile(configPath) // #nosec G304 -- fixed trusted REAPER config path, size checked above
	if err != nil {
		return WebRemoteObservation{State: ProbeUnknown}
	}
	return parseWebRemoteConfig(data)
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
	if p == nil || p.client == nil || web.State != ProbeReady || web.Port < 1 || web.Port > 65535 {
		return LiveTransportObservation{State: TransportUnavailable}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, loopbackURL(web.Port, "TRANSPORT"), nil)
	if err != nil {
		return LiveTransportObservation{State: TransportCheckFailed}
	}
	response, err := p.client.Do(request)
	if err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return LiveTransportObservation{State: TransportOffline}
		}
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
	return LiveTransportObservation{State: TransportAvailable}
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
