package resetfixture

import (
	"context"
	"errors"
	"sync"
	"testing"
)

var ErrWriterStopped = errors.New("reset fixture writer stopped")

// Writer is a deterministic stand-in for a background save/flush loop. Begin
// admits one write and pauses immediately before the callback; Release lets it
// run. Tests can supply real config Save or session FlushToStorage callbacks.
// No sleep/ticker is needed to reproduce resurrection or shutdown interleavings.
type Writer struct {
	requests chan *PendingWrite
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

type PendingWrite struct {
	release chan struct{}
	done    chan struct{}
	once    sync.Once
	err     error // published by closing done
}

// NewWriter must be called after registering the callback's store cleanup, so
// cleanup stops and joins the writer before closing its persistence handles.
// The callback must return (or have its own bounded context); Stop joins it.
func NewWriter(t testing.TB, write func() error) *Writer {
	t.Helper()
	w := &Writer{requests: make(chan *PendingWrite), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				return
			case p := <-w.requests:
				select {
				case <-w.stop:
					p.err = ErrWriterStopped
				case <-p.release:
					p.err = write()
				}
				close(p.done)
			}
		}
	}()
	t.Cleanup(w.Stop)
	return w
}

// Begin returns once the writer has accepted a pending write. Cancellation
// before admission returns an error without queuing work.
func (w *Writer) Begin(ctx context.Context) (*PendingWrite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &PendingWrite{release: make(chan struct{}), done: make(chan struct{})}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.stop:
		return nil, ErrWriterStopped
	case w.requests <- p:
		return p, nil
	}
}

// Stop cancels unreleased work and waits for any executing callback. A release
// racing Stop may run, but no callback can run after Stop returns.
func (w *Writer) Stop() {
	w.once.Do(func() { close(w.stop) })
	<-w.done
}

func (p *PendingWrite) Release() { p.once.Do(func() { close(p.release) }) }

// Wait may be called repeatedly, including after a previous wait timed out.
func (p *PendingWrite) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.err
	}
}
