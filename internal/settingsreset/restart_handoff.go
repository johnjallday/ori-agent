package settingsreset

import "sync"

// RestartHandoff is an optional process-host capability, attached before HTTP
// traffic starts. enqueue must only notify the owning host; it must not wait
// for process exit from inside the confirming handler.
type RestartHandoff struct {
	ready   func() bool
	enqueue func(string)
	once    sync.Once
}

func NewRestartHandoff(ready func() bool, enqueue func(string)) *RestartHandoff {
	return &RestartHandoff{ready: ready, enqueue: enqueue}
}

func (h *RestartHandoff) Available() bool {
	return h != nil && h.enqueue != nil && h.ready != nil && h.ready()
}

// AfterResponse is called only on the confirmed POST's successful Stage path,
// after writing its reply. GETs never enter here. Stage returns awaiting_restart
// only after fence, safe drain and durable receipt replacement succeeded.
func (h *RestartHandoff) AfterResponse(op Operation) {
	if op.State == StateAwaitingRestart && ValidOperationID(op.ID) && h.Available() {
		h.once.Do(func() { h.enqueue(op.ID) })
	}
}

// AfterCancel is the recovery host's counterpart: called only on a cancel
// request's successful path, after the canceled receipt is durable and the reply
// is written. It never runs for a request that failed, was refused or whose
// write is uncertain, so a restart can never precede the persistence it relies on.
func (h *RestartHandoff) AfterCancel(op Operation) {
	if op.State == StateCanceled && ValidOperationID(op.ID) && h.Available() {
		h.once.Do(func() { h.enqueue(op.ID) })
	}
}
