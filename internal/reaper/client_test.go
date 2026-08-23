package reaper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		case "/_/GET/TRACK/1/I_FOLDERDEPTH":
			_, _ = w.Write([]byte("GET/TRACK/1/I_FOLDERDEPTH\t0\n"))
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
	if !got.FolderDepthAvailable || track.Name != "Drums" || !track.Muted || !track.Soloed || !track.Armed || track.FolderDepth != 0 || track.PeakLeftDB != -9 || track.PeakRightDB != -8 {
		t.Fatalf("track = %+v, folder depth available = %v", track, got.FolderDepthAvailable)
	}
}

func TestClientReadsFolderDepthsInOneBoundedRequest(t *testing.T) {
	const propertyPath = "/_/GET/TRACK/1/I_FOLDERDEPTH;GET/TRACK/2/I_FOLDERDEPTH;GET/TRACK/3/I_FOLDERDEPTH;GET/TRACK/4/I_FOLDERDEPTH"
	propertyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/TRANSPORT":
			_, _ = w.Write([]byte("TRANSPORT\t0\t0\t0\t1.1.00\t1.1.00\n"))
		case "/_/TRACK":
			_, _ = w.Write([]byte(
				"TRACK\t0\tMASTER\t1536\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t1\t0\n" +
					"TRACK\t1\tParent\t129\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
					"TRACK\t2\tChild\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
					"TRACK\t3\tCloser\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
					"TRACK\t4\tMulti Close\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n",
			))
		case propertyPath:
			propertyCalls++
			_, _ = w.Write([]byte(
				"GET/TRACK/1/I_FOLDERDEPTH\t1\n" +
					"GET/TRACK/2/I_FOLDERDEPTH\t0\n" +
					"GET/TRACK/3/I_FOLDERDEPTH\t-1\n" +
					"GET/TRACK/4/I_FOLDERDEPTH\t-2\n",
			))
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
	project := filepath.Join(t.TempDir(), "Fixture.RPP")
	if err := os.WriteFile(project, []byte("<REAPER_PROJECT\nTEMPO 120 4 4\n>\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := client.ReadState(context.Background(), ProjectSource{Path: project, EntryPath: "Fixture.RPP"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int, len(state.Tracks))
	for index, track := range state.Tracks {
		got[index] = track.FolderDepth
	}
	if propertyCalls != 1 || !state.FolderDepthAvailable || !slices.Equal(got, []int{1, 0, -1, -2}) {
		t.Fatalf("property calls = %d, available = %v, depths = %v", propertyCalls, state.FolderDepthAvailable, got)
	}
}

func TestParseFolderDepthsRequiresCompleteIndexAlignedIntegers(t *testing.T) {
	tracks := []Track{{Index: 1}, {Index: 2}}
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: "GET/TRACK/1/I_FOLDERDEPTH\t1\n"},
		{name: "extra", body: "GET/TRACK/1/I_FOLDERDEPTH\t1\nGET/TRACK/2/I_FOLDERDEPTH\t0\nGET/TRACK/3/I_FOLDERDEPTH\t-1\n"},
		{name: "index mismatch", body: "GET/TRACK/2/I_FOLDERDEPTH\t1\nGET/TRACK/1/I_FOLDERDEPTH\t0\n"},
		{name: "duplicate", body: "GET/TRACK/1/I_FOLDERDEPTH\t1\nGET/TRACK/1/I_FOLDERDEPTH\t0\n"},
		{name: "not integer", body: "GET/TRACK/1/I_FOLDERDEPTH\t1.5\nGET/TRACK/2/I_FOLDERDEPTH\t0\n"},
		{name: "missing value", body: "GET/TRACK/1/I_FOLDERDEPTH\t1\nGET/TRACK/2/I_FOLDERDEPTH\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseFolderDepths([]byte(test.body), tracks); !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("parseFolderDepths() error = %v, want ErrMalformedResponse", err)
			}
		})
	}
}

func TestClientFolderDepthFailureDegradesWithoutSensitiveDetails(t *testing.T) {
	const propertyPath = "/_/GET/TRACK/1/I_FOLDERDEPTH"
	tests := []struct {
		name    string
		respond func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "timeout",
			respond: func(_ http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
		},
		{
			name: "oversized response",
			respond: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", maxRemoteResponse+1)))
			},
		},
		{
			name: "malformed response",
			respond: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("GET/TRACK/2/I_FOLDERDEPTH\t1\n"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			propertyCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/_/TRANSPORT":
					_, _ = w.Write([]byte("TRANSPORT\t0\t0\t0\t1.1.00\t1.1.00\n"))
				case "/_/TRACK":
					_, _ = w.Write([]byte(
						"TRACK\t0\tMASTER\t1536\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t1\t0\n" +
							"TRACK\t1\tParent\t129\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n",
					))
				case propertyPath:
					propertyCalls++
					test.respond(w, r)
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
			client.http.Timeout = 25 * time.Millisecond
			project := filepath.Join(t.TempDir(), "private-fixture.RPP")
			if err := os.WriteFile(project, []byte("<REAPER_PROJECT\nTEMPO 120 4 4\n>\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			state, err := client.ReadState(context.Background(), ProjectSource{Path: project, EntryPath: "Fixture.RPP"})
			if err != nil {
				t.Fatal(err)
			}
			if propertyCalls != 1 || !state.Connected || state.FolderDepthAvailable || len(state.Tracks) != 1 || state.Tracks[0].FolderDepth != 0 {
				t.Fatalf("property calls = %d, state = %+v", propertyCalls, state)
			}
			publicJSON, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{server.URL, project, filepath.Dir(project), propertyPath} {
				if strings.Contains(string(publicJSON), forbidden) {
					t.Fatalf("public state leaked %q: %s", forbidden, publicJSON)
				}
			}
		})
	}
}

func TestClientRejectsOversizedOrMisindexedFolderDepthRequests(t *testing.T) {
	oversized := make([]Track, maxFolderDepthTracks+1)
	for index := range oversized {
		oversized[index].Index = index + 1
	}
	client := &Client{}
	if _, err := client.readFolderDepths(context.Background(), 2308, oversized); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("oversized read error = %v, want ErrMalformedResponse", err)
	}
	if _, err := client.readFolderDepths(context.Background(), 2308, []Track{{Index: 2}}); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("misindexed read error = %v, want ErrMalformedResponse", err)
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
			_, _ = w.Write([]byte("TRACK\t0\tMASTER\t1536\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t1\t0\n"))
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
	body := []byte("TRACK\t1\tClean\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
		"TRACK\t2\tMuted\t136\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
		"TRACK\t3\tSoloed\t144\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n" +
		"TRACK\t4\tArmed\t192\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n")
	tracks, err := parseTracks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 4 || tracks[0].Muted || !tracks[1].Muted || !tracks[2].Soloed || !tracks[3].Armed {
		t.Fatalf("decoded tracks = %+v", tracks)
	}
}

// TestParseTracksReadsColorVerifiedAgainstLiveREAPER locks in the group 3.1
// finding: the last TRACK field is I_CUSTOMCOLOR verbatim, flag bit included.
// Values below were captured from a live REAPER session (see
// tasks-reaper-track-strips.md group 3.1).
func TestParseTracksReadsColorVerifiedAgainstLiveREAPER(t *testing.T) {
	body := []byte(
		"TRACK\t1\tRed\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t33488896\n" + // 0x1ff0000
			"TRACK\t2\tGreen\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t16842496\n" + // 0x100ff00
			"TRACK\t3\tBlue\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t16777471\n" + // 0x10000ff
			"TRACK\t4\tUncolored\t128\t1\t0\t-1500\t-1500\t1\t0\t0\t0\t0\t0\n",
	)
	tracks, err := parseTracks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 4 {
		t.Fatalf("tracks = %+v", tracks)
	}
	if tracks[0].Color != 0x1ff0000 || !tracks[0].HasCustomColor() {
		t.Fatalf("red = %+v", tracks[0])
	}
	if tracks[1].Color != 0x100ff00 || !tracks[1].HasCustomColor() {
		t.Fatalf("green = %+v", tracks[1])
	}
	if tracks[2].Color != 0x10000ff || !tracks[2].HasCustomColor() {
		t.Fatalf("blue = %+v", tracks[2])
	}
	if tracks[3].Color != 0 || tracks[3].HasCustomColor() {
		t.Fatalf("uncolored = %+v", tracks[3])
	}
}

func TestParseTracksRejectsAShortTrackLine(t *testing.T) {
	// Fewer than 14 fields predates the color field this feature relies on.
	if _, err := parseTracks([]byte("TRACK\t1\tShort\t128\t1\t0\t-1500\t-1500\n")); err != ErrMalformedResponse {
		t.Fatalf("short TRACK line = %v, want ErrMalformedResponse", err)
	}
}
