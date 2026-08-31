package plugin

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeUpdateCheckSource struct {
	mu        sync.Mutex
	installed []InstalledPlugin
	results   map[string]UpdateAvailability
	errs      map[string]error
	listErr   error
	calls     map[string]int
	blockName string
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newFakeUpdateCheckSource(names ...string) *fakeUpdateCheckSource {
	source := &fakeUpdateCheckSource{
		results: make(map[string]UpdateAvailability),
		errs:    make(map[string]error),
		calls:   make(map[string]int),
	}
	source.setInstalled(names...)
	for _, name := range names {
		source.results[name] = UpdateAvailability{Name: name, InstalledVersion: "1", AvailableVersion: "2", Available: true}
	}
	return source
}

func (s *fakeUpdateCheckSource) List() ([]InstalledPlugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]InstalledPlugin(nil), s.installed...), nil
}

func (s *fakeUpdateCheckSource) CheckUpdate(name string) (UpdateAvailability, error) {
	s.mu.Lock()
	s.calls[name]++
	result := s.results[name]
	err := s.errs[name]
	block := name == s.blockName
	entered, release := s.entered, s.release
	s.mu.Unlock()

	if block {
		s.enterOnce.Do(func() { close(entered) })
		<-release
	}
	return result, err
}

func (s *fakeUpdateCheckSource) setInstalled(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installed = make([]InstalledPlugin, 0, len(names))
	for _, name := range names {
		s.installed = append(s.installed, InstalledPlugin{Name: name})
	}
}

func (s *fakeUpdateCheckSource) callCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestUpdateCheckerStartRunsImmediateAndPeriodicChecksOnce(t *testing.T) {
	source := newFakeUpdateCheckSource("demo")
	checker := NewUpdateChecker(source)
	checker.Start(10 * time.Millisecond)
	checker.Start(10 * time.Millisecond)
	waitFor(t, time.Second, func() bool { return source.callCount("demo") >= 2 })
	checker.Stop()
	checker.Stop()

	stoppedAt := source.callCount("demo")
	time.Sleep(25 * time.Millisecond)
	if got := source.callCount("demo"); got != stoppedAt {
		t.Fatalf("checks continued after Stop: %d -> %d", stoppedAt, got)
	}
}

func TestUpdateCheckerPublishesSortedResultsAndIsolatesFailures(t *testing.T) {
	source := newFakeUpdateCheckSource("bravo", "alpha")
	checker := NewUpdateChecker(source)
	checkedAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return checkedAt }
	checker.checkCycle()

	snapshot := checker.Snapshot()
	if snapshot.Checking || snapshot.LastSuccessfulCheckAt == nil || !snapshot.LastSuccessfulCheckAt.Equal(checkedAt) {
		t.Fatalf("check metadata = %+v", snapshot)
	}
	if len(snapshot.Updates) != 2 || snapshot.Updates[0].Name != "alpha" || snapshot.Updates[1].Name != "bravo" {
		t.Fatalf("sorted updates = %+v", snapshot.Updates)
	}

	// Alpha now fails, bravo was uninstalled, and charlie was installed. The
	// previous alpha result survives, bravo is dropped, and charlie publishes.
	source.mu.Lock()
	source.errs["alpha"] = errors.New("temporary source failure")
	source.results["charlie"] = UpdateAvailability{Name: "charlie", InstalledVersion: "1", AvailableVersion: "1"}
	source.mu.Unlock()
	source.setInstalled("charlie", "alpha")
	checker.checkCycle()

	snapshot = checker.Snapshot()
	if len(snapshot.Updates) != 2 || snapshot.Updates[0].Name != "alpha" || snapshot.Updates[1].Name != "charlie" {
		t.Fatalf("failure-isolated updates = %+v", snapshot.Updates)
	}
	if !snapshot.Updates[0].Available {
		t.Fatalf("last successful alpha result was not retained: %+v", snapshot.Updates[0])
	}
	if snapshot.Updates[1].Available {
		t.Fatalf("charlie result = %+v, want unchanged", snapshot.Updates[1])
	}

	source.setInstalled()
	checker.checkCycle()
	if updates := checker.Snapshot().Updates; len(updates) != 0 {
		t.Fatalf("empty store retained updates: %+v", updates)
	}
}

func TestUpdateCheckerListFailureKeepsSnapshot(t *testing.T) {
	source := newFakeUpdateCheckSource("demo")
	checker := NewUpdateChecker(source)
	checker.checkCycle()
	before := checker.Snapshot()

	source.mu.Lock()
	source.listErr = errors.New("store unavailable")
	source.mu.Unlock()
	checker.checkCycle()
	after := checker.Snapshot()
	if after.Checking || len(after.Updates) != 1 || after.Updates[0] != before.Updates[0] {
		t.Fatalf("snapshot changed after list failure: before=%+v after=%+v", before, after)
	}
	if after.LastSuccessfulCheckAt == nil || before.LastSuccessfulCheckAt == nil || !after.LastSuccessfulCheckAt.Equal(*before.LastSuccessfulCheckAt) {
		t.Fatalf("last successful check changed after list failure: before=%+v after=%+v", before.LastSuccessfulCheckAt, after.LastSuccessfulCheckAt)
	}
}

func TestUpdateCheckerInvalidationRejectsLateInFlightResultAndStopWaits(t *testing.T) {
	source := newFakeUpdateCheckSource("demo")
	source.blockName = "demo"
	source.entered = make(chan struct{})
	source.release = make(chan struct{})
	checker := NewUpdateChecker(source)
	checker.Start(time.Hour)

	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("immediate check did not start")
	}
	if !checker.Snapshot().Checking {
		t.Fatal("snapshot did not report an active check")
	}
	checker.Invalidate("demo")

	stopped := make(chan struct{})
	go func() {
		checker.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a check was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(source.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the active check completed")
	}

	snapshot := checker.Snapshot()
	if snapshot.Checking || len(snapshot.Updates) != 0 {
		t.Fatalf("late invalidated result was published: %+v", snapshot)
	}
}
