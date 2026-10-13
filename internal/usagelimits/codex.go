package usagelimits

import (
	"context"
	"os/exec"
	"time"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
)

type codexReader struct {
	lookPath func(string) (string, error)
	home     func() string
	login    func(context.Context, string) (authdiscovery.CodexLoginStatus, error)
	passive  func(context.Context, string) (Provider, error)
}

func newCodexReader() *codexReader {
	return &codexReader{
		lookPath: exec.LookPath,
		home:     CodexHome,
		login:    authdiscovery.ReadCodexLoginStatus,
		passive:  ReadCodexSessionLog,
	}
}

// read returns nil when the CLI is not installed, matching CLI card detection.
func (r *codexReader) read(ctx context.Context) *Provider {
	if _, err := r.lookPath("codex"); err != nil {
		return nil
	}
	p := codexProvider()
	home := r.home()
	loginCtx, cancel := context.WithTimeout(ctx, time.Second)
	login, err := r.login(loginCtx, home)
	cancel()
	switch {
	case err != nil:
		p.State, p.Detail = StateUnavailable, "Unable to read Codex login status"
	case !login.SignedIn:
		p.State, p.Detail = StateNotSignedIn, "Run `codex login`"
	case login.APIKey:
		p.State, p.Detail = StateNoLimits, "Codex is using an API key, not a subscription"
	default:
		reading, err := r.passive(ctx, home)
		if err == nil {
			return &reading
		}
		p.State, p.Detail = StateUnavailable, "No Codex session has reported limits yet"
	}
	return &p
}
