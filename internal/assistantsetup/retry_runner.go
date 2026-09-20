package assistantsetup

import (
	"context"
	"sync"
	"time"
)

// RetryRunner polls persisted retry deadlines. Service.RetryDue performs all
// authority and attempt-limit checks; the runner owns only process lifecycle.
type RetryRunner struct {
	service  *Service
	interval time.Duration
	now      func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

func NewRetryRunner(service *Service, interval time.Duration) *RetryRunner {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &RetryRunner{service: service, interval: interval, now: time.Now}
}

func (r *RetryRunner) Start(parent context.Context) {
	if r == nil || r.service == nil {
		return
	}
	r.once.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		r.cancel = cancel
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(r.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_, _ = r.service.RetryDue(ctx, r.now(), 20)
				}
			}
		}()
	})
}

func (r *RetryRunner) Close() {
	if r == nil {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}
