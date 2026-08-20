package reaper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

type clientProbe struct {
	web       reapersetup.WebRemoteObservation
	transport reapersetup.LiveTransportObservation
}

func (p clientProbe) DetectWebRemote(context.Context) reapersetup.WebRemoteObservation {
	return p.web
}

func (p clientProbe) CheckTransport(context.Context, reapersetup.WebRemoteObservation) reapersetup.LiveTransportObservation {
	return p.transport
}

func TestClientConnectedTreatsUnreachableAsState(t *testing.T) {
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: 2307},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportOffline},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	checkedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return checkedAt }

	got := client.Connected(context.Background())
	if got.Connected || got.Reason != "reaper_unreachable" || !got.CheckedAt.Equal(checkedAt) {
		t.Fatalf("connected state = %+v", got)
	}
}

func TestClientReadsLiveStateAndProjectMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/TRANSPORT":
			_, _ = w.Write([]byte("TRANSPORT\t1\t12.5\t0\t1.3.00\t1.3.00\n"))
		case "/_/TRACK":
			_, _ = w.Write([]byte("TRACK\t0\tMASTER\t1536\t1.0\t0.0\t-1500\t-1500\t1.0\t0\t0\t0\t1\t0\n" +
				"TRACK\t1\tDrums\t216\t1.0\t0.0\t-900\t-800\t1.0\t0\t0\t0\t0\t0\n"))
		case "/_/BEATPOS":
			_, _ = w.Write([]byte("BEATPOS\t0\t0.0\t0.0\t0\t0.0\t4\t4\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: port},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportAvailable, Port: port},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	client.http = server.Client()

	project := filepath.Join(t.TempDir(), "Track Plan.RPP")
	if err := os.WriteFile(project, []byte("<REAPER_PROJECT 0.1\n  TEMPO 120 4 4\n>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := client.ReadState(context.Background(), ProjectSource{Path: project, EntryPath: "Track Plan.RPP"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Connected || got.Project != "Track Plan" || got.Tempo != 120 || got.TimeSignature != "4/4" || got.PlayState != "playing" || got.Position != "1.3.00" {
		t.Fatalf("state = %+v", got)
	}
	if got.TrackCount != 1 || len(got.Tracks) != 1 {
		t.Fatalf("tracks = %+v", got.Tracks)
	}
	track := got.Tracks[0]
	if track.Name != "Drums" || !track.Muted || !track.Soloed || !track.Armed || track.PeakLeftDB != -9 || track.PeakRightDB != -8 {
		t.Fatalf("track = %+v", track)
	}
}

func TestClientRunActionTriggersOnceAndReturnsResultingState(t *testing.T) {
	playState := 0
	actionCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/1007":
			actionCalls++
			playState = 1
		case "/_/TRANSPORT":
			_, _ = w.Write([]byte("TRANSPORT\t" + strconv.Itoa(playState) + "\t0\t0\t1.1.00\t1.1.00\n"))
		case "/_/TRACK":
			_, _ = w.Write([]byte("TRACK\t0\tMASTER\t1536\t1\t0\t-1500\t-1500\n"))
		case "/_/BEATPOS":
			_, _ = w.Write([]byte("BEATPOS\t0\t0\t0\t0\t0\t4\t4\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: port},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportAvailable, Port: port},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	client.http = server.Client()
	project := filepath.Join(t.TempDir(), "Song.rpp")
	if err := os.WriteFile(project, []byte("<REAPER_PROJECT\nTEMPO 120 4 4\n>\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := client.RunAction(context.Background(), "1007", ProjectSource{Path: project, EntryPath: "Song.rpp"})
	if err != nil {
		t.Fatal(err)
	}
	if actionCalls != 1 || !state.Connected || state.PlayState != "playing" {
		t.Fatalf("calls=%d state=%+v", actionCalls, state)
	}
}

func TestClientRunActionDoesNotQueueWhenDisconnected(t *testing.T) {
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: 2307},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportOffline},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	state, err := client.RunAction(context.Background(), "1007", ProjectSource{})
	if err == nil || !errors.Is(err, ErrActionDisconnected) || state.Reason != "reaper_unreachable" {
		t.Fatalf("disconnected run = state %+v, err %v", state, err)
	}
}

func TestParseTracksUsesVerifiedFlagBits(t *testing.T) {
	body := []byte("TRACK\t1\tClean\t128\t1\t0\t-1500\t-1500\n" +
		"TRACK\t2\tMuted\t136\t1\t0\t-1500\t-1500\n" +
		"TRACK\t3\tSoloed\t144\t1\t0\t-1500\t-1500\n" +
		"TRACK\t4\tArmed\t192\t1\t0\t-1500\t-1500\n")
	tracks, err := parseTracks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 4 || tracks[0].Muted || !tracks[1].Muted || !tracks[2].Soloed || !tracks[3].Armed {
		t.Fatalf("decoded tracks = %+v", tracks)
	}
}
