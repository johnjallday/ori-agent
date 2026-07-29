package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// overnightFixture is a repository with one eligible Claude agent: a bridge
// record naming an exact native session, and a recorded plan-backed usage
// window for that same session.
type overnightFixture struct {
	primary string
	feature string
	home    string
}

func newOvernightFixture(t *testing.T) overnightFixture {
	t.Helper()
	primary, feature := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")
	writePrimaryCheckoutBridgeState(t, home, feature)

	recorder := New(Dependencies{
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		Stdin:     strings.NewReader(strings.Replace(statuslinePayload, "11111111-2222-3333-4444-555555555555", "native-123", 1)),
		Getwd:     func() (string, error) { return primary, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if exit := recorder.Run(context.Background(), []string{
		"--repo-root", primary, "--home", home, "claude-usage", "record", "--statusline",
	}); exit != 0 {
		t.Fatalf("record exit = %d", exit)
	}
	return overnightFixture{primary: primary, feature: feature, home: home}
}

// run invokes one overnight command with the given answer on stdin.
func (f overnightFixture) run(t *testing.T, answer string, args ...string) (int, string, string) {
	t.Helper()
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:    &output,
		Stderr:    &stderr,
		Stdin:     strings.NewReader(answer),
		Getwd:     func() (string, error) { return f.primary, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
		Runner:    primaryCheckoutRunner{primary: f.primary, feature: f.feature},
	})
	base := []string{"--repo-root", f.primary, "--home", f.home, "--herdr-bin", "fake-herdr", "overnight"}
	exit := application.Run(context.Background(), append(base, args...))
	return exit, output.String(), stderr.String()
}

// TestOvernightStartRequiresAnExplicitSelection keeps "enrol everything" from
// existing at all: the set of agents controlled unattended is a decision.
func TestOvernightStartRequiresAnExplicitSelection(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, _, stderr := fixture.run(t, "y\n", "start")
	if exit == 0 {
		t.Fatal("start with no selection succeeded")
	}
	if !strings.Contains(stderr, "never enrols every agent") {
		t.Fatalf("stderr = %q, want the refusal explained", stderr)
	}
}

// TestOvernightStartDeclinedCreatesNothing is the confirmation gate seen from
// the outside: answering anything but yes leaves no trace.
func TestOvernightStartDeclinedCreatesNothing(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, output, _ := fixture.run(t, "n\n", "start", "--agent", "bridge")
	if exit != 0 {
		t.Fatalf("declining exited %d, want 0", exit)
	}
	if !strings.Contains(output, "Declined") {
		t.Fatalf("output = %q, want the decline acknowledged", output)
	}

	exit, listed, _ := fixture.run(t, "", "list")
	if exit != 0 {
		t.Fatalf("list exit = %d", exit)
	}
	if !strings.Contains(listed, "No Overnight Runs") {
		t.Fatalf("a declined run was persisted: %q", listed)
	}
}

// TestOvernightStartConfirmedSchedulesButPromptsNothing is the whole of group
// three in one test: a run exists, and nothing has been done to any agent.
func TestOvernightStartConfirmedSchedulesButPromptsNothing(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, output, stderr := fixture.run(t, "y\n", "start", "--agent", "bridge",
		"--start", "23:00", "--deadline", "07:00", "--timezone", "America/New_York")
	if exit != 0 {
		t.Fatalf("start exit = %d; stderr=%s\n%s", exit, stderr, output)
	}
	for _, want := range []string{
		"Overnight Run — review before confirming",
		"[1] bridge",
		"put THIS MAC to sleep",
		"never accepts or spends usage or API credits",
		"is scheduled. Nothing has been prompted yet.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("start output did not contain %q:\n%s", want, output)
		}
	}

	exit, listed, _ := fixture.run(t, "", "list", "--json")
	if exit != 0 {
		t.Fatalf("list exit = %d", exit)
	}
	var payload struct {
		Runs []struct {
			ID                string `json:"id"`
			State             string `json:"state"`
			Confirmation      string `json:"confirmation"`
			ActiveParticipant string `json:"active_participant"`
			Participants      []struct {
				State         string `json:"state"`
				Feature       string `json:"feature"`
				NativeSession string `json:"native_session"`
				DeliveryState string `json:"delivery_state"`
			} `json:"participants"`
			Wake struct {
				CandidateID string `json:"candidate_id"`
			} `json:"wake"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(listed), &payload); err != nil {
		t.Fatalf("list JSON = %q: %v", listed, err)
	}
	if len(payload.Runs) != 1 {
		t.Fatalf("runs = %+v, want exactly the confirmed one", payload.Runs)
	}
	run := payload.Runs[0]
	if run.State != "scheduled" || run.Confirmation != "interactive" {
		t.Fatalf("run = %+v, want a scheduled, interactively confirmed run", run)
	}
	if run.ActiveParticipant != "" || run.Wake.CandidateID != "" {
		t.Fatalf("run = %+v, want no active participant and no wake before it starts", run)
	}
	if len(run.Participants) != 1 || run.Participants[0].State != "queued" {
		t.Fatalf("participants = %+v, want one queued participant", run.Participants)
	}
	if run.Participants[0].NativeSession != "native-123" {
		t.Fatalf("participant session = %q, want the exact saved session", run.Participants[0].NativeSession)
	}
	if run.Participants[0].DeliveryState != "" {
		t.Fatalf("a prompt delivery exists before the run started: %q", run.Participants[0].DeliveryState)
	}

	// show finds the active run without being told which one.
	exit, shown, _ := fixture.run(t, "", "show")
	if exit != 0 || !strings.Contains(shown, run.ID) {
		t.Fatalf("show exit = %d, output = %q", exit, shown)
	}
	if !strings.Contains(shown, "resumes:  0 of 3") {
		t.Fatalf("show did not state the cycle accounting:\n%s", shown)
	}
}

// TestOvernightStartWithoutAnAnswerRefuses covers the non-interactive path: a
// run that could not be confirmed is a run that was not confirmed.
func TestOvernightStartWithoutAnAnswerRefuses(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, _, stderr := fixture.run(t, "", "start", "--agent", "bridge")
	if exit == 0 {
		t.Fatal("start with no answer on stdin created a run")
	}
	if !strings.Contains(stderr, "--confirm") {
		t.Fatalf("stderr = %q, want the scripted alternative named", stderr)
	}

	exit, listed, _ := fixture.run(t, "", "list")
	if exit != 0 || !strings.Contains(listed, "No Overnight Runs") {
		t.Fatalf("an unconfirmed run was persisted: %q", listed)
	}
}

func TestOvernightStartWithConfirmFlagIsRecordedAsSuch(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, _, stderr := fixture.run(t, "", "start", "--agent", "bridge", "--confirm")
	if exit != 0 {
		t.Fatalf("start --confirm exit = %d; stderr=%s", exit, stderr)
	}
	exit, listed, _ := fixture.run(t, "", "list", "--json")
	if exit != 0 {
		t.Fatalf("list exit = %d", exit)
	}
	if confirmation := firstRunField(t, listed, "confirmation"); confirmation != "confirm_flag" {
		t.Fatalf("confirmation = %q, want the flag recorded as such: %s", confirmation, listed)
	}
}

// TestOvernightDryRunShowsTheSummaryAndStops is the safe way to read the
// confirmation screen without answering it.
func TestOvernightDryRunShowsTheSummaryAndStops(t *testing.T) {
	fixture := newOvernightFixture(t)
	exit, output, _ := fixture.run(t, "", "start", "--agent", "bridge", "--dry-run")
	if exit != 0 {
		t.Fatalf("dry run exit = %d", exit)
	}
	if !strings.Contains(output, "Dry run: nothing was created.") {
		t.Fatalf("output = %q", output)
	}
	_, listed, _ := fixture.run(t, "", "list")
	if !strings.Contains(listed, "No Overnight Runs") {
		t.Fatalf("a dry run created something: %q", listed)
	}
}

func TestOvernightCancelLeavesAgentsAlone(t *testing.T) {
	fixture := newOvernightFixture(t)
	if exit, _, stderr := fixture.run(t, "y\n", "start", "--agent", "bridge"); exit != 0 {
		t.Fatalf("start exit = %d; %s", exit, stderr)
	}
	exit, output, stderr := fixture.run(t, "", "cancel")
	if exit != 0 {
		t.Fatalf("cancel exit = %d; %s", exit, stderr)
	}
	if !strings.Contains(output, "canceled") || !strings.Contains(output, "left untouched") {
		t.Fatalf("cancel output = %q", output)
	}
	_, listed, _ := fixture.run(t, "", "list", "--json")
	if state := firstRunField(t, listed, "state"); state != "canceled" {
		t.Fatalf("state = %q, want canceled: %s", state, listed)
	}

	// A second run may now be created, because the first is terminal.
	if exit, _, stderr := fixture.run(t, "y\n", "start", "--agent", "bridge"); exit != 0 {
		t.Fatalf("start after cancel exit = %d; %s", exit, stderr)
	}
}

// TestOvernightRefusesASecondActiveRun keeps two supervisors from each
// believing they own the queue.
func TestOvernightRefusesASecondActiveRun(t *testing.T) {
	fixture := newOvernightFixture(t)
	if exit, _, stderr := fixture.run(t, "y\n", "start", "--agent", "bridge"); exit != 0 {
		t.Fatalf("first start exit = %d; %s", exit, stderr)
	}
	exit, output, _ := fixture.run(t, "y\n", "start", "--agent", "bridge")
	if exit == 0 {
		t.Fatal("a second active run was created")
	}
	if !strings.Contains(output, "still active") {
		t.Fatalf("output = %q, want the active run named", output)
	}
}

// TestOvernightStartRefusesAnIneligibleAgent proves the Claude readiness gate
// is load-bearing at the CLI: without a usage record nothing can be enrolled.
func TestOvernightStartRefusesAnIneligibleAgent(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")
	writePrimaryCheckoutBridgeState(t, home, feature)
	fixture := overnightFixture{primary: primary, feature: feature, home: home}

	exit, _, stderr := fixture.run(t, "y\n", "start", "--agent", "bridge")
	if exit == 0 {
		t.Fatal("an agent with no Claude usage record was enrolled")
	}
	if !strings.Contains(stderr, "No Claude usage record exists") {
		t.Fatalf("stderr = %q, want the readiness refusal", stderr)
	}
}

// firstRunField reads one string field from the newest run in a list payload.
func firstRunField(t *testing.T, listed, field string) string {
	t.Helper()
	var payload struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(listed), &payload); err != nil {
		t.Fatalf("list JSON = %q: %v", listed, err)
	}
	if len(payload.Runs) == 0 {
		t.Fatalf("no runs in %q", listed)
	}
	value, _ := payload.Runs[0][field].(string)
	return value
}
