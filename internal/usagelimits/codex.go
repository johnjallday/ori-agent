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
	live     *liveReader
	now      func() time.Time
}

func newCodexReader() *codexReader {
	return &codexReader{
		lookPath: exec.LookPath,
		home:     CodexHome,
		login:    authdiscovery.ReadCodexLoginStatus,
		passive:  ReadCodexSessionLog,
		live:     &liveReader{fetch: ReadCodexAppServer, now: time.Now},
		now:      time.Now,
	}
}

// read returns nil when the CLI is not installed, matching CLI card detection.
func (r *codexReader) read(ctx context.Context) *Provider {
	return r.readWithRefresh(ctx, false)
}

func (r *codexReader) readWithRefresh(ctx context.Context, refresh bool) *Provider {
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
		reading, passiveErr := r.passive(ctx, home)
		latest, hasLatest := r.live.latestResult()
		if hasLatest && latest.State != StateOK {
			// Retry a prior auth/no-limits result, but never revive old log
			// percentages when the retry fails or is inside the refresh floor.
			if live, err := r.live.read(ctx); err == nil {
				return &live
			}
			return &latest
		}
		if hasLatest && (passiveErr != nil || latest.FetchedAt.After(reading.FetchedAt)) {
			reading, passiveErr = latest, nil
		}
		if refresh || passiveErr != nil || r.now().Sub(reading.FetchedAt) > time.Hour {
			if live, err := r.live.read(ctx); err == nil {
				return &live
			}
			reading.Detail = "Live Codex usage unavailable; showing the last reading"
		}
		if passiveErr == nil {
			return &reading
		}
		p.State, p.Detail = StateUnavailable, "No Codex session has reported limits yet"
	}
	return &p
}
