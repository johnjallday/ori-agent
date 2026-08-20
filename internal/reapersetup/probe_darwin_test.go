//go:build darwin

package reapersetup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseListeningPortsBoundsAndDeduplicatesREAPERSockets(t *testing.T) {
	got := parseListeningPorts([]byte("p123\ncREAPER\nn127.0.0.1:2308\nn*:2308\nn[::1]:2310\nninvalid\n"))
	if len(got) != 2 || got[0] != 2308 || got[1] != 2310 {
		t.Fatalf("listening ports = %#v", got)
	}
}

func TestPlatformLiveProbeFallsBackWhenConfigPortIsStale(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("TRANSPORT\t0\t0.0\n"))
	}))
	defer live.Close()
	livePort, err := strconv.Atoi(strings.TrimPrefix(live.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}

	probe := &platformProbe{
		client: http.DefaultClient,
		listeningPorts: func(context.Context) []int {
			return []int{livePort}
		},
	}
	got := probe.CheckTransport(context.Background(), WebRemoteObservation{
		State: ProbeReady,
		Port:  1,
		Ports: []int{1},
	})
	if got.State != TransportAvailable || got.Port != livePort {
		t.Fatalf("stale configured port fallback = %+v, want available discovered listener", got)
	}
}
