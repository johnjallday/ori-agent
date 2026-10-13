package usagelimits

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
)

func TestCodexInclusionAndState(t *testing.T) {
	for _, tt := range []struct {
		name                                string
		installed, signedIn, apiKey, hasLog bool
		want                                State
	}{
		{name: "not installed"},
		{name: "signed out hides old logs", installed: true, hasLog: true, want: StateNotSignedIn},
		{name: "API key hides old logs", installed: true, signedIn: true, apiKey: true, hasLog: true, want: StateNoLimits},
		{name: "no session yet", installed: true, signedIn: true, want: StateUnavailable},
		{name: "subscription reading", installed: true, signedIn: true, hasLog: true, want: StateOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newCodexReader()
			r.lookPath = func(string) (string, error) {
				if !tt.installed {
					return "", errors.New("absent")
				}
				return "/fake/codex", nil
			}
			r.home = func() string { return "/fake/home" }
			r.login = func(ctx context.Context, home string) (authdiscovery.CodexLoginStatus, error) {
				if !tt.installed {
					t.Fatal("must not read login without CLI")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("login check must have deadline")
				}
				return authdiscovery.CodexLoginStatus{SignedIn: tt.signedIn, APIKey: tt.apiKey}, nil
			}
			r.passive = func(context.Context, string) (Provider, error) {
				if !tt.signedIn || tt.apiKey {
					t.Fatal("must not read old logs without subscription")
				}
				if !tt.hasLog {
					return Provider{}, errNoCodexLimits
				}
				return Provider{State: StateOK}, nil
			}
			got := r.read(context.Background())
			if !tt.installed {
				if got != nil {
					t.Fatalf("provider should be omitted: %+v", got)
				}
				return
			}
			if got == nil || got.State != tt.want {
				t.Fatalf("got %+v, want %s", got, tt.want)
			}
		})
	}
}
