package runtimecapability

import "github.com/johnjallday/ori-agent/internal/logger"

const (
	EventDurableCheckFailed = "runtime_capability.durable_check_failed"
	EventLiveCheckFailed    = "runtime_capability.live_check_failed"
	eventFieldName          = "event"
	eventFieldAdapter       = "adapter"
	eventFieldCategory      = "error_category"
)

// emitRuntimeEvent is the single structured-log exit point and is indirected
// so redaction tests capture exactly what would be logged. Its field vocabulary
// is fixed to compiled identifiers and safe categories: never workspace IDs,
// paths, ports, filenames, command IDs, adapter errors, or summaries.
var emitRuntimeEvent = func(_ string, fields logger.Fields) {
	logger.Info("Runtime capability event", fields)
}

func runtimeFailureEvent(name, adapter, category string) {
	fields := logger.Fields{
		eventFieldName:     name,
		eventFieldCategory: safeCode(category),
	}
	if id := safeCode(adapter); id != "" {
		fields[eventFieldAdapter] = id
	}
	emitRuntimeEvent(name, fields)
}
