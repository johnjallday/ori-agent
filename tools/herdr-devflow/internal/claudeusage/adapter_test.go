package claudeusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures under testdata are sanitized copies of the two supported Claude
// payload shapes. No test here contacts Claude, reads a real transcript, or
// spends anything: every input is a file on disk and every clock is a value.

var (
	// now is the fixed observation clock.
	now = time.Date(2026, 7, 29, 20, 0, 0, 0, time.UTC)
	// fixtureReset is the epoch second the statusline fixtures report for the
	// five-hour window (1785358800 = 2026-07-29T21:00:00Z).
	fixtureReset = time.Unix(1785358800, 0).UTC()
	// fixtureSession is the session id every fixture describes.
	fixtureSession = "11111111-2222-3333-4444-555555555555"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// newAdapter builds an adapter over a temporary usage directory, seeded with
// the given statusline fixture and, optionally, a recorded failure.
func newAdapter(t *testing.T, statusline string, failure FailureClass, mutate ...func(*Sample)) *Adapter {
	t.Helper()
	adapter := NewAdapter(t.TempDir())
	if statusline != "" {
		sample, err := DecodeStatusLine(loadFixture(t, statusline), now.Add(-time.Minute))
		if err != nil {
			t.Fatalf("decode %s: %v", statusline, err)
		}
		for _, apply := range mutate {
			apply(&sample)
		}
		if err := adapter.Store.SaveSample(sample); err != nil {
			t.Fatalf("save sample: %v", err)
		}
	}
	if failure != "" {
		recorded, err := DecodeStopFailure(loadFixture(t, "stopfailure-rate-limit.json"), failure, now.Add(-time.Minute))
		if err != nil {
			t.Fatalf("decode failure: %v", err)
		}
		if err := adapter.Store.SaveFailure(recorded); err != nil {
			t.Fatalf("save failure: %v", err)
		}
	}
	return adapter
}

func TestDecodeStatusLineKeepsAbsenceDistinctFromZero(t *testing.T) {
	included, err := DecodeStatusLine(loadFixture(t, "statusline-included-window.json"), now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !included.FiveHour.Present || included.FiveHour.UsedPercentage != 23.5 {
		t.Fatalf("five-hour window = %+v, want the reported consumption", included.FiveHour)
	}
	if !included.FiveHour.ResetsAt.Equal(fixtureReset) {
		t.Fatalf("resets at %v, want the epoch second Claude reported (%v)", included.FiveHour.ResetsAt, fixtureReset)
	}
	if included.ClaudeVersion != "2.1.220" || included.SessionID != fixtureSession {
		t.Fatalf("identity = %q/%q", included.ClaudeVersion, included.SessionID)
	}

	// An API-key session reports no windows at all. That absence must survive
	// decoding: a zero-valued window would read as a fresh allowance.
	apiKey, err := DecodeStatusLine(loadFixture(t, "statusline-no-rate-limits.json"), now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if apiKey.FiveHour.Present || apiKey.SevenDay.Present || apiKey.PlanBacked() {
		t.Fatalf("an API-key session was decoded as plan-backed: %+v", apiKey)
	}
	if apiKey.AuthMode() != AuthUnknown {
		t.Fatalf("auth mode = %q, want unknown", apiKey.AuthMode())
	}

	// One window may be absent while the other is reported.
	partial, err := DecodeStatusLine(loadFixture(t, "statusline-window-absent.json"), now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if partial.FiveHour.Present || !partial.SevenDay.Present {
		t.Fatalf("windows = %+v/%+v, want only the weekly window", partial.FiveHour, partial.SevenDay)
	}
	if !partial.PlanBacked() {
		t.Fatal("a session reporting the weekly window is still a subscription session")
	}
}

func TestDecodeStatusLineRefusesUnusablePayloads(t *testing.T) {
	if _, err := DecodeStatusLine(loadFixture(t, "statusline-malformed.json"), now); err == nil {
		t.Fatal("a malformed payload decoded successfully")
	}
	if _, err := DecodeStatusLine([]byte(`{"version":"2.1.220"}`), now); err == nil {
		t.Fatal("a payload with no session id decoded successfully")
	}
	oversized := append([]byte(`{"session_id":"x","padding":"`), make([]byte, maxPayloadBytes)...)
	if _, err := DecodeStatusLine(oversized, now); err == nil {
		t.Fatal("an oversized payload was decoded")
	}
}

func TestDecodeStopFailureTakesItsClassFromTheMatcher(t *testing.T) {
	failure, err := DecodeStopFailure(loadFixture(t, "stopfailure-rate-limit.json"), FailureRateLimit, now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Class != FailureRateLimit || failure.SessionID != fixtureSession {
		t.Fatalf("failure = %+v", failure)
	}
	if _, err := DecodeStopFailure(loadFixture(t, "stopfailure-no-session.json"), FailureRateLimit, now); err == nil {
		t.Fatal("a payload with no session id decoded successfully")
	}
	if _, err := DecodeStopFailure(loadFixture(t, "stopfailure-rate-limit.json"), FailureClass("made_up"), now); err == nil {
		t.Fatal("an unrecognized failure class was accepted")
	}
	if _, err := DecodeStopFailure(loadFixture(t, "statusline-included-window.json"), FailureRateLimit, now); err == nil {
		t.Fatal("a statusline payload was accepted as a StopFailure event")
	}
}

// TestReadinessRefusesEverythingItCannotProve is the fail-closed contract: the
// only path to ready is a fresh, supported, plan-backed record for this exact
// session.
func TestReadinessRefusesEverythingItCannotProve(t *testing.T) {
	cases := []struct {
		name    string
		adapter func(t *testing.T) *Adapter
		session string
		code    ReadinessCode
	}{
		{
			name:    "no recorder has ever written",
			adapter: func(t *testing.T) *Adapter { return NewAdapter(t.TempDir()) },
			session: fixtureSession,
			code:    ReadyRecorderMissing,
		},
		{
			name:    "a record exists for a different session",
			adapter: func(t *testing.T) *Adapter { return newAdapter(t, "statusline-included-window.json", "") },
			session: "99999999-8888-7777-6666-555555555555",
			code:    ReadyRecorderMissing,
		},
		{
			name: "the record is too old to describe the window",
			adapter: func(t *testing.T) *Adapter {
				return newAdapter(t, "statusline-included-window.json", "", func(s *Sample) {
					s.ObservedAt = now.Add(-2 * time.Hour)
				})
			},
			session: fixtureSession,
			code:    ReadySampleStale,
		},
		{
			name: "the record is dated in the future",
			adapter: func(t *testing.T) *Adapter {
				return newAdapter(t, "statusline-included-window.json", "", func(s *Sample) {
					s.ObservedAt = now.Add(time.Hour)
				})
			},
			session: fixtureSession,
			code:    ReadySampleStale,
		},
		{
			name:    "the Claude version is older than the verified contract",
			adapter: func(t *testing.T) *Adapter { return newAdapter(t, "statusline-unsupported-version.json", "") },
			session: fixtureSession,
			code:    ReadyUnsupportedVersion,
		},
		{
			name:    "the session reported no subscription window",
			adapter: func(t *testing.T) *Adapter { return newAdapter(t, "statusline-no-rate-limits.json", "") },
			session: "99999999-8888-7777-6666-555555555555",
			code:    ReadyNotPlanBacked,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			readiness := testCase.adapter(t).Readiness(testCase.session, now)
			if readiness.Ready {
				t.Fatalf("readiness = %+v, want a refusal", readiness)
			}
			if readiness.Code != testCase.code {
				t.Fatalf("code = %q, want %q (%s)", readiness.Code, testCase.code, readiness.Reason)
			}
			if readiness.Reason == "" {
				t.Fatal("a refusal carried no reason")
			}
			if readiness.AuthMode != AuthUnknown {
				t.Fatalf("auth mode = %q, want unknown for a refusal", readiness.AuthMode)
			}
		})
	}
}

func TestReadinessAcceptsAFreshPlanBackedSession(t *testing.T) {
	readiness := newAdapter(t, "statusline-included-window.json", "").Readiness(fixtureSession, now)
	if !readiness.Ready || readiness.Code != ReadyOK {
		t.Fatalf("readiness = %+v, want ready", readiness)
	}
	if readiness.AuthMode != AuthPlanBacked {
		t.Fatalf("auth mode = %q, want plan-backed", readiness.AuthMode)
	}
	if readiness.ClaudeVersion != "2.1.220" {
		t.Fatalf("claude version = %q", readiness.ClaudeVersion)
	}
}

// TestClassifySleepsOnlyForAConfirmedIncludedSessionLimit is the property the
// whole sleep sequence rests on.
func TestClassifySleepsOnlyForAConfirmedIncludedSessionLimit(t *testing.T) {
	adapter := newAdapter(t, "statusline-five-hour-exhausted.json", FailureRateLimit)
	signal := adapter.Classify(fixtureSession, now, time.Time{})

	if signal.Class != LimitIncludedSession {
		t.Fatalf("class = %q, want the included session limit (%s)", signal.Class, signal.Reason)
	}
	if !signal.Sleepable {
		t.Fatalf("signal = %+v, want it sleepable", signal)
	}
	if !signal.ResetAt.Equal(fixtureReset) {
		t.Fatalf("reset = %v, want Claude's reported reset %v", signal.ResetAt, fixtureReset)
	}
	// The reset is Claude's, never detection time plus five hours.
	if signal.ResetAt.Equal(signal.DetectedAt.Add(5 * time.Hour)) {
		t.Fatal("the reset looks calculated from the detection time")
	}
	if signal.AuthMode != AuthPlanBacked {
		t.Fatalf("auth mode = %q, want plan-backed", signal.AuthMode)
	}
}

// TestClassifyRefusesEveryOtherCondition walks the classes that must never
// reach the sleep sequence, which is most of them.
func TestClassifyRefusesEveryOtherCondition(t *testing.T) {
	cases := []struct {
		name       string
		statusline string
		failure    FailureClass
		lastReset  time.Time
		mutate     func(*Sample)
		class      LimitClass
		contains   string
	}{
		{
			name:       "the weekly cap, whose reset is days away",
			statusline: "statusline-seven-day-exhausted.json",
			failure:    FailureRateLimit,
			class:      LimitWeekly,
			contains:   "weekly",
		},
		{
			name:       "context exhaustion, which waiting does not fix",
			statusline: "statusline-context-exhausted.json",
			mutate:     func(s *Sample) { s.ContextUsedPercentage = 100 },
			class:      LimitContext,
			contains:   "context window",
		},
		{
			name:       "a billing error, which would mean spending credits",
			statusline: "statusline-five-hour-exhausted.json",
			failure:    FailureBillingError,
			class:      LimitUnknown,
			contains:   "billing",
		},
		{
			name:       "an authentication failure",
			statusline: "statusline-five-hour-exhausted.json",
			failure:    FailureAuthenticationFailed,
			class:      LimitUnknown,
			contains:   "authentication_failed",
		},
		{
			name:       "an exhausted window with no rate-limited turn recorded",
			statusline: "statusline-five-hour-exhausted.json",
			class:      LimitIncludedSession,
			contains:   "no rate-limited turn",
		},
		{
			name:       "a rate-limited turn with no window reporting exhaustion",
			statusline: "statusline-included-window.json",
			failure:    FailureRateLimit,
			class:      LimitUnknown,
			contains:   "could not be identified",
		},
		{
			name:       "a reset that has already passed",
			statusline: "statusline-five-hour-exhausted.json",
			failure:    FailureRateLimit,
			mutate:     func(s *Sample) { s.FiveHour.ResetsAt = now.Add(-time.Hour) },
			class:      LimitIncludedSession,
			contains:   "already passed",
		},
		{
			name:       "no reset time at all",
			statusline: "statusline-five-hour-exhausted.json",
			failure:    FailureRateLimit,
			mutate:     func(s *Sample) { s.FiveHour.ResetsAt = time.Time{} },
			class:      LimitIncludedSession,
			contains:   "no reset time",
		},
		{
			name:       "a reset boundary this participant already handled",
			statusline: "statusline-five-hour-exhausted.json",
			failure:    FailureRateLimit,
			lastReset:  fixtureReset,
			class:      LimitIncludedSession,
			contains:   "already handled",
		},
		{
			name:       "an API-key session, however exhausted it looks",
			statusline: "statusline-no-rate-limits.json",
			failure:    FailureRateLimit,
			class:      LimitUnknown,
			contains:   "included-plan capacity",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var mutations []func(*Sample)
			if testCase.mutate != nil {
				mutations = append(mutations, testCase.mutate)
			}
			adapter := newAdapter(t, testCase.statusline, testCase.failure, mutations...)
			session := fixtureSession
			if testCase.statusline == "statusline-no-rate-limits.json" {
				session = "99999999-8888-7777-6666-555555555555"
			}
			signal := adapter.Classify(session, now, testCase.lastReset)

			if signal.Sleepable {
				t.Fatalf("signal = %+v, want it refused", signal)
			}
			if signal.Class != testCase.class {
				t.Fatalf("class = %q, want %q (%s)", signal.Class, testCase.class, signal.Reason)
			}
			if !strings.Contains(signal.Reason, testCase.contains) {
				t.Fatalf("reason = %q, want it to mention %q", signal.Reason, testCase.contains)
			}
		})
	}
}

func TestClassifyReportsNoLimitWhenNothingIsExhausted(t *testing.T) {
	signal := newAdapter(t, "statusline-included-window.json", "").Classify(fixtureSession, now, time.Time{})
	if signal.Class != LimitNone || signal.Sleepable {
		t.Fatalf("signal = %+v, want no limit", signal)
	}
}

// TestClassifyAcceptsAStrictlyNewerResetAfterAHandledOne is the repeated-cycle
// precondition: a genuinely new window may be waited out even though an older
// boundary was already consumed.
func TestClassifyAcceptsAStrictlyNewerResetAfterAHandledOne(t *testing.T) {
	adapter := newAdapter(t, "statusline-five-hour-exhausted.json", FailureRateLimit)
	signal := adapter.Classify(fixtureSession, now, fixtureReset.Add(-time.Hour))
	if !signal.Sleepable {
		t.Fatalf("signal = %+v, want a strictly newer reset to be sleepable", signal)
	}
}

func TestRecordsStayPrivateAndBounded(t *testing.T) {
	adapter := newAdapter(t, "statusline-included-window.json", FailureRateLimit)
	entries, err := os.ReadDir(adapter.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("records = %d, want one sample and one failure", len(entries))
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", entry.Name(), info.Mode().Perm())
		}
		contents, err := os.ReadFile(filepath.Join(adapter.Store.Dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// The records carry window state and identity only. Nothing from the
		// payload's prompts, transcript, workspace, or cost fields is stored.
		for _, forbidden := range []string{"transcript", "cwd", "prompt", "cost", "workspace"} {
			if strings.Contains(strings.ToLower(string(contents)), forbidden) {
				t.Fatalf("%s carried %q: %s", entry.Name(), forbidden, contents)
			}
		}
	}
}

func TestStoreRefusesSessionIdsThatCouldEscapeItsDirectory(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, id := range []string{"../escape", "a/b", "", strings.Repeat("x", 200), ".hidden"} {
		if ValidSessionID(id) {
			t.Fatalf("session id %q was accepted", id)
		}
		if err := store.SaveSample(Sample{SessionID: id}); err == nil {
			t.Fatalf("saving under session id %q succeeded", id)
		}
		if _, err := store.Sample(id); err == nil {
			t.Fatalf("reading session id %q succeeded", id)
		}
	}
}

func TestPruneRemovesRecordsForEndedSessions(t *testing.T) {
	adapter := newAdapter(t, "statusline-included-window.json", FailureRateLimit)
	stale := filepath.Join(adapter.Store.Dir, fixtureSession+".json")
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Store.Prune(now.Add(-48 * time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := adapter.Store.Sample(fixtureSession); err == nil {
		t.Fatal("a record older than the cutoff survived pruning")
	}
	if _, err := adapter.Store.Failure(fixtureSession); err != nil {
		t.Fatalf("pruning removed a record that was still current: %v", err)
	}
}
