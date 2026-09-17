package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
)

func TestIntegrationReleasesAPIBaseAcceptsOnlyLoopbackHTTPOverrides(t *testing.T) {
	for override, want := range map[string]string{
		"":                                   integrationrelease.DefaultAPIBase,
		"   ":                                integrationrelease.DefaultAPIBase,
		"http://127.0.0.1:9":                 "http://127.0.0.1:9",
		"http://127.0.0.1:8123/":             "http://127.0.0.1:8123",
		"http://localhost:8123/api":          "http://localhost:8123/api",
		"http://[::1]:8123":                  "http://[::1]:8123",
		"https://127.0.0.1:8123":             integrationrelease.DefaultAPIBase,
		"http://api.github.example":          integrationrelease.DefaultAPIBase,
		"http://127.0.0.2:8123":              integrationrelease.DefaultAPIBase,
		"http://localhost.attacker.test":     integrationrelease.DefaultAPIBase,
		"http://user:pass@127.0.0.1:8123":    integrationrelease.DefaultAPIBase,
		"http://127.0.0.1:8123/?token=x":     integrationrelease.DefaultAPIBase,
		"http://127.0.0.1:8123/#fragment":    integrationrelease.DefaultAPIBase,
		"file:///tmp/releases.json":          integrationrelease.DefaultAPIBase,
		"127.0.0.1:8123":                     integrationrelease.DefaultAPIBase,
		"http://0.0.0.0:8123":                integrationrelease.DefaultAPIBase,
		"http://127.0.0.1@evil.example:8123": integrationrelease.DefaultAPIBase,
	} {
		if got := integrationReleasesAPIBase(override); got != want {
			t.Errorf("integrationReleasesAPIBase(%q) = %q, want %q", override, got, want)
		}
	}
}
