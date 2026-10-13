package usagelimits

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/version"
)

var errCodexLiveUnavailable = errors.New("codex app server usage limits are unavailable")

type codexLiveWindow struct {
	UsedPercent   float64 `json:"usedPercent"`
	WindowMinutes int     `json:"windowDurationMins"`
	ResetsAt      int64   `json:"resetsAt"`
}

type codexLiveResult struct {
	Limits *struct {
		Primary   *codexLiveWindow `json:"primary"`
		Secondary *codexLiveWindow `json:"secondary"`
		Plan      string           `json:"planType"`
		Reached   json.RawMessage  `json:"rateLimitReachedType"`
		Credits   *struct {
			HasCredits bool   `json:"hasCredits"`
			Unlimited  bool   `json:"unlimited"`
			Balance    string `json:"balance"`
		} `json:"credits"`
	} `json:"rateLimits"`
}

// ReadCodexAppServer uses the CLI's own authentication. Neither stderr nor RPC
// errors are exposed to callers, since either may contain account information.
func ReadCodexAppServer(ctx context.Context) (Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	path, err := exec.LookPath("codex")
	if err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	// #nosec G204 -- trusted installed CLI resolved by LookPath; arguments are fixed.
	cmd := exec.CommandContext(ctx, path, "app-server")
	// A four-second total lifetime is stricter than the ten-second process cap;
	// it leaves time for the local fallback within the HTTP five-second budget.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = append(os.Environ(), "CODEX_HOME="+CodexHome())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	defer func() { _ = stdout.Close() }()
	// Inherit the provider environment with the shared Codex home rule. No token
	// is put into argv or the environment by this reader.
	if err := cmd.Start(); err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// Closing stdout also releases a read if a descendant inherited the pipe.
	stopClose := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopClose()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	encoder := json.NewEncoder(stdin)
	if err := encoder.Encode(map[string]any{
		"id": 1, "method": "initialize", "params": map[string]any{
			"clientInfo": map[string]string{"name": "ori", "title": "Ori", "version": version.GetVersion()},
		},
	}); err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	if _, _, err := readCodexRPC(scanner, 1); err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	if err := encoder.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read", "params": nil}); err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	result, authError, err := readCodexRPC(scanner, 2)
	if authError {
		p := codexProvider()
		p.State, p.Detail = StateNotSignedIn, "Run `codex login`"
		return p, nil
	}
	if err != nil {
		return Provider{}, errCodexLiveUnavailable
	}
	return parseCodexLiveResult(result, time.Now().UTC())
}

func readCodexRPC(scanner *bufio.Scanner, id int) (json.RawMessage, bool, error) {
	for scanner.Scan() {
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.ID != id {
			continue
		}
		if response.Error != nil {
			message := strings.ToLower(response.Error.Message)
			auth := strings.Contains(message, "not authenticated") || strings.Contains(message, "not logged in") || strings.Contains(message, "unauthorized") || strings.Contains(message, "authentication")
			return nil, auth, errCodexLiveUnavailable
		}
		if len(response.Result) == 0 {
			return nil, false, errCodexLiveUnavailable
		}
		return response.Result, false, nil
	}
	return nil, false, io.EOF
}

func parseCodexLiveResult(data []byte, fetchedAt time.Time) (Provider, error) {
	var result codexLiveResult
	if json.Unmarshal(data, &result) != nil || result.Limits == nil {
		return Provider{}, errCodexLiveUnavailable
	}
	limits := result.Limits
	p := codexProvider()
	p.State, p.Source, p.FetchedAt, p.Plan = StateOK, SourceCodexAppServer, fetchedAt, limits.Plan
	for _, raw := range []*codexLiveWindow{limits.Primary, limits.Secondary} {
		if raw == nil {
			continue
		}
		if raw.WindowMinutes <= 0 || raw.UsedPercent < 0 {
			return Provider{}, errCodexLiveUnavailable
		}
		w := WindowFor(raw.WindowMinutes)
		w.UsedPercent, w.ResetsAt = raw.UsedPercent, raw.ResetsAt
		p.Windows = append(p.Windows, w)
	}
	if limits.Credits != nil {
		p.Credits = &Credits{HasCredits: limits.Credits.HasCredits, Unlimited: limits.Credits.Unlimited, Balance: limits.Credits.Balance}
	}
	reached := len(limits.Reached) > 0 && string(limits.Reached) != "null"
	p.summarize(reached)
	if len(p.Windows) == 0 && !reached {
		p.State, p.Detail = StateNoLimits, "This Codex account has no subscription usage windows"
	}
	return p, nil
}
