package workspace

import "strings"

// localErrorClass categorizes a local-provider failure so scheduling and offline
// handling can react differently to the same error surface (WS6.26a). The two
// actionable classes prescribe opposite responses — cold-load is retried, offline
// is blocked — so the discriminator is explicit and table-driven rather than
// ad-hoc string matching.
type localErrorClass int

const (
	// localErrorOther is a failure that is neither a cold load nor an offline
	// server (e.g. a model-quality or validation error): no special handling.
	localErrorOther localErrorClass = iota
	// localErrorColdLoad is a transient failure consistent with the server being
	// reachable but slow to respond (model loading, connection reset mid-request,
	// read timeout): retry once (WS6.26).
	localErrorColdLoad
	// localErrorOffline is a failure consistent with the server being unreachable
	// (connection refused, DNS failure, no listener): block as offline (WS8.31).
	localErrorOffline
)

// offlineErrorPatterns indicate the server is unreachable (checked first, since a
// connect-phase refusal must classify as offline even though it also "times out").
var offlineErrorPatterns = []string{
	"connection refused",
	"no such host",
	"network is unreachable",
	"no route to host",
	"dial tcp",           // connect-phase failure (host down / wrong port)
	"connect: ",          // generic connect failure
	"server misbehaving", // DNS resolver failure
}

// coldLoadErrorPatterns indicate the server is reachable but was slow or dropped
// the request (model still loading, reset, read timeout).
var coldLoadErrorPatterns = []string{
	"connection reset",
	"reset by peer",
	"unexpected eof",
	"i/o timeout",
	"context deadline exceeded",
	"timeout",
	"timed out",
	"loading model",
	"model is loading",
	"model requires more system memory", // load-time failure, worth one retry
}

// classifyLocalError maps a provider error to a localErrorClass. Offline patterns
// win over cold-load so a connection-refused during a cold-load retry is not
// re-classified as a transient load (WS6.26a).
func classifyLocalError(err error) localErrorClass {
	if err == nil {
		return localErrorOther
	}
	s := strings.ToLower(err.Error())
	for _, p := range offlineErrorPatterns {
		if strings.Contains(s, p) {
			return localErrorOffline
		}
	}
	for _, p := range coldLoadErrorPatterns {
		if strings.Contains(s, p) {
			return localErrorColdLoad
		}
	}
	return localErrorOther
}
