package trigger

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Synthetic policy states model separate installation-local manual/automatic
// permissions. This is not a directory import fixture or an activation flow.
type triggerAdmissionSource struct {
	*fakeSource
	mu        sync.RWMutex
	manual    map[string]bool
	automatic map[string]bool
	resolved  map[string]int
}

func (s *triggerAdmissionSource) CheckWorkspaceExecution(_ context.Context, id string, automatic bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	allowed := s.manual[id]
	if automatic {
		allowed = s.automatic[id]
	}
	if !allowed {
		return workspace.ErrWorkspaceExecutionInactive
	}
	return nil
}
func (s *triggerAdmissionSource) set(id string, manual, automatic bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manual[id], s.automatic[id] = manual, automatic
}
func (s *triggerAdmissionSource) GetFolderPath(id string) (string, error) {
	s.mu.Lock()
	s.resolved[id]++
	s.mu.Unlock()
	return s.fakeSource.GetFolderPath(id)
}
func triggerAdmissionFixture(t *testing.T, ids ...string) (*Store, *triggerAdmissionSource) {
	t.Helper()
	_, raw := newTestStore(t, ids...)
	source := &triggerAdmissionSource{fakeSource: raw, manual: map[string]bool{}, automatic: map[string]bool{}, resolved: map[string]int{}}
	for _, id := range ids {
		source.set(id, true, true)
	}
	return NewStore(source), source
}
func triggerAdmissionMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func triggerBytes(t *testing.T, source *triggerAdmissionSource, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(source.folders[id], TriggersFileName))
	triggerAdmissionMust(t, err)
	return data
}

func TestTriggerAdmissionStartupLeavesInactiveDefinitionsAndPendingUntouched(t *testing.T) {
	seed, source := triggerAdmissionFixture(t, "native", "inactive", "unreviewed")
	for _, id := range []string{"native", "inactive", "unreviewed"} {
		_, err := seed.Create(Trigger{ID: id + "-hook", WorkspaceID: id, Name: "Synthetic hook", Type: TypeWebhook, Enabled: true,
			Webhook: &WebhookConfig{Token: "synthetic-shared-token"}, Action: Action{Kind: ActionDomainScan, Domain: "fixture"}})
		triggerAdmissionMust(t, err)
	}
	pending := &PendingFire{FireID: "source-pending", CreatedAt: time.Now(), Events: []Event{{Kind: "webhook", Body: "synthetic old work"}}}
	triggerAdmissionMust(t, seed.SetPendingFire("inactive", "inactive-hook", pending))
	watch, err := seed.Create(Trigger{ID: "inactive-watch", WorkspaceID: "inactive", Name: "Retained watch", Type: TypeFileWatch, Enabled: true,
		Action: Action{Kind: ActionDomainScan, Domain: "fixture"}, FileWatch: &FileWatchConfig{Path: filepath.Join(t.TempDir(), "missing-watch")}})
	triggerAdmissionMust(t, err)
	before := triggerBytes(t, source, "inactive")
	source.set("inactive", true, false)
	source.set("unreviewed", false, false)
	source.mu.Lock()
	source.resolved = map[string]int{}
	source.mu.Unlock()
	// A runtime owner without the optional interface must not erase the
	// canonical folder owner's stricter admission.
	service, err := NewService(ServiceConfig{Source: source, WorkspaceStore: newFakeWorkspaceStore()})
	triggerAdmissionMust(t, err)
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	var scans atomic.Int64
	service.RegisterDomainScanHandler("fixture", domainScanFunc(func(string, string, int, string) error { scans.Add(1); return nil }))
	triggerAdmissionMust(t, service.Start())
	t.Cleanup(func() { service.Close(); service.coalescer.WaitIdle() })
	service.coalescer.WaitIdle()
	service.watch.validateWatches()
	service.watch.handleEvent(filewatcher.WatchEvent{FilePath: filepath.Join(watch.FileWatch.Path, "file.txt"), FileName: "file.txt", Type: filewatcher.EventCreate})
	service.dispatcher.Dispatch(watch, *pending)
	if id := service.coalescer.Observe(watch, Event{Kind: "file"}); id != "" {
		t.Fatal("inactive watch opened a debounce window")
	}
	if _, err := service.TestFire("inactive", "inactive-hook"); !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
		t.Fatal("test-fire bypassed automatic admission", err)
	}
	if service.watch.watcher.IsWatching(runtimeTriggerKey(watch.WorkspaceID, watch.ID)) || scans.Load() != 0 {
		t.Fatal("inactive startup exposed a watch or action")
	}
	if !bytes.Equal(before, triggerBytes(t, source, "inactive")) {
		t.Fatal("startup consumed pending work or rewrote enabled/failure/history fields")
	}
	source.mu.RLock()
	unreviewedReads := source.resolved["unreviewed"]
	source.mu.RUnlock()
	if unreviewedReads != 0 || len(service.List("unreviewed")) != 0 {
		t.Fatal("unreviewed discovery resolved or loaded an executable file")
	}
	// Inactive copied tokens cannot shadow the unrelated admitted token.
	got, ok := service.store.GetByToken("synthetic-shared-token")
	if !ok || got.WorkspaceID != "native" {
		t.Fatal("inactive token replaced native endpoint ownership")
	}
	if _, err := service.TestFire("native", "native-hook"); err != nil {
		t.Fatal(err)
	}
	if scans.Load() != 1 {
		t.Fatal("unrelated native trigger stopped working")
	}
	triggerAdmissionMust(t, gate.TryFence(t.Context()))
}

type domainScanFunc func(string, string, int, string) error

func (fn domainScanFunc) HandleDomainScan(id, fireID string, eventCount int, summary string) error {
	return fn(id, fireID, eventCount, summary)
}

func TestTriggerAdmissionRevocationStopsDebounceAndPendingDrain(t *testing.T) {
	store, source := triggerAdmissionFixture(t, "ws1")
	trigger, err := store.Create(webhookTrigger())
	triggerAdmissionMust(t, err)
	gate := &resetstate.WorkGate{}
	started, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	coalescer := NewCoalescer(store, func(Trigger, PendingFire) { calls.Add(1); close(started); <-resume })
	coalescer.SetAdmissionGate(gate)
	coalescer.debounceFor = func(Trigger) time.Duration { return time.Millisecond }
	release := sync.OnceFunc(func() { close(resume) })
	t.Cleanup(func() { release(); coalescer.Close(); coalescer.WaitIdle() })
	coalescer.Observe(trigger, Event{Kind: "webhook"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("native fire did not start")
	}
	pending := &PendingFire{FireID: "still-pending", CreatedAt: time.Now(), Events: []Event{{Kind: "webhook"}}}
	triggerAdmissionMust(t, store.SetPendingFire("ws1", trigger.ID, pending))
	before := triggerBytes(t, source, "ws1")
	source.set("ws1", true, false)
	release()
	coalescer.WaitIdle()
	coalescer.RestorePending()
	coalescer.WaitIdle()
	if calls.Load() != 1 || !bytes.Equal(before, triggerBytes(t, source, "ws1")) {
		t.Fatal("revoked run claimed or replayed the next durable fire")
	}
	triggerAdmissionMust(t, gate.TryFence(t.Context()))

	// A revoked open window also releases its reset ownership without a
	// persistent write or a callback. Drive the timer transition explicitly.
	source.set("ws1", true, true)
	gate2 := &resetstate.WorkGate{}
	window := NewCoalescer(store, func(Trigger, PendingFire) { t.Error("revoked debounce dispatched") })
	window.SetAdmissionGate(gate2)
	window.debounceFor = func(Trigger) time.Duration { return time.Hour }
	t.Cleanup(func() { window.Close(); window.WaitIdle() })
	window.Observe(trigger, Event{Kind: "webhook"})
	source.set("ws1", true, false)
	window.closeWindow("ws1", trigger.ID)
	if !bytes.Equal(before, triggerBytes(t, source, "ws1")) {
		t.Fatal("revoked window mutated pending state")
	}
	triggerAdmissionMust(t, gate2.TryFence(t.Context()))
}

func TestTriggerAdmissionInactiveEditsDoNotProbeOrRegisterWatch(t *testing.T) {
	_, source := triggerAdmissionFixture(t, "ws1")
	source.set("ws1", true, false)
	service, err := NewService(ServiceConfig{Source: source})
	triggerAdmissionMust(t, err)
	t.Cleanup(service.Close)
	created, err := service.Create(Trigger{WorkspaceID: "ws1", Name: "Inert definition", Type: TypeFileWatch, Enabled: true,
		FileWatch: &FileWatchConfig{Path: filepath.Join(t.TempDir(), "unavailable")}, Action: Action{Kind: ActionMissionRun}})
	triggerAdmissionMust(t, err)
	_, err = service.SetEnabled("ws1", created.ID, true)
	triggerAdmissionMust(t, err)
	got, err := service.Get("ws1", created.ID)
	triggerAdmissionMust(t, err)
	if !got.Enabled || got.FailureCount != 0 || service.watch.watcher.IsWatching(runtimeTriggerKey(created.WorkspaceID, created.ID)) {
		t.Fatal("saved intent was treated as active path authority")
	}
	source.set("ws1", false, false)
	_, err = service.Update("ws1", created.ID, func(*Trigger) error { t.Fatal("unadmitted mutation callback ran"); return nil })
	if !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
		t.Fatal(err)
	}
}

// Covers the opposite composition: a legacy folder source cannot erase the
// runtime owner's policy either.
type triggerAdmissionRuntime struct {
	*fakeWorkspaceStore
	policy *triggerAdmissionSource
}

func (s *triggerAdmissionRuntime) CheckWorkspaceExecution(ctx context.Context, id string, automatic bool) error {
	return s.policy.CheckWorkspaceExecution(ctx, id, automatic)
}

func TestTriggerAdmissionRuntimeOwnerGuardsAllActionsAndWebhookRateBucket(t *testing.T) {
	_, source := triggerAdmissionFixture(t, "ws1")
	ws := &workspace.Workspace{ID: "ws1", Name: "Synthetic", MissionEnabled: true}
	runtime := &triggerAdmissionRuntime{newFakeWorkspaceStore(ws), source}
	runner, opps := &fakeMissionRunner{}, &fakeOppStore{}
	service, err := NewService(ServiceConfig{Source: source.fakeSource, WorkspaceStore: runtime, Mission: runner, Opportunities: opps, WebhookRatePerMin: 1})
	triggerAdmissionMust(t, err)
	t.Cleanup(func() { service.Close(); service.coalescer.WaitIdle() })
	var scans atomic.Int64
	service.RegisterDomainScanHandler("fixture", domainScanFunc(func(string, string, int, string) error { scans.Add(1); return nil }))
	triggers := []Trigger{}
	for _, action := range []Action{{Kind: ActionMissionRun}, {Kind: ActionTaskPrompt, Agent: "synthetic", Prompt: "Do not run"}, {Kind: ActionDomainScan, Domain: "fixture"}} {
		input := webhookTrigger()
		input.Action = action
		created, err := service.Create(input)
		triggerAdmissionMust(t, err)
		triggers = append(triggers, created)
	}
	before := triggerBytes(t, source, "ws1")
	source.set("ws1", true, false)
	for _, trigger := range triggers {
		service.dispatcher.Dispatch(trigger, webhookFire())
		if id, result := service.IngestWebhook(trigger.Webhook.Token, "", Event{Kind: "webhook"}); id != "" || result != IngestNotFound {
			t.Fatal("revoked indexed token was accepted", id, result)
		}
	}
	if len(runner.calls) != 0 || len(ws.Tasks) != 0 || scans.Load() != 0 || len(opps.opps) != 0 {
		t.Fatal("denied runtime owner permitted execution or a finding")
	}
	if len(service.rateLimiter.buckets) != 0 || !bytes.Equal(before, triggerBytes(t, source, "ws1")) {
		t.Fatal("denied ingress mutated rate or history state")
	}
}

func TestTriggerAdmissionCopiedIDsCannotCancelNativeWatchesWindowsOrRateBuckets(t *testing.T) {
	_, source := triggerAdmissionFixture(t, "native", "inactive")
	source.set("inactive", true, false)
	service, err := NewService(ServiceConfig{Source: source, WebhookRatePerMin: 1})
	triggerAdmissionMust(t, err)
	service.coalescer.debounceFor = func(Trigger) time.Duration { return time.Hour }
	t.Cleanup(func() { service.Close(); service.coalescer.WaitIdle() })
	for _, id := range []string{"native", "inactive"} {
		_, err := service.Create(Trigger{ID: "copied-watch-id", WorkspaceID: id, Name: "Watch", Type: TypeFileWatch, Enabled: true,
			Action: Action{Kind: ActionMissionRun}, FileWatch: &FileWatchConfig{Path: t.TempDir()}})
		triggerAdmissionMust(t, err)
		_, err = service.Create(Trigger{ID: "copied-hook-id", WorkspaceID: id, Name: "Hook", Type: TypeWebhook, Enabled: true,
			Action: Action{Kind: ActionMissionRun}, Webhook: &WebhookConfig{Token: "same-synthetic-token"}})
		triggerAdmissionMust(t, err)
	}
	fire, result := service.IngestWebhook("same-synthetic-token", "", Event{Kind: "webhook"})
	if fire == "" || result != IngestAccepted {
		t.Fatal("native webhook not accepted")
	}
	_, err = service.SetEnabled("inactive", "copied-watch-id", false)
	triggerAdmissionMust(t, err)
	_, err = service.RegenerateToken("inactive", "copied-hook-id")
	triggerAdmissionMust(t, err)
	triggerAdmissionMust(t, service.Delete("inactive", "copied-hook-id"))
	triggerAdmissionMust(t, service.Delete("inactive", "copied-watch-id"))
	if !service.watch.watcher.IsWatching(runtimeTriggerKey("native", "copied-watch-id")) {
		t.Fatal("inactive edit cancelled native watch")
	}
	native, err := service.Get("native", "copied-hook-id")
	triggerAdmissionMust(t, err)
	if next := service.coalescer.Observe(native, Event{Kind: "webhook"}); next != fire {
		t.Fatal("inactive delete cancelled native debounce")
	}
	if _, result := service.IngestWebhook("same-synthetic-token", "", Event{Kind: "webhook"}); result != IngestRateLimited {
		t.Fatal("inactive deletion erased native token/rate ownership", result)
	}
	_, err = service.Update("native", "copied-hook-id", func(tr *Trigger) error { tr.WorkspaceID = "inactive"; return nil })
	if err == nil {
		t.Fatal("mutation transferred canonical trigger ownership")
	}
	before := triggerBytes(t, source, "native")
	source.set("native", true, false)
	service.watch.validateWatches()
	if service.watch.watcher.IsWatching(runtimeTriggerKey("native", "copied-watch-id")) || !bytes.Equal(before, triggerBytes(t, source, "native")) {
		t.Fatal("revocation did not remove the live watch without changing saved intent")
	}
}

// Deactivation between the service's early check and the dispatch check must
// return an error, not the most recent successful fire from before deactivation.
type revokingTriggerRuntime struct {
	*triggerAdmissionRuntime
	armed atomic.Bool
}

func (s *revokingTriggerRuntime) CheckWorkspaceExecution(ctx context.Context, id string, automatic bool) error {
	err := s.triggerAdmissionRuntime.CheckWorkspaceExecution(ctx, id, automatic)
	if err == nil && automatic && s.armed.Swap(false) {
		s.policy.set(id, true, false)
	}
	return err
}
func TestTriggerAdmissionSynchronousDispatchDoesNotReturnOldSuccess(t *testing.T) {
	_, source := triggerAdmissionFixture(t, "ws1")
	runtime := &revokingTriggerRuntime{triggerAdmissionRuntime: &triggerAdmissionRuntime{newFakeWorkspaceStore(), source}}
	service, err := NewService(ServiceConfig{Source: source.fakeSource, WorkspaceStore: runtime})
	triggerAdmissionMust(t, err)
	t.Cleanup(service.Close)
	created, err := service.Create(webhookTrigger())
	triggerAdmissionMust(t, err)
	triggerAdmissionMust(t, service.store.RecordFire("ws1", created.ID, FireRecord{FireID: "old-success"}))
	before := triggerBytes(t, source, "ws1")
	runtime.armed.Store(true)
	rec, err := service.TestFire("ws1", created.ID)
	if !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) || rec.FireID != "" || !bytes.Equal(before, triggerBytes(t, source, "ws1")) {
		t.Fatal("synchronous dispatch hid its refusal behind history", rec, err)
	}
}
