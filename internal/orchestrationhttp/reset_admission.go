package orchestrationhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// SetAdmissionGate is initialization-only, before requests or task dispatch.
// This covers manual task dispatch, not every orchestration service or child.
func (h *Handler) SetAdmissionGate(gate *resetstate.WorkGate) {
	h.admissionGate = gate
	if h.taskHandlerSub != nil {
		h.taskHandlerSub.SetAdmissionGate(gate)
	}
}

// SetAdmissionGate is initialization-only. Internal synchronous execution
// helpers run under the entry/child permit, through final saves and events.
func (th *TaskHandler) SetAdmissionGate(gate *resetstate.WorkGate) {
	th.admissionGate = gate
}

func (th *TaskHandler) enterTaskRequest(w http.ResponseWriter) (func(), bool) {
	release, err := th.admissionGate.Enter()
	if err != nil {
		respondTaskAdmissionError(w, err)
		return nil, false
	}
	return release, true
}

func respondTaskAdmissionError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondError(w, http.StatusServiceUnavailable, err.Error())
}

// startTaskWork registers the child before launch/acknowledgment. Acquiring
// inside the goroutine leaves a gap after its caller returns. A parent permit
// separately covers the caller's own final persistence/response work.
func (th *TaskHandler) startTaskWork(work func()) error {
	release, err := th.admissionGate.Enter()
	if err != nil {
		return err
	}
	go func() {
		defer release()
		work()
	}()
	return nil
}
