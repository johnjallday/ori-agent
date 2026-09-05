package samplelibrary

import "github.com/johnjallday/ori-agent/internal/specialistevents"

var recordSpecialistEvent = specialistevents.Record

const (
	eventActionConnectRoot     = "connect_root"
	eventActionIndexRoot       = "index_root"
	eventActionRevokeRoot      = "revoke_root"
	eventActionEnableAnalysis  = "enable_analysis"
	eventActionDisableAnalysis = "disable_analysis"
	eventActionCopyToProject   = "copy_to_project"
)

func recordSampleEvent(name specialistevents.Name, action string, outcome specialistevents.Outcome, count int) {
	recordSpecialistEvent(name, specialistevents.Fields{
		ActionID: action,
		Outcome:  outcome,
		Count:    count,
	})
}

func recordSampleFailure(name specialistevents.Name, action string, err error) {
	if err == nil {
		return
	}
	recordSpecialistEvent(name, specialistevents.Fields{
		ActionID:   action,
		Outcome:    specialistevents.OutcomeFailed,
		ReasonCode: "sample_operation_failed",
		Count:      0,
	})
}
