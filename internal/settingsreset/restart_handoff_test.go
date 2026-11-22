package settingsreset

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestRestartHandoffQueuesOneAwaitingOperation(t *testing.T) {
	var ready atomic.Bool
	queued := make(chan string, 32)
	h := NewRestartHandoff(ready.Load, func(id string) { queued <- id })
	awaiting := Operation{ID: "reviewed-operation", State: StateAwaitingRestart}
	h.AfterResponse(awaiting)
	ready.Store(true)
	for _, state := range []OperationState{StatePreparing, StateBlocked, StateInterrupted, StatePartialFailure, StateCompleted} {
		h.AfterResponse(Operation{ID: awaiting.ID, State: state})
	}
	h.AfterResponse(Operation{ID: "bad path", State: StateAwaitingRestart})
	if len(queued) != 0 {
		t.Fatal("unavailable or unsettled reset queued a restart")
	}
	var calls sync.WaitGroup
	for range 20 {
		calls.Go(func() { h.AfterResponse(awaiting) })
	}
	calls.Wait()
	if len(queued) != 1 || <-queued != awaiting.ID {
		t.Fatal("the durable operation was not queued exactly once")
	}
}
