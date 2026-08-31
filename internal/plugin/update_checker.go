package plugin

import (
	"sort"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// DefaultUpdateCheckInterval is the process-wide plugin source check cadence.
// It is intentionally not user-configurable; tests inject a shorter interval
// through Start.
const DefaultUpdateCheckInterval = 24 * time.Hour

type updateCheckerSource interface {
	List() ([]InstalledPlugin, error)
	CheckUpdate(string) (UpdateAvailability, error)
}

// UpdateSnapshot is a copy of the process-local plugin update cache. Reading a
// snapshot never performs source, Git, or filesystem I/O.
type UpdateSnapshot struct {
	Updates               []UpdateAvailability `json:"updates"`
	Checking              bool                 `json:"checking"`
	LastSuccessfulCheckAt *time.Time           `json:"last_successful_check_at,omitempty"`
}

// UpdateChecker periodically refreshes a process-local snapshot of plugin
// update availability. Source failures are isolated per plugin and retain the
// last successful result until a later cycle succeeds.
type UpdateChecker struct {
	source updateCheckerSource
	now    func() time.Time

	schedulerMu sync.Mutex
	stop        chan struct{}
	done        chan struct{}
	stopping    bool

	snapshotMu            sync.RWMutex
	results               map[string]UpdateAvailability
	epochs                map[string]uint64
	checking              bool
	lastSuccessfulCheckAt time.Time
}

// NewUpdateChecker creates an idle checker. Start owns its scheduler goroutine.
func NewUpdateChecker(source updateCheckerSource) *UpdateChecker {
	return &UpdateChecker{
		source:  source,
		now:     time.Now,
		results: make(map[string]UpdateAvailability),
		epochs:  make(map[string]uint64),
	}
}

// Start begins the checker once, running an immediate pass before waiting for
// each interval. Non-positive intervals select the daily production default.
func (c *UpdateChecker) Start(interval time.Duration) {
	if c == nil || c.source == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultUpdateCheckInterval
	}

	c.schedulerMu.Lock()
	if c.stop != nil {
		c.schedulerMu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	c.stop, c.done = stop, done
	c.schedulerMu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		c.checkCycle()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				c.checkCycle()
			}
		}
	}()
}

// Stop halts the scheduler and waits for an active source check to finish.
func (c *UpdateChecker) Stop() {
	if c == nil {
		return
	}

	c.schedulerMu.Lock()
	if c.stop == nil {
		c.schedulerMu.Unlock()
		return
	}
	stop, done := c.stop, c.done
	if !c.stopping {
		c.stopping = true
		close(stop)
	}
	c.schedulerMu.Unlock()

	<-done

	c.schedulerMu.Lock()
	if c.done == done {
		c.stop, c.done = nil, nil
		c.stopping = false
	}
	c.schedulerMu.Unlock()
}

// Snapshot returns a sorted copy of the cached state.
func (c *UpdateChecker) Snapshot() UpdateSnapshot {
	if c == nil {
		return UpdateSnapshot{Updates: []UpdateAvailability{}}
	}
	c.snapshotMu.RLock()
	updates := make([]UpdateAvailability, 0, len(c.results))
	for _, result := range c.results {
		updates = append(updates, result)
	}
	checking := c.checking
	lastSuccessful := c.lastSuccessfulCheckAt
	c.snapshotMu.RUnlock()

	sort.Slice(updates, func(i, j int) bool { return updates[i].Name < updates[j].Name })
	snapshot := UpdateSnapshot{Updates: updates, Checking: checking}
	if !lastSuccessful.IsZero() {
		value := lastSuccessful
		snapshot.LastSuccessfulCheckAt = &value
	}
	return snapshot
}

// Invalidate immediately removes one cached result. The epoch prevents a check
// already in flight from publishing state resolved before the mutation.
func (c *UpdateChecker) Invalidate(name string) {
	if c == nil || name == "" {
		return
	}
	c.snapshotMu.Lock()
	delete(c.results, name)
	c.epochs[name]++
	c.snapshotMu.Unlock()
}

type checkedUpdate struct {
	result UpdateAvailability
	epoch  uint64
}

func (c *UpdateChecker) checkCycle() {
	c.snapshotMu.Lock()
	c.checking = true
	c.snapshotMu.Unlock()

	installed, err := c.source.List()
	if err != nil {
		logger.Warn("Plugin update check could not list installed plugins; will retry", logger.Fields{})
		c.snapshotMu.Lock()
		c.checking = false
		c.snapshotMu.Unlock()
		return
	}

	installedNames := make(map[string]struct{}, len(installed))
	successes := make([]checkedUpdate, 0, len(installed))
	for _, candidate := range installed {
		name := candidate.Name
		installedNames[name] = struct{}{}
		c.snapshotMu.RLock()
		epoch := c.epochs[name]
		c.snapshotMu.RUnlock()

		result, checkErr := c.source.CheckUpdate(name)
		if checkErr != nil {
			// Deliberately omit the raw source error: Git URLs may contain
			// credentials and local source errors may disclose private paths.
			logger.Warn("Plugin update check failed; retaining the last result", logger.Fields{"plugin": name})
			continue
		}
		successes = append(successes, checkedUpdate{result: result, epoch: epoch})
	}

	c.snapshotMu.Lock()
	for name := range c.results {
		if _, ok := installedNames[name]; !ok {
			delete(c.results, name)
		}
	}
	for _, success := range successes {
		if c.epochs[success.result.Name] == success.epoch {
			c.results[success.result.Name] = success.result
		}
	}
	c.lastSuccessfulCheckAt = c.now().UTC()
	c.checking = false
	c.snapshotMu.Unlock()
}
